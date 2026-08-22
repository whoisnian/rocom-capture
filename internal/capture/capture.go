// Package capture 负责读取数据包(实时 afpacket / 离线 pcap)、按 TCP 流重组，
// 并经 GCP 分帧、密钥提取、0x4013 解密后，产出应用层消息。
package capture

import (
	"fmt"
	"log"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcapgo"
	"github.com/google/gopacket/reassembly"
	"github.com/whoisnian/rocom-capture/internal/gcp"
)

// headBlock 取密文 body 的头块,取不出就留空(调用方按可选字段处理)。
func headBlock(key, body []byte) []byte {
	h, err := gcp.DecryptHeadBlock(key, body)
	if err != nil {
		return nil
	}
	return h
}

// Message 是一条解密后的应用层消息。
type Message struct {
	Time      time.Time
	Direction gcp.Direction
	Opcode    uint16
	Session   string // GCP 连接标识 "server:port|client:port"(client 侧为游戏客户端设备,供按设备/账号归属)
	// Sequence 与 Header 只有主动造包那条链路用得上(见 docs/inject.md),被动解析不看:
	// Sequence 是该包的 GCP 包序号(HEAD.base [9:13]),Header 是密文 body 最前面那 16 字节
	// 解出来的头块 —— c2s 的头块里带着这一包的序号与长度,填错服务端收到即断开。
	// boxarrange -selftest 靠这两者在任意一份抓包上复核头块规则。
	Sequence uint32
	Header   []byte
	Plain    []byte // 解密后完整明文(含 internal header)
	AppBody  []byte // 剥离 internal header 后的 protobuf body
}

// session 对应一个 GCP 连接，持有会话 AES 密钥(双向共享)。
type session struct {
	mu        sync.Mutex
	key       []byte
	fromCache bool // key 来自持久化缓存(重启恢复),正确性待首个 DATA 的明文校验确认
	confirmed bool // 缓存 key 已被 ValidPlain 确认有效(仅用于避免重复日志)
}

// setKey 记录 ACK 新协商的密钥:权威且即时生效,清除缓存态。
func (s *session) setKey(k []byte) {
	s.mu.Lock()
	s.key, s.fromCache, s.confirmed = k, false, false
	s.mu.Unlock()
}
func (s *session) getKey() []byte { s.mu.Lock(); defer s.mu.Unlock(); return s.key }

// loadCachedKey 预热重启前缓存的密钥,标记为待确认。
func (s *session) loadCachedKey(k []byte) {
	s.mu.Lock()
	s.key, s.fromCache, s.confirmed = k, true, false
	s.mu.Unlock()
}

// clearKey 清除已判定失效的缓存密钥,回退到"无密钥"状态等待新 ACK。
func (s *session) clearKey() {
	s.mu.Lock()
	s.key, s.fromCache, s.confirmed = nil, false, false
	s.mu.Unlock()
}

// cacheState 返回 (来自缓存, 已确认)。
func (s *session) cacheState() (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fromCache, s.confirmed
}
func (s *session) markConfirmed() { s.mu.Lock(); s.confirmed = true; s.mu.Unlock() }

// KeyStore 持久化连接会话密钥,供抓包服务重启后继续解密仍存活的连接。
// 由 store.Store 实现;Engine.Keys 为 nil 时退化为纯内存(重启即丢密钥)。
type KeyStore interface {
	LoadKey(connID string) ([]byte, bool) // 连接首次出现时预热;无/过期返回 false
	SaveKey(connID string, key []byte)    // 收到 ACK 提取到密钥时落盘
}

// Engine 是抓包解析引擎。
type Engine struct {
	Port int
	Out  chan Message
	Keys KeyStore // 可选:会话密钥持久化(见 KeyStore)

	mu       sync.Mutex
	sessions map[string]*session
	noKey    int
	badKey   int

	// skipIPs:凡 src 或 dst 命中即整包丢弃。用于单臂网关去重——网关对转发流量做
	// SNAT 后从同一网卡再发一次,该副本的客户端侧地址是网关本机 IP;忽略本机 IP 即只
	// 保留 NAT 前的真实客户端会话,避免同一游戏流被解析两次(见 RunLive 自动填充)。
	skipIPs map[netip.Addr]bool
}

