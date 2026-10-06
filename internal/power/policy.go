// Power/idle policy (ARCHITECTURE §24, ROADMAP M7-T1).
//
// Decide is a pure function from an Observation (AC/battery, user idle time,
// sleep state, wall clock) and an operator Config to a Decision (profile,
// whether deep work pauses, whether to hold a power assertion, and why). It
// contains no clocks, no OS calls, and no locks, so every row of the §24
// table is a table test.
//
// The Supervisor (supervisor.go) owns the clock and the OS Observer; it
// applies Decisions to the PowerManager and fires transition hooks.
package power

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Config is the §24 power policy, resolved from config.Power + config.Agent.
type Config struct {
	// RequireACForDeepWork pauses deep work on battery when true.
	RequireACForDeepWork bool
	// BatteryPauseThresholdPercent pauses even with an override below this.
	BatteryPauseThresholdPercent int
	// AllowBatteryOverride permits deep work on battery above the threshold.
	AllowBatteryOverride bool
	// PauseOnSleep pauses deep work while the system is suspended.
	PauseOnSleep bool
	// ResumeOnWake resumes checkpointed work after a wake.
	ResumeOnWake bool
	// DaydreamOnIdle enables autonomous background work while idle on AC.
	DaydreamOnIdle bool
	// ActiveHours is the §29 "HH:MM-HH:MM" window (e.g. "00:00-24:00").
	ActiveHours string
	// IdleThreshold is how long the user must be idle to enter autonomous
	// mode (§24 "user idle for configured period").
	IdleThreshold time.Duration
}

// Observation is one reading of the world. Zero values are conservative:
// OnAC=false is treated as "on battery", IdleFor=0 as "user active".
type Observation struct {
	OnAC           bool
	BatteryPercent int // 0 means unknown
	IdleFor        time.Duration
	Sleeping       bool
	// JustWoke is set by the Supervisor when a poll gap implies the machine
	// was suspended. It is an event, not a state.
	JustWoke bool
	// Now is the wall clock used to evaluate ActiveHours. Zero means the
	// caller does not care (Decide treats a zero Now as inside the window
	// only when ActiveHours is the all-day default).
	Now time.Time
}

// Decision is the resolved §24 action for one observation.
type Decision struct {
	Profile          Profile
	AllowDaydreaming bool
	Pause            bool
	Assertion        bool
	Reason           string
}

// Decide resolves the §24 table. Order matters: sleep and battery can pause
// regardless of the clock; only then do active hours and idleness select the
// profile.
func Decide(obs Observation, cfg Config) Decision {
	if obs.Sleeping && cfg.PauseOnSleep {
		return Decision{Profile: ProfileInteractive, Pause: true, Reason: "system sleeping (pause_on_sleep)"}
	}

	// Battery arms. Without override, any battery use pauses deep work.
	// With override, deep work runs at reduced concurrency above the
	// threshold and pauses below/at it.
	if !obs.OnAC {
		if !cfg.AllowBatteryOverride {
			return Decision{
				Profile: ProfileInteractive, Pause: true,
				Reason: "on battery (require_ac_for_deep_work; set allow_battery_override to permit)",
			}
		}
		if obs.BatteryPercent > 0 && obs.BatteryPercent <= cfg.BatteryPauseThresholdPercent {
			return Decision{
				Profile: ProfileInteractive, Pause: true,
				Reason: fmt.Sprintf("battery %d%% at/below threshold %d%%", obs.BatteryPercent, cfg.BatteryPauseThresholdPercent),
			}
		}
		// Override active above threshold: deep work, but never autonomous
		// background work on battery (§17.2 disables daydreaming on battery).
		return Decision{
			Profile: ProfileInteractive, AllowDaydreaming: false,
			Reason: "battery override: deep work at reduced concurrency, no daydreaming",
		}
	}

	// On AC. The clock and idleness select the profile.
	if !withinActiveHours(obs.Now, cfg.ActiveHours) {
		return Decision{
			Profile: ProfileInteractive,
			Reason:  fmt.Sprintf("outside active hours (%s)", cfg.ActiveHours),
		}
	}
	if cfg.DaydreamOnIdle && obs.IdleFor >= cfg.IdleThreshold && cfg.IdleThreshold > 0 {
		return Decision{
			Profile: ProfileAutonomous, AllowDaydreaming: true, Assertion: true,
			Reason: "AC + idle: autonomous background work",
		}
	}
	return Decision{Profile: ProfileInteractive, Reason: "AC but user active: deferential execution"}
}

// withinActiveHours reports whether now falls in the "HH:MM-HH:MM" window.
// "00:00-24:00" is all day. A malformed spec (config validation rejects it
// upstream) fails open so an operator typo cannot entirely stall the daemon.
func withinActiveHours(now time.Time, spec string) bool {
	start, end, ok := parseWindow(spec)
	if !ok {
		return true
	}
	if now.IsZero() {
		return true
	}
	m := now.Hour()*60 + now.Minute()
	switch {
	case start == end:
		return true
	case start < end:
		return m >= start && m < end
	default: // overnight window
		return m >= start || m < end
	}
}

// parseWindow parses "HH:MM-HH:MM" into minute offsets. The end may be
// "24:00" (end of day).
func parseWindow(spec string) (start, end int, ok bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return 0, 0, false
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	s, ok1 := parseClock(parts[0])
	e, ok2 := parseClock(parts[1])
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return s, e, true
}

// parseClock parses "HH:MM", allowing "24:00".
func parseClock(s string) (int, bool) {
	s = strings.TrimSpace(s)
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, false
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 24 {
		return 0, false
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, false
	}
	if h == 24 && m != 0 {
		return 0, false
	}
	return h*60 + m, true
}
