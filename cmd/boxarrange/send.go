package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/whoisnian/rocom-capture/internal/gcp"
	"github.com/whoisnian/rocom-capture/internal/pet"
	"github.com/whoisnian/rocom-capture/internal/petbox"
	"github.com/whoisnian/rocom-capture/internal/relay"
)

type sendOpts struct {
	listen    string
	upstream  string
	yes       bool
	relayOnly bool
}

// runSend 真发:先把连接接管下来,再按节奏逐条换位,每条都核对回包。
// relayOnly 时只接管转发、一条都不注入,用来单独验「接管连接本身不会把游戏搞坏」。
func runSend(o simOpts, n sendOpts) {
	var p *plan
	if !n.relayOnly {
		var err error
		if p, err = buildPlan(o); err != nil {
			fail("%v", err)
		}
		p.header(o.pacer)
		if len(p.swaps) == 0 {
			return
		}
	}
	printNftHelp(n)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ready := make(chan *relay.Conn, 4)
	gw := &relay.Gateway{
		Listen:   n.listen,
		Upstream: n.upstream,
		Logf:     func(format string, a ...any) { fmt.Printf("· "+format+"\n", a...) },
	}
	errc := make(chan error, 1)
	go func() { errc <- gw.Run(ctx, ready) }()

	fmt.Printf("在 %s 上等游戏连接进来…(Ctrl-C 退出)\n", n.listen)
	if n.relayOnly {
		fmt.Println("只中转、不注入。接上以后正常玩几分钟:开仓库、翻页、接任务、传送,")
		fmt.Println("看有没有卡住不响应或掉线。")
	}

	// 一条连接结束**绝不能**让进程退出:nft 规则还挂着,8195 仍被重定向到这个端口,
	// 这边一没了游戏就再也连不上(connection refused),表现为持续网络错误。
	// 所以一直守着,游戏断了自己重连,我们照样接管下一条。
	var started atomic.Bool
	for {
		select {
		case conn := <-ready:
			go watch(ctx, conn)
			if !n.relayOnly {
				go runOnce(ctx, conn, p, o, n, &started)
			}
		case err := <-errc:
			if err != nil {
				fail("中转出错: %v", err)
			}
			return
		case <-ctx.Done():
			fmt.Println("\n退出中转。别忘了删掉重定向规则:sudo nft delete table ip rocom")
			return
		}
	}
}

// runOnce 只让第一条拿到密钥的连接执行计划:计划是按库里的快照算的,跑两遍就错位了。
func runOnce(ctx context.Context, conn *relay.Conn, p *plan, o simOpts, n sendOpts, started *atomic.Bool) {
	if !started.CompareAndSwap(false, true) {
		return // 已经有一条在跑了
	}
	if ok, checked := conn.HeaderRuleOK(); !ok {
		fmt.Println("· 头块规则在这条连接上不成立,不注入。抓一份包跑 -selftest 看是哪一条变了。")
		return
	} else if checked == 0 {
		fmt.Println("· 还没见到客户端的请求,头块规则无从核对 —— 在游戏里随便点两下再回车。")
	}
	if !n.yes && !confirmFirstStep(p) {
		return
	}
	execute(ctx, conn, p, o)
}

// watch 跟一条连接到死,期间报转发计数,结束时说清是谁先挂的、最后在发什么。
// 断得不明不白最难查,所以宁可多打几行。
func watch(ctx context.Context, conn *relay.Conn) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			c2s, s2c, injected := conn.Stats()
			ok, n := conn.HeaderRuleOK()
			mark := "✓"
			if !ok {
				mark = "✗ 已停用注入"
			}
			fmt.Printf("· 转发 上行 %d / 下行 %d 包,注入 %d,头块规则 %s(核过 %d 条)\n",
				c2s, s2c, injected, mark, n)
		case <-conn.Done():
			c2s, s2c, injected := conn.Stats()
			fmt.Printf("\n· 连接结束:%s\n", conn.Reason())
			fmt.Printf("  共转发 上行 %d / 下行 %d 包,注入 %d\n", c2s, s2c, injected)
			if tr := conn.Trace(); len(tr) > 0 {
				fmt.Println("  断开前最后几条:")
				for _, line := range tr {
					fmt.Println("   ", line)
				}
			}
			fmt.Println("  仍在监听,游戏重连会自动接管。")
			return
		case <-ctx.Done():
			return
		}
	}
}

