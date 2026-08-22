package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/whoisnian/rocom-capture/internal/capture"
	"github.com/whoisnian/rocom-capture/internal/gcp"
	"github.com/whoisnian/rocom-capture/internal/petbox"
)

// runSelftest 拿 pcap 里真实的 0x1887 请求回来验组包:把真包拆开,再用本工具重新编码,
// 与原字节比对。这是「能不能造出服务器认的字节」唯一有意义的证据,比自己跟自己对拍强。
// 同一个客户端应当逐字节一致;换客户端因 protobuf 字段顺序不同,只比语义与长度。
// 顺带用 internal/gcp 的解密/分帧把自己产出的完整包解回来,确认头部也拼对了。
//
// 可以一次给多份抓包,当成一条连续的流读 —— 轮转出来的长会话必须这样喂:密钥只在第一份的
// ACK 里,而高位包序号在最后几份(见 capture.RunOfflineFiles)。
func runSelftest(paths []string) {
	log.SetOutput(io.Discard) // 静音 capture 包的连接日志

	e := capture.NewEngine(8195)
	var readErr error
	done := make(chan struct{})
	go func() { defer close(done); readErr = e.RunOfflineFiles(paths...) }()

	var st stats
	var swaps [][]byte
	for m := range e.Out {
		st.observe(m)
		if m.Direction == gcp.C2S && m.Opcode == petbox.OpChangePet {
			swaps = append(swaps, m.Plain)
		}
	}
	// 路径打错(或 glob 加了引号没展开)时不要报成「没抓到 ACK」,那会让人去查抓包时机。
	<-done
	if readErr != nil {
		fail("读抓包失败:%v", readErr)
	}

	okAssume := st.report(len(paths))
	reportSwaps(swaps, false)
	if !okAssume {
		fail("协议假设有不成立的,别拿这个客户端直接 -send")
	}
}

// reportSwaps 拿收集到的真实 0x1887 请求逐条验组包。live 区分「这份抓包里没有」和
// 「嗅探期间没人拖宠物」两种空手而归 —— 后者只要在游戏里拖一只就有了。
func reportSwaps(swaps [][]byte, live bool) {
	fmt.Println()
	bad := 0
	for i, plain := range swaps {
		fmt.Printf("== 第 %d 条真实 %#04x 请求(明文 %d 字节)\n", i+1, petbox.OpChangePet, len(plain))
		if !checkOne(plain) {
			bad++
		}
		fmt.Println()
	}
	switch {
	case len(swaps) == 0 && live:
		fmt.Printf("嗅探期间没见到 c2s %#04x —— 组包这一半没验到。在游戏里手动拖一只宠物换位就有了。\n",
			petbox.OpChangePet)
	case len(swaps) == 0:
		fmt.Printf("这份包里没有 c2s %#04x —— 组包这一半没验到。想验的话在游戏里手动拖一只宠物换位再抓。\n",
			petbox.OpChangePet)
	case bad > 0:
		fail("组包自检失败:%d/%d 条对不上", bad, len(swaps))
	default:
		fmt.Printf("组包自检通过:%d 条真包全部重建成功\n", len(swaps))
	}
}

