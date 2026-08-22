package main

import (
	"testing"

	"github.com/whoisnian/rocom-capture/internal/petbox"
)

// TestWireOrderFingerprint 钉住「字段顺序指纹」认得出本工具自己编的顺序(iOS 的 4,3,2,1)。
// 真包上认到的是别的顺序,那才说明这台客户端与本工具不同(见 docs/inject.md 3.5)。
func TestWireOrderFingerprint(t *testing.T) {
	body := petbox.EncodeSwap(petbox.Swap{
		From: petbox.Slot{Gid: 41683, Box: 35, Pos: 21},
		To:   petbox.Slot{Box: 35, Pos: 1},
	})
	if got, want := wireOrder(body, true), "1{4,3,2,1},2{4,3,2,1}"; got != want {
		t.Fatalf("wireOrder = %q, 期望 %q", got, want)
	}
}

// TestWireOrderTruncated 截断的输入只能返回已经认出的部分,不许 panic —— 真包里
// 任何一段解不动都不该让自检崩掉。
func TestWireOrderTruncated(t *testing.T) {
	body := petbox.EncodeSwap(petbox.Swap{From: petbox.Slot{Gid: 1, Box: 2, Pos: 3}})
	for i := range body {
		if got := wireOrder(body[:i], true); got == "" && i > 1 {
			continue // 认不出东西也可以,只要不崩
		}
	}
}
