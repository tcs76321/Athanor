package mce

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
)

// episodicLogItem is the standard compactable fixture (log + episodic →
// deterministic, §10.3).
func episodicLogItem(content string) MemoryItem {
	return MemoryItem{
		Content:    []byte(content),
		Profile:    Profile{Type: EpistemicLog, State: TemporalEpisodic},
		Ref:        SourceRef{JobID: "job-1", ProjectID: "proj-1"},
		SourceHash: "src-hash",
	}
}

// fakeCompactor counts calls and, by default, returns a different string each
// call, so a test can prove an identical input never reaches it twice.
type fakeCompactor struct {
	calls   int32
	fixed   string
	version map[CompactionKind]string
	err     error
}

func (f *fakeCompactor) Compact(_ context.Context, _ MemoryItem, _ CompactionKind) (string, error) {
	n := atomic.AddInt32(&f.calls, 1)
	if f.err != nil {
		return "", f.err
	}
	if f.fixed != "" {
		return f.fixed, nil
	}
	return fmt.Sprintf("compacted-call-%d", n), nil
}

func (f *fakeCompactor) TemplateVersion(kind CompactionKind) string {
	if f.version != nil {
		if v, ok := f.version[kind]; ok {
			return v
		}
	}
	return "test-v1"
}

func (f *fakeCompactor) callCount() int { return int(atomic.LoadInt32(&f.calls)) }

// TestCompactionDeterminism is the M5-T6 acceptance test (ROADMAP §M5-T6,
// §10.3): the same input yields the same output across runs. The compactor is
// deliberately unstable, so the only thing that can make the two outputs equal
// is the content-address cache (ADR-0025 §1).
func TestCompactionDeterminism(t *testing.T) {
	_, st := newStore(t)
	cs := NewCompactStore(st)
	ctx := context.Background()
	comp := &fakeCompactor{}
	item := episodicLogItem("boom: exit 1\n")

	first, err := cs.CompactMemory(ctx, item, comp)
	if err != nil {
		t.Fatalf("first compaction: %v", err)
	}
	if first.Cached {
		t.Fatal("first compaction must not be cached")
	}
	second, err := cs.CompactMemory(ctx, item, comp)
	if err != nil {
		t.Fatalf("second compaction: %v", err)
	}
	if !second.Cached {
		t.Fatal("second compaction must be a cache hit")
	}
	if second.Content != first.Content {
		t.Fatalf("output drifted: %q vs %q", second.Content, first.Content)
	}
	if second.InputHash != first.InputHash {
		t.Fatalf("input hash drifted: %q vs %q", second.InputHash, first.InputHash)
	}
	if got := comp.callCount(); got != 1 {
		t.Fatalf("compactor called %d times, want 1", got)
	}
}

// TestCompactionDeterminismSurvivesRestart proves the content-address row is
// persisted, so a reopen returns the same bytes without a model call. The
// compactor is swapped between stores: only storage can make the outputs match.
func TestCompactionDeterminismSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "athanor.db")
	_, st1 := openStoreAt(t, path)
	ctx := context.Background()
	item := episodicLogItem("restart me\n")

	first, err := NewCompactStore(st1).CompactMemory(ctx, item, &fakeCompactor{fixed: "first-run"})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	_ = st1.Close()

	_, st2 := openStoreAt(t, path)
	secondComp := &fakeCompactor{fixed: "second-run"}
	second, err := NewCompactStore(st2).CompactMemory(ctx, item, secondComp)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Content != first.Content {
		t.Fatalf("restart drifted: %q vs %q", second.Content, first.Content)
	}
	if !second.Cached {
		t.Fatal("post-restart compaction must be a cache hit")
	}
	if secondComp.callCount() != 0 {
		t.Fatalf("post-restart compactor called %d times, want 0", secondComp.callCount())
	}
}

// TestCompactionTemplateVersionChangesKey pins that a template bump is a new
// content-address key (ADR-0025 §1), so old outputs stay addressable.
func TestCompactionTemplateVersionChangesKey(t *testing.T) {
	_, st := newStore(t)
	cs := NewCompactStore(st)
	ctx := context.Background()
	comp := &fakeCompactor{version: map[CompactionKind]string{CompactionDeterministic: "v1"}}
	item := episodicLogItem("template\n")

	first, err := cs.CompactMemory(ctx, item, comp)
	if err != nil {
		t.Fatal(err)
	}
	comp.version[CompactionDeterministic] = "v2"
	second, err := cs.CompactMemory(ctx, item, comp)
	if err != nil {
		t.Fatal(err)
	}
	if first.InputHash == second.InputHash {
		t.Fatal("template-version change did not change the input hash")
	}
	if second.Cached {
		t.Fatal("a new template version must be a cache miss")
	}
	if comp.callCount() != 2 {
		t.Fatalf("compactor called %d times, want 2", comp.callCount())
	}
}

