// Tests for the quarantined_files repository (migration 0008;
// ADR-0015). The repo is the only writer; the ingress and
// egress pipelines go through Put/Get/List. Idempotency
// (the duplicate_ignored path) is the structural property
// under test.
package store

import (
	"context"
	"testing"
	"time"

	"github.com/tcs76321/athanor/migrations"
)

func openQuarantineTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestQuarantineRepo_PutAndGet(t *testing.T) {
	st := openQuarantineTestStore(t)
	qr := NewQuarantineRepo(st)
	ctx := context.Background()
	q := Quarantine{
		SHA256:     "abc123",
		RelPath:    "inbox/foo.txt",
		Reason:     "scanner:test:rejected",
		SourceSize: 42,
		StoredPath: "/state/workspace/quarantine/2026-09-04/abc123.txt",
		Pipeline:   "ingress",
	}
	existed, err := qr.Put(ctx, q)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if existed {
		t.Errorf("Put returned existed=true on first call")
	}
	got, err := qr.Get(ctx, "abc123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Reason != q.Reason {
		t.Errorf("Reason = %q, want %q", got.Reason, q.Reason)
	}
	if got.Pipeline != "ingress" {
		t.Errorf("Pipeline = %q, want ingress", got.Pipeline)
	}
	if got.SourceSize != 42 {
		t.Errorf("SourceSize = %d, want 42", got.SourceSize)
	}
}

func TestQuarantineRepo_PutIsIdempotent(t *testing.T) {
	st := openQuarantineTestStore(t)
	qr := NewQuarantineRepo(st)
	ctx := context.Background()
	q := Quarantine{
		SHA256:   "dup",
		RelPath:  "inbox/x.txt",
		Reason:   "test",
		Pipeline: "ingress",
	}
	if _, err := qr.Put(ctx, q); err != nil {
		t.Fatal(err)
	}
	existed, err := qr.Put(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if !existed {
		t.Errorf("Put returned existed=false on duplicate; want true")
	}
	rows, err := qr.List(ctx, time.Time{}, "ingress", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("row count = %d, want 1 (duplicate Put must not create a second row)", len(rows))
	}
}

func TestQuarantineRepo_GetNotFound(t *testing.T) {
	st := openQuarantineTestStore(t)
	qr := NewQuarantineRepo(st)
	_, err := qr.Get(context.Background(), "missing")
	if err != ErrQuarantineNotFound {
		t.Errorf("err = %v, want ErrQuarantineNotFound", err)
	}
}

func TestQuarantineRepo_PutRejectsEmptySHA(t *testing.T) {
	st := openQuarantineTestStore(t)
	qr := NewQuarantineRepo(st)
	_, err := qr.Put(context.Background(), Quarantine{Pipeline: "ingress"})
	if err == nil {
		t.Error("Put with empty SHA returned nil error; want failure")
	}
}

func TestQuarantineRepo_PutRejectsInvalidPipeline(t *testing.T) {
	st := openQuarantineTestStore(t)
	qr := NewQuarantineRepo(st)
	_, err := qr.Put(context.Background(), Quarantine{SHA256: "x", Pipeline: "bogus"})
	if err == nil {
		t.Error("Put with invalid pipeline returned nil error; want failure (CHECK constraint)")
	}
}

func TestQuarantineRepo_ListFilters(t *testing.T) {
	st := openQuarantineTestStore(t)
	qr := NewQuarantineRepo(st)
	ctx := context.Background()
	for i, p := range []string{"ingress", "egress", "ingress", "user-prompt"} {
		_, err := qr.Put(ctx, Quarantine{
			SHA256:     string(rune('a' + i)),
			RelPath:    "x",
			Reason:     "x",
			Pipeline:   p,
			IngestedAt: time.Now().Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := qr.List(ctx, time.Time{}, "ingress", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("ingress-only count = %d, want 2", len(rows))
	}
	for _, r := range rows {
		if r.Pipeline != "ingress" {
			t.Errorf("row Pipeline = %q, want ingress", r.Pipeline)
		}
	}
}
