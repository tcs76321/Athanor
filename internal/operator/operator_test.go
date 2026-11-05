package operator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestDefaultIsFull(t *testing.T) {
	c, err := New(nil, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	st := c.Status()
	if st.Mode != ModeFull || st.Requested != ModeFull {
		t.Fatalf("default = %q/%q, want full/full", st.Mode, st.Requested)
	}
	if !st.Allowed || !st.Daydream {
		t.Fatalf("full should allow work and daydreaming: %+v", st)
	}
	if st.Concurrency != 4 || st.CPUQuota != 1 {
		t.Fatalf("full limits = %d/%.2f, want 4/1", st.Concurrency, st.CPUQuota)
	}
}

func TestModeLimits(t *testing.T) {
	c, err := New(nil, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if err := c.Set(ctx, ModeLite, 0, ""); err != nil {
		t.Fatal(err)
	}
	st := c.Status()
	if st.Concurrency != 1 || st.CPUQuota <= 0 || st.CPUQuota >= 1 {
		t.Fatalf("lite limits = %d/%.2f, want 1/capped", st.Concurrency, st.CPUQuota)
	}
	if st.Daydream {
		t.Fatal("lite must not daydream")
	}

	if err := c.Set(ctx, ModeDeep, 0, ""); err != nil {
		t.Fatal(err)
	}
	st = c.Status()
	if !st.Allowed || st.Daydream || st.Concurrency != 4 {
		t.Fatalf("deep should allow work but not daydream: %+v", st)
	}

	if err := c.Stop(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	st = c.Status()
	if st.Allowed || st.Concurrency != 0 || st.CPUQuota != 0 || st.Daydream {
		t.Fatalf("stopped should allow nothing: %+v", st)
	}
}

func TestStoppedResumeHooks(t *testing.T) {
	c, err := New(nil, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	var stopped, resumed int
	c.SetHooks(Hooks{
		OnStopped: func(context.Context) { stopped++ },
		OnResumed: func(context.Context) { resumed++ },
	})
	ctx := context.Background()

	if err := c.Stop(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	if stopped != 1 || resumed != 0 {
		t.Fatalf("after stop: stopped=%d resumed=%d", stopped, resumed)
	}
	// Repeating the current mode must not re-fire cleanup.
	if err := c.Stop(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	if stopped != 1 {
		t.Fatalf("repeated stop fired cleanup again: %d", stopped)
	}
	if err := c.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if resumed != 1 {
		t.Fatalf("after resume: resumed=%d, want 1", resumed)
	}
}

func TestTemporaryHoldExpiresToAuto(t *testing.T) {
	c, err := New(nil, 3, func() Presence { return Presence{Present: true} })
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.UseComputer(ctx, 20*time.Millisecond, "using the computer"); err != nil {
		t.Fatal(err)
	}
	st := c.Status()
	if st.Mode != ModeLite || !st.HoldActive {
		t.Fatalf("hold not active: %+v", st)
	}
	time.Sleep(40 * time.Millisecond)
	st = c.Status()
	if st.Mode != ModeFull {
		t.Fatalf("expired hold mode = %q, want auto→full", st.Mode)
	}
	if !st.Auto || st.Requested != ModeAuto {
		t.Fatalf("expired hold should report auto, got requested=%q auto=%v", st.Requested, st.Auto)
	}
	if st.HoldActive {
		t.Fatal("expired hold still active")
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	st := testStore(t)
	c, err := New(st, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Set(ctx, ModeDeep, 0, ""); err != nil {
		t.Fatal(err)
	}
	again, err := New(st, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Status().Mode; got != ModeDeep {
		t.Fatalf("reloaded mode = %q, want deep", got)
	}
}

func TestCorruptPersistedStateIsAnError(t *testing.T) {
	st := testStore(t)
	if _, err := st.DB().Exec(
		`INSERT INTO system_state (key, value) VALUES (?, ?)`, "operator:state", `{"requested":"sideways"}`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := New(st, 2, nil); err == nil {
		t.Fatal("unknown saved mode was silently accepted")
	}
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name string
		p    Presence
		want Mode
	}{
		{"away long", Presence{Present: false, Idle: time.Hour}, ModeDeep},
		{"away briefly", Presence{Present: false, Idle: 5 * time.Minute}, ModeFull},
		{"present busy", Presence{Present: true, Load: 0.9}, ModeLite},
		{"present light", Presence{Present: true, Load: 0.1}, ModeFull},
	}
	for _, tc := range cases {
		if got := Decide(tc.p); got != tc.want {
			t.Errorf("%s: Decide = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestFilePresence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "presence.json")

	// A missing heartbeat is "present": never claim the user is away.
	if p := FilePresence(path)(); !p.Present {
		t.Fatal("missing presence file should default to present")
	}
	raw, _ := json.Marshal(map[string]any{"present": false, "idle_seconds": 3600, "load": 0.2})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	p := FilePresence(path)()
	if p.Present || p.Idle != time.Hour || p.Load != 0.2 {
		t.Fatalf("presence = %+v", p)
	}
	if got := Decide(p); got != ModeDeep {
		t.Fatalf("long absence decided %q, want deep", got)
	}
}

type baseCap int

func (b baseCap) MaxConcurrentJobs() int { return int(b) }

func TestCeilingComposition(t *testing.T) {
	c, err := New(nil, 6, nil)
	if err != nil {
		t.Fatal(err)
	}
	ceil := Ceiling{Controller: c, Base: baseCap(4)}
	if got := ceil.MaxConcurrentJobs(); got != 4 {
		t.Fatalf("full ceiling = %d, want min(6,4)=4", got)
	}
	if err := c.Set(context.Background(), ModeLite, 0, ""); err != nil {
		t.Fatal(err)
	}
	if got := ceil.MaxConcurrentJobs(); got != 1 {
		t.Fatalf("lite ceiling = %d, want 1", got)
	}
	if err := c.Stop(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	if got := ceil.MaxConcurrentJobs(); got != 0 {
		t.Fatalf("stopped ceiling = %d, want 0", got)
	}
	// Nil controller falls back to the power/config ceiling.
	if got := (Ceiling{Base: baseCap(3)}).MaxConcurrentJobs(); got != 3 {
		t.Fatalf("base-only ceiling = %d, want 3", got)
	}
}
