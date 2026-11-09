package engine

import (
	"sync"
	"time"

	"github.com/tcs76321/athanor/internal/project"
)

// PriorityAging is how long a waiting job keeps its base priority before it is
// promoted one tier (never to critical). It prevents low-priority work from
// starving while high-priority work is continuous.
const PriorityAging = 30 * time.Minute

// priorityRank maps a project priority to the dispatch rank. Lower runs first.
func priorityRank(p string) int {
	switch p {
	case project.PriorityCritical:
		return 0
	case project.PriorityHigh:
		return 1
	case project.PriorityMedium:
		return 2
	case project.PriorityLow:
		return 3
	default:
		return 2
	}
}

type priorityWaiter struct {
	jobID string
	rank  int
	seq   uint64
	at    time.Time
}

// priorityQueue is the engine's fairness gate. Every job waiting for a slot
// registers here; only the best-ranked waiters may attempt the execution CAS.
// It is deliberately pure local state (no DB) so the ordering policy is
// testable without a store.
type priorityQueue struct {
	mu      sync.Mutex
	waiters map[string]*priorityWaiter
	seq     uint64
}

func newPriorityQueue() *priorityQueue {
	return &priorityQueue{waiters: map[string]*priorityWaiter{}}
}

func (q *priorityQueue) add(jobID string, rank int, now time.Time) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.waiters[jobID]; ok {
		return
	}
	q.seq++
	q.waiters[jobID] = &priorityWaiter{jobID: jobID, rank: rank, seq: q.seq, at: now}
}

func (q *priorityQueue) remove(jobID string) {
	if q == nil {
		return
	}
	q.mu.Lock()
	delete(q.waiters, jobID)
	q.mu.Unlock()
}

// allows reports whether jobID is among the best-ranked waiters for the
// available slots. A job not in the queue is allowed (the queue is an
// ordering aid, not an authorization gate). Aging moves a waiter one tier for
// every full PriorityAging interval it has waited, but never to critical.
func (q *priorityQueue) allows(jobID string, slots int, now time.Time, aging time.Duration) bool {
	if q == nil || slots < 1 {
		return q == nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	self, ok := q.waiters[jobID]
	if !ok {
		return true
	}
	selfRank := agedRank(self, now, aging)
	better := 0
	for id, w := range q.waiters {
		if id == jobID {
			continue
		}
		r := agedRank(w, now, aging)
		if r < selfRank || (r == selfRank && w.seq < self.seq) {
			better++
		}
	}
	return better < slots
}

func agedRank(w *priorityWaiter, now time.Time, aging time.Duration) int {
	rank := w.rank
	if rank <= 1 || aging <= 0 {
		return rank
	}
	waited := now.Sub(w.at)
	for waited >= aging && rank > 1 {
		rank--
		waited -= aging
	}
	return rank
}