// TestCompactionKindSeparation pins that the same bytes under a deterministic
// profile and a semantic profile are distinct rows.
func TestCompactionKindSeparation(t *testing.T) {
	_, st := newStore(t)
	cs := NewCompactStore(st)
	ctx := context.Background()
	comp := &fakeCompactor{fixed: "x"}
	det := episodicLogItem("same bytes\n")
	sem := MemoryItem{
		Content: []byte("same bytes\n"),
		Profile: Profile{Type: EpistemicDocumentation, State: TemporalArchival},
	}

	a, err := cs.CompactMemory(ctx, det, comp)
	if err != nil {
		t.Fatal(err)
	}
	b, err := cs.CompactMemory(ctx, sem, comp)
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != CompactionDeterministic || b.Kind != CompactionSemantic {
		t.Fatalf("kinds = %s/%s, want deterministic/semantic", a.Kind, b.Kind)
	}
	if a.InputHash == b.InputHash {
		t.Fatal("different kinds shared an input hash")
	}
	if comp.callCount() != 2 {
		t.Fatalf("compactor called %d times, want 2", comp.callCount())
	}
}

// TestCompactionInputChangeMisses pins that a content change is a cache miss.
func TestCompactionInputChangeMisses(t *testing.T) {
	_, st := newStore(t)
	cs := NewCompactStore(st)
	ctx := context.Background()
	comp := &fakeCompactor{fixed: "x"}
	if _, err := cs.CompactMemory(ctx, episodicLogItem("one\n"), comp); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CompactMemory(ctx, episodicLogItem("two\n"), comp); err != nil {
		t.Fatal(err)
	}
	if comp.callCount() != 2 {
		t.Fatalf("compactor called %d times, want 2", comp.callCount())
	}
}

// TestCompactionStoreRejectsNonZeroTemperature pins the storage-layer Temp-0.0
// invariant (§10.3, ADR-0025 §1): the CHECK is the last of the three guards.
func TestCompactionStoreRejectsNonZeroTemperature(t *testing.T) {
	_, st := newStore(t)
	_, err := st.DB().ExecContext(context.Background(), `
		INSERT INTO compacted_memory
		    (id, kind, profile_type, profile_state, input_hash, template_version,
		     persona, temperature, source_bytes, compacted_bytes, content)
		VALUES ('cm-x','deterministic','log','episodic','h','v','security',0.5,1,1,'c')`)
	if err == nil {
		t.Fatal("CHECK (temperature = 0.0) did not reject a non-zero temperature")
	}
}

// countMemoryCompacted counts the `context`/`memory_compacted` audit rows.
func countMemoryCompacted(t *testing.T, st *store.Store) int {
	t.Helper()
	evs, err := st.QueryEvents(context.Background(), store.EventFilter{Category: "context"})
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	n := 0
	for _, e := range evs {
		if strings.Contains(e.DataJSON, `"memory_compacted"`) {
			n++
		}
	}
	return n
}

// TestCompactionAuditsOnlyOnMiss pins that a cache hit writes no audit row —
// one stored input is one event, however many times it is requested.
func TestCompactionAuditsOnlyOnMiss(t *testing.T) {
	_, st := newStore(t)
	cs := NewCompactStore(st)
	ctx := context.Background()
	comp := &fakeCompactor{fixed: "x"}
	item := episodicLogItem("audit\n")

	if _, err := cs.CompactMemory(ctx, item, comp); err != nil {
		t.Fatal(err)
	}
	if got := countMemoryCompacted(t, st); got != 1 {
		t.Fatalf("events after miss = %d, want 1", got)
	}
	if _, err := cs.CompactMemory(ctx, item, comp); err != nil {
		t.Fatal(err)
	}
	if got := countMemoryCompacted(t, st); got != 1 {
		t.Fatalf("events after hit = %d, want 1", got)
	}
}

// TestCompactionRejectsFullFidelityProfile pins that the matrix (not the
// caller) decides what may be compacted: code is always divided (§10.1).
func TestCompactionRejectsFullFidelityProfile(t *testing.T) {
	_, st := newStore(t)
	cs := NewCompactStore(st)
	_, err := cs.CompactMemory(context.Background(),
		MemoryItem{Content: []byte("package x\n"), Profile: Profile{Type: EpistemicCode, State: TemporalEpisodic}},
		&fakeCompactor{fixed: "x"})
	if !errors.Is(err, ErrNotCompactable) {
		t.Fatalf("err = %v, want ErrNotCompactable", err)
	}
}

