// Package deploy keeps the M7-T7/T8 packaging artifacts honest: the Job Pod
// base image, the systemd/launchd service templates, and the install script
// must exist and carry the placeholders install.sh substitutes. Like
// internal/ci and internal/deps, this is a structural test over
// configuration files, not source ASTs.
package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func findRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func TestPackagingArtifactsExist(t *testing.T) {
	root := findRoot(t)
	cases := []struct {
		path     string
		contains []string
	}{
		{"deploy/jobpod.Containerfile", []string{"FROM", "USER", "pytest"}},
		{"deploy/athanor.service", []string{"ExecStart=__BINARY__", "WantedBy=default.target"}},
		{"deploy/com.athanor.agent.plist", []string{"com.athanor.agent", "__BINARY__", "RunAtLoad"}},
		{"scripts/install.sh", []string{"--service", "deploy/athanor.service", "deploy/com.athanor.agent.plist", "__STATE__"}},
	}
	for _, c := range cases {
		raw, err := os.ReadFile(filepath.Join(root, c.path))
		if err != nil {
			t.Fatalf("read %s: %v", c.path, err)
		}
		for _, want := range c.contains {
			if !strings.Contains(string(raw), want) {
				t.Errorf("%s is missing %q", c.path, want)
			}
		}
	}
}

func TestMakefileHasPackagingTargets(t *testing.T) {
	root := findRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	mk := string(raw)
	for _, target := range []string{"\njobpod-image:", "\ninstall:"} {
		if !strings.Contains(mk, target) {
			t.Errorf("Makefile is missing the %q target", strings.TrimSpace(target))
		}
	}
}