// runSelftestLive 在网卡上被动嗅探,把同一套协议假设**边玩边核**。
//
// 存在的理由:要验「序号超过 65536 之后头块还是不是这个规则」得连续玩两个多小时,而
// 中转那条路要在网关上再插一层重定向 —— 网关上已经有 clash 在 iptables mangle 里代理全部
// 流量,再叠一层不划算。这里一个字节都不发、什么规则都不动,与 rocom-capture 同样是纯被动
// 抓包,只是把消息喂给协议假设自检而不是入库。
//
// 需要 root(AF_PACKET)。Ctrl-C 打最终报告。
func runSelftestLive(iface, dbPath string, every time.Duration) {
	e := capture.NewEngine(8195)
	if ks, ok := openKeyStore(dbPath); ok {
		e.Keys = ks // 借 rocom-capture 已经落盘的会话密钥,好中途接上正在跑的连接
		fmt.Printf("会话密钥借用 %s(rocom-capture 落的盘),可以接上已经建立的连接。\n", dbPath)
	} else {
		fmt.Printf("打不开 %s,拿不到已有连接的密钥 —— 得在游戏建立连接之前就开着本命令。\n", dbPath)
	}
	errc := make(chan error, 1)
	go func() { errc <- e.RunLive(iface) }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("在 %s 上被动嗅探 8195(不发任何包,不改任何规则)。Ctrl-C 打最终报告。\n", iface)
	fmt.Println("要验序号高位字节的话,得让游戏**一条连接**连续跑两个多小时 —— 中途重连会归零,")
	fmt.Println("下面每次报的就是当前最长那条连接的序号上限(头块只在上行有,所以看上行那个数)。")
	fmt.Println()

	var st stats
	var swaps [][]byte
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case m, ok := <-e.Out:
			if !ok {
				st.report(0)
				reportSwaps(swaps, true)
				return
			}
			st.observe(m)
			// 组包那一半:嗅探期间玩家自己拖一只宠物,就能拿真包验这个客户端的字段顺序
			// (安卓与 iOS 的内层顺序不一样,见 docs/inject.md 3.5)。
			if m.Direction == gcp.C2S && m.Opcode == petbox.OpChangePet {
				swaps = append(swaps, m.Plain)
			}
		case <-t.C:
			c2sSeq, anySeq, _, total := st.maxSeq()
			mark := "✓"
			if len(st.hdrBad) > 0 {
				mark = "✗"
			}
			hi := ""
			if st.hdrHi > 0 {
				hi = fmt.Sprintf(",其中 %d 条 ≥65536", st.hdrHi)
			}
			fmt.Printf("· c2s %d 条,头块规则 %s(核过 %d 条%s),最长连接上行序号到 %d(双向 %d,共 %d 条连接)\n",
				st.c2sData, mark, st.hdrChecked, hi, c2sSeq, anySeq, total)
		case err := <-errc:
			fail("抓包失败: %v(AF_PACKET 需要 root)", err)
		case <-ctx.Done():
			fmt.Println()
			st.report(0)
			reportSwaps(swaps, true)
			return
		}
	}
}

// stats 汇总 internal/relay 赖以工作的那几条假设。换客户端、换游戏版本时先跑一遍:
// 这些假设是从抓包归纳出来的,不是协议文档写死的。
//
// 包序号与请求序号都是**每条 TCP 连接各自从 1 开始**的,所以按连接分开统计 —— 混在一起
// 看的话,游戏中途重连就会报出一堆并不存在的「重号」,而序号上限也会被看成一直在涨。
type stats struct {
	c2sData    int
	misaligned int            // 明文长度不是 16 倍数的
	badTail    int            // 尾部不是 [填充]["tsf4g"][尾长] 的
	shells     map[string]int // 外壳取值分布
	hdrChecked int            // 头块规则复核过的条数
	hdrHi      int            // 其中序号 ≥ 65536 的条数(高位字节真被用到的那部分)
	hdrBad     []string       // 不符合头块规则的
	conns      map[string]*connStat
	order      []string   // 连接出现顺序
	fields     fieldStats // 顺带观察几个「还属于猜测」的字段(见 fields.go)
}

// connStat 是一条 TCP 连接内的计数。
type connStat struct {
	seqHi  uint32         // 观察到的最大 GCP 包序号(两个方向合计)
	c2sSeq uint32         // 上行方向的最大 GCP 包序号
	reqs   []uint32       // 上行非零请求序号(按出现顺序)
	echoes map[uint32]int // 下行非零回声计数
	notify int            // 下行回声为 0 的(通知)
}

func (s *stats) conn(id string) *connStat {
	if s.conns == nil {
		s.conns, s.shells = map[string]*connStat{}, map[string]int{}
	}
	c := s.conns[id]
	if c == nil {
		c = &connStat{echoes: map[uint32]int{}}
		s.conns[id], s.order = c, append(s.order, id)
	}
	return c
}

