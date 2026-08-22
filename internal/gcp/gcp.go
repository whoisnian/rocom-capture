// Package gcp 实现洛克王国 tsf4g/GCP 协议的分帧、密钥提取与 0x4013 解密。
//
// 协议布局(实测，多字节默认大端)：
//
//	HEAD.base 定长 21 字节:
//	  [0:2]   magic = 0x3366
//	  [2:4]   head_version  [4:6] body_version
//	  [6:8]   command (BE): 0x1001 SYN / 0x1002 ACK / 0x4013 DATA / ...
//	  [8]     方向/flag    [9:13] sequence(BE u32)
//	  [13:17] hdr_len(BE u32)   [17:21] body_len(BE u32)
//	  整包长度 = hdr_len + body_len, body 从 hdr_len 偏移开始
//
// 密钥: 0x1002 ACK 的 header_extra[2:18] 是 16 字节 AES key(服务器明文下发)。
// 解密: 0x4013 body 用 AES-CBC, embedded_iv(body[:16]=IV) 或 fixed_iv。
//
// 解密明文里的应用层 opcode: c2s 在偏移 6, s2c 在偏移 2(BE u16)。
package gcp

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
)

const (
	FixedHdrLen = 21
	keyOffset   = 2  // ACK header_extra 内 key 起始
	keyLen      = 16 // AES-128

	// s2c 明文 internal header: [0:2]保留 [2:4]opcode [4:6]=0x55aa [6:10]seq [10:]body
	s2cBodyOffset   = 10
	s2cOpcodeOffset = 2
	c2sOpcodeOffset = 6
	s2cMarkerOffset = 4 // s2c 明文固定标记 0x55aa 的偏移
)

// s2cMarker 是 s2c 明文 internal header [4:6] 的固定标记,用于校验解密密钥是否正确。
var s2cMarker = []byte{0x55, 0xaa}

// ValidPlain 判断解密明文结构是否正确(即所用会话密钥是否匹配)。
// s2c 明文 [4:6] 恒为 0x55aa,密钥错误时解出的是乱码,该标记不符即可判定;
// c2s 无此固定标记,恒返回 true(下游只据 s2c 归属/解析,c2s 仅供调试)。
func ValidPlain(dir Direction, plain []byte) bool {
	if dir != S2C {
		return true
	}
	return len(plain) >= s2cMarkerOffset+2 &&
		plain[s2cMarkerOffset] == s2cMarker[0] && plain[s2cMarkerOffset+1] == s2cMarker[1]
}

var Magic = []byte{0x33, 0x66}

// FixedAESIV 是 fixed_iv 模式使用的固定 IV(全零)。
var FixedAESIV = make([]byte, 16)

// GCP command 类型
const (
	CmdSYN       = 0x1001
	CmdACK       = 0x1002
	CmdAuthReq   = 0x2001
	CmdAuthRsp   = 0x2002
	CmdData      = 0x4013
	CmdHeartbeat = 0x9001
	CmdSStop     = 0x5002
)

// Direction 表示流量方向。
type Direction uint8

const (
	C2S Direction = iota // 客户端 -> 服务器 (dst port 8195)
	S2C                  // 服务器 -> 客户端
)

func (d Direction) String() string {
	if d == C2S {
		return "c2s"
	}
	return "s2c"
}

// Packet 是一个完整的 GCP 协议包(已按 hdr_len+body_len 切分)。
type Packet struct {
	Command     uint16
	Sequence    uint32
	HeaderExtra []byte // [FixedHdrLen:hdr_len]
	Body        []byte // [hdr_len:hdr_len+body_len], DATA 为密文
}

