package ui

import "testing"

func TestHubUnsubscribeDropsEmptyJobMap(t *testing.T) {
	h := NewHub()
	_, cancel := h.Subscribe("job-1")
	cancel()
	if len(h.subs) != 0 {
		t.Fatalf("hub retained %d empty job map(s) after unsubscribe", len(h.subs))
	}
}
