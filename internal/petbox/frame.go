// Package petbox 构造「盒内宠物换位」的客户端请求(ZONE_PET_BOX_CHANGE_PET_REQ 0x1887),
// 并把一份目标布局拆成可按人工节奏逐条发出的两两交换序列。
//
// 与仓库其余部分的关系:本包只**拼字节**,不打开任何 socket,也不被抓包链路引用。
// internal/capture 一线仍是纯被动的「不读内存、不注入进程、只解析网络流量」,
// 真要把这些字节发出去是 internal/relay 的事。
//
// 字节布局全部取自实测真包(0x1887 的三条取自一份 iOS 抓包,头块与对齐的统计取自仓库里
// 全部抓包共 19138 条 c2s、含一条 22 分钟的完整会话)。见下方 var 块前的布局注释、
// docs/inject.md 与 docs/protocol.md。
package petbox

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/whoisnian/rocom-capture/internal/gcp"
)

// OpChangePet 是盒位交换请求的应用层 opcode(ZONE_PET_BOX_CHANGE_PET_REQ)。
const OpChangePet uint16 = 0x1887

// 一条完整 c2s 包的实测布局,以 iOS 抓包里第一条 0x1887 为例(把盒4 格27 的宠物
// 拖进同盒格28 的空位)。
//
// GCP 包(明文头 25 字节 = 21 定长 + 4 extend):
//
//	3366 000b 000b 4013 00 <seq u32> 00000019 <bodyLen u32> | 00000300 | <body>
//	magic hv   bv   DATA fl                    hdrLen=25                 extend
//
// body = AES-128-CBC(固定全零 IV)加密的 [16 字节头块 || 应用层明文],所以 bodyLen 恒为
// 明文长度 + 16。
//
// **那 16 字节不是随机 IV。** 解密时把它当 IV、拿它去解后面的密文,数学上与「零 IV 解整段、
// 丢掉第一块」完全等价,所以被动解析怎么看都对;但要自己造包就必须把它填对。用零 IV 解开
// 真包的第一块,里面是有结构的东西(实测同一会话的两条 0x1887):
//
//	00 01 02 5c  51af  0607  0839  0a 0b 0c 0d 0e 0e
//	         └[3] 每包计数器      └[8:10] 随内容变
//	              └[4:6] 本会话恒定
//
// 底子是一段 buf[i] = i 的递增填充,字段异或进去,构造规则见 BuildHeader。
// 填随机 16 字节的话服务端解出垃圾,收到即断开且不给 ret_code —— 那时一切「内容层面」的
// 排查全落空,正是因为服务端压根没看到一个合法的包。
//
// 解密后的明文:
//
//	00000000  45 b4 c7 20 00 01 18 87  28 00 00 00 00 38 0a 09  |E.. ....(....8..|
//	00000010  20 1b 18 04 10 00 08 95  23 12 08 20 1c 18 04 10  | .......#.. ....|
//	00000020  00 08 00 e4 11 c8 20 18  ad 18 74 73 66 34 67 0d  |...... ...tsf4g.|
//	          └ head6  ┘└op ┘└tag┘└ reqSeq ┘└ protobuf ...
//	                                      ... ┘└ 填充 ┘└"tsf4g"┘└尾长
//
// 几条要点(对齐与填充是在 2026-08-15 那份 pcap 的 323 条 c2s 消息上统计的;
// -selftest 会在任意一份抓包上把这些逐条复核一遍):
//   - 明文总长 323/323 都是 16 的倍数。AES-CBC 没有 PKCS7,靠尾部那段填充对齐,
//     最后一字节写明尾部总长(填充 + "tsf4g" + 自己),接收方据此剥掉。
//   - 填充内容按长度分组两两不重复(padlen=6 的 69 条 69 个不同值、padlen=14 的 41 条
//     41 个不同值),是随机字节而不是校验和 —— 整条 c2s 消息里没有任何完整性校验,
//     这也是「伪造一条」成本极低的原因。
//   - reqSeq 是客户端请求序号(NTY 类恒为 0),服务器把它原样抄回下行头 [6:10],客户端靠它
//     认领回包。号是一条连接内自增的,但**发送顺序未必递增**:客户端会给要成批发的请求
//     预分配号段,插进来的单条请求抢到大号却先发出去(见 internal/relay 的 ReqSeqGap)。
//   - head6 与 tag 见 Template:**不是常量**,别照抄写死。
var (
	tailMagic = []byte("tsf4g")

	// GCP 头:c2s DATA 实测恒为 hdrLen=25、flag=0x00、extend=00000300。
	// magic / command / 固定 IV 直接用 internal/gcp 的,别在这儿再抄一份。
	gcpHdrLen  = uint32(25)
	gcpExtend  = []byte{0x00, 0x00, 0x03, 0x00}
	gcpVersion = uint16(0x000b)
	gcpFlagC2S = byte(0x00)
)

