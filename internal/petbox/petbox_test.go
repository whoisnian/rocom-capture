package petbox

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/whoisnian/rocom-capture/internal/gcp"
)

// realPackets 是 pcap/rocom-20260822-003405.pcap00(iOS iPhone16,1)里三条真实 0x1887,
// 对应三次已知操作。字段顺序是这个客户端的 4,3,2,1 —— 也是本包编码采用的顺序。
var realPackets = []struct {
	name string
	hex  string
	req  uint32
	from Slot
	to   Slot
}{{
	// 第 4 页把 5 排 3 列(格 27)的宠物拖到 5 排 4 列(格 28)的**空位**
	name: "拖进同盒空位",
	hex: "45b4c7200001" + "1887" + "2800" + "00000038" +
		"0a09" + "201b" + "1804" + "1000" + "089523" + // ori{pos=27,id=4,in_team=0,gid=4501}
		"1208" + "201c" + "1804" + "1000" + "0800" + // tar{pos=28,id=4,in_team=0,gid=0}
		"e411c82018ad18" + "7473663467" + "0d", // 填充 7B + tsf4g + 尾长 13
	req: 56, from: Slot{Gid: 4501, Box: 4, Pos: 27}, to: Slot{Gid: 0, Box: 4, Pos: 28},
}, {
	// 第 4 页 5 排 1 列(格 25)与 5 排 2 列(格 26)对换,两边都有宠物
	name: "同盒两只对换",
	hex: "45b4c7200001" + "1887" + "3800" + "0000003c" +
		"0a09" + "2019" + "1804" + "1000" + "08b915" +
		"1208" + "201a" + "1804" + "1000" + "084e" +
		"0b30faba735818" + "7473663467" + "0d",
	req: 60, from: Slot{Gid: 2745, Box: 4, Pos: 25}, to: Slot{Gid: 78, Box: 4, Pos: 26},
}, {
	// 第 5 页 4 排 1 列(格 19)跨页搬到第 4 页;UI 不让选格子,客户端自己挑了格 6 的空位
	name: "跨盒进空位",
	hex: "45b4c7200001" + "1887" + "0006" + "0000003d" +
		"0a09" + "2013" + "1805" + "1000" + "089423" +
		"1208" + "2006" + "1804" + "1000" + "0800" +
		"146e58d2873ab3" + "7473663467" + "0d",
	req: 61, from: Slot{Gid: 4500, Box: 5, Pos: 19}, to: Slot{Gid: 0, Box: 4, Pos: 6},
}}

func TestPlaintextMatchesRealPackets(t *testing.T) {
	for _, tc := range realPackets {
		t.Run(tc.name, func(t *testing.T) {
			want, err := hex.DecodeString(tc.hex)
			if err != nil {
				t.Fatal(err)
			}
			p, err := ParsePlain(want)
			if err != nil {
				t.Fatalf("解析真包失败: %v", err)
			}
			if p.ReqSeq != tc.req {
				t.Errorf("请求序号 = %d, want %d", p.ReqSeq, tc.req)
			}
			if p.Swap.From != tc.from || p.Swap.To != tc.to {
				t.Errorf("解出的交换 = %s", p.Swap)
			}
			if len(p.Pad) != PadLen(len(EncodeSwap(p.Swap))) {
				t.Errorf("填充长度 %d 与 PadLen 算的不符", len(p.Pad))
			}
			got, err := Plaintext(p.Template, OpChangePet, p.ReqSeq, EncodeSwap(p.Swap), p.Pad)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("重建的明文与真包不一致\n真包 %x\n重建 %x", want, got)
			}
		})
	}
}

