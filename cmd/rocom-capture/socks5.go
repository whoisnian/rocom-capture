package main

import (
	"bytes"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"sync"

	"github.com/armon/go-socks5"
	"github.com/whoisnian/rocom-capture/internal/capture"
	"golang.org/x/net/context"
)

type gamePortRules struct {
	port int
}

func (r gamePortRules) Allow(ctx context.Context, req *socks5.Request) (context.Context, bool) {
	return ctx, req != nil && req.Command == socks5.ConnectCommand &&
		req.DestAddr != nil && req.DestAddr.Port == r.port
}

// socksLogWriter 丢弃端口规则的预期拒绝日志，避免手机的其他连接刷满 stderr/journald。
// 拨号失败、协议错误等真正异常仍交给 go-socks5 的 logger 输出。
type socksLogWriter struct {
	dst io.Writer
}

func (w socksLogWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("blocked by rules")) {
		return len(p), nil
	}
	return w.dst.Write(p)
}

type trackedConn struct {
	net.Conn
	release func()
	once    sync.Once
}

func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

func newSOCKS5Config(port int, eng *capture.Engine) *socks5.Config {
	return &socks5.Config{
		Rules: gamePortRules{port: port},
		Logger: log.New(socksLogWriter{dst: os.Stderr}, "", log.LstdFlags),
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var dialer net.Dialer
			conn, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			tcp, ok := conn.LocalAddr().(*net.TCPAddr)
			if !ok || tcp.Port < 0 || tcp.Port > 65535 {
				return conn, nil
			}
			ip, ok := netip.AddrFromSlice(tcp.IP)
			if !ok {
				return conn, nil
			}
			release := eng.AllowSelfEndpoint(netip.AddrPortFrom(ip.Unmap(), uint16(tcp.Port)))
			return &trackedConn{Conn: conn, release: release}, nil
		},
	}
}

func startSOCKS5(addr string, port int, eng *capture.Engine) {
	go func() {
		server, err := socks5.New(newSOCKS5Config(port, eng))
		if err != nil {
			log.Fatalf("[SOCKS5] 创建服务失败: %v", err)
		}
		log.Printf("[SOCKS5] 无认证代理监听: %s (仅允许 CONNECT 目标端口 %d)", addr, port)
		if err := server.ListenAndServe("tcp", addr); err != nil {
			log.Fatalf("[SOCKS5] 服务异常退出: %v", err)
		}
	}()
}
