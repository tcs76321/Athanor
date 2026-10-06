//go:build linux

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tcs76321/athanor/internal/power"
)

// newPowerObserver returns the Linux power/idle observer: /sys/class/power_supply
// for AC + battery and xprintidle (when a display session exists) for idle.
func newPowerObserver() power.Observer { return linuxObserver{} }

type linuxObserver struct{}

func (linuxObserver) Observe(ctx context.Context) (power.Observation, error) {
	obs := power.Observation{OnAC: true, Now: time.Now()}

	const root = "/sys/class/power_supply"
	entries, err := os.ReadDir(root)
	if err == nil {
		hasBattery := false
		for _, e := range entries {
			name := e.Name()
			switch {
			case strings.HasPrefix(name, "AC"), strings.HasPrefix(name, "ADP"):
				if b, err := os.ReadFile(filepath.Join(root, name, "online")); err == nil {
					obs.OnAC = strings.TrimSpace(string(b)) == "1"
				}
			case strings.HasPrefix(name, "BAT"):
				hasBattery = true
				if b, err := os.ReadFile(filepath.Join(root, name, "capacity")); err == nil {
					if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
						obs.BatteryPercent = n
					}
				}
			}
		}
		// A desktop with no battery is always "on AC".
		if !hasBattery {
			obs.OnAC = true
		}
	}

	// Idle: a headless host (no display session) is treated as idle; an
	// interactive X/Wayland session is polled through xprintidle when it is
	// available, else treated as active (conservative).
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		obs.IdleFor = time.Hour
	} else if out, err := exec.CommandContext(ctx, "xprintidle").Output(); err == nil {
		if ms, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64); err == nil {
			obs.IdleFor = time.Duration(ms) * time.Millisecond
		}
	}
	return obs, nil
}

// newOSWatcher returns a Linux power assertion backed by `systemd-inhibit`.
func newOSWatcher() power.OSWatcher { return &linuxWatcher{} }

type linuxWatcher struct {
	mu  sync.Mutex
	cmd *exec.Cmd
}

func (w *linuxWatcher) AcquirePowerAssertion() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd != nil {
		return nil
	}
	cmd := exec.Command("systemd-inhibit", "--what=idle:sleep", "--why=athanor autonomous work", "sleep", "infinity")
	if err := cmd.Start(); err != nil {
		return err
	}
	w.cmd = cmd
	return nil
}

func (w *linuxWatcher) ReleasePowerAssertion() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd == nil {
		return nil
	}
	if w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
	}
	_ = w.cmd.Wait()
	w.cmd = nil
	return nil
}
