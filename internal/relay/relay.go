// Package relay 是一个 GCP 感知的透明中转:把手机到游戏服务器 8195 的连接接管下来原样
// 转发,同时能在中间插进自己的请求、并把自己那份回包认领走。
//
// 这是仓库里唯一会**主动发包**的东西,与抓包链路完全分开:internal/capture 那边仍旧是
// 「不读内存、不注入进程、只解析网络流量」,本包不在那条链路上,只有 cmd/boxarrange -send
// 会把它跑起来。
//
// 为什么必须做成中转,而不是往现成的连接里塞包:GCP 头里的 sequence 是每个方向一个的
// 逐包计数器(实测两个方向都从 1 起、每包 +1,SYN/AUTH/心跳/DATA 共用同一个计数)。
// 凭空插一个包就会把客户端后续包的编号顶掉。做成中转之后改编号只是给上行包的 raw[9:13]
// 加上已注入的包数,除这 4 个字节以外转发的字节与原包一模一样(连 HEAD.extend 都原样带过去)。
//
// 两条必须守住的不变量:
//
//   - **绝不改客户端的应用层请求序号**(明文 [10:14]),理由见 ReqSeqGap。
//   - **一次 Read 里的包必须一次写出去**,不能逐包各写一次,见 forward。
package relay

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/whoisnian/rocom-capture/internal/gcp"
	"github.com/whoisnian/rocom-capture/internal/petbox"
)

// ReqSeqGap 是自己的请求序号相对客户端当前进度的提前量。
// 导出是为了让 boxarrange 的字段观察能拿它算「按客户端实测的涨号速度还够用多久」。
//
// 应用层请求序号(明文 c2s [10:14])是请求与回包的配对标记,服务器把它原样抄回下行头 [6:10]。
// **绝对不要去改客户端那个号。** 它本来就不按发送顺序递增:客户端给要成批发的请求预分配
// 号段,而单独插进来的请求抢到大号却先发出去(实测 1 2 3 4 5 6 20 7 8 …)。按到达顺序重编号
// 会把那条抢跑的包连锁位移,实测每次都在它之后约 25ms 被 SSTOP。
//
// 既然客户端自己的号都不连续,服务器显然不在乎连续性,那就没有理由去动它。自己的请求从
// 「客户端当前最大号 + 这个提前量」取号:隔得足够远才不会撞上它预分配出去、马上要用的号
// (撞上就会误认领它的回包)。等回包只有几十毫秒,客户端不可能在这期间涨这么多号,
// 所以再长的会话也不会追上来。
const ReqSeqGap uint32 = 100000

// Gateway 在本地端口上等游戏连接进来。用法见 cmd/boxarrange。
type Gateway struct {
	Listen   string                        // 本地监听地址,如 ":4940"
	Upstream string                        // 覆盖上游地址;留空则用 SO_ORIGINAL_DST 取原始目的地
	Logf     func(format string, a ...any) // 可选日志
}

// Run 按 Listen 开监听并转发,直到 ctx 结束。
func (g *Gateway) Run(ctx context.Context, ready chan<- *Conn) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", g.Listen)
	if err != nil {
		return err
	}
	return g.Serve(ctx, ln, ready)
}

// Serve 在给定监听器上一直接受连接并转发,直到 ctx 结束。每条拿到会话密钥的连接送一次
// 进 ready(送不进去也不阻塞转发)。
func (g *Gateway) Serve(ctx context.Context, ln net.Listener, ready chan<- *Conn) error {
	defer ln.Close()
	go func() { <-ctx.Done(); ln.Close() }()

	for {
		cli, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go g.serve(ctx, cli, ready)
	}
}

