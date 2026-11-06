// Package operator owns the user-facing operating mode of Athanor: Deep,
// Full, Lite, and Stopped. It is deliberately separate from the power policy
// (AC/battery/sleep) and from the kill switch: those are safety signals, while
// this is the household agreement about how much of the machine Athanor may
// use and whether feedback is expected.
package operator

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/tcs76321/athanor/internal/store"
)

// Mode is the effective operating mode.
type Mode string

const (
	ModeAuto    Mode = "auto"
	ModeDeep    Mode = "deep"
	ModeFull    Mode = "full"
	ModeLite    Mode = "lite"
	ModeStopped Mode = "stopped"
)

// Default is the safe household default: the user is around, Athanor may work
// but should stay responsive, and daydreaming is allowed only when nothing
// better is queued.
const Default = ModeFull

// Presence is the host-facing signal. It must never carry app names, window
// titles, keystrokes, or content; only availability and load.
type Presence struct {
	Present bool
	Idle    time.Duration
	Load    float64 // 0..1, host CPU/GPU pressure
}

// Hooks run when the effective mode crosses the stopped boundary. They let the
// Core pause active work and unload models without coupling this package to the
// engine or Ollama.
type Hooks struct {
	OnStopped func(ctx context.Context)
	OnResumed func(ctx context.Context)
}

// Status is the dashboard-facing snapshot.
type Status struct {
	Requested   Mode
	Mode        Mode
	Auto        bool
	Reason      string
	HoldUntil   time.Time
	HoldActive  bool
	Allowed     bool
	Daydream    bool
	Concurrency int
	CPUQuota    float64
}