// TestOtherClientPacket 用 2026-08-15 安卓(OnePlus,上一个游戏版本)的真包:内容照样
// 解得出来,但那个客户端的 protobuf 字段顺序是 2,3,1,4,与 iOS 的 4,3,2,1 不同,只比语义。
// 记在这里是为了钉住「字段顺序换客户端就会变,别把逐字节一致当成不变量」。
func TestOtherClientPacket(t *testing.T) {
	old := "000000020002" + "1887" + "c850" + "0000005a" +
		"0a0a" + "1000" + "1815" + "08c0b302" + "2010" +
		"120a" + "1000" + "1815" + "0889b302" + "2016" +
		"416047f4" + "7473663467" + "0a"
	raw, err := hex.DecodeString(old)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParsePlain(raw)
	if err != nil {
		t.Fatalf("解析安卓真包失败: %v", err)
	}
	if p.Swap.From != (Slot{Gid: 39360, Box: 21, Pos: 16}) || p.Swap.To != (Slot{Gid: 39305, Box: 21, Pos: 22}) {
		t.Errorf("解出的交换 = %s", p.Swap)
	}
	got, err := Plaintext(p.Template, OpChangePet, p.ReqSeq, EncodeSwap(p.Swap), p.Pad)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(raw) {
		t.Errorf("重建长度 %d != 真包 %d(字段顺序不同也不该改变长度)", len(got), len(raw))
	}
	if bytes.Equal(got, raw) {
		t.Error("居然逐字节相同了 —— 说明编码顺序被改成了安卓那套,请更新本用例的说明")
	}
}

func TestPadLenAlignsTo16(t *testing.T) {
	for n := range 64 {
		if total := 14 + n + PadLen(n) + 6; total%16 != 0 {
			t.Errorf("body=%d 时总长 %d 不是 16 的倍数", n, total)
		}
	}
}

// TestPlan 随机造排列,验证交换序列确实能把布局变成目标顺序,且次数是最少的 n-环数。
func TestPlan(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for range 200 {
		n := 1 + r.IntN(40)
		slots := make([]Slot, n)
		for i := range slots {
			slots[i] = Slot{Gid: uint32(1000 + i), Box: int32(1 + i/30), Pos: int32(1 + i%30)}
		}
		want := make([]uint32, n)
		for i, j := range r.Perm(n) {
			want[i] = slots[j].Gid
		}

		swaps, err := Plan(slots, want)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}

		// 照着交换序列走一遍,看最终每个格位上是不是 want 里对应的那只。
		cur := make([]uint32, n)
		posOf := map[uint32]int{}
		for i, s := range slots {
			cur[i], posOf[s.Gid] = s.Gid, i
		}
		for _, sw := range swaps {
			i, j := posOf[sw.From.Gid], posOf[sw.To.Gid]
			if cur[i] != sw.From.Gid || cur[j] != sw.To.Gid {
				t.Fatalf("n=%d: 交换 %s 的源宠物与当时的布局对不上", n, sw)
			}
			if slots[i].Box != sw.From.Box || slots[i].Pos != sw.From.Pos ||
				slots[j].Box != sw.To.Box || slots[j].Pos != sw.To.Pos {
				t.Fatalf("n=%d: 交换 %s 带的格位不是这两只宠物当时所在的格位", n, sw)
			}
			cur[i], cur[j] = cur[j], cur[i]
			posOf[cur[i]], posOf[cur[j]] = i, j
		}
		for i := range cur {
			if cur[i] != want[i] {
				t.Fatalf("n=%d: 第 %d 个格位收尾是 %d, want %d", n, i, cur[i], want[i])
			}
		}
		if min := n - cycles(slots, want); len(swaps) != min {
			t.Errorf("n=%d: 用了 %d 次交换, 最少 %d 次", n, len(swaps), min)
		}
	}
}

// cycles 数「当前布局 → 目标顺序」这个置换有几个环:每个长度 k 的环要 k-1 次交换,
// 所以最少交换次数 = n - 环数。
func cycles(slots []Slot, want []uint32) int {
	at := map[uint32]int{}
	for i, s := range slots {
		at[s.Gid] = i
	}
	seen := make([]bool, len(slots))
	n := 0
	for i := range slots {
		if seen[i] {
			continue
		}
		n++
		for j := i; !seen[j]; j = at[want[j]] {
			seen[j] = true
		}
	}
	return n
}

func TestEncryptRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0xab}, 16)
	sw := Swap{From: Slot{Gid: 39360, Box: 21, Pos: 16}, To: Slot{Gid: 39305, Box: 21, Pos: 22}}
	wire, plain, err := Build(key, DefaultTemplate, 7, 90, sw)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain)%16 != 0 {
		t.Fatalf("明文 %d 字节, 不是 16 的倍数", len(plain))
	}
	// GCP 头 25 字节 + 头块 16 字节 + 与明文等长的密文
	if want := 25 + 16 + len(plain); len(wire) != want {
		t.Errorf("整包 %d 字节, want %d", len(wire), want)
	}
	// 头块必须能原样解回来:那 16 字节不是随机 IV,填错服务端收到即断开(见 Encrypt 的说明)
	body := wire[25:]
	back, err := gcp.DecryptHeadBlock(key, body)
	if err != nil {
		t.Fatal(err)
	}
	want, err := BuildHeader(7, plain, DefaultHeaderTail)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, want) {
		t.Errorf("解回来的头块 %x, want %x", back, want)
	}
	// 而按「内嵌 IV」那套解出来的仍应是应用层明文 —— 两种解释在数学上等价,
	// 所以被动解析怎么看都对,这正是当初没发现填错的原因。
	blk, _ := aes.NewCipher(key)
	got := make([]byte, len(plain))
	cipher.NewCBCDecrypter(blk, body[:16]).CryptBlocks(got, body[16:])
	if !bytes.Equal(got, plain) {
		t.Errorf("内嵌 IV 解出的明文与原文不符")
	}
}

// TestBuildHeader 用两个不同会话里的真 0x1887 钉住头块的构造规则:一段 buf[i]=i 的递增
// 填充,把 [0:4] 包序号、[4:6] 0x55aa、[6:10] 长度异或进去。两条的内容是同两只宠物位置
// 对调、head6 与随机填充完全不同,长度字段却一样 —— 正合「只由 body 长度决定」。
// [10:16] 是没初始化的残留(这两条都是 …0e0e,而多数真包是 …0e0f),不参与比对。
func TestBuildHeader(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header string
		plain  string
		gcpSeq uint32
	}{{
		name:   "04:59 真包",
		header: "0001025c51af060708390a0b0c0d0e0e",
		plain: "46e9cd80000118878800" + "00000034" +
			"0a09" + "1000180308e622201e" + "1209" + "1000180308da22201d" +
			"2cbb82ba9f6d" + "7473663467" + "0c",
		gcpSeq: 95,
	}, {
		name:   "05:37 真包(同两只,位置对调)",
		header: "0001026751af060708390a0b0c0d0e0e",
		plain: "447bc18000011887" + "9800" + "00000036" +
			"1209" + "100008da221803201e" + "0a09" + "100008e6221803201d" +
			"477edc04cec9" + "7473663467" + "0c",
		gcpSeq: 100,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := hex.DecodeString(tc.header)
			if err != nil {
				t.Fatal(err)
			}
			p, err := hex.DecodeString(tc.plain)
			if err != nil {
				t.Fatal(err)
			}
			if err := CheckHeader(h, tc.gcpSeq, p); err != nil {
				t.Errorf("真头块不符合构造规则: %v", err)
			}
			got, err := BuildHeader(tc.gcpSeq, p, DefaultHeaderTail)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got[:10], h[:10]) {
				t.Errorf("构造出的头块 %x, 真包 %x(前 10 字节才有规则)", got, h)
			}
		})
	}
}

