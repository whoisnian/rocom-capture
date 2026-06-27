package server

import (
	"encoding/json"
	"sync"
)

// envelope 是推送给前端的统一消息封装。
type envelope struct {
	Type string `json:"type"` // pet | event | debug
	Data any    `json:"data"`
}

// Hub 管理 SSE 订阅者并广播实时消息。
type Hub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

// NewHub 创建广播中心。
func NewHub() *Hub {
	return &Hub{subs: make(map[chan []byte]struct{})}
}

func (h *Hub) subscribe() chan []byte {
	ch := make(chan []byte, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
	h.mu.Unlock()
}

// Broadcast 把一条消息广播给所有订阅者(满则丢弃，避免阻塞)。
func (h *Hub) Broadcast(typ string, data any) {
	msg, err := json.Marshal(envelope{Type: typ, Data: data})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}
