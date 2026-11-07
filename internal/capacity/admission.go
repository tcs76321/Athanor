package capacity

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Lane names the admission classes. Solo is the reserved full-capability lane;
// pods is code work that needs a Job Pod; light is text/document/data work.
const (
	LaneSolo  = "solo"
	LanePods  = "pods"
	LaneLight = "light"
)

// ErrNoLane reports a machine plan with no usable lane for a class.
var ErrNoLane = errors.New("capacity: no lane available")

// Admission bounds concurrent jobs by lane. It is intentionally conservative
// about mixed occupancy: when pod and light work are both active, the total is
// capped at the smaller plan so one class's model/pod budget cannot be
// exceeded by the other.
type Admission struct {
	mu    sync.Mutex
	solo  int
	pods  int
	light int
	used  map[string]int
}

// NewAdmission builds lanes from a machine report.
func NewAdmission(rep Report) *Admission {
	a := &Admission{used: map[string]int{}}
	if rep.Solo != nil {
		a.solo = 1
	}
	if rep.SwarmPods != nil {
		a.pods = rep.SwarmPods.Slots
	}
	if rep.SwarmLight != nil {
		a.light = rep.SwarmLight.Slots
	}
	return a
}

// MaxConcurrentJobs is the aggregate ceiling the engine can report. It is the
// largest lane, not the sum: the lanes share one machine budget.
func (a *Admission) MaxConcurrentJobs() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return maxInt(maxInt(a.solo, a.pods), a.light)
}

// Acquire waits for a slot in the requested lane. The returned release must be
// called exactly once. A canceled context returns ctx.Err().
func (a *Admission) Acquire(ctx context.Context, lane string) (func(), error) {
	lane = normalizeLane(lane)
	a.mu.Lock()
	capLane := a.laneCapLocked(lane)
	a.mu.Unlock()
	if capLane < 1 {
		return nil, fmt.Errorf("%w: %s", ErrNoLane, lane)
	}
	for {
		a.mu.Lock()
		if a.canAdmitLocked(lane) {
			a.used[lane]++
			a.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					a.mu.Lock()
					a.used[lane]--
					a.mu.Unlock()
				})
			}, nil
		}
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func normalizeLane(lane string) string {
	switch lane {
	case LaneSolo, LanePods, LaneLight:
		return lane
	default:
		return LaneLight
	}
}

func (a *Admission) laneCapLocked(lane string) int {
	switch lane {
	case LaneSolo:
		return a.solo
	case LanePods:
		return a.pods
	default:
		return a.light
	}
}

func (a *Admission) canAdmitLocked(lane string) bool {
	laneCap := a.laneCapLocked(lane)
	if laneCap < 1 {
		return false
	}
	if a.used[lane] >= laneCap {
		return false
	}
	total := 0
	for _, n := range a.used {
		total += n
	}
	// Mixed pod/light occupancy uses the smaller plan. One class alone may
	// use its full lane (plus the solo reservation when it exists).
	capTotal := maxInt(a.solo, laneCap)
	if a.used[LanePods] > 0 && a.used[LaneLight] > 0 && a.pods > 0 && a.light > 0 {
		capTotal = minInt(a.pods, a.light)
	}
	return total < capTotal
}