// State is persisted in system_state.
type State struct {
	Requested  Mode      `json:"requested"`
	HoldUntil  time.Time `json:"hold_until,omitempty"`
	HoldReason string    `json:"hold_reason,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Controller owns the persisted mode and derives the effective mode.
type Controller struct {
	store    *store.Store
	key      string
	maxJobs  int
	presence func() Presence
	hooks    Hooks

	mu    sync.RWMutex
	state State
}

// New loads the persisted operator state. A missing state defaults to Full; a
// corrupt state is an error rather than silently changing the household mode.
func New(st *store.Store, maxJobs int, presence func() Presence) (*Controller, error) {
	c := &Controller{
		store: st, key: "operator:state", maxJobs: maxJobs, presence: presence,
		state: State{Requested: Default, UpdatedAt: time.Now().UTC()},
	}
	if maxJobs < 1 {
		c.maxJobs = 1
	}
	if st == nil {
		return c, nil
	}
	var raw string
	err := st.DB().QueryRow(`SELECT value FROM system_state WHERE key = ?`, c.key).Scan(&raw)
	switch {
	case err == nil:
		if uerr := json.Unmarshal([]byte(raw), &c.state); uerr != nil {
			return nil, fmt.Errorf("operator: parsing saved mode: %w", uerr)
		}
		if !validRequested(c.state.Requested) {
			return nil, fmt.Errorf("operator: unknown saved mode %q", c.state.Requested)
		}
	case errors.Is(err, sql.ErrNoRows):
		// First run: keep the safe default.
	default:
		return nil, fmt.Errorf("operator: reading saved mode: %w", err)
	}
	return c, nil
}

func validRequested(m Mode) bool {
	switch m {
	case ModeAuto, ModeDeep, ModeFull, ModeLite, ModeStopped:
		return true
	default:
		return false
	}
}

// SetHooks wires the stopped/resumed side effects. They are called outside the
// controller lock.
func (c *Controller) SetHooks(h Hooks) {
	c.mu.Lock()
	c.hooks = h
	c.mu.Unlock()
}

// Set applies a requested mode. A positive hold makes it temporary; when the
// hold expires the controller returns to Auto. ModeStopped is always explicit
// and never auto-resumes.
func (c *Controller) Set(ctx context.Context, requested Mode, hold time.Duration, reason string) error {
	if !validRequested(requested) {
		return fmt.Errorf("operator: unknown mode %q", requested)
	}
	now := time.Now().UTC()
	c.mu.Lock()
	old := c.effectiveLocked(now).Mode
	oldState := c.state
	c.state.Requested = requested
	c.state.HoldUntil = time.Time{}
	c.state.HoldReason = ""
	if hold > 0 && requested != ModeStopped && requested != ModeAuto {
		c.state.HoldUntil = now.Add(hold)
		c.state.HoldReason = reason
	}
	if requested == ModeStopped {
		c.state.HoldReason = reason
	}
	c.state.UpdatedAt = now
	state := c.state
	c.mu.Unlock()

	if err := c.persist(ctx, state); err != nil {
		// Restore the previous in-memory state: reporting success while the
		// mode was only changed in memory would silently lose the operator's
		// choice on restart.
		c.mu.Lock()
		c.state = oldState
		c.mu.Unlock()
		return err
	}
	c.after(ctx, old)
	return nil
}

// Stop is the red-button path: explicit, persistent, and never automatic.
func (c *Controller) Stop(ctx context.Context, reason string) error {
	return c.Set(ctx, ModeStopped, 0, reason)
}

// UseComputer is a temporary Lite hold. It expires back to Auto.
func (c *Controller) UseComputer(ctx context.Context, d time.Duration, reason string) error {
	return c.Set(ctx, ModeLite, d, reason)
}

// Resume clears a manual stop and returns to Auto.
func (c *Controller) Resume(ctx context.Context) error {
	return c.Set(ctx, ModeAuto, 0, "")
}

// Status returns the effective mode and the policy values derived from it.
func (c *Controller) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.statusLocked(time.Now().UTC())
}

// Allowed is the engine gate: stopped means no phase may run.
func (c *Controller) Allowed() bool { return c.Status().Allowed }

// DaydreamAllowed is true only in Full. Deep is for committed unattended
// work, not idle filler; Lite is for user work only.
func (c *Controller) DaydreamAllowed() bool { return c.Status().Daydream }

// Concurrency caps job concurrency by mode.
func (c *Controller) Concurrency() int { return c.Status().Concurrency }

func (c *Controller) statusLocked(now time.Time) Status {
	eff := c.effectiveLocked(now)
	s := Status{
		Requested: eff.Requested,
		Mode:      eff.Mode,
		Auto:      eff.Requested == ModeAuto,
		Reason:    eff.Reason,
		Allowed:   eff.Mode != ModeStopped,
		Daydream:  eff.Mode == ModeFull,
	}
	if !c.state.HoldUntil.IsZero() && now.Before(c.state.HoldUntil) {
		s.HoldActive = true
		s.HoldUntil = c.state.HoldUntil
	}
	switch eff.Mode {
	case ModeStopped:
		s.Concurrency = 0
		s.CPUQuota = 0
	case ModeLite:
		s.Concurrency = 1
		s.CPUQuota = 0.30
	default:
		s.Concurrency = c.maxJobs
		s.CPUQuota = 1
	}
	return s
}

type effective struct {
	// Requested is the mode the user (or Auto) is asking for after an expired
	// temporary hold has been folded back into Auto.
	Requested Mode
	Mode      Mode
	Reason    string
}

func (c *Controller) effectiveLocked(now time.Time) effective {
	// An active temporary hold wins over the requested mode. An expired hold
	// falls through to Auto: "use the computer for 30m" must not become a
	// permanent Lite change.
	requested := c.state.Requested
	if !c.state.HoldUntil.IsZero() {
		if now.Before(c.state.HoldUntil) {
			reason := c.state.HoldReason
			if reason == "" {
				reason = "temporary hold"
			}
			return effective{
				Requested: c.state.Requested,
				Mode:      c.state.Requested,
				Reason:    fmt.Sprintf("%s until %s", reason, c.state.HoldUntil.Local().Format("15:04")),
			}
		}
		requested = ModeAuto
	}
	switch requested {
	case ModeDeep, ModeFull, ModeLite, ModeStopped:
		return effective{Requested: requested, Mode: requested, Reason: manualReason(requested)}
	case ModeAuto:
		p := c.presenceLocked()
		return effective{Requested: ModeAuto, Mode: Decide(p), Reason: autoReason(p)}
	default:
		return effective{Requested: Default, Mode: Default, Reason: "default"}
	}
}

func (c *Controller) presenceLocked() Presence {
	if c.presence == nil {
		return Presence{Present: true}
	}
	return c.presence()
}

// Decide is the auto policy. Deep requires a long absence; a present but light
// user gets Full; active/heavy use gets Lite. Ambiguity selects the quieter
// mode.
func Decide(p Presence) Mode {
	if !p.Present {
		if p.Idle >= 45*time.Minute {
			return ModeDeep
		}
		return ModeFull
	}
	if p.Load >= 0.65 {
		return ModeLite
	}
	return ModeFull
}

func manualReason(m Mode) string {
	switch m {
	case ModeDeep:
		return "manual: committed to unattended work; no feedback expected"
	case ModeFull:
		return "manual: user present; work and daydream when nothing better is queued"
	case ModeLite:
		return "manual: user work only; CPU capped; no background"
	case ModeStopped:
		return "manual: stopped; cleanup complete; explicit resume required"
	default:
		return "manual"
	}
}

func autoReason(p Presence) string {
	if !p.Present {
		if p.Idle >= 45*time.Minute {
			return fmt.Sprintf("auto: user away for %s; long unattended work", p.Idle.Round(time.Minute))
		}
		return "auto: user away briefly; Full until the long-idle threshold"
	}
	if p.Load >= 0.65 {
		return "auto: user present and host busy; Lite"
	}
	return "auto: user present and host light; Full with daydreaming"
}

func (c *Controller) after(ctx context.Context, old Mode) {
	c.mu.RLock()
	hooks := c.hooks
	newMode := c.effectiveLocked(time.Now().UTC()).Mode
	c.mu.RUnlock()
	if old == newMode {
		return
	}
	switch {
	case newMode == ModeStopped && hooks.OnStopped != nil:
		hooks.OnStopped(ctx)
	case old == ModeStopped && hooks.OnResumed != nil:
		hooks.OnResumed(ctx)
	}
}

func (c *Controller) persist(ctx context.Context, s State) error {
	if c.store == nil {
		return nil
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("operator: encoding mode: %w", err)
	}
	if _, err := c.store.DB().ExecContext(ctx,
		`INSERT INTO system_state (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, c.key, string(raw)); err != nil {
		return fmt.Errorf("operator: persisting mode: %w", err)
	}
	return nil
}

// FilePresence reads a small host-written JSON heartbeat. It is intentionally
// optional: without a host helper the controller defaults to Full, never to
// Deep.
func FilePresence(path string) func() Presence {
	return func() Presence {
		if path == "" {
			return Presence{Present: true}
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return Presence{Present: true}
		}
		var f struct {
			Present    *bool   `json:"present"`
			IdleSecond float64 `json:"idle_seconds"`
			Load       float64 `json:"load"`
		}
		if json.Unmarshal(raw, &f) != nil {
			return Presence{Present: true}
		}
		p := Presence{Present: true, Load: f.Load}
		if f.Present != nil {
			p.Present = *f.Present
		}
		if f.IdleSecond > 0 {
			p.Idle = time.Duration(f.IdleSecond * float64(time.Second))
		}
		return p
	}
}
