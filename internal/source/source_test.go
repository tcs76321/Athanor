package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectSpecsClassifiesURLsAndFiles(t *testing.T) {
	specs := ProjectSpecs([]string{"https://example.com/a", "note.md", " "}, []string{"wire"})
	if len(specs) != 3 {
		t.Fatalf("specs = %d, want 3: %+v", len(specs), specs)
	}
	if specs[0].Kind != KindURL || specs[0].Location != "https://example.com/a" {
		t.Errorf("URL spec = %+v", specs[0])
	}
	if specs[1].Kind != KindFile || specs[1].Location != filepath.Join("sources", "note.md") {
		t.Errorf("file spec = %+v", specs[1])
	}
	if specs[2].Kind != KindDir || specs[2].Location != "wire" {
		t.Errorf("dir spec = %+v", specs[2])
	}
}

func TestLoadLocalReadsSourcesAndWire(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sources/note.md", "# Note\nA durable fact.")
	writeFile(t, root, "wire/day1/a.md", "story-001 launched")

	m, err := LoadLocal(root, ProjectSpecs([]string{"note.md"}, []string{"wire"}), DefaultLimits())
	if err != nil {
		t.Fatalf("LoadLocal: %v", err)
	}
	if len(m.Loaded) != 2 {
		t.Fatalf("loaded = %d, want 2: %+v", len(m.Loaded), m.Loaded)
	}
	// Deterministic order: sources/note.md sorts before wire/day1/a.md.
	if m.Loaded[0].Name != "note.md" || m.Loaded[1].Name != "wire/day1/a.md" {
		t.Errorf("order = %q, %q", m.Loaded[0].Name, m.Loaded[1].Name)
	}
	texts := m.SourceTexts()
	if !strings.Contains(texts["note.md"], "durable fact") {
		t.Errorf("SourceTexts = %+v", texts)
	}
	if !strings.Contains(texts["wire/day1/a.md"], "story-001") {
		t.Errorf("wire text missing: %+v", texts)
	}
	if m.Loaded[0].Hash == "" || m.Loaded[0].Hash == m.Loaded[1].Hash {
		t.Errorf("hashes should be content-derived and distinct: %+v", m.Loaded)
	}
}

func TestLoadLocalRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("host secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "wire"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "wire", "evil.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	m, err := LoadLocal(root, ProjectSpecs(nil, []string{"wire"}), DefaultLimits())
	if err != nil {
		t.Fatalf("LoadLocal: %v", err)
	}
	if len(m.Loaded) != 0 {
		t.Fatalf("loaded a symlink: %+v", m.Loaded)
	}
	if len(m.Skipped) != 1 || !strings.Contains(m.Skipped[0].Reason, "symlink") {
		t.Errorf("skipped = %+v, want a symlink refusal", m.Skipped)
	}
}

func TestLoadLocalRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	specs := []Spec{{Kind: KindFile, Location: "../secret.md", Name: "secret"}}
	m, err := LoadLocal(root, specs, DefaultLimits())
	if err != nil {
		t.Fatalf("LoadLocal: %v", err)
	}
	if len(m.Loaded) != 0 || len(m.Skipped) != 1 {
		t.Fatalf("manifest = %+v", m)
	}
	if !strings.Contains(m.Skipped[0].Reason, "traversal") {
		t.Errorf("reason = %q", m.Skipped[0].Reason)
	}
}

func TestLoadLocalCapsFileSize(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sources/big.md", strings.Repeat("x", 64))
	limits := Limits{MaxFiles: 10, MaxFileBytes: 16, MaxTotalBytes: 1024}
	m, err := LoadLocal(root, ProjectSpecs([]string{"big.md"}, nil), limits)
	if err != nil {
		t.Fatalf("LoadLocal: %v", err)
	}
	if len(m.Loaded) != 0 || len(m.Skipped) != 1 {
		t.Fatalf("manifest = %+v", m)
	}
	if !strings.Contains(m.Skipped[0].Reason, "byte cap") {
		t.Errorf("reason = %q", m.Skipped[0].Reason)
	}
}

func TestLoadLocalCapsFilesAndTotal(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		writeFile(t, root, filepath.Join("wire", name), "0123456789")
	}
	m, err := LoadLocal(root, ProjectSpecs(nil, []string{"wire"}),
		Limits{MaxFiles: 2, MaxFileBytes: 100, MaxTotalBytes: 15})
	if err != nil {
		t.Fatalf("LoadLocal: %v", err)
	}
	if len(m.Loaded) != 1 {
		t.Fatalf("loaded = %d, want 1 under a 15-byte total cap: %+v", len(m.Loaded), m.Loaded)
	}
	if len(m.Skipped) == 0 {
		t.Error("expected a visible skip for the capped file")
	}
}

func TestLoadLocalSkipsBinaryAndUnknownExtensions(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "wire"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "wire", "blob.md"), []byte{0, 1, 2}, 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "wire/image.png", "not really a png")

	m, err := LoadLocal(root, ProjectSpecs(nil, []string{"wire"}), DefaultLimits())
	if err != nil {
		t.Fatalf("LoadLocal: %v", err)
	}
	if len(m.Loaded) != 0 {
		t.Fatalf("loaded binary/unknown files: %+v", m.Loaded)
	}
	if len(m.Skipped) != 1 || !strings.Contains(m.Skipped[0].Reason, "binary") {
		// The .png is silently ignored as a non-text file; only the NUL
		// file is a named skip.
		t.Errorf("skipped = %+v", m.Skipped)
	}
}

func TestPromptBlockLabelsSources(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sources/note.md", "the fact")
	m, err := LoadLocal(root, ProjectSpecs([]string{"note.md"}, nil), DefaultLimits())
	if err != nil {
		t.Fatalf("LoadLocal: %v", err)
	}
	block := m.PromptBlock(4096)
	for _, want := range []string{"<<<UNTRUSTED DATA", "SOURCE: note.md", "the fact", "<<<END UNTRUSTED DATA>>>"} {
		if !strings.Contains(block, want) {
			t.Errorf("block missing %q:\n%s", want, block)
		}
	}
}

func TestLoadLocalEmptyRootIsNoOp(t *testing.T) {
	m, err := LoadLocal("", ProjectSpecs([]string{"note.md"}, nil), DefaultLimits())
	if err != nil {
		t.Fatalf("LoadLocal: %v", err)
	}
	if len(m.Loaded) != 0 || len(m.Skipped) != 0 {
		t.Fatalf("manifest = %+v", m)
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
