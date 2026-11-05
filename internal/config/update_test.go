package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdatePersonasPreservesAndValidates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := `# local configuration
version: 2
personas:
  wide:
    model: "old-wide:latest"
    context_target: 8192
  tall:
    model: "old-tall:latest"
  main:
    model: "old-main:latest"
  security:
    model: "old-security:latest"
  alternative:
    model: "old-alternative:latest"
execution:
  dag_decomposition: false
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	err := UpdatePersonas(path, map[string]PersonaConfig{
		"tall":     {Model: "new-tall:14b", Family: "qwen", ContextTarget: 32768},
		"security": {Model: "new-sec:7b", Family: "gemma"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("updated config does not load: %v", err)
	}
	if got.Personas.Tall.Model != "new-tall:14b" || got.Personas.Tall.Family != "qwen" || got.Personas.Tall.ContextTarget != 32768 {
		t.Errorf("tall = %+v", got.Personas.Tall)
	}
	if got.Personas.Security.Model != "new-sec:7b" || got.Personas.Security.Family != "gemma" {
		t.Errorf("security = %+v", got.Personas.Security)
	}
	if got.Personas.Wide.Model != "old-wide:latest" || got.Execution.DAGDecomposition {
		t.Errorf("unrelated fields changed: wide=%q dag=%v", got.Personas.Wide.Model, got.Execution.DAGDecomposition)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# local configuration") {
		t.Error("comment was not preserved")
	}
	backups, err := filepath.Glob(path + ".bak.*")
	if err != nil || len(backups) == 0 {
		t.Fatalf("no timestamped backup written: %v", err)
	}
}

func TestUpdateJobPodResources(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := `version: 2
job_pod:
  image: "localhost/athanor-jobpod:latest"
  memory_mb: 512
  cpus: 1.0
  tmpfs_mb: 256
  pids_limit: 64
personas:
  main:
    model: "keep-me:7b"
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateJobPodResources(path, JobPodResources{MemoryMB: 4096, CPUs: 2.5, TmpfsMB: 2048, PidsLimit: 256}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.JobPod.MemoryMB != 4096 || cfg.JobPod.CPUs != 2.5 || cfg.JobPod.TmpfsMB != 2048 || cfg.JobPod.PidsLimit != 256 {
		t.Errorf("resources = %+v", cfg.JobPod)
	}
	if cfg.JobPod.Image != "localhost/athanor-jobpod:latest" || cfg.Personas.Main.Model != "keep-me:7b" {
		t.Error("unrelated fields changed")
	}
	if err := UpdateJobPodResources(path, JobPodResources{MemoryMB: 1, CPUs: 1, TmpfsMB: 1, PidsLimit: 1}); err == nil {
		t.Fatal("out-of-range resources accepted")
	}
}

func TestUpdatePersonasRejectsUnknownRole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 2\npersonas: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdatePersonas(path, map[string]PersonaConfig{"boss": {Model: "x"}}); err == nil {
		t.Fatal("unknown role was accepted")
	}
}

func TestUpdatePersonasMissingFile(t *testing.T) {
	if err := UpdatePersonas(filepath.Join(t.TempDir(), "missing.yaml"), map[string]PersonaConfig{"main": {Model: "x"}}); err == nil {
		t.Fatal("missing file was accepted")
	}
}