func (g *Gateway) serve(ctx context.Context, cli net.Conn, ready chan<- *Conn) {
	dst := g.Upstream
	if dst == "" {
		orig, err := originalDst(cli)
		if err != nil {
			g.logf("取原始目的地失败(没配 nftables 重定向?): %v", err)
			cli.Close()
			return
		}
		dst = orig
	}
	up, err := new(net.Dialer).DialContext(ctx, "tcp", dst)
	if err != nil {
		g.logf("连上游 %s 失败: %v", dst, err)
		cli.Close()
		return
	}
	g.logf("接管连接 %s → %s", cli.RemoteAddr(), dst)

	c := &Conn{
		client:  cli,
		up:      up,
		logf:    g.logf,
		shells:  map[petbox.Template]int{},
		tails:   map[petbox.HeaderTail]int{},
		waiters: map[uint64]chan []byte{},
		ready:   make(chan struct{}),
		done:    make(chan struct{}),
	}
	c.run(ctx, ready)
}

func (g *Gateway) logf(format string, a ...any) {
	if g.Logf != nil {
		g.Logf(format, a...)
	}
}

// Conn 是一条被接管的游戏连接。
//
// 注入曾经每次都会赔上这条连接:服务端接受并执行换位(回 0x1888、游戏里确实生效),随后在
// 收到注入之后**第一条客户端 DATA** 约一个 RTT 之后 SSTOP。根因是 GCP 包序号在密文头块里
// 还有一份副本,而这里为了给注入腾号只顺延了 GCP 头那一份 —— 见 noteC2S 与 petbox.RenumberSeq,
// 两份一起改之后线上实测不再掉线。
//
// 换位是持久的:断开重连后仍在,所以万一还是被踢,已完成的步数不会白跑。
type Conn struct {
	client, up net.Conn
	logf       func(string, ...any)

	mu       sync.Mutex
	key      []byte
	lastC2S  uint32                    // 最后一个写给服务器的上行包序号
	injected uint32                    // 已注入包数(上行包序号偏移)
	reqMax   uint32                    // 客户端用到的最大请求序号(只观察,不改写)
	lastReq  uint32                    // 我们自己用掉的最大请求序号
	shells   map[petbox.Template]int   // 观察到的请求外壳取值计数(见 template)
	tails    map[petbox.HeaderTail]int // 观察到的头块尾巴取值计数(见 headerTail)
	hdrSeqOK int                       // 头块里的包序号与外层对上的客户端包数
	hdrSeqNG uint32                    // 对不上的那个序号(非 0 表示规则在本连接上不成立)
	waiters  map[uint64]chan []byte
	nC2S     uint64 // 转发过的上下行包数,供上层确认「流量确实在走这儿」
	nS2C     uint64

	trace []traceEntry // 最近若干条包,连接断掉时打出来定位断在哪一步

	readySink chan<- *Conn // 拿到密钥时把自己送进去(送不进也不阻塞转发)
	readyOnce sync.Once
	ready     chan struct{}
	doneOnce  sync.Once
	done      chan struct{}
	err       error
	closedBy  string
	sawSStop  bool // 收到过 0x5002:服务端主动断开,不是网络故障
}

// cmdName 给 GCP 包类型一个名字。断线报告里 0x5002 与 0x4013 的区别至关重要:
// 前者是服务端明确踢人,后者只是普通数据,别让人对着裸数字猜。
func cmdName(cmd uint16) string {
	switch cmd {
	case gcp.CmdSYN:
		return "SYN"
	case gcp.CmdACK:
		return "ACK"
	case gcp.CmdAuthReq:
		return "AUTH_REQ"
	case gcp.CmdAuthRsp:
		return "AUTH_RSP"
	case gcp.CmdData:
		return "DATA"
	case gcp.CmdHeartbeat:
		return "心跳"
	case gcp.CmdSStop:
		return "SSTOP(服务端断开)"
	default:
		return fmt.Sprintf("cmd=%#04x", cmd)
	}
}

// traceEntry 是一条转发记录。连接断了要能说清「断之前最后在发什么」,
// 否则只剩一个 EOF,连是谁先挂的都不知道。
type traceEntry struct {
	at   time.Time
	c2s  bool
	cmd  uint16
	op   uint16 // 仅 DATA 且已有密钥时有效
	size int
}

