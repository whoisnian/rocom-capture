package server

import (
	"encoding/json"
	"sync"
)

// envelope 是推送给前端的统一消息封装。
type envelope struct {
	Type    string `json:"type"`              // pet | event | debug
	Account string `json:"account,omitempty"` // 所属账号("" = 全局/调试,前端不按账号过滤)
	Data    any    `json:"data"`
}

// streamMsg 是投递给订阅者的一条消息:预先序列化的 JSON,外带 type/account 供订阅端过滤,
// 免得每个连接都重新反序列化。
type streamMsg struct {
	typ     string
	account string
	data    []byte
}

// Hub 管理 SSE 订阅者并广播实时消息。
type Hub struct {
	mu   sync.Mutex
	subs map[chan streamMsg]struct{}
}

// NewHub 创建广播中心。
func NewHub() *Hub {
	return &Hub{subs: make(map[chan streamMsg]struct{})}
}

func (h *Hub) subscribe() chan streamMsg {
	ch := make(chan streamMsg, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) unsubscribe(ch chan streamMsg) {
	h.mu.Lock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
	h.mu.Unlock()
}

// Broadcast 把一条消息广播给所有订阅者(满则丢弃，避免阻塞)。account 为消息所属账号,
// 传 "" 表示全局消息(所有连接都收);订阅端按 account/type 决定是否转发(见 handleStream)。
func (h *Hub) Broadcast(typ, account string, data any) {
	msg, err := json.Marshal(envelope{Type: typ, Account: account, Data: data})
	if err != nil {
		return
	}
	m := streamMsg{typ: typ, account: account, data: msg}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- m:
		default:
		}
	}
}
