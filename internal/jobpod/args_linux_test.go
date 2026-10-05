//go:build linux

package jobpod

import (
	"strings"
	"testing"
)

// TestBuildArgs_LinuxSeccompPresent (M2-T2 acceptance, Linux) asserts
// the seccomp flag is in the argv with the containers-common default
// profile path. seccomp is a meaningful kernel enforcement on Linux;
// its absence (or the Docker-only `runtime/default` spelling, which
// Podman reads as a file path) is a real containment regression.
func TestBuildArgs_LinuxSeccompPresent(t *testing.T) {
	args := buildArgs(sampleSpec())
	joined := joinArgs(args)
	if !strings.Contains(joined, "seccomp=/usr/share/containers/seccomp.json") {
		t.Errorf("linux argv missing seccomp profile\ngot: %s", joined)
	}
}