const traceKeep = 16

// note 记一条转发记录(调用方须持有 c.mu)。
func (c *Conn) note(c2s bool, cmd, op uint16, size int) {
	c.trace = append(c.trace, traceEntry{at: time.Now(), c2s: c2s, cmd: cmd, op: op, size: size})
	if len(c.trace) > traceKeep {
		c.trace = append(c.trace[:0], c.trace[1:]...)
	}
}

// Trace 返回最近若干条转发记录的可读形式。
func (c *Conn) Trace() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.trace))
	for _, e := range c.trace {
		arrow := "←"
		if e.c2s {
			arrow = "→"
		}
		op := ""
		if e.cmd == gcp.CmdData {
			op = fmt.Sprintf(" op=%#04x", e.op)
		}
		out = append(out, fmt.Sprintf("%s %s %s%s %dB",
			e.at.Format("15:04:05.000"), arrow, cmdName(e.cmd), op, e.size))
	}
	return out
}

// Reason 说明连接是怎么结束的:谁先挂的、什么错。EOF 是对端正常关闭,不是故障。
func (c *Conn) Reason() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	who := c.closedBy
	if who == "" {
		who = "未知"
	}
	if c.sawSStop {
		// 服务器发过 SSTOP 才断的:这是服务端「决定」踢掉这条连接,不是链路故障,也不是
		// 转发出错。注入之后出现的话,先看 trace 里断点是不是又落在某条客户端包之后
		// (那是 Conn 说明里那条老毛病的形状),再往账号/会话状态那边查。
		return "服务器发了 SSTOP 主动断开(不是网络故障),随后 " + who + " 关闭"
	}
	switch {
	case c.err != nil:
		return who + " 出错: " + c.err.Error()
	default:
		return who + " 正常关闭(EOF/连接已关)"
	}
}

// markReady 在首次拿到密钥时放行等待方。
func (c *Conn) markReady() {
	c.readyOnce.Do(func() {
		close(c.ready)
		if c.readySink != nil {
			select {
			case c.readySink <- c:
			default:
			}
		}
	})
}

// Ready 在拿到会话密钥后关闭。拿不到密钥就没法加密自己的包,也就没法注入。
func (c *Conn) Ready() <-chan struct{} { return c.ready }

// Done 在连接结束后关闭,Err 给出原因。
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err 返回连接结束的原因(正常收尾为 nil)。
func (c *Conn) Err() error { c.mu.Lock(); defer c.mu.Unlock(); return c.err }

func (c *Conn) run(ctx context.Context, ready chan<- *Conn) {
	defer c.up.Close()
	defer c.client.Close()

	c.readySink = ready
	go func() {
		<-ctx.Done()
		c.client.Close()
		c.up.Close()
	}()
	// 上行:客户端 → 服务器
	go func() {
		c.finish("客户端侧", c.pump(c.client, true))
		c.up.Close()
	}()
	// 下行:服务器 → 客户端(拿到密钥就在这条路上)
	c.finish("服务器侧", c.pump(c.up, false))
}

func (c *Conn) finish(who string, err error) {
	c.doneOnce.Do(func() {
		c.mu.Lock()
		c.closedBy = who
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			c.err = err
		}
		for _, ch := range c.waiters {
			close(ch)
		}
		c.waiters = map[uint64]chan []byte{}
		c.mu.Unlock()
		close(c.done)
	})
}

// pump 读一个方向的字节流,按 GCP 包切开处理,再整批写给对端。
func (c *Conn) pump(src net.Conn, c2s bool) error {
	buf := make([]byte, 0, 64*1024)
	tmp := make([]byte, 32*1024)
	for {
		n, err := src.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			used, ferr := c.forward(buf, c2s)
			buf = buf[used:]
			if ferr != nil {
				return ferr
			}
			// 缓冲整体前移,免得底层数组一直长
			if len(buf) == 0 && cap(buf) > 1<<20 {
				buf = make([]byte, 0, 64*1024)
			}
		}
		if err != nil {
			return err
		}
	}
}

