package mce

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile writes rel under root, creating parents.
func writeFile(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestIngestFileRoundTrip(t *testing.T) {
	cs, _ := newStore(t)
	ctx := context.Background()
	root := t.TempDir()
	src := []byte(goSource)
	writeFile(t, root, "pkg/demo.go", src)

	res, err := cs.IngestFile(ctx, root, "pkg/demo.go",
		IngestOptions{LosslessSwapping: true, FallbackLines: 120}, SourceRef{})
	if err != nil {
		t.Fatalf("IngestFile: %v", err)
	}
	if res.Skipped || res.Chunks == 0 || res.SourceHash == "" {
		t.Fatalf("unexpected result: %+v", res)
	}
	got, err := cs.Reassemble(ctx, res.SourceHash)
	if err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Fatalf("reassembled %d bytes, want %d", len(got), len(src))
	}
}

func TestIngestFileRefusesWhenSwappingDisabled(t *testing.T) {
	cs, _ := newStore(t)
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, root, "demo.go", []byte(goSource))

	_, err := cs.IngestFile(ctx, root, "demo.go",
		IngestOptions{LosslessSwapping: false}, SourceRef{})
	if !errors.Is(err, ErrSwappingDisabled) {
		t.Fatalf("err = %v, want ErrSwappingDisabled", err)
	}
	var n int
	if err := cs.db.DB().QueryRow(`SELECT COUNT(*) FROM context_chunks`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("chunks written while swapping disabled: %d", n)
	}
}

func TestIngestFileRejectsTraversal(t *testing.T) {
	cs, _ := newStore(t)
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, root, "inside.go", []byte(goSource))

	if _, err := cs.IngestFile(ctx, root, "../outside.go",
		IngestOptions{LosslessSwapping: true}, SourceRef{}); err == nil {
		t.Error("traversal path was accepted")
	}
}

func TestIngestFileRejectsSymlinkEscape(t *testing.T) {
	cs, _ := newStore(t)
	ctx := context.Background()
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.go")
	if err := os.WriteFile(secret, []byte(goSource), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.go")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unsupported here: %v", err)
	}
	if _, err := cs.IngestFile(ctx, root, "link.go",
		IngestOptions{LosslessSwapping: true}, SourceRef{}); err == nil {
		t.Error("symlink escaping the root was accepted")
	}
}

func TestIngestFileSkipsOversizeSource(t *testing.T) {
	cs, _ := newStore(t)
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, root, "big.go", bytes.Repeat([]byte("x"), 4096))

	res, err := cs.IngestFile(ctx, root, "big.go",
		IngestOptions{LosslessSwapping: true, MaxSourceBytes: 1024}, SourceRef{})
	if err != nil {
		t.Fatalf("IngestFile: %v", err)
	}
	if !res.Skipped || res.Chunks != 0 {
		t.Fatalf("expected a skip, got %+v", res)
	}
	var chunks int
	if err := cs.db.DB().QueryRow(`SELECT COUNT(*) FROM context_chunks`).Scan(&chunks); err != nil {
		t.Fatal(err)
	}
	if chunks != 0 {
		t.Fatalf("oversize source stored %d chunks, want 0", chunks)
	}
	var events int
	if err := cs.db.DB().QueryRow(
		`SELECT COUNT(*) FROM events WHERE category='context' AND data_json LIKE '%source_skipped%'`,
	).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("skip audit events = %d, want 1", events)
	}
}

func TestIngestFileSplitsOversizeChunks(t *testing.T) {
	cs, _ := newStore(t)
	ctx := context.Background()
	root := t.TempDir()
	// One AST declaration far larger than the cap.
	src := []byte("package big\n\nfunc Big() {\n" + strings.Repeat("\t_ = 0\n", 400) + "}\n")
	writeFile(t, root, "big.go", src)

	const cap = 512
	res, err := cs.IngestFile(ctx, root, "big.go",
		IngestOptions{LosslessSwapping: true, MaxChunkBytes: cap}, SourceRef{})
	if err != nil {
		t.Fatalf("IngestFile: %v", err)
	}
	recs, err := cs.ListBySource(ctx, res.SourceHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) < 2 {
		t.Fatalf("expected the oversized declaration to be split, got %d chunk(s)", len(recs))
	}
	for _, r := range recs {
		if size := r.ByteEnd - r.ByteStart; size > cap {
			t.Errorf("chunk %s = %d bytes, exceeds cap %d", r.ID, size, cap)
		}
	}
	got, err := cs.Reassemble(ctx, res.SourceHash)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, src) {
		t.Fatal("byte-exactness lost after chunk splitting")
	}
}
