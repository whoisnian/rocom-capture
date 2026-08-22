package relay

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/whoisnian/rocom-capture/internal/gcp"
	"github.com/whoisnian/rocom-capture/internal/petbox"
)

var testKey = []byte("0123456789abcdef")

// rspBody 特意长过一个 AES 分组:下行只认领不改写,多块密文必须一字不差地到客户端手上,
// 拿它来验。
var rspBody = bytes.Repeat([]byte("回包正文abcdef0123"), 6)

// frame 拼一个 GCP 包(HEAD.base 21 字节 + extend + body)。
func frame(cmd uint16, flag byte, seq uint32, extra, body []byte) []byte {
	out := make([]byte, 0, 21+len(extra)+len(body))
	out = append(out, gcp.Magic...)
	out = binary.BigEndian.AppendUint16(out, 0x000b)
	out = binary.BigEndian.AppendUint16(out, 0x000b)
	out = binary.BigEndian.AppendUint16(out, cmd)
	out = append(out, flag)
	out = binary.BigEndian.AppendUint32(out, seq)
	out = binary.BigEndian.AppendUint32(out, uint32(21+len(extra)))
	out = binary.BigEndian.AppendUint32(out, uint32(len(body)))
	out = append(out, extra...)
	return append(out, body...)
}

// s2cPlain 拼一条下行明文:[0:2]保留 [2:4]opcode [4:6]=55aa [6:10]回声 [10:]body,补齐 16。
func s2cPlain(opcode uint16, echo uint32, body []byte) []byte {
	out := make([]byte, 10, 16+len(body)+16)
	binary.BigEndian.PutUint16(out[2:4], opcode)
	out[4], out[5] = 0x55, 0xaa
	binary.BigEndian.PutUint32(out[6:10], echo)
	out = append(out, body...)
	for len(out)%16 != 0 {
		out = append(out, 0)
	}
	return out
}

// encC2S 拼一条上行 DATA,头块按真客户端的规则算 —— 里面镜像着这一包的 GCP 包序号,
// 中转顺延序号时必须连它一起改(见 petbox.RenumberSeq)。
func encC2S(t *testing.T, seq uint32, plain []byte) []byte {
	return encC2STail(t, seq, plain, petbox.DefaultHeaderTail)
}

func encC2STail(t *testing.T, seq uint32, plain []byte, tail petbox.HeaderTail) []byte {
	t.Helper()
	h, err := petbox.BuildHeader(seq, plain, tail)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := petbox.Encrypt(testKey, h, plain)
	if err != nil {
		t.Fatal(err)
	}
	return frame(gcp.CmdData, 0x00, seq, []byte{0, 0, 3, 0}, enc)
}

// encS2C 拼一条下行 DATA。下行的头块里没有序号副本(1223 条真包核过),内容随便填。
func encS2C(t *testing.T, seq uint32, plain []byte) []byte {
	t.Helper()
	enc, err := petbox.Encrypt(testKey, bytes.Repeat([]byte{0x5a}, 16), plain)
	if err != nil {
		t.Fatal(err)
	}
	return frame(gcp.CmdData, 0x01, seq, []byte{0, 0, 3, 0}, enc)
}

func readFrame(t *testing.T, c net.Conn) []byte {
	t.Helper()
	head := make([]byte, 21)
	if err := readFull(c, head); err != nil {
		t.Fatalf("读包头: %v", err)
	}
	hdrLen := binary.BigEndian.Uint32(head[13:17])
	bodyLen := binary.BigEndian.Uint32(head[17:21])
	rest := make([]byte, int(hdrLen)-21+int(bodyLen))
	if err := readFull(c, rest); err != nil {
		t.Fatalf("读包体: %v", err)
	}
	return append(head, rest...)
}

// readFrameQuiet 读一个完整 GCP 包,出错返回 nil。假服务器用它:客户端关连接是正常剧情,
// 而且它跑在别的 goroutine 里,不能调 t.Fatal。
func readFrameQuiet(c net.Conn) []byte {
	head := make([]byte, 21)
	if readFull(c, head) != nil {
		return nil
	}
	hdrLen := binary.BigEndian.Uint32(head[13:17])
	bodyLen := binary.BigEndian.Uint32(head[17:21])
	if hdrLen < 21 {
		return nil
	}
	rest := make([]byte, int(hdrLen)-21+int(bodyLen))
	if readFull(c, rest) != nil {
		return nil
	}
	return append(head, rest...)
}

