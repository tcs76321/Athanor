// Package budget is the horizon-budget ledger (ADR-0091): a fixed accounting
// window, a protected interactive reserve, and reserve-then-settle grants.
//
// The pure half (this file) has no I/O: window math and bucket ceilings are
// deterministic, so the operator can reason about when the reset happens and
// the tests do not depend on a clock. The store-backed half is in ledger.go.
package budget

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Priority selects which bucket a grant draws from.
type Priority string

const (
	// Interactive is operator-initiated work. It may use the whole window.
	Interactive Priority = "interactive"
	// Background is indexing, consolidation, and daydreaming. It may not
	// touch the interactive reserve.
	Background Priority = "background"
)

// Valid reports whether p is a known priority.
func (p Priority) Valid() bool { return p == Interactive || p == Background }

// Settings is the operator configuration for one horizon.
//
// Tokens and Wall are the window ceilings. A zero ceiling disables that
// dimension; both zero disables the ledger entirely (unlimited), which is the
// pre-ADR-0091 behavior and the safe default for existing deployments.
type Settings struct {
	Window        time.Duration
	WindowStart   string // "HH:MM" local
	Tokens        int64
	Wall          time.Duration
	ReservePct    int // interactive reserve, 0..100
	BackgroundPct int // background share, 0..100 (defaults to 100-ReservePct)
	MaxGrant      int64
}

// Enabled reports whether the ledger enforces anything.
func (s Settings) Enabled() bool { return s.Window > 0 && (s.Tokens > 0 || s.Wall > 0) }

// Window is one accounting period, in the operator's local time.
type Window struct {
	Start time.Time
	End   time.Time
}

// WindowFor returns the window containing now. A window begins at WindowStart
// local time; if now is before today's boundary, the window began on an
// earlier period. The calculation is pure and DST-safe (time.Date resolves the
// local offset).
func WindowFor(now time.Time, s Settings) (Window, error) {
	if s.Window <= 0 {
		return Window{}, fmt.Errorf("budget: window must be positive")
	}
	hour, minute, err := parseClock(s.WindowStart)
	if err != nil {
		return Window{}, err
	}
	loc := now.Location()
	local := now.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	for start.After(local) {
		start = start.Add(-s.Window)
	}
	return Window{Start: start, End: start.Add(s.Window)}, nil
}

// parseClock accepts "HH:MM" (00:00 through 23:59). An empty value means
// midnight.
func parseClock(v string) (int, int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, 0, nil
	}
	parts := strings.Split(v, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("budget: window_start %q must be HH:MM", v)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, 0, fmt.Errorf("budget: window_start hour %q out of range", parts[0])
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("budget: window_start minute %q out of range", parts[1])
	}
	return h, m, nil
}

// Validate checks the settings without touching a clock or a database.
func (s Settings) Validate() error {
	if s.Window <= 0 {
		return fmt.Errorf("budget: window must be positive")
	}
	if _, _, err := parseClock(s.WindowStart); err != nil {
		return err
	}
	if s.Tokens < 0 {
		return fmt.Errorf("budget: tokens_per_window must be >= 0")
	}
	if s.Wall < 0 {
		return fmt.Errorf("budget: wall_time_per_window must be >= 0")
	}
	if s.ReservePct < 0 || s.ReservePct > 100 {
		return fmt.Errorf("budget: interactive_reserve_percent must be 0..100")
	}
	if s.BackgroundPct < 0 || s.BackgroundPct > 100 {
		return fmt.Errorf("budget: background_share_percent must be 0..100")
	}
	if s.BackgroundPct != 0 && s.ReservePct+s.BackgroundPct != 100 {
		return fmt.Errorf("budget: interactive_reserve_percent + background_share_percent must be 100")
	}
	if s.MaxGrant < 0 {
		return fmt.Errorf("budget: max_grant_tokens must be >= 0")
	}
	return nil
}

// BackgroundCeiling is the token ceiling available to background work: the
// share the operator set, or the complement of the reserve when the share is
// not configured.
func (s Settings) BackgroundCeiling(total int64) int64 {
	pct := s.BackgroundPct
	if pct == 0 {
		pct = 100 - s.ReservePct
	}
	return total * int64(pct) / 100
}

// CeilingFor returns the token ceiling for a priority. Interactive may use the
// whole window; background may not cross the reserve.
func (s Settings) CeilingFor(p Priority, total int64) int64 {
	if p == Background {
		return s.BackgroundCeiling(total)
	}
	return total
}

// WallCeilingFor returns the wall-time ceiling for a priority. With no wall
// ceiling configured, both buckets are unbounded in wall time.
func WallCeilingFor(p Priority, total time.Duration, reservePct, backgroundPct int) time.Duration {
	if total <= 0 {
		return 0
	}
	if p != Background {
		return total
	}
	pct := backgroundPct
	if pct == 0 {
		pct = 100 - reservePct
	}
	return time.Duration(int64(total) * int64(pct) / 100)
}

// GrantSize returns how much of a request the ledger may grant: the smaller of
// the request, the per-grant cap, and what remains. A non-positive result means
// "deny".
func GrantSize(request, maxGrant, remaining int64) int64 {
	if request <= 0 {
		return 0
	}
	if maxGrant > 0 && request > maxGrant {
		request = maxGrant
	}
	if remaining < request {
		request = remaining
	}
	if request < 0 {
		return 0
	}
	return request
}

// WallGrantSize is GrantSize for wall time.
func WallGrantSize(request, remaining time.Duration) time.Duration {
	if request <= 0 {
		return 0
	}
	if remaining < request {
		request = remaining
	}
	if request < 0 {
		return 0
	}
	return request
}
