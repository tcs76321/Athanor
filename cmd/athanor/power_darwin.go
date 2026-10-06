//go:build darwin

package main

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tcs76321/athanor/internal/power"
)

// newPowerObserver returns the macOS power/idle observer: `pmset -g batt`
// for AC + battery and `ioreg -c IOHIDSystem` for HID idle time.
func newPowerObserver() power.Observer { return darwinObserver{} }

type darwinObserver struct{}

func (darwinObserver) Observe(ctx context.Context) (power.Observation, error) {
	obs := power.Observation{OnAC: true, Now: time.Now()}

	if out, err := exec.CommandContext(ctx, "pmset", "-g", "batt").Output(); err == nil {
		s := string(out)
		obs.OnAC = strings.Contains(s, "'AC Power'")
		obs.BatteryPercent = parseBatteryPercent(s)
	}
	if out, err := exec.CommandContext(ctx, "ioreg", "-c", "IOHIDSystem").Output(); err == nil {
		if ns, ok := parseHIDIdleTime(string(out)); ok {
			obs.IdleFor = time.Duration(ns)
		}
	}
	return obs, nil
}

// parseBatteryPercent extracts the first "NN%" in `pmset -g batt` output.
func parseBatteryPercent(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		j := i
		for j > 0 && s[j-1] >= '0' && s[j-1] <= '9' {
			j--
		}
		if j == i {
			continue
		}
		if n, err := strconv.Atoi(s[j:i]); err == nil && n >= 0 && n <= 100 {
			return n
		}
	}
	return 0
}

// parseHIDIdleTime extracts the HIDIdleTime nanoseconds from ioreg output.
func parseHIDIdleTime(s string) (int64, bool) {
	const marker = `"HIDIdleTime" = `
	i := strings.Index(s, marker)
	if i < 0 {
		return 0, false
	}
	rest := strings.TrimSpace(s[i+len(marker):])
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(rest[:end], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// newOSWatcher returns a macOS power assertion backed by `caffeinate`: it
// keeps the machine from idle-sleeping while autonomous deep work runs.
func newOSWatcher() power.OSWatcher { return &darwinWatcher{} }

type darwinWatcher struct {
	mu  sync.Mutex
	cmd *exec.Cmd
}

func (w *darwinWatcher) AcquirePowerAssertion() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd != nil {
		return nil
	}
	cmd := exec.Command("caffeinate", "-i", "-w", strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		return err
	}
	w.cmd = cmd
	return nil
}

func (w *darwinWatcher) ReleasePowerAssertion() error {
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