// TestBuildHeaderForOurSwap 钉住我们自己那条请求的长度字段:EncodeSwap 出来是 21 字节 body,
// 头块 [8:10] 应为 0x0809 ^ (21+26) = 0x0826。填错服务端收到即断,所以钉成金值。
func TestBuildHeaderForOurSwap(t *testing.T) {
	sw := Swap{From: Slot{Gid: 76, Box: 3, Pos: 1}, To: Slot{Gid: 416, Box: 3, Pos: 9}}
	body := EncodeSwap(sw)
	if len(body) != 21 {
		t.Fatalf("body = %d 字节, want 21", len(body))
	}
	plain, err := Plaintext(DefaultTemplate, OpChangePet, 52, body, make([]byte, PadLen(len(body))))
	if err != nil {
		t.Fatal(err)
	}
	h, err := BuildHeader(0x74, plain, DefaultHeaderTail)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := binary.BigEndian.Uint16(h[8:10]), uint16(0x0826); got != want {
		t.Errorf("[8:10] = %#04x, want %#04x", got, want)
	}
	if got, want := h[3], byte(0x74^3); got != want {
		t.Errorf("[3] = %#02x, want %#02x", got, want)
	}
}

// TestBuildHeaderWideSeq 钉住「序号按 32 位填」:只异或低字节的话,序号一过 255 就把 [2]
// 留成填充值,而一条会话轻松超过 255 个包 —— 那之后每一次注入都会被服务端判死。
func TestBuildHeaderWideSeq(t *testing.T) {
	plain, err := BuildPlain(DefaultTemplate, 7, Swap{
		From: Slot{Gid: 1, Box: 1, Pos: 1},
		To:   Slot{Gid: 2, Box: 1, Pos: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, seq := range []uint32{1, 255, 256, 357, 65535, 65536, 0x01020304} {
		h, err := BuildHeader(seq, plain, DefaultHeaderTail)
		if err != nil {
			t.Fatal(err)
		}
		if got := binary.BigEndian.Uint32(h[0:4]) ^ 0x00010203; got != seq {
			t.Errorf("序号 %d: 头块 [0:4] 解出 %d(%x)", seq, got, h[:4])
		}
		if err := CheckHeader(h, seq, plain); err != nil {
			t.Errorf("序号 %d: %v", seq, err)
		}
	}
}

// TestRenumberSeq 验证「顺延一条已加密上行包的序号」:密文里那份副本跟着改,
// 应用层明文一个字节都不许动。中转注入之后每条客户端包都要走这一步。
func TestRenumberSeq(t *testing.T) {
	key := bytes.Repeat([]byte{0x3c}, 16)
	sw := Swap{From: Slot{Gid: 76, Box: 3, Pos: 1}, To: Slot{Gid: 416, Box: 3, Pos: 9}}
	plain, err := BuildPlain(DefaultTemplate, 52, sw)
	if err != nil {
		t.Fatal(err)
	}
	// 跨过 256 的整数倍:低字节相同但高字节不同的那一对(511→767)是老实现的盲区
	for _, tc := range []struct{ from, to uint32 }{{7, 8}, {254, 257}, {300, 302}, {511, 512}, {511, 767}} {
		t.Run(fmt.Sprintf("%d→%d", tc.from, tc.to), func(t *testing.T) {
			header, err := BuildHeader(tc.from, plain, DefaultHeaderTail)
			if err != nil {
				t.Fatal(err)
			}
			body, err := Encrypt(key, header, plain)
			if err != nil {
				t.Fatal(err)
			}
			if err := RenumberSeq(key, body, tc.from, tc.to); err != nil {
				t.Fatal(err)
			}
			got, err := gcp.DecryptHeadBlock(key, body)
			if err != nil {
				t.Fatal(err)
			}
			if err := CheckHeader(got, tc.to, plain); err != nil {
				t.Errorf("改完的头块与新序号对不上: %v", err)
			}
			// 应用层明文必须原封不动 —— 只有头块那一个字节该变
			back, err := gcpLikeDecrypt(key, body)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(back, plain) {
				t.Errorf("明文被改动了:\n得到 %x\n应为 %x", back, plain)
			}
		})
	}
}

// gcpLikeDecrypt 按「内嵌 IV」那套把 body 解回应用层明文(与 internal/gcp 的读法一致)。
func gcpLikeDecrypt(key, body []byte) ([]byte, error) {
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(body)-16)
	cipher.NewCBCDecrypter(blk, body[:16]).CryptBlocks(out, body[16:])
	return out, nil
}
