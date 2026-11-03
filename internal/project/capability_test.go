package project

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func newCapabilityRepo(t *testing.T) *Repo {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	return NewRepo(s)
}

func TestInferCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name string
		goal string
		want string
	}{
		{"code", "Implement a small parser module with tests.", CapabilityCode},
		{"data", "Normalize the JSON events to the schema and aggregate totals.", CapabilityData},
		{"document", "Write an architecture decision record with the required sections.", CapabilityDocument},
		{"text", "Write a short email in a friendly tone.", CapabilityText},
		{"media", "Render a short video and a cover image.", CapabilityMedia},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caps := InferCapabilities(tc.goal, "")
			if !slices.Contains(caps, tc.want) {
				t.Errorf("caps = %v, want %s", caps, tc.want)
			}
		})
	}

	// A mixed goal keeps both capabilities.
	caps := InferCapabilities("Implement the API and write the README documentation.", "")
	if !slices.Contains(caps, CapabilityCode) || !slices.Contains(caps, CapabilityDocument) {
		t.Errorf("mixed caps = %v, want code and document", caps)
	}

	// Repository evidence adds code even when the goal is vague.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	caps = InferCapabilities("Make the thing better.", dir)
	if !slices.Contains(caps, CapabilityCode) {
		t.Errorf("repo caps = %v, want code", caps)
	}
}

func TestInferTaskCapability(t *testing.T) {
	if got := InferTaskCapability("Write the README for the parser.", []string{CapabilityCode, CapabilityDocument}, ""); got != CapabilityDocument {
		t.Errorf("task capability = %q, want document", got)
	}
	if got := InferTaskCapability("Implement the parser and make the tests pass.", []string{CapabilityDocument}, ""); got != CapabilityCode {
		t.Errorf("task capability = %q, want code", got)
	}
	if got := InferTaskCapability("Continue the work.", []string{CapabilityData}, ""); got != CapabilityData {
		t.Errorf("fallback capability = %q, want project primary data", got)
	}
}

func TestDetectTestCommand(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		body string
		want string
	}{
		{"go", "go.mod", "module example.com/x\n", "go test ./..."},
		{"cargo", "Cargo.toml", "[package]\nname = \"x\"\n", "cargo test"},
		{"pytest-config", "pyproject.toml", "[tool.pytest.ini_options]\n", "pytest -q"},
		{"make", "Makefile", "test:\n\tgo test ./...\n", "make test"},
		{"package", "package.json", `{"scripts":{"test":"node test.js"}}`, "npm test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, tc.file), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := DetectTestCommand(dir); got != tc.want {
				t.Errorf("command = %q, want %q", got, tc.want)
			}
		})
	}
	if got := DetectTestCommand(t.TempDir()); got != "" {
		t.Errorf("empty repo command = %q, want empty", got)
	}
}

func TestCreateInferredPersistsCapabilities(t *testing.T) {
	r := newCapabilityRepo(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, task, err := r.CreateInferred(t.Context(), "inferred", "Implement the parser and write a README.", dir, []string{"tests pass"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(p.Capabilities, CapabilityCode) || !slices.Contains(p.Capabilities, CapabilityDocument) {
		t.Errorf("project capabilities = %v, want code+document", p.Capabilities)
	}
	if task.Capability != CapabilityCode {
		t.Errorf("task capability = %q, want code", task.Capability)
	}
	if got := p.TestCommand(); got != "go test ./..." {
		t.Errorf("detected test command = %q, want go test ./...", got)
	}
}