// execute 逐条换位。**任何失败都只停下、不退进程** —— 进程一退,还挂着的 nft 规则
// 会把游戏彻底挡在门外(重定向到一个没人监听的端口),比没跑过还糟。
func execute(ctx context.Context, conn *relay.Conn, p *plan, o simOpts) {
	steps := len(p.swaps)
	if o.limit > 0 && o.limit < steps {
		steps = o.limit
	}
	width := len(strconv.Itoa(steps))
	start := time.Now()
	for i, sw := range p.swaps[:steps] {
		reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		rsp, err := conn.SendSwap(reqCtx, sw)
		cancel()
		if err != nil {
			p.step(i, len(p.swaps), width, sw, "✗ 发送失败")
			fmt.Printf("第 %d 步失败: %v —— 停在这里,已完成 %d 步,布局就停在这个状态。\n", i+1, err, i)
			return
		}
		if err := checkRsp(rsp, sw); err != nil {
			p.step(i, len(p.swaps), width, sw, "✗ "+err.Error())
			fmt.Printf("第 %d 步被服务器拒绝或落位不符,停在这里(已完成 %d 步)。\n", i+1, i)
			return
		}
		p.step(i, len(p.swaps), width, sw, "✓")

		if i+1 == steps {
			break
		}
		d := o.pacer.Next(i + 1)
		select {
		case <-time.After(d):
		case <-ctx.Done():
			fmt.Printf("已中断,完成 %d/%d 步。\n", i+1, len(p.swaps))
			return
		case <-conn.Done():
			fmt.Printf("连接断了,完成 %d/%d 步。已完成的换位是持久的,重连后仍在。\n", i+1, len(p.swaps))
			fmt.Println("重连之后先让抓包刷新一次库,再跑下一轮接着排。")
			return
		}
	}
	fmt.Printf("\n完成 %d/%d 步,耗时 %s\n", steps, len(p.swaps), round(time.Since(start)))
	fmt.Println("游戏里的宠物仓库可能还是旧画面,退出仓库再进去就是新顺序了。")
	fmt.Println("中转继续开着,玩完 Ctrl-C 退出即可。")
}

// checkRsp 核对回包:ret_code 必须为 0,且 box_pet_change 里两只宠物确实落到了对方的格位。
// 这一步是「服务器真的按我们说的做了」的唯一证据,别只看没报错。
func checkRsp(plain []byte, sw petbox.Swap) error {
	body := gcp.AppBody(gcp.S2C, plain)
	if body == nil {
		return fmt.Errorf("回包过短")
	}
	if code, msg := retInfo(body); code != 0 {
		if msg != "" {
			return fmt.Errorf("服务器返回 ret_code=%d %s", code, msg)
		}
		return fmt.Errorf("服务器返回 ret_code=%d", code)
	}
	got := map[uint32]petbox.Slot{}
	for _, e := range pet.ParseBoxMoves(body) {
		got[e.Gid] = petbox.Slot{Gid: e.Gid, Box: e.BoxID, Pos: e.Slot + 1}
	}
	for gid, want := range map[uint32]petbox.Slot{
		sw.From.Gid: {Gid: sw.From.Gid, Box: sw.To.Box, Pos: sw.To.Pos},
		sw.To.Gid:   {Gid: sw.To.Gid, Box: sw.From.Box, Pos: sw.From.Pos},
	} {
		g, ok := got[gid]
		if !ok {
			return fmt.Errorf("回包里没有 %d 的落位", gid)
		}
		if g.Box != want.Box || g.Pos != want.Pos {
			return fmt.Errorf("%d 落到了盒%d 格%d,预期盒%d 格%d", gid, g.Box, g.Pos, want.Box, want.Pos)
		}
	}
	return nil
}

