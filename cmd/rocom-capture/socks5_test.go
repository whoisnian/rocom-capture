package main

import (
	"bytes"
	"testing"

	"github.com/armon/go-socks5"
	"golang.org/x/net/context"
)

func TestGamePortRules(t *testing.T) {
	rules := gamePortRules{port: 8195}
	tests := []struct {
		name string
		req  *socks5.Request
		want bool
	}{
		{name: "game connect", req: &socks5.Request{Command: socks5.ConnectCommand, DestAddr: &socks5.AddrSpec{Port: 8195}}, want: true},
		{name: "other port", req: &socks5.Request{Command: socks5.ConnectCommand, DestAddr: &socks5.AddrSpec{Port: 443}}},
		{name: "bind", req: &socks5.Request{Command: socks5.BindCommand, DestAddr: &socks5.AddrSpec{Port: 8195}}},
		{name: "missing destination", req: &socks5.Request{Command: socks5.ConnectCommand}},
		{name: "nil request"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got := rules.Allow(context.Background(), tt.req)
			if got != tt.want {
				t.Fatalf("Allow() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSOCKSLogWriter(t *testing.T) {
	var dst bytes.Buffer
	w := socksLogWriter{dst: &dst}

	blocked := []byte("[ERR] socks: Failed to handle request: Connect to 203.0.113.1:443 blocked by rules\n")
	if n, err := w.Write(blocked); err != nil || n != len(blocked) {
		t.Fatalf("Write(blocked) = %d, %v", n, err)
	}
	if dst.Len() != 0 {
		t.Fatalf("blocked request was logged: %q", dst.String())
	}

	realError := []byte("[ERR] socks: Failed to handle request: connection reset\n")
	if n, err := w.Write(realError); err != nil || n != len(realError) {
		t.Fatalf("Write(real error) = %d, %v", n, err)
	}
	if got := dst.String(); got != string(realError) {
		t.Fatalf("real error log = %q, want %q", got, realError)
	}
}
