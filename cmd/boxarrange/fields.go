package main

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/whoisnian/rocom-capture/internal/capture"
	"github.com/whoisnian/rocom-capture/internal/gcp"
	"github.com/whoisnian/rocom-capture/internal/relay"
)

// fieldStats 观察「不是硬规则、但换版本换客户端时想知道」的那几个字段。
//
// 与 stats 分工:那边验的是中转赖以工作的硬规则(填错就不能发包,不成立就停用注入),
// 这边不判对错,只把 docs/inject.md 第 4 节里还属于猜测的几条钉在真实数据上 ——
// 头块 [10:16] 的残留到底是不是「未初始化的栈字节」、s2c [6:10] 的「每会话恒定」在长会话上
// 还成不成立、客户端的请求号涨得多快(决定注入取 +100000 还有多少余量)。
//
// 这些都要**长会话**才看得出来:几分钟的样本里什么都是「恒定」的。
type fieldStats struct {
	tails       map[[6]byte]int // c2s 头块 [10:16],异或掉递增填充之后的残留
	tailsFull   bool            // 取值种类超过 tailsCap,已停止收新种类(计数仍继续)
	ids         map[uint32]int  // s2c 头块 [6:10],同样异或掉填充
	maxBody     int             // c2s protobuf body 最大长度
	first, last time.Time
	c2s, s2c    int
}

// tailsCap 兜住「残留真是随机垃圾」的情况:iOS 那台一秒内就能出二十几种,别让 map 无限涨。
const tailsCap = 4096

func (f *fieldStats) observe(m capture.Message) {
	if f.tails == nil {
		f.tails, f.ids = map[[6]byte]int{}, map[uint32]int{}
	}
	if f.first.IsZero() {
		f.first = m.Time
	}
	f.last = m.Time

	if m.Direction == gcp.S2C {
		f.s2c++
		if len(m.Header) == 16 {
			f.ids[binary.BigEndian.Uint32(m.Header[6:10])^0x06070809]++
		}
		return
	}
	f.c2s++
	if bl, ok := bodyLen(m.Plain); ok && bl > f.maxBody {
		f.maxBody = bl
	}
	if len(m.Header) != 16 {
		return
	}
	var t [6]byte
	for i := range t {
		t[i] = m.Header[10+i] ^ byte(10+i)
	}
	if _, seen := f.tails[t]; seen {
		f.tails[t]++
	} else if len(f.tails) < tailsCap {
		f.tails[t] = 1
	} else {
		f.tailsFull = true
	}
}

// report 打印观察结果。reqLo/reqHi 是客户端请求号的下界与上界(由 stats 那边数出来的),
// 用来算注入取号的提前量还剩多少余量。
func (f *fieldStats) report(reqLo, reqHi uint32) {
	if f.c2s == 0 && f.s2c == 0 {
		return
	}
	fmt.Println("== 字段观察(不判对错,只记分布;换版本/换客户端时看这里)")

	if n := len(f.tails); n > 0 {
		var top [6]byte
		max := 0
		for t, c := range f.tails {
			if c > max {
				top, max = t, c
			}
		}
		more := ""
		if f.tailsFull {
			more = fmt.Sprintf(",超过 %d 种后不再收新种类", tailsCap)
		}
		fmt.Printf("  头块 [10:16] 残留:%d 种取值%s,最常见 %x 占 %.1f%%\n",
			n, more, top, 100*float64(max)/float64(f.c2s))
		// 尾巴恒定与否是判断「未初始化残留 vs 某个小字段」的线索:恒定更像是个字段。
		if hi, ok := constTail(f.tails); ok {
			fmt.Printf("    其中 [14:16] 恒为 %#04x —— 这台客户端的尾巴是稳的\n", hi)
		} else {
			fmt.Printf("    [14:16] 也在变 —— 更像未初始化的栈字节\n")
		}
	}
	if n := len(f.ids); n > 0 {
		fmt.Printf("  s2c 头块 [6:10]:%d 种取值", n)
		for id, c := range f.ids {
			fmt.Printf(" %#08x(%d 条)", id, c)
		}
		if n == 1 {
			fmt.Printf(" —— 「每会话恒定」在本样本上成立")
		}
		fmt.Println()
	}
	fmt.Printf("  c2s protobuf body 最大 %d 字节(长度字段 %s)\n",
		f.maxBody, usedThirdByte(f.maxBody))

	span := f.last.Sub(f.first)
	if span <= 0 {
		return
	}
	h := span.Hours()
	fmt.Printf("  跨度 %s:上行 %.2f 包/秒、下行 %.2f 包/秒\n",
		span.Round(time.Second), float64(f.c2s)/span.Seconds(), float64(f.s2c)/span.Seconds())
	if reqHi > reqLo && h > 0 {
		rate := float64(reqHi-reqLo) / h
		fmt.Printf("  请求号 %d..%d,约 %.0f 号/小时 —— 注入取 +%d 的提前量够用 %.0f 小时\n",
			reqLo, reqHi, rate, relay.ReqSeqGap, float64(relay.ReqSeqGap)/rate)
	}
}