func readFull(c net.Conn, b []byte) error {
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for n := 0; n < len(b); {
		m, err := c.Read(b[n:])
		n += m
		if err != nil {
			return err
		}
	}
	return nil
}

// decrypt 解一个下行 DATA 包,返回明文。
func decrypt(t *testing.T, pkt []byte) []byte {
	t.Helper()
	hdrLen := binary.BigEndian.Uint32(pkt[13:17])
	plain, err := gcp.DecryptData(testKey, pkt[hdrLen:])
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	return plain
}

// fakeServer:先下发 ACK 带密钥,之后每收到一条 DATA 就回一条 opcode+1、回声原样抄回的包。
// 收到的包序号与请求序号全部记下来供断言。
type fakeServer struct {
	mu      sync.Mutex
	seenSeq []uint32
	seenReq []uint32
	hdrBad  []string // 头块里那份包序号与外层对不上的,真服务端见到就踢人
	tails   []string // 每条上行包头块 [10:16] 的原样
	outSeq  uint32
}

// run 循环接受连接:游戏断线重连时中转必须还在,所以假服务器也得接得住第二条。
func (s *fakeServer) run(t *testing.T, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go s.serve(t, conn)
	}
}

func (s *fakeServer) serve(t *testing.T, conn net.Conn) {
	defer conn.Close()
	for {
		pkt := readFrameQuiet(conn)
		if pkt == nil {
			return
		}
		cmd := binary.BigEndian.Uint16(pkt[6:8])
		hdrLen := binary.BigEndian.Uint32(pkt[13:17])
		s.mu.Lock()
		s.seenSeq = append(s.seenSeq, binary.BigEndian.Uint32(pkt[9:13]))
		s.mu.Unlock()

		switch cmd {
		case gcp.CmdSYN:
			extra := make([]byte, 2+16)
			copy(extra[2:], testKey)
			if _, err := conn.Write(frame(gcp.CmdACK, 0x01, s.nextOut(), extra, nil)); err != nil {
				return
			}
		case gcp.CmdData:
			plain, err := gcp.DecryptData(testKey, pkt[hdrLen:])
			if err != nil {
				t.Errorf("服务器解密失败: %v", err)
				return
			}
			op := binary.BigEndian.Uint16(plain[6:8])
			req := binary.BigEndian.Uint32(plain[10:14])
			s.mu.Lock()
			s.seenReq = append(s.seenReq, req)
			// 外层序号与密文头块里那份副本必须一致(tsf4g 的 TConnd 有 CheckSequence)
			seq := binary.BigEndian.Uint32(pkt[9:13])
			if hdr, herr := gcp.DecryptHeadBlock(testKey, pkt[hdrLen:]); herr != nil {
				s.hdrBad = append(s.hdrBad, fmt.Sprintf("GCP序号 %d 取不出头块: %v", seq, herr))
			} else {
				s.tails = append(s.tails, fmt.Sprintf("%x", hdr[10:16]))
				if cerr := petbox.CheckHeader(hdr, seq, plain); cerr != nil {
					s.hdrBad = append(s.hdrBad, fmt.Sprintf("GCP序号 %d(op=%#04x): %v", seq, op, cerr))
				}
			}
			s.mu.Unlock()
			if _, err := conn.Write(encS2C(t, s.nextOut(), s2cPlain(op+1, req, rspBody))); err != nil {
				return
			}
		}
	}
}

func (s *fakeServer) nextOut() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outSeq++
	return s.outSeq
}

