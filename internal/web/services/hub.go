package services

import "sync"

type Hub struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan any]struct{}
}

func NewHub() *Hub { return &Hub{subscribers: map[string]map[chan any]struct{}{}} }

func (h *Hub) Subscribe(topic string) (chan any, func()) {
	ch := make(chan any, 16)
	h.mu.Lock()
	if h.subscribers[topic] == nil {
		h.subscribers[topic] = map[chan any]struct{}{}
	}
	h.subscribers[topic][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subscribers[topic], ch)
		close(ch)
		h.mu.Unlock()
	}
}

func (h *Hub) Publish(topic string, value any) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subscribers[topic] {
		select {
		case ch <- value:
		default:
		}
	}
}
