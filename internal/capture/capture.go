// Package capture 负责读取数据包(实时 afpacket / 离线 pcap)、按 TCP 流重组，
// 并经 GCP 分帧、密钥提取、0x4013 解密后，产出应用层消息。
package capture

import (
	"log"
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

// Message 是一条解密后的应用层消息。
type Message struct {
	Time      time.Time
	Direction gcp.Direction
	Opcode    uint16
	Session   string // GCP 连接标识 "server:port|client:port"(client 侧为游戏客户端设备,供按设备/账号归属)
	Plain     []byte // 解密后完整明文(含 internal header)
	AppBody   []byte // 剥离 internal header 后的 protobuf body
}

// session 对应一个 GCP 连接，持有会话 AES 密钥(双向共享)。
type session struct {
	mu  sync.Mutex
	key []byte
}

func (s *session) setKey(k []byte) { s.mu.Lock(); s.key = k; s.mu.Unlock() }
func (s *session) getKey() []byte  { s.mu.Lock(); defer s.mu.Unlock(); return s.key }

// Engine 是抓包解析引擎。
type Engine struct {
	Port int
	Out  chan Message

	mu       sync.Mutex
	sessions map[string]*session
	noKey    int
}

// NewEngine 创建引擎，port 为游戏服务器端口(8195)。
func NewEngine(port int) *Engine {
	return &Engine{
		Port:     port,
		Out:      make(chan Message, 4096),
		sessions: make(map[string]*session),
	}
}

// NoKeyDropped 返回因尚无会话密钥而丢弃的 DATA 包数。
func (e *Engine) NoKeyDropped() int { e.mu.Lock(); defer e.mu.Unlock(); return e.noKey }

func (e *Engine) incNoKey() { e.mu.Lock(); e.noKey++; e.mu.Unlock() }

func (e *Engine) getSession(id string) *session {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.sessions[id]
	if s == nil {
		s = &session{}
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

// process 是抓包/离线共用的处理循环。
func (e *Engine) process(src *gopacket.PacketSource) {
	factory := &streamFactory{e: e}
	pool := reassembly.NewStreamPool(factory)
	asm := reassembly.NewAssembler(pool)
	count := 0
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
		asm.AssembleWithContext(netLayer.NetworkFlow(), tcp, &assyContext{ci: pkt.Metadata().CaptureInfo})
		count++
		if count%2000 == 0 {
			asm.FlushCloseOlderThan(time.Now().Add(-2 * time.Minute))
		}
	}
	asm.FlushAll()
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
			op, ok := gcp.AppOpcode(dir, plain)
			if !ok {
				continue
			}
			s.e.emit(Message{
				Time:      ts,
				Direction: dir,
				Opcode:    op,
				Session:   s.connID,
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
