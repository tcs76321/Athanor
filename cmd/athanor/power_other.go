//go:build !darwin && !linux

package main

import (
	"context"
	"time"

	"github.com/tcs76321/athanor/internal/power"
)

// newPowerObserver is the fallback for platforms without a power adapter:
// assume AC and an active user, so the daemon stays in the conservative
// interactive profile and never daydreams or asserts.
func newPowerObserver() power.Observer { return fallbackObserver{} }

type fallbackObserver struct{}

func (fallbackObserver) Observe(context.Context) (power.Observation, error) {
	return power.Observation{OnAC: true, IdleFor: 0, Now: time.Now()}, nil
}

// newOSWatcher is a no-op assertion on unsupported platforms.
func newOSWatcher() power.OSWatcher { return &power.NoopWatcher{} }