// forward 把这次读到的字节切成完整包、逐个处理,再**一次性**写给对端,返回已消费字节数。
//
// 「一次 Read 攒成一次写」不是性能优化,是行为正确性:此前逐个 GCP 包各写一次,加上 Go
// 默认的 TCP_NODELAY,手机原本塞在一个 TCP 段里的十条消息到上游就成了十个小段,分段特征
// 与直连差得很远,而那时每次都会在客户端那条环境上报之后约 25ms 被服务器 SSTOP。
func (c *Conn) forward(buf []byte, c2s bool) (used int, err error) {
	dst := c.client
	if c2s {
		dst = c.up
		// 上行与注入(SendSwap)写的是同一条 socket,而注入要在锁里分配包序号。
		// 分配与写出之间若被另一批插进去,服务器看到的序号就乱了,所以整段持锁。
		// 注意 c.mu 不可重入:走上行的函数一律不得再自己加锁。
		c.mu.Lock()
		defer c.mu.Unlock()
	}
	for {
		pkt, _, perr := nextFrame(buf[used:])
		if perr != nil {
			return used, perr
		}
		if pkt == nil {
			break
		}
		if c2s {
			// 改不动序号副本就断掉整条连接,而且**这条包不能发**:带着自相矛盾的序号
			// 上线会被服务端判死整个会话,比在这儿断掉难查得多。它前面那截照常写出去。
			if nerr := c.noteC2S(pkt); nerr != nil {
				if used > 0 {
					if _, werr := dst.Write(buf[:used]); werr != nil {
						return used, werr
					}
				}
				return used, nerr
			}
		} else {
			c.handleS2C(pkt)
		}
		used += len(pkt)
	}
	if used > 0 {
		if _, err = dst.Write(buf[:used]); err != nil {
			return used, err
		}
	}
	return used, nil
}

// nextFrame 从缓冲头部切出一个完整 GCP 包(返回的是 buf 的切片),rest 是剩余字节数。
// 与 gcp.Deframe 的差别:保留原始字节、不做失步重同步 —— 中转必须原样转发,
// 一旦对不上 magic 说明流已经错位,继续猜只会把客户端的连接搞坏,直接断掉更安全。
func nextFrame(buf []byte) (pkt []byte, rest int, err error) {
	if len(buf) < gcp.FixedHdrLen {
		return nil, len(buf), nil
	}
	if buf[0] != gcp.Magic[0] || buf[1] != gcp.Magic[1] {
		return nil, 0, fmt.Errorf("relay: GCP 流失步(头两字节 %02x%02x)", buf[0], buf[1])
	}
	hdrLen := int(binary.BigEndian.Uint32(buf[13:17]))
	bodyLen := int(binary.BigEndian.Uint32(buf[17:21]))
	if hdrLen < gcp.FixedHdrLen || hdrLen+bodyLen > 8*1024*1024 {
		return nil, 0, fmt.Errorf("relay: GCP 头长度不合理(hdr=%d body=%d)", hdrLen, bodyLen)
	}
	total := hdrLen + bodyLen
	if len(buf) < total {
		return nil, len(buf), nil
	}
	return buf[:total], len(buf) - total, nil
}