// retInfo 取 Rsp.ret_info(字段 1)里的 ret_code(字段 1)与 ret_msg(字段 2)。
func retInfo(body []byte) (code int64, msg string) {
	inner, ok := subMessage(body, 1)
	if !ok {
		return 0, ""
	}
	for len(inner) > 0 {
		num, typ, n := protowire.ConsumeTag(inner)
		if n < 0 {
			return code, msg
		}
		inner = inner[n:]
		switch {
		case num == 1 && typ == protowire.VarintType:
			v, n := protowire.ConsumeVarint(inner)
			if n < 0 {
				return code, msg
			}
			code, inner = int64(int32(v)), inner[n:]
		case num == 2 && typ == protowire.BytesType:
			v, n := protowire.ConsumeBytes(inner)
			if n < 0 {
				return code, msg
			}
			msg, inner = string(v), inner[n:]
		default:
			n := protowire.ConsumeFieldValue(num, typ, inner)
			if n < 0 {
				return code, msg
			}
			inner = inner[n:]
		}
	}
	return code, msg
}

func subMessage(body []byte, field protowire.Number) ([]byte, bool) {
	for len(body) > 0 {
		num, typ, n := protowire.ConsumeTag(body)
		if n < 0 {
			return nil, false
		}
		body = body[n:]
		if num == field && typ == protowire.BytesType {
			v, n := protowire.ConsumeBytes(body)
			if n < 0 {
				return nil, false
			}
			return v, true
		}
		n = protowire.ConsumeFieldValue(num, typ, body)
		if n < 0 {
			return nil, false
		}
		body = body[n:]
	}
	return nil, false
}

// confirmFirstStep 把第一步要动的两只宠物报出来让人对着游戏核一眼。
//
// 计划是按 rocom.db 的**快照**算的,而快照可能过期、也可能是另一个账号的。拿着错误的 gid
// 去请求换位,在服务端看来就是「动不属于你的宠物」—— 那类请求被直接掐断连接毫不意外,
// 而且不会给 ret_code。所以开跑前先人眼对一次,比事后猜便宜得多。
func confirmFirstStep(p *plan) bool {
	sw := p.swaps[0]
	fmt.Println("\n第一步会动这两只 —— 请在游戏里翻到对应页,逐项核对:")
	fmt.Printf("    盒%d 第%d行第%d列  %s\n", sw.From.Box, (sw.From.Pos-1)/6+1, (sw.From.Pos-1)%6+1,
		p.info[sw.From.Gid].label(sw.From.Gid))
	fmt.Printf("    盒%d 第%d行第%d列  %s\n", sw.To.Box, (sw.To.Pos-1)/6+1, (sw.To.Pos-1)%6+1,
		p.info[sw.To.Gid].label(sw.To.Gid))
	fmt.Println("对不上就别继续:说明库里的快照过期了,或者当前登录的不是 -account 指定的账号。")
	return confirm("对得上、且宠物仓库已打开的话,回车开始(Ctrl-C 放弃):")
}

func confirm(prompt string) bool {
	fmt.Print(prompt)
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		fmt.Println()
		return false
	}
	return true
}

func printNftHelp(n sendOpts) {
	port := n.listen
	if len(port) > 0 && port[0] == ':' {
		port = port[1:]
	}
	fmt.Println("接管方式(需要 root,规则要在游戏建立连接之前就位):")
	fmt.Println("  sudo nft add table ip rocom")
	fmt.Println("  sudo nft add chain ip rocom pre '{ type nat hook prerouting priority dstnat; }'")
	fmt.Printf("  sudo nft add rule ip rocom pre ip saddr 10.42.0.0/24 tcp dport 8195 redirect to :%s\n", port)
	fmt.Println("  # 用完删掉: sudo nft delete table ip rocom")
	fmt.Println("saddr 写热点整个网段而不是某台手机的 IP:换台设备 IP 就变了,规则匹配不上时游戏是")
	fmt.Println("直连的 —— 日志一片正常,其实什么都没接管到。以「接管连接」那行确认真的接上了。")
	fmt.Println("规则加好后把游戏切后台再切回来(或重登)让它重新连一次,本工具才接得到。")
	fmt.Println("接管期间流量变成「手机↔网关」「网关↔服务器」两段,两段的一端都是网关自己的 IP,")
	fmt.Println("而 rocom-capture 实时抓包会跳过本机 IP(单臂网关去重),所以这段时间它多半抓不到东西。")
	fmt.Println()
}