// NewEngine 创建引擎，port 为游戏服务器端口(8195)。
func NewEngine(port int) *Engine {
	return &Engine{
		Port:     port,
		Out:      make(chan Message, 4096),
		sessions: make(map[string]*session),
		skipIPs:  make(map[netip.Addr]bool),
	}
}

// AddSkipIP 登记一个需忽略的 IP(见 skipIPs)。非并发安全,须在 Run* 前调用。
func (e *Engine) AddSkipIP(ip netip.Addr) { e.skipIPs[ip.Unmap()] = true }

// NoKeyDropped 返回因尚无会话密钥而丢弃的 DATA 包数。
func (e *Engine) NoKeyDropped() int { e.mu.Lock(); defer e.mu.Unlock(); return e.noKey }

// BadKeyDropped 返回因密钥错误(明文校验不通过,多为缓存密钥失效)而丢弃的 DATA 包数。
func (e *Engine) BadKeyDropped() int { e.mu.Lock(); defer e.mu.Unlock(); return e.badKey }

func (e *Engine) incNoKey()  { e.mu.Lock(); e.noKey++; e.mu.Unlock() }
func (e *Engine) incBadKey() { e.mu.Lock(); e.badKey++; e.mu.Unlock() }

func (e *Engine) getSession(id string) *session {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.sessions[id]
	if s == nil {
		s = &session{}
		if e.Keys != nil {
			if k, ok := e.Keys.LoadKey(id); ok {
				s.loadCachedKey(k)
				log.Printf("从缓存恢复会话密钥 [%s]", id)
			}
		}
		e.sessions[id] = s
	}
	return s
}

func (e *Engine) emit(m Message) { e.Out <- m }

// RunOffline 离线回放 pcap 文件，处理完毕后关闭 Out。
func (e *Engine) RunOffline(pcapPath string) error {
	f, err := os.Open(pcapPath)
	if err != nil {
		return err
	}
	defer f.Close()
	r, err := pcapgo.NewReader(f)
	if err != nil {
		return err
	}
	src := gopacket.NewPacketSource(r, r.LinkType())
	e.process(src)
	close(e.Out)
	return nil
}

// RunOfflineFiles 按给定顺序回放多份 pcap,**当成一条连续的流**处理。
//
// 轮转出来的抓包必须这样读:会话密钥只在第一份的 0x1002 ACK 里,而要看的高位包序号在最后
// 几份 —— 单独喂任何一份都拿不到完整信息。汇编器与会话表跨文件保留,只在最后 FlushAll。
// 文件边界上会丢掉跨界的那点 TCP 分段,gcp.Deframe 靠 magic 重新同步,代价是漏掉一两条消息。
func (e *Engine) RunOfflineFiles(paths ...string) error {
	asm := reassembly.NewAssembler(reassembly.NewStreamPool(&streamFactory{e: e}))
	var st loopState
	for _, path := range paths {
		if err := func() error {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			r, err := pcapgo.NewReader(f)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			e.packetLoop(asm, gopacket.NewPacketSource(r, r.LinkType()), &st)
			return nil
		}(); err != nil {
			asm.FlushAll()
			close(e.Out)
			return err
		}
	}
	asm.FlushAll()
	close(e.Out)
	return nil
}

// flush 参数:阈值一律用抓包时钟(最新包时间戳)而非墙钟——实时流里墙钟永远追不上
// "活跃连接"的数据时间,会导致中段接入时被缓冲等待缺失分段的起始数据一直不下推。
const (
	flushEvery = 200             // 每处理这么多包尝试一次 flush
	flushLag   = time.Second     // 跨间隙滞留数据超过此时长即下推(不再等缺失分段),近实时
	closeIdle  = 2 * time.Minute // 连接空闲超过此时长才关闭,不误关活跃连接
)