var (
	errPadLen     = errors.New("petbox: 填充长度与对齐要求不符")
	errShortPlain = errors.New("petbox: 明文过短")
)

// Template 是一条 c2s 请求的「外壳」:明文 [0:6] 与 [8:10]。
//
// 这两处**不是常量**,别写死。实测取值像 16 字节对齐的指针(45b4c720 / 02a7c350 /
// 6d07a7b0 / 6dd75f50 …),一条会话里 plain[8:10] 能出现三十几种值,跨会话、跨版本
// 全不一样 —— 是客户端结构体里没初始化的填充,服务器不可能拿它做什么。
// 真发时由 internal/relay 从当前连接观察到的请求里学一个最常见的照抄(见 Conn.template),
// 离线模拟才退回 DefaultTemplate。
type Template struct {
	Head6 [6]byte
	Tag   [2]byte
}

// DefaultTemplate 取自 2026-08-22 那份 iOS 抓包里出现最多的那组(110 条 c2s 里 85 条)。
// 只用于离线模拟这种拿不到真连接的场合。
var DefaultTemplate = Template{
	Head6: [6]byte{0x45, 0xb4, 0xc7, 0x20, 0x00, 0x01},
	Tag:   [2]byte{0x28, 0x00},
}

// Slot 是一个宠物所在的格位。Box 是盒号(1 起),Pos 是格号(1 起,等于 store 里的 slot+1)。
// 盒子是 6 列 × 5 行,所以 Pos = (行-1)×6 + 列(2026-08-22 iOS 抓包里三次操作逐个对上)。
// InTeam 为真表示该宠物在队伍里而非盒子里;本包只处理盒内,恒为 false。
type Slot struct {
	Gid    uint32
	InTeam bool
	Box    int32
	Pos    int32
}

// Swap 是一次换位,与在游戏里拖一下等价。
//
// To.Gid 为 0 表示目标格是空的 —— 实测成立,而且可以跨盒(2026-08-22 抓到 iOS 客户端
// 把盒5 格19 的宠物送进盒4 格6 的空位)。跨页拖动时游戏 UI 不让选具体格子,但客户端
// 自己算了一个空格填进 pos,所以我们反而能指定得比 UI 更精确。
type Swap struct{ From, To Slot }

func (s Swap) String() string {
	return fmt.Sprintf("盒%d 格%d(%d) ↔ 盒%d 格%d(%d)",
		s.From.Box, s.From.Pos, s.From.Gid, s.To.Box, s.To.Pos, s.To.Gid)
}

