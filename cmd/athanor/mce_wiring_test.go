package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func boolPtr(v bool) *bool { return &v }

func testRegistry(t *testing.T) *llm.Registry {
	t.Helper()
	reg, err := llm.NewRegistry(config.Personas{
		Wide:        config.PersonaConfig{Model: "wide-model"},
		Tall:        config.PersonaConfig{Model: "tall-model"},
		Main:        config.PersonaConfig{Model: "main-model"},
		Security:    config.PersonaConfig{Model: "security-model"},
		Alternative: config.PersonaConfig{Model: "alternative-model"},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return reg
}

func openMigrated(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "athanor.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestStartMCERequiresMigratedStore is the M5-T2.7 boot test: the daemon must
// fail loudly when migration 0009 has not run, and the runtime it returns must
// expose a usable chunk store and summarizer.
func TestStartMCERequiresMigratedStore(t *testing.T) {
	st := openMigrated(t)
	reg := testRegistry(t)
	client := llm.NewClient("http://127.0.0.1:1", nil)

	if _, err := startMCE(st, reg, client, config.ContextEngine{}); err == nil {
		t.Fatal("startMCE on an unmigrated database returned a nil error")
	}

	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	rt, err := startMCE(st, reg, client, config.ContextEngine{})
	if err != nil {
		t.Fatalf("startMCE after migrate: %v", err)
	}
	if rt.Store == nil || rt.Summarizer == nil {
		t.Fatalf("incomplete mce runtime: %+v", rt)
	}
	if rt.CompactStore == nil || rt.Compactor == nil {
		t.Fatalf("mce runtime missing compaction wiring: %+v", rt)
	}
	if !rt.LosslessSwapping {
		t.Error("lossless swapping should default to enabled")
	}
	if rt.SummarizerPersona != llm.RoleWide {
		t.Errorf("summarizer persona = %q, want %q", rt.SummarizerPersona, llm.RoleWide)
	}

	// The runtime's store is usable end to end.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"),
		[]byte("package a\n\nfunc A() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := rt.Store.IngestFile(context.Background(), root, "a.go",
		mce.IngestOptions{LosslessSwapping: rt.LosslessSwapping}, mce.SourceRef{})
	if err != nil {
		t.Fatalf("IngestFile: %v", err)
	}
	if res.Chunks == 0 {
		t.Fatal("ingest produced no chunks")
	}
}

// TestStartMCEReflectsSwappingFlag pins that the runtime mirrors the config so
// the M5-T3 swap and the ingestion gate can branch on it.
func TestStartMCEReflectsSwappingFlag(t *testing.T) {
	st := openMigrated(t)
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	rt, err := startMCE(st, testRegistry(t), llm.NewClient("http://127.0.0.1:1", nil),
		config.ContextEngine{EnableLosslessSwapping: boolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	if rt.LosslessSwapping {
		t.Error("runtime ignored enable_lossless_swapping=false")
	}
}