func (s *stats) observe(m capture.Message) {
	if len(m.Plain) < 14 {
		return
	}
	s.fields.observe(m)
	c := s.conn(m.Session)
	if m.Sequence > c.seqHi {
		c.seqHi = m.Sequence
	}
	if m.Direction == gcp.S2C {
		if echo := binary.BigEndian.Uint32(m.Plain[6:10]); echo == 0 {
			c.notify++
		} else {
			c.echoes[echo]++
		}
		return
	}
	s.c2sData++
	// 头块只有上行才有,所以「规则验到哪个序号区间」必须单看上行:下行包量是上行的两倍多,
	// 混在一起会把「下行到过 8 万」误当成「上行验过 65536 以上」。
	if m.Sequence > c.c2sSeq {
		c.c2sSeq = m.Sequence
	}
	if len(m.Plain)%16 != 0 {
		s.misaligned++
	}
	// 头块规则:包序号与长度都得对得上。这是**自己造包才碰得到**的一条,填错服务端收到即断开,
	// 而被动解析永远看不出问题 —— 所以必须在真包上核,不能只靠自己跟自己对拍。
	if len(m.Header) == 16 {
		s.hdrChecked++
		// 单独数「序号 ≥ 65536 的那部分」:跨越边界的样本里,总数说明不了高位区验过多少条。
		if m.Sequence >= 1<<16 {
			s.hdrHi++
		}
		if err := petbox.CheckHeader(m.Header, m.Sequence, m.Plain); err != nil {
			if len(s.hdrBad) < 5 {
				s.hdrBad = append(s.hdrBad, fmt.Sprintf("GCP序号 %d op=%#04x: %v", m.Sequence, m.Opcode, err))
			}
		}
	}
	if !validTail(m.Plain) {
		s.badTail++
	}
	if req := binary.BigEndian.Uint32(m.Plain[10:14]); req != 0 {
		c.reqs = append(c.reqs, req)
		s.shells[hex.EncodeToString(m.Plain[0:6])+"/"+hex.EncodeToString(m.Plain[8:10])]++
	}
}

// maxSeq 返回所有连接里最大的包序号:c2s 一份、两个方向合计一份,以及 c2s 那份属于第几条连接。
// 关键是**单条连接**能到多高:序号每条连接重新从 1 数,抓够时长不等于序号够大。
// 判断头块规则验到哪个区间只能看 c2s 那份(见 observe 里的说明)。
func (s *stats) maxSeq() (c2sSeq, anySeq uint32, which, total int) {
	for i, id := range s.order {
		c := s.conns[id]
		if c.c2sSeq > c2sSeq {
			c2sSeq, which = c.c2sSeq, i+1
		}
		if c.seqHi > anySeq {
			anySeq = c.seqHi
		}
	}
	return c2sSeq, anySeq, which, len(s.order)
}

func validTail(plain []byte) bool {
	n := int(plain[len(plain)-1])
	if n < 6 || n > len(plain)-14 {
		return false
	}
	tail := plain[len(plain)-n:]
	return string(tail[len(tail)-6:len(tail)-1]) == "tsf4g"
}