// EncodeSwap 编码 ZonePetBoxChangePetReq{ori_info=1, tar_info=2}。
//
// 内层 PetBoxPetChange 的字段顺序照抄 iOS 客户端的 4,3,2,1(pos, id, is_in_team, pet_gid)。
// protobuf 本不要求有序,而这个顺序在不同客户端之间**确实不一样**,连外层 ori/tar 都会反:
//   - 安卓 OnePlus / 2026-08-15 那版:外层 1,2,内层 2,3,1,4
//   - iOS iPhone16,1 / 2026-08-22 那版:外层 1,2,内层 4,3,2,1
//   - Windows / 2026-08-22 那版:外层 2,1(tar_info 先发),内层 2,1,3,4
//
// 三份包平台与版本都在变,分不出是平台差异还是版本差异,只知道它不是常量(各自包内是稳定的)。
// 所以「与真包逐字节一致」只在同一个客户端上成立,换一个只能比语义 —— 三种顺序服务端都收。
// is_in_team=false 与 pet_gid=0 都显式写出 —— 该消息是 proto2 optional,客户端确实
// 发了 `10 00` / `08 00`。
func EncodeSwap(sw Swap) []byte {
	var out []byte
	out = appendLen(out, 1, encodeChange(sw.From))
	out = appendLen(out, 2, encodeChange(sw.To))
	return out
}

func encodeChange(s Slot) []byte {
	var b []byte
	b = appendVarint(b, 4, uint64(s.Pos))     // pos = 格号(1 起)
	b = appendVarint(b, 3, uint64(s.Box))     // id = 盒号
	b = appendVarint(b, 2, boolVal(s.InTeam)) // is_in_team
	b = appendVarint(b, 1, uint64(s.Gid))     // pet_gid(空位为 0)
	return b
}

func appendVarint(b []byte, field int32, v uint64) []byte {
	b = protowire.AppendTag(b, protowire.Number(field), protowire.VarintType)
	return protowire.AppendVarint(b, v)
}

func appendLen(b []byte, field int32, v []byte) []byte {
	b = protowire.AppendTag(b, protowire.Number(field), protowire.BytesType)
	return protowire.AppendBytes(b, v)
}