// process 是抓包/离线共用的处理循环。
func (e *Engine) process(src *gopacket.PacketSource) {
	asm := reassembly.NewAssembler(reassembly.NewStreamPool(&streamFactory{e: e}))
	var st loopState
	e.packetLoop(asm, src, &st)
	asm.FlushAll()
}

// loopState 是跨包源保留的处理状态(多份轮转抓包要当成一条流,见 RunOfflineFiles)。
type loopState struct {
	lastTS time.Time
	count  int
}

// packetLoop 把一个包源喂给汇编器。多份抓包共用一个汇编器时按顺序调用,中间不 FlushAll。
func (e *Engine) packetLoop(asm *reassembly.Assembler, src *gopacket.PacketSource, st *loopState) {
	for pkt := range src.Packets() {
		netLayer := pkt.NetworkLayer()
		tcpLayer := pkt.Layer(layers.LayerTypeTCP)
		if netLayer == nil || tcpLayer == nil {
			continue
		}
		tcp, _ := tcpLayer.(*layers.TCP)
		if int(tcp.SrcPort) != e.Port && int(tcp.DstPort) != e.Port {
			continue
		}
		if len(e.skipIPs) > 0 {
			nf := netLayer.NetworkFlow()
			if ip, ok := netip.AddrFromSlice(nf.Src().Raw()); ok && e.skipIPs[ip.Unmap()] {
				continue // SNAT 后的重复副本(源为本机/网关 IP)
			}
			if ip, ok := netip.AddrFromSlice(nf.Dst().Raw()); ok && e.skipIPs[ip.Unmap()] {
				continue
			}
		}
		ci := pkt.Metadata().CaptureInfo
		if ci.Timestamp.After(st.lastTS) {
			st.lastTS = ci.Timestamp
		}
		asm.AssembleWithContext(netLayer.NetworkFlow(), tcp, &assyContext{ci: ci})
		st.count++
		if st.count%flushEvery == 0 {
			// T: 下推 seen 时间早于 lastTS-flushLag 的滞留数据(含中段接入的起始 backlog);
			// TC: 仅关闭 lastTS-closeIdle 前无活动的连接,活跃连接保持存活。
			asm.FlushWithOptions(reassembly.FlushOptions{
				T:  st.lastTS.Add(-flushLag),
				TC: st.lastTS.Add(-closeIdle),
			})
		}
	}
}

// assyContext 为 reassembly 提供包的捕获信息(时间戳)。
type assyContext struct{ ci gopacket.CaptureInfo }

func (c *assyContext) GetCaptureInfo() gopacket.CaptureInfo { return c.ci }

// streamFactory 为每个 TCP 单向流创建 stream，并关联到同一 GCP 会话。
type streamFactory struct{ e *Engine }

func (f *streamFactory) New(netFlow, tpFlow gopacket.Flow, tcp *layers.TCP, _ reassembly.AssemblerContext) reassembly.Stream {
	// reassembly 每个连接只创建一个 Stream，双向数据都进同一 Stream，
	// 方向由 ReassembledSG 的 sg.Info() 给出。
	// initiatorIsDevice: reassembly 的 “client” 是连接发起方(第一个包的 src)。
	// 触发包若 DstPort==8195(c2s)，则发起方是游戏客户端设备(手机/平板/PC)。
	initiatorIsDevice := int(tcp.DstPort) == f.e.Port
	// 规范化 connID 为 "server|client"(client 侧为游戏客户端设备)。
	var connID string
	if int(tcp.SrcPort) == f.e.Port {
		connID = netFlow.Src().String() + ":" + tpFlow.Src().String() + "|" + netFlow.Dst().String() + ":" + tpFlow.Dst().String()
	} else {
		connID = netFlow.Dst().String() + ":" + tpFlow.Dst().String() + "|" + netFlow.Src().String() + ":" + tpFlow.Src().String()
	}
	server, client, _ := strings.Cut(connID, "|")
	log.Printf("检测到新连接: 客户端 %s → 服务器 %s", client, server)
	return &stream{e: f.e, sess: f.e.getSession(connID), connID: connID, initiatorIsDevice: initiatorIsDevice}
}