// report 打印各项假设是否成立,返回是否全部通过。
func (s *stats) report(nFiles int) bool {
	ok := true
	bad := func(format string, a ...any) {
		ok = false
		fmt.Printf("  ✗ "+format+"\n", a...)
	}

	files := ""
	if nFiles > 1 {
		files = fmt.Sprintf(",%d 份抓包连读", nFiles)
	}
	fmt.Printf("== 协议假设自检(c2s DATA %d 条%s)\n", s.c2sData, files)
	if s.c2sData == 0 {
		bad("一条 c2s DATA 都没解出来 —— 抓包没抓到 0x1002 ACK?没密钥就什么都验不了")
		return false
	}
	if s.misaligned == 0 {
		fmt.Printf("  ✓ 明文长度全部 16 字节对齐(%d/%d)\n", s.c2sData, s.c2sData)
	} else {
		bad("有 %d/%d 条明文长度不是 16 的倍数 —— 填充对齐的前提不成立", s.misaligned, s.c2sData)
	}
	if s.badTail == 0 {
		fmt.Printf("  ✓ 尾部都是 [填充][\"tsf4g\"][尾长]\n")
	} else {
		bad("有 %d 条尾部结构对不上", s.badTail)
	}
	switch {
	case s.hdrChecked == 0:
		bad("一条头块都没取到 —— 拿不到密钥就核不了这一条")
	case len(s.hdrBad) == 0:
		fmt.Printf("  ✓ 头块 [0:4]=包序号、[4:6]=0x55aa、[6:10]=body 长度+26 全部成立(%d 条)\n", s.hdrChecked)
		// 序号高位字节要真被用到过,这条规则才算在那个区间验过(见 docs/inject.md 3.2)。
		// 看的是**单条连接**的上限:序号每条连接重新从 1 数,重连一次就归零。
		c2sSeq, anySeq, which, total := s.maxSeq()
		where := fmt.Sprintf("(共 %d 条连接,最长的是第 %d 条;双向合计到过 %d)", total, which, anySeq)
		switch {
		case c2sSeq >= 1<<24:
			fmt.Printf("    单条连接上行包序号到过 %d%s —— [0:4] 四个字节都被用到过,序号是完整 u32 已实测\n", c2sSeq, where)
			fmt.Printf("      其中 %d 条上行包序号 ≥65536,头块规则同样成立\n", s.hdrHi)
		case c2sSeq >= 1<<16:
			fmt.Printf("    单条连接上行包序号到过 %d%s —— 用到了 [1],序号至少 24 位已实测,[0] 仍未覆盖\n", c2sSeq, where)
			fmt.Printf("      其中 %d 条上行包序号 ≥65536,头块规则同样成立\n", s.hdrHi)
		default:
			fmt.Printf("    ~ 单条连接上行包序号只到 %d%s,[0:2] 从没被用到 —— 这份样本验不了 65536 以上\n", c2sSeq, where)
		}
	default:
		bad("有头块不符合规则(共核 %d 条),这个版本改了头块格式,别拿它注入:", s.hdrChecked)
		for _, e := range s.hdrBad {
			fmt.Printf("      %s\n", e)
		}
	}

	// 请求序号:只看有没有重号 —— 注入取的号必须落在客户端号段之外(见 relay.ReqSeqGap)。
	// **不要求递增**:客户端会给成批发的请求预分配号段,插进来的单条请求抢到大号却先发出去。
	nReq, dup, desc, multi, miss, nEcho, nNotify := 0, 0, 0, 0, 0, 0, 0
	var lo, hi uint32
	for _, id := range s.order {
		c := s.conns[id]
		seen := map[uint32]bool{}
		for i, r := range c.reqs {
			if lo == 0 || r < lo {
				lo = r
			}
			if r > hi {
				hi = r
			}
			if seen[r] {
				dup++
			}
			if i > 0 && r < c.reqs[i-1] {
				desc++
			}
			seen[r] = true
			if c.echoes[r] == 0 {
				miss++
			}
		}
		for _, n := range c.echoes {
			if n > 1 {
				multi++
			}
		}
		nReq, nEcho, nNotify = nReq+len(c.reqs), nEcho+len(c.echoes), nNotify+c.notify
	}
	if nReq == 0 {
		bad("没有带请求序号的上行包,序号规律无从验证")
	} else {
		if dup == 0 {
			fmt.Printf("  ✓ 请求序号 %d..%d 共 %d 条无重号(其中 %d 处不按发送顺序递增)\n",
				lo, hi, nReq, desc)
		} else {
			bad("请求序号有 %d 处重号 —— 认领回包按 (opcode, 号) 配对,重号会认错", dup)
		}
		switch {
		case multi > 0:
			bad("有 %d 个请求号收到了多条回包 —— 认领回包的逻辑要改成不能只认第一条", multi)
		case miss > 0:
			fmt.Printf("  ~ %d 条请求没等到回包(抓包尾部截断也会这样,不一定是问题)\n", miss)
		default:
			fmt.Printf("  ✓ 请求与回包严格 1:1(%d 对,另有 %d 条回声为 0 的通知)\n", nEcho, nNotify)
		}
	}

	// 外壳:取值越杂越证明它是没初始化的填充,relay 从连接里学是对的
	fmt.Printf("  · 请求外壳(明文 [0:6]/[8:10])共 %d 种取值", len(s.shells))
	if len(s.shells) > 1 {
		fmt.Println(" —— 逐包不同,确认该学不该写死")
	} else {
		fmt.Println(" —— 这份包里恒定,注入会照抄它")
	}

	s.fields.report(lo, hi)
	return ok
}