func boolVal(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

// PadLen 返回 protobuf 长度为 n 时尾部该补多少字节随机填充(把明文补齐到 16 的倍数)。
func PadLen(n int) int {
	return (16 - (14+n+6)%16) % 16
}

// Plaintext 拼出一条 c2s 应用层明文(布局见文件开头 var 块前的注释)。
// pad 长度须等于 PadLen(len(body)):真实使用时传随机字节,自检时传真包里那几个字节。
func Plaintext(tpl Template, opcode uint16, reqSeq uint32, body, pad []byte) ([]byte, error) {
	if len(pad) != PadLen(len(body)) {
		return nil, fmt.Errorf("%w: 给了 %d,应为 %d", errPadLen, len(pad), PadLen(len(body)))
	}
	out := make([]byte, 0, 14+len(body)+len(pad)+6)
	out = append(out, tpl.Head6[:]...)
	out = binary.BigEndian.AppendUint16(out, opcode)
	out = append(out, tpl.Tag[:]...)
	out = binary.BigEndian.AppendUint32(out, reqSeq)
	out = append(out, body...)
	out = append(out, pad...)
	out = append(out, tailMagic...)
	return append(out, byte(len(pad)+len(tailMagic)+1)), nil
}

// Encrypt 把 [头块 || 明文] 用固定全零 IV 做 AES-CBC,得到 GCP DATA 的 body。
// header 必须是 16 字节的**真头块**(见文件开头的说明),明文长度须为 16 的倍数。
func Encrypt(key, header, plain []byte) ([]byte, error) {
	if len(plain) == 0 || len(plain)%16 != 0 {
		return nil, fmt.Errorf("petbox: 明文长度 %d 不是 16 的倍数", len(plain))
	}
	if len(header) != 16 {
		return nil, fmt.Errorf("petbox: 头块长度 %d,应为 16", len(header))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	in := make([]byte, 0, 16+len(plain))
	in = append(append(in, header...), plain...)
	out := make([]byte, len(in))
	cipher.NewCBCEncrypter(block, gcp.FixedAESIV).CryptBlocks(out, in)
	return out, nil
}

// 头块的构造:一段 buf[i] = i 的递增填充,把一个结构体**异或**进去。异或掉这段填充之后,
// 上下行各自是这样(2026-08 版,4 份抓包 702 条 c2s / 1755 条 s2c 逐条核过):
//
//	c2s  [0:4]  u32  GCP 包序号(完整 32 位,见下)              19138/19138
//	     [4:6]  u16  恒为 0x55aa,与 s2c 应用层明文那个标记同值   10374/10374
//	     [6:10] u32  protobuf body 长度 + 26                    19138/19138
//	     [10:16]     逐客户端不同的残留,见下
//
//	s2c  [0:2]  u16  恒 0                                       1755/1755
//	     [2:6]  u32  protobuf body 长度 + 26(与 c2s 同一条规则)  1392/1392
//	     [6:10] u32  每会话恒定(观察到 0x320cb979 / 0x340c81aa)
//	     [10:16]     恒 0                                       1755/1755
//
// **序号必须按 32 位填。** 只异或低字节的话,序号一过 255 就把 [2](乃至 [1][0])留成填充值,
// 那之后注入必被服务端判死。长度字段填错同样致命 —— 服务端解出垃圾,收到即断且不给 ret_code。
// 偏置 26 来历不明,但上下行同值。
//
// 但要注意样本边界:全部样本(含一条 22 分钟的完整会话)里包序号最大只到 10829,
// **[1] 与 [0] 从没被用过**。真实一局能到十几个小时,按实测 7.95 包/秒算,开局 2 小时 17 分
// 就会用到 [1]。所以「[0:4] 是完整 u32」在 65536 以上是**外推**,依据有两条:19138 条里
// [0:2] 一次都没出现过非零值(真是独立字段不至于全 0);同结构的长度字段第三字节确实会被
// 大包用到([8] 取到 0~3),说明这类字段本来就是 u32 而非 u16。
// relay 对此有运行时兜底:每条客户端包都拿 HeaderSeq 核一遍,对不上就拒绝注入(见 Conn)。
//
// [10:16] **逐客户端不同**:一份 iOS 抓包里同一个 opcode 能出二十几种取值、同一秒内乱跳
// (所以服务端显然不看);而另一台 22 分钟 10374 条包里 [14:16] 恒为 1、[10:14] 只偶尔冒出
// 四个垃圾字节。像是「有的实现清了结构体尾巴、有的没清」。既然逐客户端不同就别猜 ——
// 与 head6/tag 同理,由 relay 照抄当前连接观察到的那份(见 HeaderTail 与 Conn.headerTail)。
const (
	headerXorMark = 0x55aa // c2s [4:6]
	headerLenBias = 26     // 长度字段异或的是 protobuf body 长度加这个偏置
)

// bodyLenOf 取明文里 protobuf body 的长度(去掉 14 字节头、填充与 6 字节尾)。
func bodyLenOf(plain []byte) (int, bool) {
	if len(plain) < 21 {
		return 0, false
	}
	tail := int(plain[len(plain)-1])
	if tail < 6 || tail > len(plain)-14 {
		return 0, false
	}
	return len(plain) - 14 - tail, true
}

// HeaderTail 是头块 [10:16] 那 6 个字节的**原始**值(没异或掉填充)。逐客户端不同,
// 见上面的说明:真发时由 relay 从当前连接照抄一份,离线才用 DefaultHeaderTail。
type HeaderTail [6]byte

// DefaultHeaderTail 就是递增填充本身(异或掉之后全 0),只用于离线模拟。
var DefaultHeaderTail = HeaderTail{0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f}

// BuildHeader 按上述规则从零构造一个 c2s 头块。前 10 个字节完全由 gcpSeq 与明文决定,
// [10:16] 照抄 tail(那段是客户端相关的残留,不是能算出来的东西)。
func BuildHeader(gcpSeq uint32, plain []byte, tail HeaderTail) ([]byte, error) {
	bl, ok := bodyLenOf(plain)
	if !ok {
		return nil, fmt.Errorf("petbox: 明文结构不对,取不出 body 长度")
	}
	h := make([]byte, 16)
	for i := range h {
		h[i] = byte(i)
	}
	xorU32(h[0:4], gcpSeq)
	xorU16(h[4:6], headerXorMark)
	xorU32(h[6:10], uint32(bl+headerLenBias))
	copy(h[10:16], tail[:])
	return h, nil
}

// HeaderSeq 从一份头块里解出 GCP 包序号。只看 [0:4],不需要明文,故可以对每条客户端包
// 廉价地核一遍(见 relay 的运行时兜底)。
func HeaderSeq(header []byte) (uint32, bool) {
	if len(header) < 4 {
		return 0, false
	}
	return binary.BigEndian.Uint32(header[0:4]) ^ 0x00010203, true
}

// CheckHeader 反过来校验一份真头块是否符合上述规则,供离线自检。
// 只校验有规则的那 10 个字节;[10:16] 是残留垃圾,不比。
func CheckHeader(header []byte, gcpSeq uint32, plain []byte) error {
	want, err := BuildHeader(gcpSeq, plain, DefaultHeaderTail)
	if err != nil {
		return err
	}
	if got, _ := HeaderSeq(header); got != gcpSeq {
		return fmt.Errorf("petbox: 头块 [0:4] 解出包序号 %d, 应为 %d", got, gcpSeq)
	}
	for _, seg := range [][2]int{{4, 6}, {6, 10}} {
		lo, hi := seg[0], seg[1]
		if !bytes.Equal(header[lo:hi], want[lo:hi]) {
			return fmt.Errorf("petbox: 头块 [%d:%d] 为 %x, 按规则应为 %x",
				lo, hi, header[lo:hi], want[lo:hi])
		}
	}
	return nil
}

func xorU16(b []byte, v uint16) {
	binary.BigEndian.PutUint16(b, binary.BigEndian.Uint16(b)^v)
}

func xorU32(b []byte, v uint32) {
	binary.BigEndian.PutUint32(b, binary.BigEndian.Uint32(b)^v)
}

// RenumberSeq 把一条已加密的 c2s DATA body 里那份包序号副本改成 newSeq,原地重写密文。
//
// 中转插进自己的包之后,后面每条客户端包的 GCP 头序号都要顺延,而序号在密文里还有一份
// (见上面的构造规则)。只改外层不改内层,服务端一对就把会话判死。
//
// 不需要知道头块的底数:副本是 base ^ 旧序号,再异或上 (旧 ^ 新) 就得到 base ^ 新序号。
func RenumberSeq(key, body []byte, oldSeq, newSeq uint32) error {
	if len(body) < 16 || len(body)%16 != 0 {
		return fmt.Errorf("petbox: body 长度 %d 不是 16 的非零倍数", len(body))
	}
	if oldSeq == newSeq {
		return nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	plain := make([]byte, len(body))
	cipher.NewCBCDecrypter(block, gcp.FixedAESIV).CryptBlocks(plain, body)
	xorU32(plain[0:4], oldSeq^newSeq)
	cipher.NewCBCEncrypter(block, gcp.FixedAESIV).CryptBlocks(body, plain)
	return nil
}

// Frame 给密文 body 套上 GCP 头,得到可直接写进 TCP 连接的完整一包。
// gcpSeq 是该连接上行方向的包序号,实测逐包递增(与应用层 reqSeq 是两套计数)。
func Frame(gcpSeq uint32, encBody []byte) []byte {
	out := make([]byte, 0, int(gcpHdrLen)+len(encBody))
	out = append(out, gcp.Magic...)
	out = binary.BigEndian.AppendUint16(out, gcpVersion) // head_version
	out = binary.BigEndian.AppendUint16(out, gcpVersion) // body_version
	out = binary.BigEndian.AppendUint16(out, gcp.CmdData)
	out = append(out, gcpFlagC2S)
	out = binary.BigEndian.AppendUint32(out, gcpSeq)
	out = binary.BigEndian.AppendUint32(out, gcpHdrLen)
	out = binary.BigEndian.AppendUint32(out, uint32(len(encBody)))
	out = append(out, gcpExtend...)
	return append(out, encBody...)
}

// BuildPlain 生成一条盒位交换请求的应用层明文(填充取随机字节)。
// 中转注入(internal/relay)只需要明文,加密与套头由那边按连接的密钥/序号来做。
func BuildPlain(tpl Template, reqSeq uint32, sw Swap) ([]byte, error) {
	body := EncodeSwap(sw)
	pad := make([]byte, PadLen(len(body)))
	if _, err := rand.Read(pad); err != nil {
		return nil, err
	}
	return Plaintext(tpl, OpChangePet, reqSeq, body, pad)
}

// Build 一步生成一条完整的、可直接写进 TCP 连接的 GCP 包(填充取随机字节,头块按
// BuildHeader 的规则算)。同时返回明文,便于打印/自检。
func Build(key []byte, tpl Template, gcpSeq, reqSeq uint32, sw Swap) (wire, plain []byte, err error) {
	if plain, err = BuildPlain(tpl, reqSeq, sw); err != nil {
		return nil, nil, err
	}
	header, err := BuildHeader(gcpSeq, plain, DefaultHeaderTail)
	if err != nil {
		return nil, nil, err
	}
	enc, err := Encrypt(key, header, plain)
	if err != nil {
		return nil, nil, err
	}
	return Frame(gcpSeq, enc), plain, nil
}

// Parsed 是从一条真实 c2s 明文里拆出来的全部内容(自检拿它与本包的编码对比)。
type Parsed struct {
	Template Template
	ReqSeq   uint32
	Swap     Swap
	Pad      []byte
}

// ParsePlain 把一条 c2s 明文拆开。外壳(head6/tag)原样带出来而不做校验 —— 它本来就
// 各包不同(见 Template)。
func ParsePlain(plain []byte) (Parsed, error) {
	var p Parsed
	if len(plain) < 14+6 {
		return p, errShortPlain
	}
	tailLen := int(plain[len(plain)-1])
	if tailLen < 6 || tailLen > len(plain)-14 {
		return p, fmt.Errorf("petbox: 尾长 %d 不合理", tailLen)
	}
	tail := plain[len(plain)-tailLen:]
	if string(tail[len(tail)-6:len(tail)-1]) != string(tailMagic) {
		return p, errors.New("petbox: 尾部缺少 tsf4g 标记")
	}
	p.Template = Template{Head6: [6]byte(plain[0:6]), Tag: [2]byte(plain[8:10])}
	p.ReqSeq = binary.BigEndian.Uint32(plain[10:14])
	p.Pad = tail[:len(tail)-6]
	sw, err := decodeSwap(plain[14 : len(plain)-tailLen])
	p.Swap = sw
	return p, err
}

// decodeSwap 解 ZonePetBoxChangePetReq 的两个子消息(只认 varint 字段,够用即可)。
func decodeSwap(body []byte) (Swap, error) {
	var sw Swap
	for len(body) > 0 {
		num, typ, n := protowire.ConsumeTag(body)
		if n < 0 {
			return sw, errors.New("petbox: 请求体 tag 解析失败")
		}
		body = body[n:]
		if typ != protowire.BytesType {
			return sw, fmt.Errorf("petbox: 字段 %d 的 wire type 意外(%d)", num, typ)
		}
		v, n := protowire.ConsumeBytes(body)
		if n < 0 {
			return sw, errors.New("petbox: 请求体子消息解析失败")
		}
		body = body[n:]
		s, err := decodeChange(v)
		if err != nil {
			return sw, err
		}
		switch num {
		case 1:
			sw.From = s
		case 2:
			sw.To = s
		}
	}
	return sw, nil
}

func decodeChange(b []byte) (Slot, error) {
	var s Slot
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 || typ != protowire.VarintType {
			return s, errors.New("petbox: PetBoxPetChange 字段解析失败")
		}
		b = b[n:]
		v, n := protowire.ConsumeVarint(b)
		if n < 0 {
			return s, errors.New("petbox: PetBoxPetChange varint 解析失败")
		}
		b = b[n:]
		switch num {
		case 1:
			s.Gid = uint32(v)
		case 2:
			s.InTeam = v != 0
		case 3:
			s.Box = int32(v)
		case 4:
			s.Pos = int32(v)
		}
	}
	return s, nil
}
