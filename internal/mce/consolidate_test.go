package mce

import (
	"context"
	"errors"
	"testing"
)

// fakeSource returns its items up to the requested limit and records the limit.
type fakeSource struct {
	items     []MemoryItem
	lastLimit int
}

func (f *fakeSource) Next(_ context.Context, limit int) ([]MemoryItem, error) {
	f.lastLimit = limit
	if limit < len(f.items) {
		return f.items[:limit], nil
	}
	return f.items, nil
}

// TestConsolidatorCompactsSource is the §17.1 memory-consolidation pass: a
// source of compactable items is compacted, with the §17.3 counters tallied.
func TestConsolidatorCompactsSource(t *testing.T) {
	_, st := newStore(t)
	src := &fakeSource{items: []MemoryItem{
		episodicLogItem("one\n"),
		episodicLogItem("two\n"),
		episodicLogItem("three\n"),
	}}
	comp := &fakeCompactor{fixed: "x"}
	cons := NewConsolidator(NewCompactStore(st), comp, src)

	res, err := cons.RunOnce(context.Background(), 20)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if res.Processed != 3 || res.Compacted != 3 || res.Cached != 0 || res.Skipped != 0 || res.Failed != 0 {
		t.Fatalf("result = %+v, want processed/compacted 3", res)
	}
	if comp.callCount() != 3 {
		t.Fatalf("compactor called %d times, want 3", comp.callCount())
	}
	if res.BytesBefore == 0 || res.BytesAfter == 0 {
		t.Fatalf("byte counters not populated: %+v", res)
	}
	if src.lastLimit != 20 {
		t.Fatalf("source limit = %d, want 20", src.lastLimit)
	}
}

// TestConsolidatorSecondPassIsCache pins that a repeat pass over an unchanged
// source makes no model call (ADR-0025 §1).
func TestConsolidatorSecondPassIsCache(t *testing.T) {
	_, st := newStore(t)
	src := &fakeSource{items: []MemoryItem{episodicLogItem("one\n"), episodicLogItem("two\n")}}
	comp := &fakeCompactor{fixed: "x"}
	cons := NewConsolidator(NewCompactStore(st), comp, src)
	ctx := context.Background()

	if _, err := cons.RunOnce(ctx, 20); err != nil {
		t.Fatal(err)
	}
	res, err := cons.RunOnce(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if res.Compacted != 0 || res.Cached != 2 {
		t.Fatalf("second pass = %+v, want all cached", res)
	}
	if comp.callCount() != 2 {
		t.Fatalf("compactor called %d times total, want 2", comp.callCount())
	}
}

// TestConsolidatorSkipsFullFidelity pins that the §10.3 matrix governs the
// pass: a code item is skipped, its log sibling is compacted.
func TestConsolidatorSkipsFullFidelity(t *testing.T) {
	_, st := newStore(t)
	src := &fakeSource{items: []MemoryItem{
		{Content: []byte("package x\n"), Profile: Profile{Type: EpistemicCode, State: TemporalEpisodic}},
		episodicLogItem("boom\n"),
	}}
	comp := &fakeCompactor{fixed: "x"}
	cons := NewConsolidator(NewCompactStore(st), comp, src)

	res, err := cons.RunOnce(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if res.Processed != 2 || res.Skipped != 1 || res.Compacted != 1 {
		t.Fatalf("result = %+v, want 2 processed / 1 skipped / 1 compacted", res)
	}
}

// TestConsolidatorCountsFailures pins that a compactor error is counted, not
// fatal, and stores nothing.
func TestConsolidatorCountsFailures(t *testing.T) {
	_, st := newStore(t)
	src := &fakeSource{items: []MemoryItem{episodicLogItem("one\n"), episodicLogItem("two\n")}}
	cons := NewConsolidator(NewCompactStore(st), &fakeCompactor{err: errors.New("model down")}, src)

	res, err := cons.RunOnce(context.Background(), 20)
	if err != nil {
		t.Fatalf("RunOnce should not fail on a per-item compactor error: %v", err)
	}
	if res.Processed != 2 || res.Failed != 2 || res.Compacted != 0 {
		t.Fatalf("result = %+v, want 2 failed", res)
	}
	if got := countMemoryCompacted(t, st); got != 0 {
		t.Fatalf("events = %d, want 0", got)
	}
}

// TestConsolidatorEmptySource pins the caught-up no-op.
func TestConsolidatorEmptySource(t *testing.T) {
	_, st := newStore(t)
	cons := NewConsolidator(NewCompactStore(st), &fakeCompactor{fixed: "x"}, &fakeSource{})
	res, err := cons.RunOnce(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if res.Processed != 0 {
		t.Fatalf("result = %+v, want zero", res)
	}
}

// TestConsolidatorRequiresInputs pins the invalid-input contracts.
func TestConsolidatorRequiresInputs(t *testing.T) {
	_, st := newStore(t)
	cs := NewCompactStore(st)
	ctx := context.Background()

	if _, err := NewConsolidator(cs, nil, &fakeSource{}).RunOnce(ctx, 20); !errors.Is(err, ErrCompactorNeeded) {
		t.Fatalf("nil compactor: err = %v, want ErrCompactorNeeded", err)
	}
	if _, err := NewConsolidator(cs, &fakeCompactor{fixed: "x"}, nil).RunOnce(ctx, 20); err == nil {
		t.Fatal("nil source: want error")
	}
	if _, err := NewConsolidator(cs, &fakeCompactor{fixed: "x"}, &fakeSource{}).RunOnce(ctx, 0); err == nil {
		t.Fatal("limit 0: want error")
	}
}