// Deframe 从字节流缓冲中切出尽可能多的完整 GCP 包，
// 返回解析出的包和未消费的剩余字节(等待更多数据)。
func Deframe(buf []byte) (pkts []Packet, rest []byte) {
	off := 0
	for off+FixedHdrLen <= len(buf) {
		if buf[off] != Magic[0] || buf[off+1] != Magic[1] {
			// 失步，向后找下一个 magic
			nxt := indexMagic(buf, off+1)
			if nxt < 0 {
				return pkts, buf[len(buf):] // 丢弃无法识别的尾部噪声
			}
			off = nxt
			continue
		}
		cmd := binary.BigEndian.Uint16(buf[off+6 : off+8])
		seq := binary.BigEndian.Uint32(buf[off+9 : off+13])
		hdrLen := int(binary.BigEndian.Uint32(buf[off+13 : off+17]))
		bodyLen := int(binary.BigEndian.Uint32(buf[off+17 : off+21]))
		if hdrLen < FixedHdrLen || hdrLen+bodyLen > 8*1024*1024 {
			off += 2 // 头部不合理，跳过当前 magic 继续找
			continue
		}
		total := hdrLen + bodyLen
		if off+total > len(buf) {
			break // 包不完整，等待更多数据
		}
		he := make([]byte, hdrLen-FixedHdrLen)
		copy(he, buf[off+FixedHdrLen:off+hdrLen])
		body := make([]byte, bodyLen)
		copy(body, buf[off+hdrLen:off+total])
		pkts = append(pkts, Packet{Command: cmd, Sequence: seq, HeaderExtra: he, Body: body})
		off += total
	}
	rest = make([]byte, len(buf)-off)
	copy(rest, buf[off:])
	return pkts, rest
}

func indexMagic(buf []byte, from int) int {
	for i := from; i+1 < len(buf); i++ {
		if buf[i] == Magic[0] && buf[i+1] == Magic[1] {
			return i
		}
	}
	return -1
}

// ExtractKey 从 0x1002 ACK 包的 header_extra 提取 16 字节 AES 会话密钥。
func ExtractKey(ackHeaderExtra []byte) ([]byte, bool) {
	if len(ackHeaderExtra) < keyOffset+keyLen {
		return nil, false
	}
	key := make([]byte, keyLen)
	copy(key, ackHeaderExtra[keyOffset:keyOffset+keyLen])
	return key, true
}

var errShort = errors.New("gcp: 0x4013 body 过短")

// DecryptData 解密 0x4013 DATA 的 body，自动尝试 embedded_iv 与 fixed_iv。
// 返回解密后的应用层明文。
func DecryptData(key, body []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	// embedded_iv: body[:16]=IV, body[16:]=密文
	if len(body) >= 32 && (len(body)-16)%16 == 0 {
		iv := body[:16]
		ct := body[16:]
		out := make([]byte, len(ct))
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, ct)
		return out, nil
	}
	// fixed_iv: 整体即密文
	if len(body) >= 16 && len(body)%16 == 0 {
		out := make([]byte, len(body))
		cipher.NewCBCDecrypter(block, FixedAESIV).CryptBlocks(out, body)
		return out, nil
	}
	return nil, errShort
}

// DecryptHeadBlock 取出 DATA body 最前面那 16 字节的头块。
//
// 那 16 字节常被当成 CBC 的 IV(DecryptData 的 embedded_iv 分支就是这么读的),数学上与
// 「零 IV 解整段、丢掉第一块」完全等价 —— 所以被动解析怎么读都对。但它其实是个有结构的
// 头块:c2s 里带着这一包的 GCP 包序号与长度,自己造包时必须填对,否则服务端收到即断开。
// 结构见 docs/inject.md,构造与校验在 internal/petbox。
//
// 零 IV 下第一块的明文就是它的 ECB 解密结果(CBC 首块与全零 IV 异或等于自身)。
func DecryptHeadBlock(key, body []byte) ([]byte, error) {
	if len(body) < 16 {
		return nil, errShort
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 16)
	block.Decrypt(out, body[:16])
	return out, nil
}

// AppOpcode 从解密明文中提取应用层 opcode。
func AppOpcode(dir Direction, plain []byte) (uint16, bool) {
	off := c2sOpcodeOffset
	if dir == S2C {
		off = s2cOpcodeOffset
	}
	if len(plain) < off+2 {
		return 0, false
	}
	return binary.BigEndian.Uint16(plain[off : off+2]), true
}

// AppBody 返回解密明文中应用层 protobuf body 部分(剥离 internal header)。
func AppBody(dir Direction, plain []byte) []byte {
	if dir == S2C {
		if len(plain) >= s2cBodyOffset {
			return plain[s2cBodyOffset:]
		}
		return nil
	}
	// c2s internal header 长度：[0:6] + opcode[6:8] + ... 实测 body 也在固定偏移之后，
	// 但 c2s 业务请求体本项目不解析，保留完整明文供调试。
	if len(plain) >= 8 {
		return plain[8:]
	}
	return nil
}
