package power

import (
	"testing"
	"time"
)

// baseCfg is the §29 default posture: AC required, 20% threshold, no
// override, 5m idle, all-day active window, daydream on idle.
func baseCfg() Config {
	return Config{
		RequireACForDeepWork:         true,
		BatteryPauseThresholdPercent: 20,
		AllowBatteryOverride:         false,
		PauseOnSleep:                 true,
		ResumeOnWake:                 true,
		DaydreamOnIdle:               true,
		ActiveHours:                  "00:00-24:00",
		IdleThreshold:                5 * time.Minute,
	}
}

func TestDecideOnBatteryNoOverridePauses(t *testing.T) {
	d := Decide(Observation{OnAC: false, BatteryPercent: 80}, baseCfg())
	if !d.Pause {
		t.Fatalf("on battery without override must pause: %+v", d)
	}
	if d.Profile != ProfileInteractive {
		t.Errorf("profile = %q, want interactive", d.Profile)
	}
}

func TestDecideOnBatteryOverrideAboveThresholdRuns(t *testing.T) {
	cfg := baseCfg()
	cfg.AllowBatteryOverride = true
	d := Decide(Observation{OnAC: false, BatteryPercent: 50}, cfg)
	if d.Pause {
		t.Fatalf("override above threshold must not pause: %+v", d)
	}
	if d.Profile != ProfileInteractive || d.AllowDaydreaming || d.Assertion {
		t.Errorf("battery override must run reduced and never daydream: %+v", d)
	}
}

func TestDecideOnBatteryOverrideBelowThresholdPauses(t *testing.T) {
	cfg := baseCfg()
	cfg.AllowBatteryOverride = true
	d := Decide(Observation{OnAC: false, BatteryPercent: 20}, cfg)
	if !d.Pause {
		t.Fatalf("override at/below threshold must pause: %+v", d)
	}
}

func TestDecideOnBatteryOverrideUnknownPercentRuns(t *testing.T) {
	cfg := baseCfg()
	cfg.AllowBatteryOverride = true
	d := Decide(Observation{OnAC: false, BatteryPercent: 0}, cfg)
	if d.Pause {
		t.Fatalf("unknown battery percent with override must not pause: %+v", d)
	}
}

func TestDecideSleepingPausesWhenConfigured(t *testing.T) {
	d := Decide(Observation{Sleeping: true, OnAC: true}, baseCfg())
	if !d.Pause || d.Profile != ProfileInteractive {
		t.Fatalf("sleeping with pause_on_sleep must pause: %+v", d)
	}
}

func TestDecideSleepingIgnoredWhenPauseOnSleepOff(t *testing.T) {
	cfg := baseCfg()
	cfg.PauseOnSleep = false
	d := Decide(Observation{Sleeping: true, OnAC: true, IdleFor: time.Hour}, cfg)
	if d.Pause {
		t.Fatalf("pause_on_sleep=false must not pause: %+v", d)
	}
	if d.Profile != ProfileAutonomous {
		t.Errorf("profile = %q, want autonomous (AC + idle)", d.Profile)
	}
}

func TestDecideACIdleGoesAutonomous(t *testing.T) {
	d := Decide(Observation{OnAC: true, IdleFor: 10 * time.Minute}, baseCfg())
	if d.Pause {
		t.Fatalf("AC idle must not pause: %+v", d)
	}
	if d.Profile != ProfileAutonomous || !d.AllowDaydreaming || !d.Assertion {
		t.Fatalf("AC idle must be autonomous + daydream + assertion: %+v", d)
	}
}

func TestDecideACActiveIsInteractive(t *testing.T) {
	d := Decide(Observation{OnAC: true, IdleFor: time.Second}, baseCfg())
	if d.Pause || d.Profile != ProfileInteractive || d.AllowDaydreaming {
		t.Fatalf("AC active must be interactive, not paused: %+v", d)
	}
}

func TestDecideDaydreamDisabledOnIdle(t *testing.T) {
	cfg := baseCfg()
	cfg.DaydreamOnIdle = false
	d := Decide(Observation{OnAC: true, IdleFor: time.Hour}, cfg)
	if d.Pause {
		t.Fatalf("daydream_on_idle=false must not pause: %+v", d)
	}
	if d.Profile != ProfileInteractive || d.AllowDaydreaming {
		t.Fatalf("daydream_on_idle=false must stay interactive without daydreaming: %+v", d)
	}
}

func TestDecideOutsideActiveHours(t *testing.T) {
	cfg := baseCfg()
	cfg.ActiveHours = "09:00-17:00"
	// 2026-01-01 03:00 UTC is outside the window.
	now := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)
	d := Decide(Observation{OnAC: true, IdleFor: time.Hour, Now: now}, cfg)
	if d.Pause {
		t.Fatalf("outside active hours must not pause, only de-prioritize: %+v", d)
	}
	if d.Profile != ProfileInteractive || d.AllowDaydreaming {
		t.Fatalf("outside active hours must be interactive: %+v", d)
	}
}

func TestWithinActiveHours(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 1, 1, h, m, 0, 0, time.UTC) }
	cases := []struct {
		spec string
		now  time.Time
		want bool
	}{
		{"00:00-24:00", at(0, 0), true},
		{"00:00-24:00", at(23, 59), true},
		{"09:00-17:00", at(9, 0), true},
		{"09:00-17:00", at(16, 59), true},
		{"09:00-17:00", at(17, 0), false},
		{"09:00-17:00", at(8, 59), false},
		{"22:00-06:00", at(23, 30), true}, // overnight
		{"22:00-06:00", at(2, 0), true},
		{"22:00-06:00", at(12, 0), false},
		{"garbage", at(12, 0), true}, // fail open
	}
	for _, c := range cases {
		if got := withinActiveHours(c.now, c.spec); got != c.want {
			t.Errorf("withinActiveHours(%s, %s) = %v, want %v", c.now.Format("15:04"), c.spec, got, c.want)
		}
	}
}
