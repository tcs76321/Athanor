package artifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func openStore(t *testing.T) (*Store, *store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	artDir := filepath.Join(dir, "artifacts")
	// Parent project row for FK integrity.
	if _, err := s.DB().Exec(
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','demo','text','write something worth reading')`,
	); err != nil {
		t.Fatal(err)
	}
	return NewStore(s, artDir), s, artDir
}

func TestKindValid(t *testing.T) {
	for _, k := range []Kind{KindCode, KindDocument, KindDataset, KindProposal, KindEvaluation, KindMedia, KindConfiguration} {
		if !k.Valid() {
			t.Errorf("Kind(%q).Valid() = false, want true", k)
		}
	}
	for _, k := range []Kind{"", "text", "CODE"} {
		if k.Valid() {
			t.Errorf("Kind(%q).Valid() = true, want false", k)
		}
	}
}

// TestStatusFlow pins the §9.3 diagram as code.
func TestStatusFlow(t *testing.T) {
	legal := []struct{ from, to Status }{
		{StatusDraft, StatusCandidate},
		{StatusDraft, StatusQuarantine},
		{StatusCandidate, StatusAccepted},
		{StatusCandidate, StatusRejected},
		{StatusCandidate, StatusQuarantine},
	}
	for _, tc := range legal {
		if !CanTransition(tc.from, tc.to) {
			t.Errorf("CanTransition(%s → %s) = false, want legal", tc.from, tc.to)
		}
	}
	illegal := []struct{ from, to Status }{
		{StatusDraft, StatusAccepted}, // must pass through candidate
		{StatusDraft, StatusRejected},
		{StatusAccepted, StatusCandidate}, // terminal states are final
		{StatusAccepted, StatusDraft},
		{StatusRejected, StatusCandidate},
		{StatusQuarantine, StatusDraft},
		{StatusCandidate, StatusDraft},     // no backwards edges
		{StatusDraft, StatusSuperseded},    // draft is not yet a candidate; promote first
		{StatusSuperseded, StatusAccepted}, // superseded is terminal
		{StatusRejected, StatusAccepted},
	}
	for _, tc := range illegal {
		if CanTransition(tc.from, tc.to) {
			t.Errorf("CanTransition(%s → %s) = true, want illegal", tc.from, tc.to)
		}
		err := ValidateTransition(tc.from, tc.to)
		var ise *IllegalStatusError
		if !errors.As(err, &ise) {
			t.Errorf("ValidateTransition(%s → %s) = %v, want IllegalStatusError", tc.from, tc.to, err)
		}
	}
}

func TestStatusTerminal(t *testing.T) {
	for s, want := range map[Status]bool{
		StatusDraft: false, StatusCandidate: false,
		StatusAccepted: true, StatusRejected: true, StatusQuarantine: true, StatusSuperseded: true,
	} {
		if got := s.Terminal(); got != want {
			t.Errorf("Status(%q).Terminal() = %v, want %v", s, got, want)
		}
	}
}

func TestCreateDraft(t *testing.T) {
	a, s, artDir := openStore(t)
	ctx := context.Background()

	art, err := a.CreateDraft(ctx, "p1", KindDocument, []byte("# Report\n\nFirst draft."))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if art.Status != StatusDraft || art.Version != 1 {
		t.Errorf("new artifact = %s v%d, want draft v1", art.Status, art.Version)
	}
	if art.ContentHash == "" || len(art.ContentHash) != 64 {
		t.Errorf("content hash = %q, want sha-256 hex", art.ContentHash)
	}
	if filepath.Dir(art.StoragePath) != artDir {
		t.Errorf("storage path %q not under %q", art.StoragePath, artDir)
	}

	// Content is on disk and reads back identical.
	data, err := a.ReadContent(ctx, art.ID)
	if err != nil {
		t.Fatalf("ReadContent: %v", err)
	}
	if string(data) != "# Report\n\nFirst draft." {
		t.Errorf("content round-trip = %q", string(data))
	}

	// Content file has restrictive permissions (0600).
	info, err := os.Stat(art.StoragePath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("content file mode = %o, want 600", perm)
	}

	// Creation is audited.
	events, err := s.QueryEvents(ctx, store.EventFilter{ProjectID: "p1", Category: "jobs"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events after create = %d, want 1", len(events))
	}

	// Unknown kinds are rejected before touching disk or DB.
	if _, err := a.CreateDraft(ctx, "p1", Kind("spell"), []byte("x")); err == nil {
		t.Error("unknown kind accepted, want rejection")
	}
	// Unknown projects are rejected by FK.
	if _, err := a.CreateDraft(ctx, "ghost", KindDocument, []byte("x")); err == nil {
		t.Error("unknown project accepted, want FK rejection")
	}
}