// noteC2S 改写一条上行包的序号并记账。调用方须持有 c.mu(见 forward)。
//
// 注意序号要改两处:GCP 头里那个明文的,以及密文头块里那份副本(见 petbox.RenumberSeq)。
// 只改前者的话,注入之后客户端的每一条 DATA 都带着两个互相矛盾的序号,服务端一对就
// 把会话判死 —— 三次实测都是注入后的**第一条客户端 DATA** 到达约一个 RTT 后被 SSTOP,
// 两份一起改之后不再复现。
func (c *Conn) noteC2S(pkt []byte) error {
	cmd := binary.BigEndian.Uint16(pkt[6:8])
	hdrLen := int(binary.BigEndian.Uint32(pkt[13:17]))

	var op uint16
	if cmd == gcp.CmdData && c.key != nil {
		c.checkHeaderSeq(pkt[hdrLen:], binary.BigEndian.Uint32(pkt[9:13]))
		// 只解第一个分组就够读到 opcode 与请求序号(都落在明文头 16 字节里)。
		if plain16, ok := peek16(c.key, pkt[hdrLen:]); ok {
			op = binary.BigEndian.Uint16(plain16[6:8])
			if req := binary.BigEndian.Uint32(plain16[10:14]); req != 0 {
				// 顺带把这条请求的外壳记下来,注入时照抄一个最常见的(见 template)。
				// 只学有请求序号的包:通知类(序号为 0)那批外壳取值明显是另一路。
				c.shells[petbox.Template{
					Head6: [6]byte(plain16[0:6]),
					Tag:   [2]byte(plain16[8:10]),
				}]++
				// 只观察不改写(见 ReqSeqGap 的注释:动它就会被踢)
				if req > c.reqMax {
					c.reqMax = req
				}
			}
		}
	}
	old := binary.BigEndian.Uint32(pkt[9:13])
	seq := old + c.injected
	if seq != old {
		binary.BigEndian.PutUint32(pkt[9:13], seq)
		// 密文里那份副本得跟着改。改不动就别把这条包发出去 —— 带着矛盾序号上线,
		// 服务端会把整条会话判死,比在这儿断掉难查得多。
		if cmd == gcp.CmdData && c.key != nil {
			if err := petbox.RenumberSeq(c.key, pkt[hdrLen:], old, seq); err != nil {
				return fmt.Errorf("relay: 改写包序号副本失败: %w", err)
			}
		}
	}
	c.lastC2S = seq
	c.nC2S++
	c.note(true, cmd, op, len(pkt))
	return nil
}

// checkHeaderSeq 拿客户端自己的包核一遍「头块 [0:4] = GCP 包序号」这条规则。
// 调用方须持有 c.mu(在 noteC2S 里,序号尚未改写)。
//
// 为什么要在运行时核:这条规则是从抓包归纳的,而抓包都是几分钟的分析样本,包序号最大只到
// 885 —— 序号的高两个字节在真包里**从没被用过**。真实一局能到十几个小时、二十几万个包,
// 一定会越过 65536 用到 [1]。把「u32」当成外推、让客户端每条包替我们验,比赌它对要好:
// 只要有一条对不上就说明这个版本的头块不是我们理解的样子,那就别注入 —— 猜错的代价是
// 服务端把整条会话判死。
func (c *Conn) checkHeaderSeq(body []byte, seq uint32) {
	h, err := gcp.DecryptHeadBlock(c.key, body)
	if err != nil {
		return
	}
	// 顺带把 [10:16] 那段客户端相关的残留学下来(见 petbox.HeaderTail)
	c.tails[petbox.HeaderTail(h[10:16])]++
	got, ok := petbox.HeaderSeq(h)
	if !ok {
		return
	}
	// 注意:上行包在中转手里会被顺延,这里拿到的是**改写前**的原始序号,与客户端自己写进
	// 头块的那份同源;注入之后客户端包的两份也仍应一致(RenumberSeq 会同步改)。
	if got == seq {
		c.hdrSeqOK++
		return
	}
	if c.hdrSeqNG == 0 {
		c.hdrSeqNG = seq
		c.logf("⚠ 头块里的包序号对不上:外层 %d,头块解出 %d(此前 %d 条都对得上)",
			seq, got, c.hdrSeqOK)
		c.logf("  这个版本的头块规则与我们理解的不一样,已停用注入。别硬发 —— 服务端会把整条会话判死。")
	}
}