// clientReq 让假客户端发一条自己的请求(用 petbox 的模板,请求序号自己指定)。
func clientReq(t *testing.T, cli net.Conn, gcpSeq, reqSeq uint32) {
	t.Helper()
	plain, err := petbox.BuildPlain(petbox.DefaultTemplate, reqSeq, petbox.Swap{
		From: petbox.Slot{Gid: 9, Box: 1, Pos: 1},
		To:   petbox.Slot{Gid: 8, Box: 1, Pos: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Write(encC2S(t, gcpSeq, plain)); err != nil {
		t.Fatal(err)
	}
}

// TestRelayInjectLeavesClientReqSeqAlone 端到端跑一遍中转,断言四件事:
//   - **客户端的请求号原样上线,一个都不许改**(改了会被服务器踢,见 ReqSeqGap 前的注释)
//   - 注入用的号取自客户端号段之外
//   - 服务器看到的 GCP 包序号连续,客户端的包按注入条数顺延(这个必须改,否则重号)
//   - 客户端自己的回包原样收到、正文一字不差
func TestRelayInjectLeavesClientReqSeqAlone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	srvLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srvLn.Close()
	srv := &fakeServer{}
	go srv.run(t, srvLn)

	gwLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gw := &Gateway{Upstream: srvLn.Addr().String()}
	ready := make(chan *Conn, 1)
	go func() { _ = gw.Serve(ctx, gwLn, ready) }()

	cli, err := net.Dial("tcp", gwLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	// 1) 握手:中转顺带截下密钥
	if _, err := cli.Write(frame(gcp.CmdSYN, 0x00, 1, nil, nil)); err != nil {
		t.Fatal(err)
	}
	ack := readFrame(t, cli)
	if cmd := binary.BigEndian.Uint16(ack[6:8]); cmd != gcp.CmdACK {
		t.Fatalf("首包不是 ACK,是 %#04x", cmd)
	}
	var conn *Conn
	select {
	case conn = <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("等不到拿到密钥的连接")
	}

	// 2) 客户端先自己发一条(请求号 1),回包应当原样回到客户端
	clientReq(t, cli, 2, 1)
	got := readFrame(t, cli)
	plain := decrypt(t, got)
	if echo := binary.BigEndian.Uint32(plain[6:10]); echo != 1 {
		t.Errorf("第一条回包的回声 = %d, want 1", echo)
	}
	if seq := binary.BigEndian.Uint32(got[9:13]); seq != 2 {
		t.Errorf("第一条回包的 GCP 序号 = %d, want 2", seq)
	}

	// 3) 注入一条:回包回到 SendSwap,不该落到客户端
	rsp, err := conn.SendSwap(ctx, petbox.Swap{
		From: petbox.Slot{Gid: 1, Box: 3, Pos: 1},
		To:   petbox.Slot{Gid: 2, Box: 3, Pos: 2},
	})
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	if op := binary.BigEndian.Uint16(rsp[2:4]); op != petbox.OpChangePet+1 {
		t.Errorf("注入的回包 opcode = %#04x, want %#04x", op, petbox.OpChangePet+1)
	}
	if echo := binary.BigEndian.Uint32(rsp[6:10]); echo <= 100 {
		t.Errorf("注入用掉的请求号 = %d, 应当远离客户端号段", echo)
	}

	// 4) 客户端再发一条(它自己数到 2):号必须原样上线,回包也原样回来。
	// 自己那条回包同样照转给客户端,所以客户端会先收到我们那条(回声 100001),
	// 再收到自己的(回声 2);下行序号一路不改。
	clientReq(t, cli, 3, 2)
	var plain2 []byte
	for range 3 {
		got = readFrame(t, cli)
		plain2 = decrypt(t, got)
		if binary.BigEndian.Uint32(plain2[6:10]) == 2 {
			break
		}
	}
	if echo := binary.BigEndian.Uint32(plain2[6:10]); echo != 2 {
		t.Errorf("没等到客户端自己那条回包(最后一条回声 = %d)", echo)
	}
	if seq := binary.BigEndian.Uint32(got[9:13]); seq != 4 {
		t.Errorf("第二条回包的 GCP 序号 = %d, want 4(下行一律不改)", seq)
	}
	if body := plain2[10 : 10+len(rspBody)]; !bytes.Equal(body, rspBody) {
		t.Errorf("改回回声之后正文被破坏了:\n得到 %q\n应为 %q", body, rspBody)
	}

	srv.mu.Lock()
	seenSeq := append([]uint32(nil), srv.seenSeq...)
	seenReq := append([]uint32(nil), srv.seenReq...)
	hdrBad := append([]string(nil), srv.hdrBad...)
	srv.mu.Unlock()
	for _, b := range hdrBad {
		t.Errorf("上行包的内外层序号对不上: %s", b)
	}
	// SYN=1、客户端第一条=2、注入=3、客户端第二条原本是 3 → 改写成 4
	if want := []uint32{1, 2, 3, 4}; !equal(seenSeq, want) {
		t.Errorf("服务器看到的 GCP 序号 = %v, want %v", seenSeq, want)
	}
	// 重点:客户端那两条的号原样上线(1 和 2),中间夹着我们自己取的远号
	if len(seenReq) != 3 || seenReq[0] != 1 || seenReq[2] != 2 {
		t.Errorf("服务器看到的请求序号 = %v, want [1 <远号> 2] —— 客户端的号一个都不许改", seenReq)
	}
	if seenReq[1] <= 100 {
		t.Errorf("注入用的号 %d 落在客户端号段里了", seenReq[1])
	}
}

func equal(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestInjectKeepsHeaderSeqInSync 钉住那个把连接搞死的根因:GCP 包序号在密文头块里还有
// 一份副本(petbox.BuildHeader 的 buf[3]),中转给客户端的包顺延外层序号时必须连它一起改。
//
// 只改外层的话,注入之后客户端的每一条 DATA 都带着两个互相矛盾的序号。实测服务端接受并
// 执行注入的换位,然后在收到注入后**第一条客户端 DATA** 约一个 RTT 之后 SSTOP —— 三次实测
// 断点都在这里,与吞不吞回包、请求号取多少全无关。
//
// 这里注入两次再让客户端连发几条,覆盖偏移大于 1 的情形。
func TestInjectKeepsHeaderSeqInSync(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	srvLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srvLn.Close()
	srv := &fakeServer{}
	go srv.run(t, srvLn)

	gwLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gw := &Gateway{Upstream: srvLn.Addr().String()}
	ready := make(chan *Conn, 1)
	go func() { _ = gw.Serve(ctx, gwLn, ready) }()

	cli, err := net.Dial("tcp", gwLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	if _, err := cli.Write(frame(gcp.CmdSYN, 0x00, 1, nil, nil)); err != nil {
		t.Fatal(err)
	}
	readFrame(t, cli)
	var conn *Conn
	select {
	case conn = <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("等不到拿到密钥的连接")
	}

	// 客户端的 GCP 包序号自己从 2 往下数,中间夹两次注入
	// 不在这儿收回包:自己那条回包也照转给客户端,按条数配对会错位。
	// 收尾统一等服务器把该收的都收全。
	next := uint32(2)
	sendClient := func(reqSeq uint32) {
		clientReq(t, cli, next, reqSeq)
		next++
	}
	inject := func() {
		if _, err := conn.SendSwap(ctx, petbox.Swap{
			From: petbox.Slot{Gid: 1, Box: 3, Pos: 1},
			To:   petbox.Slot{Gid: 2, Box: 3, Pos: 2},
		}); err != nil {
			t.Fatalf("注入失败: %v", err)
		}
	}

	sendClient(1)
	inject()
	sendClient(2)
	inject()
	for r := uint32(3); r <= 6; r++ {
		sendClient(r)
	}

	// SYN + 6 条客户端包 + 2 条注入 = 9
	const wantN = 9
	deadline := time.Now().Add(5 * time.Second)
	for {
		srv.mu.Lock()
		n := len(srv.seenSeq)
		srv.mu.Unlock()
		if n >= wantN || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	srv.mu.Lock()
	bad := append([]string(nil), srv.hdrBad...)
	seen := append([]uint32(nil), srv.seenSeq...)
	srv.mu.Unlock()
	for _, b := range bad {
		t.Errorf("上行包的内外层序号对不上: %s", b)
	}
	// 服务器看到的包序号必须是一条不断的 1..N
	want := make([]uint32, wantN)
	for i := range want {
		want[i] = uint32(i + 1)
	}
	if !equal(seen, want) {
		t.Errorf("服务器看到的 GCP 序号 = %v, want %v", seen, want)
	}
}

// TestRefusesInjectWhenHeaderRuleBroken 守住「不确定就别发」这条:头块规则是从几分钟的
// 抓包样本归纳的,包序号最大只到 885,高两个字节从没被真包用过。真实一局十几个小时会越过
// 65536,那时规则对不对只有客户端自己的包说了算 —— 只要有一条对不上就停用注入,别硬发。
func TestRefusesInjectWhenHeaderRuleBroken(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	srvLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srvLn.Close()
	srv := &fakeServer{}
	go srv.run(t, srvLn)

	gwLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gw := &Gateway{Upstream: srvLn.Addr().String()}
	ready := make(chan *Conn, 1)
	go func() { _ = gw.Serve(ctx, gwLn, ready) }()

	cli, err := net.Dial("tcp", gwLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	if _, err := cli.Write(frame(gcp.CmdSYN, 0x00, 1, nil, nil)); err != nil {
		t.Fatal(err)
	}
	readFrame(t, cli)
	var conn *Conn
	select {
	case conn = <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("等不到拿到密钥的连接")
	}

	// 先来一条规矩的:规则成立,注入应当放行
	clientReq(t, cli, 2, 1)
	readFrame(t, cli)
	if ok, n := conn.HeaderRuleOK(); !ok || n == 0 {
		t.Fatalf("规矩的包之后 HeaderRuleOK = %v/%d, 应当成立", ok, n)
	}

	// 再来一条头块里序号写错的(模拟「这个版本不是我们理解的样子」)
	plain, err := petbox.BuildPlain(petbox.DefaultTemplate, 2, petbox.Swap{
		From: petbox.Slot{Gid: 9, Box: 1, Pos: 1},
		To:   petbox.Slot{Gid: 8, Box: 1, Pos: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	badHdr, err := petbox.BuildHeader(999999, plain, petbox.DefaultHeaderTail) // 与外层的 3 对不上
	if err != nil {
		t.Fatal(err)
	}
	enc, err := petbox.Encrypt(testKey, badHdr, plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Write(frame(gcp.CmdData, 0x00, 3, []byte{0, 0, 3, 0}, enc)); err != nil {
		t.Fatal(err)
	}
	readFrame(t, cli)

	if ok, _ := conn.HeaderRuleOK(); ok {
		t.Error("头块序号对不上之后 HeaderRuleOK 仍为真")
	}
	if _, err := conn.SendSwap(ctx, petbox.Swap{
		From: petbox.Slot{Gid: 1, Box: 3, Pos: 1},
		To:   petbox.Slot{Gid: 2, Box: 3, Pos: 2},
	}); err == nil {
		t.Error("规则不成立时 SendSwap 仍然发了出去 —— 这一枪会赔上整条会话")
	}
}

// TestInjectCopiesClientHeaderTail 守住「头块尾巴要照抄客户端」:[10:16] 那 6 字节逐客户端
// 不同 —— 一台 22 分钟 10374 条包里恒为 0a0b0c0d0e0e(解码 1),另一台同一秒内能出二十几种值。
// 服务端显然不看它,但没理由去猜:抄当前连接的那份最稳,和 head6/tag 同一个道理。
func TestInjectCopiesClientHeaderTail(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	srvLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srvLn.Close()
	srv := &fakeServer{}
	go srv.run(t, srvLn)

	gwLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gw := &Gateway{Upstream: srvLn.Addr().String()}
	ready := make(chan *Conn, 1)
	go func() { _ = gw.Serve(ctx, gwLn, ready) }()

	cli, err := net.Dial("tcp", gwLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	if _, err := cli.Write(frame(gcp.CmdSYN, 0x00, 1, nil, nil)); err != nil {
		t.Fatal(err)
	}
	readFrame(t, cli)
	var conn *Conn
	select {
	case conn = <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("等不到拿到密钥的连接")
	}

	// 这台「客户端」的尾巴与默认填充明显不同
	tail := petbox.HeaderTail{0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0e}
	for i := uint32(1); i <= 3; i++ {
		plain, err := petbox.BuildPlain(petbox.DefaultTemplate, i, petbox.Swap{
			From: petbox.Slot{Gid: 9, Box: 1, Pos: 1},
			To:   petbox.Slot{Gid: 8, Box: 1, Pos: 2},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cli.Write(encC2STail(t, i+1, plain, tail)); err != nil {
			t.Fatal(err)
		}
		readFrame(t, cli)
	}

	if _, err := conn.SendSwap(ctx, petbox.Swap{
		From: petbox.Slot{Gid: 1, Box: 3, Pos: 1},
		To:   petbox.Slot{Gid: 2, Box: 3, Pos: 2},
	}); err != nil {
		t.Fatalf("注入失败: %v", err)
	}

	srv.mu.Lock()
	got := append([]string(nil), srv.tails...)
	srv.mu.Unlock()
	if len(got) != 4 {
		t.Fatalf("服务器收到 %d 条上行 DATA, want 4", len(got))
	}
	want := fmt.Sprintf("%x", tail[:])
	if got[3] != want {
		t.Errorf("注入那条的头块尾巴 = %s, want %s(应当照抄客户端的,而不是默认填充 %x)",
			got[3], want, petbox.DefaultHeaderTail[:])
	}
}

// TestGatewayKeepsServingAfterClose 钉住一个踩过的坑:一条连接结束不能让中转停摆。
// nft 规则还挂着的时候,监听一没,游戏重连就是 connection refused,表现为持续网络错误
// —— 比没跑过还糟。
func TestGatewayKeepsServingAfterClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	srvLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srvLn.Close()
	go (&fakeServer{}).run(t, srvLn)

	gwLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gw := &Gateway{Upstream: srvLn.Addr().String()}
	ready := make(chan *Conn, 4)
	go func() { _ = gw.Serve(ctx, gwLn, ready) }()

	dial := func(round int) *Conn {
		cli, err := net.Dial("tcp", gwLn.Addr().String())
		if err != nil {
			t.Fatalf("第 %d 次连接失败: %v", round, err)
		}
		if _, err := cli.Write(frame(gcp.CmdSYN, 0x00, 1, nil, nil)); err != nil {
			t.Fatalf("第 %d 次握手失败: %v", round, err)
		}
		if cmd := binary.BigEndian.Uint16(readFrame(t, cli)[6:8]); cmd != gcp.CmdACK {
			t.Fatalf("第 %d 次没收到 ACK", round)
		}
		var conn *Conn
		select {
		case conn = <-ready:
		case <-time.After(5 * time.Second):
			t.Fatalf("第 %d 次等不到拿到密钥的连接", round)
		}
		cli.Close()
		return conn
	}

	first := dial(1)
	select {
	case <-first.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("客户端都关了,连接却没结束")
	}
	if r := first.Reason(); r == "" {
		t.Error("Reason 应当说明是谁先挂的")
	} else {
		t.Logf("第一条连接结束:%s", r)
	}
	if len(first.Trace()) == 0 {
		t.Error("Trace 应当留下断开前的包记录")
	}

	// 关键:第一条死了,第二条还得接得上
	if second := dial(2); second == nil {
		t.Fatal("第一条结束后中转就不干活了")
	}
}

// writeRec 记录每次 Write 的长度,用来看转发是攒批还是逐包。
type writeRec struct {
	net.Conn
	sizes []int
}

func (w *writeRec) Write(b []byte) (int, error) {
	w.sizes = append(w.sizes, len(b))
	return len(b), nil
}
func (w *writeRec) Read([]byte) (int, error) { return 0, io.EOF }
func (w *writeRec) Close() error             { return nil }

// TestForwardBatchesOneWritePerRead 钉住一个踩过的坑:一次 Read 里的多个 GCP 包必须
// 合成一次写出去,不能逐包各写一次。
//
// 逐包写 + TCP_NODELAY 会把手机原本塞在一个 TCP 段里的多条消息拆成多个小段。实测:
// 零解析、只做 io.Copy 的哑代理一分钟不掉线,而逐包写出的这套每次都在客户端那条
// 环境上报之后约 25ms 被服务器 SSTOP。差别就在这里。
func TestForwardBatchesOneWritePerRead(t *testing.T) {
	rec := &writeRec{}
	c := &Conn{
		client:  rec,
		up:      rec,
		shells:  map[petbox.Template]int{},
		waiters: map[uint64]chan []byte{},
		done:    make(chan struct{}),
	}
	// 一次读进来三个完整包 + 半个
	var buf []byte
	for i := range 3 {
		buf = append(buf, frame(gcp.CmdData, 0x00, uint32(i+1), []byte{0, 0, 3, 0},
			bytes.Repeat([]byte{byte(i)}, 48))...)
	}
	full := len(buf)
	buf = append(buf, 0x33, 0x66, 0x00) // 残缺的下一个包头

	used, err := c.forward(buf, true)
	if err != nil {
		t.Fatal(err)
	}
	if used != full {
		t.Errorf("消费了 %d 字节, want %d(残缺的那截该留着等下次读)", used, full)
	}
	if len(rec.sizes) != 1 {
		t.Fatalf("写了 %d 次 %v —— 三个包必须一次写出去", len(rec.sizes), rec.sizes)
	}
	if rec.sizes[0] != full {
		t.Errorf("这一次写了 %d 字节, want %d", rec.sizes[0], full)
	}
}

// TestRelayIsByteExactWithoutInjection 验证一件一直只靠推理、从没测过的事:不注入时,
// 中转必须是**逐字节**的管道 —— 上行序号改写的偏移是 0,应当写回原值;下行一个字节都不碰。
func TestRelayIsByteExactWithoutInjection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	srvLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srvLn.Close()

	// 假服务器:把收到的字节原样记下来,并按请求回包
	var mu sync.Mutex
	var gotUp []byte
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		conn, err := srvLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			pkt := readFrameQuiet(conn)
			if pkt == nil {
				return
			}
			mu.Lock()
			gotUp = append(gotUp, pkt...)
			mu.Unlock()
			switch binary.BigEndian.Uint16(pkt[6:8]) {
			case gcp.CmdSYN:
				extra := make([]byte, 2+16)
				copy(extra[2:], testKey)
				if _, err := conn.Write(frame(gcp.CmdACK, 0x01, 1, extra, nil)); err != nil {
					return
				}
			case gcp.CmdData:
				hdrLen := binary.BigEndian.Uint32(pkt[13:17])
				plain, err := gcp.DecryptData(testKey, pkt[hdrLen:])
				if err != nil {
					return
				}
				op := binary.BigEndian.Uint16(plain[6:8])
				req := binary.BigEndian.Uint32(plain[10:14])
				if _, err := conn.Write(encS2C(t, req+1, s2cPlain(op+1, req, rspBody))); err != nil {
					return
				}
			}
		}
	}()

	gwLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gw := &Gateway{Upstream: srvLn.Addr().String()}
	ready := make(chan *Conn, 4)
	go func() { _ = gw.Serve(ctx, gwLn, ready) }()

	cli, err := net.Dial("tcp", gwLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	// 客户端发出去的原始字节,逐条累计
	var sentUp []byte
	send := func(b []byte) {
		sentUp = append(sentUp, b...)
		if _, err := cli.Write(b); err != nil {
			t.Fatal(err)
		}
	}

	send(frame(gcp.CmdSYN, 0x00, 1, nil, nil))
	if cmd := binary.BigEndian.Uint16(readFrame(t, cli)[6:8]); cmd != gcp.CmdACK {
		t.Fatal("没收到 ACK")
	}
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("等不到拿到密钥的连接")
	}

	// 连发若干条请求(请求号 1..6),期间不注入任何东西
	for i := uint32(1); i <= 6; i++ {
		plain, err := petbox.BuildPlain(petbox.DefaultTemplate, i, petbox.Swap{
			From: petbox.Slot{Gid: 100 + i, Box: 1, Pos: int32(i)},
			To:   petbox.Slot{Gid: 200 + i, Box: 1, Pos: int32(i) + 1},
		})
		if err != nil {
			t.Fatal(err)
		}
		send(encC2S(t, i+1, plain))
		readFrame(t, cli) // 收掉回包,免得堵住
	}

	cli.Close()
	select {
	case <-srvDone:
	case <-time.After(5 * time.Second):
	}

	mu.Lock()
	up := append([]byte(nil), gotUp...)
	mu.Unlock()
	if len(up) != len(sentUp) {
		t.Fatalf("服务器收到 %d 字节,客户端发了 %d 字节", len(up), len(sentUp))
	}
	for i := range up {
		if up[i] != sentUp[i] {
			t.Fatalf("上行第 %d 字节被改了:发出 %02x,到达 %02x\n  发出 %x\n  到达 %x",
				i, sentUp[i], up[i],
				sentUp[max(0, i-8):min(len(sentUp), i+8)], up[max(0, i-8):min(len(up), i+8)])
		}
	}
}
