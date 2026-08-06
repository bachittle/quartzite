package api

import (
	"encoding/json"
	"sync"
)

// Event is one server-sent message.
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

// Hub fans events out to connected browsers. Subscribers that fall behind are
// dropped rather than allowed to block a write: a slow tab must never stall an
// agent's API call.
type Hub struct {
	mu   sync.RWMutex
	subs map[chan []byte]struct{}
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{subs: map[chan []byte]struct{}{}} }

// Subscribe registers a client and returns its channel plus a cancel function.
func (h *Hub) Subscribe() (chan []byte, func()) {
	ch := make(chan []byte, 16)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

// Publish broadcasts an event to every current subscriber.
func (h *Hub) Publish(ev Event) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs {
		select {
		case ch <- b:
		default: // subscriber is behind; drop rather than block
		}
	}
}

// Count reports connected subscribers, shown on the dashboard.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}
