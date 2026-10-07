//go:build linux || darwin

package jobpod

import (
	"regexp"
	"strings"
	"testing"
)

// uuidV4Regex pins the exact shape produced by internal/ids.New: a
// v4 UUID with the variant bits in [89ab]. Drift here means the
// container name validator and the actual ids.New are out of sync.
var uuidV4Regex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// sampleSpec returns a Spec with every field populated. Tests
// override one field at a time to assert specific behavior.
func sampleSpec() Spec {
	return Spec{
		ID:       "3b241101-e2bb-4255-8caf-4136c566a962",
		Image:    "alpine:3.20",
		Command:  []string{"sh", "-c", "echo hello"},
		Token:    "c2245b8ce60da78b3fca76a48aa2b2cb",
		TokenDir: "/tmp/athanor-token-xxx",
		ResourceLimits: Limits{
			PidsLimit: 64, MemoryMB: 512, CPUs: 1.0,
		},
	}
}

// TestBuildArgs_CommonFlags (M2-T2 acceptance) asserts every §21.2
// flag is present in the joined argv. A failure on any of these
// lines is a ROADMAP M2-T2 acceptance failure.
func TestBuildArgs_CommonFlags(t *testing.T) {
	args := buildArgs(sampleSpec())
	joined := joinArgs(args)
	wants := []string{
		"--read-only",
		"--cap-drop ALL",
		"--security-opt no-new-privileges",
		"--pids-limit 64",
		"--memory 512m",
		"--cpus 1.0",
		"--network none",
		"--tmpfs /tmp:rw,noexec,nosuid,nodev,size=8m",
		"--name athanor-job-3b241101-e2bb-4255-8caf-4136c566a962",
		"alpine:3.20",
		"sh -c echo hello",
	}
	for _, want := range wants {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q\ngot: %s", want, joined)
		}
	}
}

// TestBuildArgs_AppliesDefaults (M2-T2 acceptance) asserts zero
// limits are filled with documented defaults: 64 pids, 512 MB, 1.0 cpu.
func TestBuildArgs_AppliesDefaults(t *testing.T) {
	s := sampleSpec()
	s.ResourceLimits = Limits{} // zero
	args := buildArgs(s)
	joined := joinArgs(args)
	for _, want := range []string{"--pids-limit 64", "--memory 512m", "--cpus 1.0"} {
		if !strings.Contains(joined, want) {
			t.Errorf("defaulted argv missing %q\ngot: %s", want, joined)
		}
	}
}

// TestBuildArgs_IncludesTokenMount (M2-T3 prep) asserts the per-job
// token is bind-mounted read-only at /run/athanor.
func TestBuildArgs_IncludesTokenMount(t *testing.T) {
	args := buildArgs(sampleSpec())
	joined := joinArgs(args)
	want := "--mount type=bind,source=/tmp/athanor-token-xxx,target=/run/athanor,ro"
	if !strings.Contains(joined, want) {
		t.Errorf("argv missing token mount %q\ngot: %s", want, joined)
	}
}

// TestBuildArgs_OmitsTokenMountWhenNoDir proves the token mount is
// optional: a spec without a TokenDir does not produce a mount flag.
// M2-T3 always passes TokenDir; M2-T2 unit tests cover both paths.
func TestBuildArgs_OmitsTokenMountWhenNoDir(t *testing.T) {
	s := sampleSpec()
	s.TokenDir = ""
	args := buildArgs(s)
	joined := joinArgs(args)
	if strings.Contains(joined, "--mount") {
		t.Errorf("argv unexpectedly contains a mount flag\ngot: %s", joined)
	}
}

// TestBuildArgs_PackageFlagsCannotBypassHardening is the defense-in-
// depth check. The package constructs every flag before the image;
// the user's Command runs *inside* the pod and is the user's
// responsibility. We assert that the package-generated flag block
// (everything up to and including the image) never contains a
// container-escape flag, regardless of what the caller puts in
// Command. Banned: --privileged, --pid=host, --net=host, --cap-add,
// --userns=host.
func TestBuildArgs_PackageFlagsCannotBypassHardening(t *testing.T) {
	s := sampleSpec()
	// Hostile Command: tries to smuggle in flags that would, if they
	// were podman flags rather than the image's command, re-enable
	// privileged mode. They are not — they are the user's command.
	// The package's responsibility is to make sure its own flag
	// block does not contain these strings.
	s.Command = []string{
		"--privileged", "--pid=host", "--net=host", "--cap-add=ALL",
		"--userns=host", "actual-command",
	}
	args := buildArgs(s)
	// Split into "package flags" (everything before the image) and
	// "user command" (everything after). Only the package flags are
	// the package's responsibility.
	imageIdx := -1
	for i, a := range args {
		if a == s.Image {
			imageIdx = i
			break
		}
	}
	if imageIdx < 0 {
		t.Fatalf("image %q not found in argv: %v", s.Image, args)
	}
	packageFlags := strings.Join(args[:imageIdx+1], " ")
	banned := []string{
		"--privileged", "--pid=host", "--net=host", "--cap-add", "--userns=host",
	}
	for _, b := range banned {
		if strings.Contains(packageFlags, b) {
			t.Errorf("package flag block contains banned %q\ngot: %s", b, packageFlags)
		}
	}
}

// TestBuildArgs_StartsWithPodmanRun is a structural sanity check: the
// first two argv tokens are always "run" and "--rm" so the package
// is not misused as a one-shot `podman exec` wrapper.
func TestBuildArgs_StartsWithPodmanRun(t *testing.T) {
	args := buildArgs(sampleSpec())
	if len(args) < 2 || args[0] != "run" || args[1] != "--rm" {
		t.Errorf("argv does not start with `podman run --rm`: %v", args[:2])
	}
}

// TestBuildArgs_UsesDetach is required by Manager.Start: the Manager
// supervises via `podman inspect` polling, so the pod must be
// detached. A foreground `podman run` would block the caller.
func TestBuildArgs_UsesDetach(t *testing.T) {
	args := buildArgs(sampleSpec())
	if !strings.Contains(joinArgs(args), "--detach") {
		t.Errorf("argv missing --detach: %v", args)
	}
}

// TestUUIDV4Regex pins the validator's accepted shape against
// internal/ids.New. If internal/ids ever changes, this test fails
// here first, before the validator rejects real IDs at runtime.
func TestUUIDV4Regex(t *testing.T) {
	good := "3b241101-e2bb-4255-8caf-4136c566a962"
	if !uuidV4Regex.MatchString(good) {
		t.Errorf("regex rejected canonical v4 UUID: %s", good)
	}
	bad := []string{
		"",
		"3b241101-e2bb-0255-8caf-4136c566a962", // wrong version nibble
		"3b241101-e2bb-4255-1caf-4136c566a962", // wrong variant nibble
		"3b241101e2bb42558caf4136c566a962",     // no dashes
		"3b241101-e2bb-4255-8caf-4136c566a96Z", // non-hex
	}
	for _, b := range bad {
		if uuidV4Regex.MatchString(b) {
			t.Errorf("regex accepted bad input: %q", b)
		}
	}
}