func checkOne(plain []byte) bool {
	p, err := petbox.ParsePlain(plain)
	if err != nil {
		fmt.Println("  解析真包失败:", err)
		return false
	}
	sw := p.Swap
	fmt.Printf("  请求序号 %d,填充 %d 字节,外壳 %x/%x\n", p.ReqSeq, len(p.Pad), p.Template.Head6, p.Template.Tag)
	fmt.Printf("  交换 %s\n", sw)
	// 内层字段顺序每条会话重新定,只当这一条会话的观察值(见 wireOrder)。
	if tail := int(plain[len(plain)-1]); tail >= 6 && tail <= len(plain)-14 {
		fmt.Printf("  内层字段顺序 %s\n", wireOrder(plain[14:len(plain)-tail], true))
	}

	// 外壳与填充都用真包里的那份:它们本就是逐包不同的垃圾/随机字节,不是我们要验的东西。
	got, err := petbox.Plaintext(p.Template, petbox.OpChangePet, p.ReqSeq, petbox.EncodeSwap(sw), p.Pad)
	if err != nil {
		fmt.Println("  重新组包失败:", err)
		return false
	}
	switch {
	case bytes.Equal(got, plain):
		fmt.Println("  ✓ 明文逐字节一致")
	case len(got) == len(plain) && sameSwap(got, plain):
		// protobuf 不要求字段有序,而不同客户端的顺序确实不同(安卓 08-15 是 2,3,1,4,
		// iOS 08-22 是 4,3,2,1)。语义与长度都对上就够了,服务器不看顺序。
		fmt.Println("  ✓ 语义与长度一致,仅 protobuf 字段顺序不同(这份包与本工具不是同一个客户端)")
	default:
		fmt.Println("  ✗ 明文对不上")
		fmt.Printf("    真包 %s\n    重建 %s\n", hex.EncodeToString(plain), hex.EncodeToString(got))
		return false
	}

	// 头部与加密走一遍回环:用 internal/gcp(线上验证过的解析路径)把自己产出的包解回来。
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		fmt.Println("  生成密钥失败:", err)
		return false
	}
	wire, _, err := petbox.Build(key, p.Template, 12345, p.ReqSeq, sw)
	if err != nil {
		fmt.Println("  组完整包失败:", err)
		return false
	}
	pkts, rest := gcp.Deframe(wire)
	if len(pkts) != 1 || len(rest) != 0 {
		fmt.Printf("  ✗ 分帧结果异常:%d 个包、剩 %d 字节\n", len(pkts), len(rest))
		return false
	}
	if pkts[0].Command != gcp.CmdData || pkts[0].Sequence != 12345 {
		fmt.Printf("  ✗ 头部字段异常:command=%#04x sequence=%d\n", pkts[0].Command, pkts[0].Sequence)
		return false
	}
	back, err := gcp.DecryptData(key, pkts[0].Body)
	if err != nil {
		fmt.Println("  ✗ 解密失败:", err)
		return false
	}
	// Build 用的是随机填充,所以不逐字节比:长度、外壳/序号那 14 字节、解出来的交换,对上即可。
	if len(back) != len(plain) || !bytes.Equal(back[:14], plain[:14]) || !sameSwap(back, plain) {
		fmt.Printf("  ✗ 回环解出的明文对不上:%s\n", hex.EncodeToString(back))
		return false
	}
	op, _ := gcp.AppOpcode(gcp.C2S, back)
	if op != petbox.OpChangePet {
		fmt.Printf("  ✗ 回环解出的 opcode 是 %#04x\n", op)
		return false
	}
	fmt.Printf("  ✓ 完整包 %d 字节:分帧 → 解密 → opcode %#04x 全部对上\n", len(wire), op)
	return true
}

// sameSwap 比较两条明文解出来的内容(外壳、请求序号、交换),用于 protobuf 字段顺序
// 不同、字节比不出来的情况。
func sameSwap(a, b []byte) bool {
	pa, err := petbox.ParsePlain(a)
	if err != nil {
		return false
	}
	pb, err := petbox.ParsePlain(b)
	if err != nil {
		return false
	}
	return pa.Template == pb.Template && pa.ReqSeq == pb.ReqSeq && pa.Swap == pb.Swap
}
