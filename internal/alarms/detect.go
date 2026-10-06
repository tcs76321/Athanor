package alarms

import (
	"fmt"
	"sort"
	"time"
)

// Thresholds are the detector cutoffs. They are constants for now; a §29
// `alarms:` block can expose them later without changing Detect.
type Thresholds struct {
	QualityWindow      int
	QualityRejectRate  float64
	StuckAfter         time.Duration
	LoopRepeats        int
	TokenBudget        int
	ResourceHigh       float64
	SecurityWindowMins int
}

// DefaultThresholds returns the shipped thresholds: >50% rejection over the
// last 10 jobs (§22.3 quality), 5 repeated identical tool calls (loop), a
// 200k-token per-job soft budget, 30m with no progress (stuck), and 95%
// pressure for resource alarms.
func DefaultThresholds() Thresholds {
	return Thresholds{
		QualityWindow:      10,
		QualityRejectRate:  0.5,
		StuckAfter:         30 * time.Minute,
		LoopRepeats:        5,
		TokenBudget:        200_000,
		ResourceHigh:       0.95,
		SecurityWindowMins: 15,
	}
}

// JobActivity is one non-terminal job's progress signal.
type JobActivity struct {
	ID           string
	ProjectID    string
	State        string
	LastProgress time.Time
}

// Snapshot is the pure detector input. A zero field simply produces no alarm
// for that signal.
type Snapshot struct {
	// RecentResults is the most-recent-first StrategyOutcome result set.
	RecentResults []string
	// JobTokens is per active job token cost.
	JobTokens map[string]int
	// ActiveJobs are non-terminal jobs.
	ActiveJobs []JobActivity
	// LoopRepeats is "jobID|tool|request" -> identical-call count.
	LoopRepeats map[string]int
	// SecurityEvents counts rejected/denied airlock+network events in the
	// window.
	SecurityEvents int
	// EventFlags counts named audit events (hallucinated_path,
	// self_modification_attempt, drift_attempt).
	EventFlags map[string]int
	// MemoryPressure / DiskPressure are 0..1 (0 = unknown).
	MemoryPressure float64
	DiskPressure   float64
}

// acceptedResult mirrors strategy's accepted outcomes.
func acceptedResult(r string) bool { return r == "accepted_new" || r == "accepted_previous" }

// Detect evaluates every §22.3 detector against s and returns the alarms to
// raise. It is pure and deterministic: the same snapshot and time produce the
// same slice, sorted by category.
func Detect(s Snapshot, th Thresholds, now time.Time) []Alarm {
	var out []Alarm
	add := func(category Category, message, jobID, projectID string) {
		out = append(out, Alarm{
			Category: category, Level: DefaultLevel(category),
			Message: message, JobID: jobID, ProjectID: projectID,
		})
	}

	// Quality: rejection rate over the last window.
	if th.QualityWindow > 0 && len(s.RecentResults) >= th.QualityWindow {
		window := s.RecentResults[:th.QualityWindow]
		rejected := 0
		for _, r := range window {
			if !acceptedResult(r) {
				rejected++
			}
		}
		rate := float64(rejected) / float64(len(window))
		if rate > th.QualityRejectRate {
			add(CategoryQuality, fmt.Sprintf("rejection rate %.0f%% over the last %d jobs (> %.0f%%)",
				rate*100, len(window), th.QualityRejectRate*100), "", "")
		}
	}

	// Stuck: an active job with no progress past the threshold.
	if th.StuckAfter > 0 {
		for _, j := range s.ActiveJobs {
			if j.LastProgress.IsZero() || now.Sub(j.LastProgress) <= th.StuckAfter {
				continue
			}
			add(CategoryStuck, fmt.Sprintf("no progress for %s in state %s",
				now.Sub(j.LastProgress).Round(time.Second), j.State), j.ID, j.ProjectID)
		}
	}

	// Loop: repeated identical tool calls.
	if th.LoopRepeats > 0 {
		for _, key := range sortedKeys(s.LoopRepeats) {
			if s.LoopRepeats[key] >= th.LoopRepeats {
				add(CategoryLoop, fmt.Sprintf("%d identical tool calls (%s)", s.LoopRepeats[key], key), jobFromKey(key), "")
			}
		}
	}

	// Budget: a job over its token budget.
	if th.TokenBudget > 0 {
		for _, jobID := range sortedKeys(s.JobTokens) {
			if s.JobTokens[jobID] > th.TokenBudget {
				add(CategoryBudget, fmt.Sprintf("token budget exceeded (%d > %d)", s.JobTokens[jobID], th.TokenBudget), jobID, "")
			}
		}
	}

	// Resource: memory / disk pressure.
	if th.ResourceHigh > 0 {
		if s.MemoryPressure >= th.ResourceHigh {
			add(CategoryResource, fmt.Sprintf("memory pressure %.0f%% >= %.0f%%", s.MemoryPressure*100, th.ResourceHigh*100), "", "")
		}
		if s.DiskPressure >= th.ResourceHigh {
			add(CategoryResource, fmt.Sprintf("disk pressure %.0f%% >= %.0f%%", s.DiskPressure*100, th.ResourceHigh*100), "", "")
		}
	}

	// Security: any rejected/denied egress or airlock event in the window.
	if s.SecurityEvents > 0 {
		add(CategorySecurity, fmt.Sprintf("%d security event(s) in the window", s.SecurityEvents), "", "")
	}

	// Event-flag categories raised by other guards.
	if n := s.EventFlags["hallucinated_path"]; n > 0 {
		add(CategoryHallucination, fmt.Sprintf("%d hallucinated path reference(s)", n), "", "")
	}
	if n := s.EventFlags["self_modification_attempt"]; n > 0 {
		add(CategorySelfModification, fmt.Sprintf("%d self-modification attempt(s)", n), "", "")
	}
	if n := s.EventFlags["drift_attempt"]; n > 0 {
		add(CategoryDrift, fmt.Sprintf("%d drift attempt(s)", n), "", "")
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		if out[i].JobID != out[j].JobID {
			return out[i].JobID < out[j].JobID
		}
		return out[i].Message < out[j].Message
	})
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// jobFromKey extracts the job id from a "jobID|tool|request" loop key.
func jobFromKey(key string) string {
	for i := 0; i < len(key); i++ {
		if key[i] == '|' {
			return key[:i]
		}
	}
	return key
}