// stream 处理单个 TCP 连接的双向数据，各方向独立累积、分帧、解密。
type stream struct {
	e                 *Engine
	sess              *session
	connID            string
	initiatorIsDevice bool
	bufC2S            []byte
	bufS2C            []byte
}

func (s *stream) Accept(_ *layers.TCP, _ gopacket.CaptureInfo, _ reassembly.TCPFlowDirection, _ reassembly.Sequence, _ *bool, _ reassembly.AssemblerContext) bool {
	return true
}

func (s *stream) ReassembledSG(sg reassembly.ScatterGather, ac reassembly.AssemblerContext) {
	l, _ := sg.Lengths()
	if l == 0 {
		return
	}
	rdir, _, _, _ := sg.Info()
	// 把 reassembly 方向映射为 c2s/s2c。
	dir := gcp.S2C
	if (rdir == reassembly.TCPDirClientToServer) == s.initiatorIsDevice {
		dir = gcp.C2S
	}
	buf := &s.bufS2C
	if dir == gcp.C2S {
		buf = &s.bufC2S
	}
	*buf = append(*buf, sg.Fetch(l)...)
	pkts, rest := gcp.Deframe(*buf)
	*buf = rest
	ts := ac.GetCaptureInfo().Timestamp
	for _, p := range pkts {
		switch p.Command {
		case gcp.CmdACK:
			if k, ok := gcp.ExtractKey(p.HeaderExtra); ok {
				if s.sess.getKey() == nil {
					log.Printf("会话密钥就绪 [%s]", s.connID)
				}
				s.sess.setKey(k)
				if s.e.Keys != nil {
					s.e.Keys.SaveKey(s.connID, k)
				}
			}
		case gcp.CmdData:
			key := s.sess.getKey()
			if key == nil {
				s.e.incNoKey()
				continue
			}
			plain, err := gcp.DecryptData(key, p.Body)
			if err != nil {
				continue
			}
			// 校验明文结构:密钥错误时解出乱码。s2c 明文标记 0x55aa 恒定,正确密钥必过校验,
			// 故任一 s2c 校验失败即判定密钥错误(常见于缓存密钥失效:连接在停机期间已重连)。
			if !gcp.ValidPlain(dir, plain) {
				s.e.incBadKey()
				if fc, _ := s.sess.cacheState(); fc {
					log.Printf("缓存密钥校验失败,已清除(连接可能在服务停机期间重连;需在游戏内重新登录以捕获新密钥) [%s]", s.connID)
					s.sess.clearKey()
				}
				continue
			}
			// 缓存密钥经首个 s2c 校验通过即确认有效,给出明确日志(与"疑似失效"区分)。
			if fc, cf := s.sess.cacheState(); fc && !cf && dir == gcp.S2C {
				s.sess.markConfirmed()
				log.Printf("缓存密钥已确认有效,继续解析 [%s]", s.connID)
			}
			op, ok := gcp.AppOpcode(dir, plain)
			if !ok {
				continue
			}
			s.e.emit(Message{
				Time:      ts,
				Direction: dir,
				Opcode:    op,
				Session:   s.connID,
				Sequence:  p.Sequence,
				Header:    headBlock(key, p.Body),
				Plain:     plain,
				AppBody:   gcp.AppBody(dir, plain),
			})
		}
	}
}

func (s *stream) ReassemblyComplete(_ reassembly.AssemblerContext) bool {
	log.Printf("连接断开 [%s]", s.connID)
	return false
}
