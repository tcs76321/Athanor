// Package ci enforces the CI wiring for the behavioral integration
// probes as an executable test (F1).
//
// F1 made the M2 pod-hardening probes (internal/jobpod/security_test.go),
// the M2-T4b exec probe (internal/jobpod/exec_integration_test.go), and
// the M4 gateway probes (internal/gateway/integration_test.go) run in CI.
// That wiring is configuration, not code: a future edit could delete the
// `integration` job from .github/workflows/ci.yml or narrow the
// `test-integration` target back to a single package, silently returning
// the project to "the probes only ever run on a developer's laptop".
//
// This test reads the two files that carry the wiring and fails the build
// if either drifts. It is in `internal/ci/` rather than `internal/gate/`
// for the same reason `internal/deps` is separate: it is a structural
// guarantee about configuration files, not about the source AST. Gate G1
// stays focused on tool-execution containment.
//
// What this test does NOT cover: that the job actually *passes* on a
// runner. Only a real CI run proves that. F3-T7 promoted the job to a
// required check (no `continue-on-error`) and this test now asserts that,
// so a regression cannot quietly return the probes to advisory status.
// F3-T1 added the `vuln` and `tidy` jobs, also asserted here.
package ci

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// workflow is the subset of a GitHub Actions workflow this test asserts
// on. Unknown fields are ignored by yaml.v3, so the struct stays small
// and does not break when the workflow gains unrelated keys.
type workflow struct {
	Jobs map[string]struct {
		ContinueOnError bool `yaml:"continue-on-error"`
		Steps           []struct {
			Name string `yaml:"name"`
			Run  string `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// TestIntegrationJobRunsTheProbes asserts that ci.yml carries an
// `integration` job whose steps invoke `make integration-images` and
// `make test-integration` — the target that runs both probe packages.
func TestIntegrationJobRunsTheProbes(t *testing.T) {
	root, err := findModuleRoot()
	if err != nil {
		t.Fatalf("finding module root: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("reading ci.yml: %v", err)
	}
	var wf workflow
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parsing ci.yml: %v", err)
	}
	job, ok := wf.Jobs["integration"]
	if !ok {
		t.Fatalf("ci.yml has no `integration` job; the behavioral probes would "+
			"no longer run in CI. Jobs present: %v", jobNames(wf))
	}
	// F3-T7: the job must gate merges, not merely observe. A regression
	// that re-adds continue-on-error would silently return the probes to
	// advisory status.
	if job.ContinueOnError {
		t.Errorf("the `integration` job is continue-on-error; the behavioral "+
			"probes must gate merges (F3-T7). Jobs present: %v", jobNames(wf))
	}
	var runs []string
	for _, s := range job.Steps {
		runs = append(runs, s.Run)
	}
	if !anyContains(runs, "make test-integration") {
		t.Errorf("the `integration` job does not run `make test-integration`; steps run: %q", runs)
	}
	if !anyContains(runs, "make integration-images") {
		t.Errorf("the `integration` job does not pull the probe images "+
			"(`make integration-images`); steps run: %q", runs)
	}
}

// TestToolchainJobsRunTheChecks asserts ci.yml carries the F3-T1
// toolchain jobs (`vuln`, `tidy`) wired to the matching Makefile
// targets. Without the CI jobs the checks only exist as documented
// intentions; without the Makefile targets the jobs have nothing to
// call. This pins both halves, in the same spirit as the integration
// test above.
func TestToolchainJobsRunTheChecks(t *testing.T) {
	root, err := findModuleRoot()
	if err != nil {
		t.Fatalf("finding module root: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("reading ci.yml: %v", err)
	}
	var wf workflow
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parsing ci.yml: %v", err)
	}
	for job, want := range map[string]string{
		"vuln": "make vuln",
		"tidy": "make tidy-check",
	} {
		j, ok := wf.Jobs[job]
		if !ok {
			t.Errorf("ci.yml has no %q job; the F3-T1 toolchain check would "+
				"no longer run. Jobs present: %v", job, jobNames(wf))
			continue
		}
		var runs []string
		for _, s := range j.Steps {
			runs = append(runs, s.Run)
		}
		if !anyContains(runs, want) {
			t.Errorf("the %q job does not run %q; steps run: %q", job, want, runs)
		}
	}

	mk, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	if _, ok := targetRecipe(string(mk), "vuln"); !ok {
		t.Errorf("Makefile has no `vuln` target; the CI vuln job has nothing to run")
	}
	if _, ok := targetRecipe(string(mk), "tidy-check"); !ok {
		t.Errorf("Makefile has no `tidy-check` target; the CI tidy job has nothing to run")
	}
}

// TestMakefileIntegrationTargetCoversBothPackages asserts that the
// `test-integration` recipe runs BOTH probe packages. A regression to
// `./internal/jobpod/...` alone would silently drop the M4 gateway probes.
func TestMakefileIntegrationTargetCoversBothPackages(t *testing.T) {
	root, err := findModuleRoot()
	if err != nil {
		t.Fatalf("finding module root: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	recipe, ok := targetRecipe(string(raw), "test-integration")
	if !ok {
		t.Fatalf("Makefile has no `test-integration` target")
	}
	for _, want := range []string{"ATHANOR_RUN_INTEGRATION=1", "./internal/jobpod/...", "./internal/gateway/..."} {
		if !strings.Contains(recipe, want) {
			t.Errorf("test-integration recipe is missing %q; recipe: %q", want, recipe)
		}
	}
	// The image-pull target keeps the probes from silently pulling images.
	images, ok := targetRecipe(string(raw), "integration-images")
	if !ok {
		t.Fatalf("Makefile has no `integration-images` target")
	}
	for _, img := range []string{"alpine:3.20", "python:3.12-alpine"} {
		if !strings.Contains(images, img) {
			t.Errorf("integration-images recipe is missing %q; recipe: %q", img, images)
		}
	}
}

// targetRecipe returns the tab-indented recipe lines of a Makefile target,
// joined by newlines. A target is the exact line `name:` at column 0; its
// recipe is the following run of lines that begin with a tab.
func targetRecipe(makefile, target string) (string, bool) {
	lines := strings.Split(makefile, "\n")
	header := target + ":"
	for i, line := range lines {
		if line != header {
			continue
		}
		var recipe []string
		for _, next := range lines[i+1:] {
			if !strings.HasPrefix(next, "\t") {
				break
			}
			recipe = append(recipe, strings.TrimSpace(next))
		}
		return strings.Join(recipe, "\n"), true
	}
	return "", false
}

// jobNames returns the sorted job names, for a readable failure message.
func jobNames(wf workflow) []string {
	names := make([]string, 0, len(wf.Jobs))
	for name := range wf.Jobs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// anyContains reports whether any string in ss contains sub.
func anyContains(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// findModuleRoot walks up the directory tree looking for go.mod.
func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