// TestCompactionRequiresCompactor pins the nil-seam contract.
func TestCompactionRequiresCompactor(t *testing.T) {
	_, st := newStore(t)
	cs := NewCompactStore(st)
	if _, err := cs.CompactMemory(context.Background(), episodicLogItem("x"), nil); !errors.Is(err, ErrCompactorNeeded) {
		t.Fatalf("err = %v, want ErrCompactorNeeded", err)
	}
}

// TestTreatmentForMatrix is the §10.3 treatment-matrix acceptance table: only
// the rows the matrix assigns to compaction compact; code and everything
// unlisted stay full fidelity (ADR-0025 §2).
func TestTreatmentForMatrix(t *testing.T) {
	tests := []struct {
		name string
		p    Profile
		mode TreatmentMode
		kind CompactionKind
	}{
		{"code active → division", Profile{EpistemicCode, TemporalActive}, ModeDivision, ""},
		{"code episodic → division", Profile{EpistemicCode, TemporalEpisodic}, ModeDivision, ""},
		{"test_output active → division", Profile{EpistemicTestOutput, TemporalActive}, ModeDivision, ""},
		{"test_output recent → division", Profile{EpistemicTestOutput, TemporalRecent}, ModeDivision, ""},
		{"log active → division", Profile{EpistemicLog, TemporalActive}, ModeDivision, ""},
		{"conversation recent → division", Profile{EpistemicConversation, TemporalRecent}, ModeDivision, ""},
		{"log episodic → deterministic", Profile{EpistemicLog, TemporalEpisodic}, ModeCompaction, CompactionDeterministic},
		{"test_output episodic → deterministic", Profile{EpistemicTestOutput, TemporalEpisodic}, ModeCompaction, CompactionDeterministic},
		{"conversation archival → semantic", Profile{EpistemicConversation, TemporalArchival}, ModeCompaction, CompactionSemantic},
		{"documentation episodic → semantic", Profile{EpistemicDocumentation, TemporalEpisodic}, ModeCompaction, CompactionSemantic},
		{"documentation archival → semantic", Profile{EpistemicDocumentation, TemporalArchival}, ModeCompaction, CompactionSemantic},
		{"unknown type episodic → division", Profile{"mystery", TemporalEpisodic}, ModeDivision, ""},
		{"unknown state → division", Profile{EpistemicLog, "mystery"}, ModeDivision, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := TreatmentFor(tc.p)
			if got.Mode != tc.mode {
				t.Fatalf("mode = %s, want %s", got.Mode, tc.mode)
			}
			if tc.mode == ModeCompaction && got.Kind != tc.kind {
				t.Fatalf("kind = %s, want %s", got.Kind, tc.kind)
			}
		})
	}
}

// TestCompactionFailurePersistsNothing pins that a compactor error stores no
// row and no audit event, so the item is retryable.
func TestCompactionFailurePersistsNothing(t *testing.T) {
	_, st := newStore(t)
	cs := NewCompactStore(st)
	ctx := context.Background()
	item := episodicLogItem("x")

	if _, err := cs.CompactMemory(ctx, item, &fakeCompactor{err: errors.New("model down")}); err == nil {
		t.Fatal("expected the compactor error to propagate")
	}
	if got := countMemoryCompacted(t, st); got != 0 {
		t.Fatalf("events after failure = %d, want 0", got)
	}
	if _, err := cs.CompactMemory(ctx, item, &fakeCompactor{fixed: "y"}); err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
}

// TestHasSourceAndJob pins the daydream sources' caught-up checks.
func TestHasSourceAndJob(t *testing.T) {
	_, st := newStore(t)
	cs := NewCompactStore(st)
	ctx := context.Background()
	if _, err := cs.CompactMemory(ctx, episodicLogItem("has\n"), &fakeCompactor{fixed: "x"}); err != nil {
		t.Fatal(err)
	}
	if got, err := cs.HasJob(ctx, "job-1", CompactionDeterministic); err != nil || !got {
		t.Fatalf("HasJob = %v, %v; want true, nil", got, err)
	}
	if got, err := cs.HasSource(ctx, "src-hash", CompactionDeterministic); err != nil || !got {
		t.Fatalf("HasSource = %v, %v; want true, nil", got, err)
	}
	if got, _ := cs.HasJob(ctx, "other", CompactionDeterministic); got {
		t.Fatal("unknown job reported present")
	}
	if got, _ := cs.HasJob(ctx, "", CompactionDeterministic); got {
		t.Fatal("empty job id reported present")
	}
	if got, _ := cs.HasSource(ctx, "src-hash", CompactionSemantic); got {
		t.Fatal("source reported present for a different kind")
	}
}