// headerTail 取本连接观察到的最常见头块尾巴。与 template 同理:那 6 个字节逐客户端不同,
// 写死会在换客户端时对不上,照抄当前连接的最稳。调用方须持有 c.mu。
func (c *Conn) headerTail() petbox.HeaderTail {
	best, n := petbox.DefaultHeaderTail, 0
	for tail, cnt := range c.tails {
		if cnt > n {
			best, n = tail, cnt
		}
	}
	return best
}

// HeaderRuleOK 报告「头块规则在本连接上是否一直成立」,以及核过多少条。
func (c *Conn) HeaderRuleOK() (ok bool, checked int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hdrSeqNG == 0, c.hdrSeqOK
}

// template 取本连接观察到的最常见请求外壳。外壳那 8 个字节是客户端没初始化的填充
// (见 petbox.Template),写死会跨版本/跨会话失效,照抄本连接的最稳。
// 一条都没观察到(客户端从登录起一句请求都没发)才退回 petbox.DefaultTemplate。
// 调用方须持有 c.mu。
func (c *Conn) template() petbox.Template {
	best, n := petbox.DefaultTemplate, 0
	for tpl, cnt := range c.shells {
		if cnt > n {
			best, n = tpl, cnt
		}
	}
	return best
}

// handleS2C 处理一条下行包:截会话密钥、认领自己请求的回包、记账。
// 下行字节一律原样转发 —— 自己那条回包也照转给客户端,让它的本地状态跟着更新。
func (c *Conn) handleS2C(pkt []byte) {
	cmd := binary.BigEndian.Uint16(pkt[6:8])
	hdrLen := int(binary.BigEndian.Uint32(pkt[13:17]))

	if cmd == gcp.CmdSStop {
		c.mu.Lock()
		c.sawSStop = true
		c.mu.Unlock()
	}
	if cmd == gcp.CmdACK {
		if key, ok := gcp.ExtractKey(pkt[gcp.FixedHdrLen:hdrLen]); ok {
			c.mu.Lock()
			c.key = key
			c.mu.Unlock()
			c.logf("拿到会话密钥,可以注入了")
			c.markReady()
		}
	}

	var op uint16
	if cmd == gcp.CmdData {
		op = c.claimRsp(pkt[hdrLen:])
	}

	c.mu.Lock()
	c.nS2C++
	c.note(false, cmd, op, len(pkt))
	c.mu.Unlock()
}

// Stats 返回至今转发/注入的包数。只中转不注入地试水时,靠它确认流量真的在走这条路
// (nft 规则没生效的话游戏是直连的,一切正常但什么也没验证到)。
func (c *Conn) Stats() (c2s, s2c uint64, injected uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nC2S, c.nS2C, c.injected
}

// claimRsp 看一条下行 DATA 的回声号是不是我们自己请求的回包,是就解出来交给等待方。
// 返回该包的 opcode(仅供 trace 显示)。客户端自己的回包与通知(回声为 0)一律不碰。
//
// 先只解一个分组读 opcode/回声号,没人等这个号就不用解全包(宠物列表一条 44KB)。
func (c *Conn) claimRsp(body []byte) (opcode uint16) {
	c.mu.Lock()
	key := c.key
	c.mu.Unlock()
	if key == nil {
		return 0
	}
	head, ok := peek16(key, body)
	if !ok || !gcp.ValidPlain(gcp.S2C, head) {
		return 0
	}
	op := binary.BigEndian.Uint16(head[2:4])
	echo := binary.BigEndian.Uint32(head[6:10])
	if echo == 0 {
		return op
	}

	c.mu.Lock()
	ch, waiting := c.waiters[waiterKey(op, echo)]
	if waiting {
		delete(c.waiters, waiterKey(op, echo))
	}
	c.mu.Unlock()
	if !waiting {
		return op
	}

	plain, err := gcp.DecryptData(key, body)
	if err != nil {
		c.logf("自己的回包解密失败: %v", err)
		close(ch)
		return op
	}
	ch <- plain
	return op
}

