package ui

import "sync"

// Hub is a per-job token fan-out.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[chan string]struct{}
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{subs: map[string]map[chan string]struct{}{}} }

// Publish sends a token chunk to every subscriber of jobID, dropping it for
// a subscriber whose buffer is full.
func (h *Hub) Publish(jobID, text string) {
	if text == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[jobID] {
		select {
		case ch <- text:
		default:
		}
	}
}

// Subscribe returns a channel of token chunks and an unsubscribe function.
func (h *Hub) Subscribe(jobID string) (<-chan string, func()) {
	ch := make(chan string, 256)
	h.mu.Lock()
	if h.subs[jobID] == nil {
		h.subs[jobID] = map[chan string]struct{}{}
	}
	h.subs[jobID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[jobID], ch)
		h.mu.Unlock()
	}
}
