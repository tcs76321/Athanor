package engine

import (
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/project"
)

func TestPriorityRankFollowsProjectTiers(t *testing.T) {
	cases := map[string]int{
		project.PriorityCritical: 0,
		project.PriorityHigh:     1,
		project.PriorityMedium:   2,
		project.PriorityLow:      3,
		"":                       2,
		"unknown":                2,
	}
	for p, want := range cases {
		if got := priorityRank(p); got != want {
			t.Errorf("priorityRank(%q) = %d, want %d", p, got, want)
		}
	}
}

func TestPriorityQueueAdmitsTheBestWaiter(t *testing.T) {
	q := newPriorityQueue()
	now := time.Now()
	q.add("low", 3, now)
	q.add("high", 1, now)
	q.add("medium", 2, now)

	if q.allows("low", 1, now, PriorityAging) {
		t.Fatal("low-priority job was admitted ahead of high and medium")
	}
	if !q.allows("high", 1, now, PriorityAging) {
		t.Fatal("high-priority job was not admitted")
	}
	q.remove("high")
	if !q.allows("medium", 1, now, PriorityAging) {
		t.Fatal("medium was not admitted after high left")
	}
	if q.allows("low", 1, now, PriorityAging) {
		t.Fatal("low was admitted ahead of medium")
	}
	q.remove("medium")
	if !q.allows("low", 1, now, PriorityAging) {
		t.Fatal("low was not admitted when it was the only waiter")
	}
}

func TestPriorityAgingNeverPromotesToCritical(t *testing.T) {
	q := newPriorityQueue()
	now := time.Now()
	q.add("low", priorityRank(project.PriorityLow), now)
	// Two full aging intervals promote low -> medium -> high.
	if q.allows("low", 1, now.Add(2*PriorityAging), PriorityAging) != true {
		t.Fatal("sole waiter should be admitted")
	}
	w := q.waiters["low"]
	if got := agedRank(w, now.Add(3*PriorityAging), PriorityAging); got != priorityRank(project.PriorityHigh) {
		t.Fatalf("aged rank = %d, want high; low may never become critical", got)
	}
	if got := agedRank(w, now.Add(100*PriorityAging), PriorityAging); got != priorityRank(project.PriorityHigh) {
		t.Fatalf("aged rank = %d after a long wait, want high", got)
	}
}

func TestPriorityQueueUnknownJobIsAllowed(t *testing.T) {
	q := newPriorityQueue()
	if !q.allows("not-waiting", 1, time.Now(), PriorityAging) {
		t.Fatal("a job outside the queue was blocked")
	}
	var nilQueue *priorityQueue
	if !nilQueue.allows("x", 1, time.Now(), PriorityAging) {
		t.Fatal("nil queue blocked a job")
	}
}