func waiterKey(opcode uint16, reqSeq uint32) uint64 {
	return uint64(opcode)<<32 | uint64(reqSeq)
}

// peek16 只解密头 16 字节明文(CBC 的第一个分组只依赖 IV 与第一块密文)。
func peek16(key, body []byte) ([]byte, bool) {
	if len(body) < 32 {
		return nil, false
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, false
	}
	out := make([]byte, 16)
	cipher.NewCBCDecrypter(block, body[:16]).CryptBlocks(out, body[16:32])
	return out, true
}

// writeInjected 加密并写出一条自己的包。调用方须持有 c.mu。
func (c *Conn) writeInjected(plain, header []byte, op uint16) error {
	enc, err := petbox.Encrypt(c.key, header, plain)
	if err != nil {
		return err
	}
	seq := c.lastC2S + 1 // 必须与 BuildHeader 里用的那个一致(头块 [3] 由它异或而来)
	wire := petbox.Frame(seq, enc)
	if _, err := c.up.Write(wire); err != nil {
		return err
	}
	c.lastC2S = seq
	c.injected++
	// 注入的包也要进 trace,否则断线报告里只看得到客户端的包、看不见自己发了什么
	c.note(true, gcp.CmdData, op, len(wire))
	c.logf("注入 op=%#04x GCP序号=%d 共 %d 字节", op, seq, len(wire))
	return nil
}

// SendSwap 注入一次盒位交换,等到回包为止,返回回包的完整明文(含 internal header)。
// 回包按 (opcode, 请求序号) 认领 —— 认领只是把它交给这里,同一条包仍会转给客户端。
//
// 请求序号取「客户端当前最大号 + ReqSeqGap」,完全不碰客户端自己的号(理由见 reqSeqGap)。
// 回包 opcode 取请求 opcode + 1:游戏里 REQ/RSP 成对且相邻(0x1887 → 0x1888)。
func (c *Conn) SendSwap(ctx context.Context, sw petbox.Swap) ([]byte, error) {
	const op = petbox.OpChangePet

	ch := make(chan []byte, 1)
	c.mu.Lock()
	if c.key == nil {
		c.mu.Unlock()
		return nil, errors.New("relay: 还没拿到会话密钥")
	}
	if c.hdrSeqNG != 0 {
		c.mu.Unlock()
		return nil, fmt.Errorf("relay: 头块规则在本连接上不成立(序号 %d 那条对不上),拒绝注入",
			c.hdrSeqNG)
	}
	req := c.reqMax + ReqSeqGap
	if c.lastReq >= req {
		req = c.lastReq + 1
	}
	c.lastReq = req
	c.waiters[waiterKey(op+1, req)] = ch

	unwind := func(err error) ([]byte, error) {
		delete(c.waiters, waiterKey(op+1, req))
		c.mu.Unlock()
		return nil, err
	}
	plain, err := petbox.BuildPlain(c.template(), req, sw)
	if err != nil {
		return unwind(err)
	}
	// 头块前 10 字节按规则从零构造,尾巴照抄客户端的(见 petbox.BuildHeader)
	header, err := petbox.BuildHeader(c.lastC2S+1, plain, c.headerTail())
	if err != nil {
		return unwind(err)
	}
	if err := c.writeInjected(plain, header, op); err != nil {
		return unwind(err)
	}
	c.mu.Unlock()

	select {
	case rsp, ok := <-ch:
		if !ok {
			return nil, errors.New("relay: 连接结束,没等到回包")
		}
		return rsp, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.waiters, waiterKey(op+1, req))
		c.mu.Unlock()
		return nil, ctx.Err()
	case <-c.done:
		return nil, errors.New("relay: 连接已断开")
	}
}