// lenBias 与 petbox 构造头块时用的偏置同值,这里只用来说明长度字段的第三字节有没有被用到。
const lenBias = 26

// bodyLen 取明文里 protobuf body 的长度(去掉 14 字节 internal header、填充与 6 字节尾)。
func bodyLen(plain []byte) (int, bool) {
	if len(plain) < 21 {
		return 0, false
	}
	tail := int(plain[len(plain)-1])
	if tail < 6 || tail > len(plain)-14 {
		return 0, false
	}
	return len(plain) - 14 - tail, true
}

// constTail 报告所有残留的末两字节是不是同一个值。
func constTail(tails map[[6]byte]int) (uint16, bool) {
	first, hi := true, uint16(0)
	for t := range tails {
		v := binary.BigEndian.Uint16(t[4:6])
		if first {
			hi, first = v, false
		} else if v != hi {
			return 0, false
		}
	}
	return hi, !first
}

// usedThirdByte 说明长度字段 [6:10] 的第三个字节有没有真被用到 —— 它是「这类字段本来就是
// u32 而非 u16」的旁证之一(见 docs/inject.md 3.2)。
func usedThirdByte(maxBody int) string {
	if maxBody+lenBias >= 1<<8 {
		return "第三字节 [8] 用到了"
	}
	return "包太小,第三字节 [8] 没用到"
}

// wireOrder 把一段 protobuf 按线格式走一遍,记下字段编号**出现的先后**,嵌套的展开成
// `1{2,3,1,4}`。protobuf 本不要求有序,服务器也不看:仓库里六份真包出了六种顺序,全被照单
// 全收。顺序**同一条会话里恒定、换一条会话就变**(见 docs/inject.md 3.5),不是客户端指纹 ——
// 所以「与真包逐字节一致」一般做不到,自检只比语义,这里打出来仅作这一条会话的观察值。
//
// 只展开一层(0x1887 的 ori_info/tar_info 就一层),解不动就返回已经认出的部分。
func wireOrder(b []byte, nest bool) string {
	var sb []byte
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 {
			break
		}
		b = b[n:]
		num, typ := key>>3, key&7
		if len(sb) > 0 {
			sb = append(sb, ',')
		}
		sb = append(sb, []byte(fmt.Sprint(num))...)

		var val []byte
		switch typ {
		case 0: // varint
			_, n := binary.Uvarint(b)
			if n <= 0 {
				return string(sb)
			}
			b = b[n:]
		case 1: // 64 位
			if len(b) < 8 {
				return string(sb)
			}
			b = b[8:]
		case 2: // 长度前缀
			l, n := binary.Uvarint(b)
			if n <= 0 || uint64(len(b[n:])) < l {
				return string(sb)
			}
			val, b = b[n:n+int(l)], b[n+int(l):]
		case 5: // 32 位
			if len(b) < 4 {
				return string(sb)
			}
			b = b[4:]
		default:
			return string(sb)
		}
		if typ == 2 && nest {
			if inner := wireOrder(val, false); inner != "" {
				sb = append(sb, '{')
				sb = append(sb, []byte(inner)...)
				sb = append(sb, '}')
			}
		}
	}
	return string(sb)
}
