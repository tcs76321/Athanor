// Gate G2 (M2-T3, ADR-0008): structural proof that the internal
// API at /internal/v1/ is correctly contained.
//
// Gate G1 forbids tool-execution in the agent's own code
// (os/exec, container clients, raw syscalls). Gate G2 adds the
// M2-T3-specific structural guarantees that come with exposing a
// pod-callable HTTP surface:
//
//  1. internal/internalapi/ has no dependency on internal/llm.
//     The internal API serves the pod, not the model; if a
//     future change imports the LLM client, the pod would
//     have an indirect path to model calls that bypasses the
//     tool surface — a Gate G2 violation.
//
//  2. internal/internalapi/middleware.go references
//     crypto/subtle.ConstantTimeCompare by name. The
//     constant-time token compare is the structural defense
//     against timing attacks; bypassing it with `==` is a
//     Gate G2 violation. The text-search is structural, not a
//     runtime test of constant-time behavior — see the
//     test for the rationale.
//
//  3. Every http.Handle call under internal/internalapi/ that
//     registers a /internal/v1/ path is reached through
//     authMiddleware. The structural check is "handlers.go
//     contains the literal authMiddleware(a.tokens, ...)" —
//     we don't parse the AST for this; a grep on the file is
//     enough because handlers.go is the only registration
//     point.
//
// What Gate G2 does NOT prove:
//
//   - That the bearer token never leaks. (Covered by
//     TestToken_NotInPodmanArgv in internal/jobpod and by
//     not logging the token in middleware.go.)
//   - That every future handler goes through the middleware.
//     The structural check requires that handlers.go
//     references authMiddleware; a new file that registers
//     handlers on a different mux would not be caught. The
//     intended discipline is "all internal API routes are
//     registered in handlers.go" — a package convention, not
//     a Go enforcement.
//   - That the auth flow is correct. The behavioral tests in
//     internal/internalapi/internalapi_test.go exercise
//     the happy and unhappy paths; Gate G2 is the structural
//     backstop only.
package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)
// internalapiDir is the package whose surface Gate G2
// constrains. Relative to the test file (internal/gate/).
const internalapiDir = "../../internal/internalapi"

// TestGateG2NoLLMDependency walks internal/internalapi/ and
// asserts no production source imports internal/llm. The
// internal API is the only path a Job Pod has to do anything;
// if it imported the LLM client, the pod would have an indirect
// model-call path that bypasses every other containment check.
func TestGateG2NoLLMDependency(t *testing.T) {
	violations := 0
	err := filepath.WalkDir(internalapiDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		// Cheap text search: any line containing the import
		// path is a violation. We use text rather than the
		// AST because the import path string is unique
		// enough to be unambiguous and we want the gate
		// test to be readable in isolation.
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, "github.com/tcs76321/athanor/internal/llm") {
				t.Errorf("%s imports internal/llm — internal API must not depend on the LLM client (Gate G2)", path)
				violations++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if violations > 0 {
		t.Fatalf("%d Gate G2 violations: internal API depends on internal/llm", violations)
	}
}

// TestGateG2ConstantTimeComparePresent is the structural
// guarantee that the bearer-token compare uses crypto/subtle.
//
// Bypassing ConstantTimeCompare with `==` would re-introduce a
// timing side channel; the structural check is a backstop, not
// the only defense. The actual constant-time property is exercised
// in unit tests (TestAuthMiddleware_AcceptsValidToken and friends)
// — those tests are behavioral, this gate is structural.
func TestGateG2ConstantTimeComparePresent(t *testing.T) {
	middleware := filepath.Join(internalapiDir, "middleware.go")
	raw, err := os.ReadFile(middleware)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "ConstantTimeCompare") {
		t.Errorf("%s does not reference ConstantTimeCompare; the bearer-token compare must be constant-time (Gate G2)", middleware)
	}
}


// TestGateG2InternalAPIRoutesGoThroughMiddleware asserts every
// route registered under /internal/v1/ in handlers.go is reached
// through authMiddleware. The check is structural (text search):
// handlers.go must reference authMiddleware, and every
// /internal/v1/ route registration must be paired with it.
//
// We count `mux.Handle("..."` calls (one per route registration)
// and `authMiddleware(a.tokens,` calls (one per wrap). The two
// must match. We do not count occurrences of "/internal/v1/" in
// the file because a path pattern + a comment can both mention
// the prefix; counting Handle calls is unambiguous.
//
// M2-T4: the count grows from 3 to 5 (execute_code, run_tests).
// This test was deliberately written to scale with the route
// table — adding a new route without the middleware wrap fails
// the structural check.
func TestGateG2InternalAPIRoutesGoThroughMiddleware(t *testing.T) {
	handlers := filepath.Join(internalapiDir, "handlers.go")
	raw, err := os.ReadFile(handlers)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)

	// The file must reference authMiddleware at all.
	if !strings.Contains(body, "authMiddleware(") {
		t.Errorf("%s does not reference authMiddleware; every /internal/v1/ route must be wrapped (Gate G2)", handlers)
		return
	}

	// Count every mux.Handle call (each line is one route
	// registration in this file's style).
	handleCount := strings.Count(body, "mux.Handle(")
	wrapCount := strings.Count(body, "authMiddleware(a.tokens,")
	if handleCount == 0 {
		t.Errorf("%s has no mux.Handle calls; the API registers no routes (Gate G2)", handlers)
		return
	}
	if wrapCount != handleCount {
		t.Errorf("%s: %d mux.Handle calls but %d authMiddleware wraps; every route must be wrapped (Gate G2)",
			handlers, handleCount, wrapCount)
	}
}

// TestGateG2ToolEnvelopeBypassImpossible is the M2-T4 structural
// defense against bypassing the per-job allowlist. Every route
// handler that is registered for a tool name from the
// internalapi closed set must reference a.tools.EnvelopeFor —
// the only path the handler may use to decide whether the
// request is allowed. A future refactor that reads the tool
// name from the URL or the body and skips the envelope check
// fails this test.
//
// The check is structural (text search): for each tool the
// closed set contains, the handler file must reference
// a.tools.EnvelopeFor. We assert presence rather than count
// because a handler may reference EnvelopeFor once or twice
// (e.g. once for ErrUnknownTool, once for Allows); both are
// acceptable. The only failure mode we care about is "zero
// references in the handler file".
//
// Note: the test reads handlers.go AND exec.go (the file
// that owns the execute_code / run_tests handlers in M2-T4).
// The exact file layout is documented in the plan; a future
// refactor that splits the exec handlers into a different
// file must extend this test deliberately.
func TestGateG2ToolEnvelopeBypassImpossible(t *testing.T) {
	// The handler logic lives in exec.go (M2-T4), not handlers.go.
	// We walk the package to find every .go file that contains a
	// tool name from the closed set and assert that file
	// references a.tools.EnvelopeFor.
	toolNames := []string{"execute_code", "run_tests", "lint", "fetch_url", "search_web"}
	for _, name := range toolNames {
		path, content, ok := findFileContaining(internalapiDir, name)
		if !ok {
			t.Errorf("could not find any .go file under %s containing %q; the handler must exist", internalapiDir, name)
			continue
		}
		if !strings.Contains(content, "a.tools.EnvelopeFor") {
			t.Errorf("%s handles %q but does not reference a.tools.EnvelopeFor; the per-job allowlist can be bypassed (Gate G2)", path, name)
		}
	}
}

// TestGateG2GatewayToolRoutesRegistered is the M4-T7 route-existence
// assertion (ADR-0019 §5, the ADR-0009 pattern): the two gateway-backed
// tools must have their internal API routes registered in handlers.go.
// The envelope-bypass test above proves the handlers consult the
// per-job allowlist; this test proves the routes exist for the
// handlers to sit behind. A future rename that drops a route without
// dropping the tool from the closed set fails here.
func TestGateG2GatewayToolRoutesRegistered(t *testing.T) {
	handlers := filepath.Join(internalapiDir, "handlers.go")
	raw, err := os.ReadFile(handlers)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, route := range []string{
		`"POST /internal/v1/jobs/{id}/fetch_url"`,
		`"POST /internal/v1/jobs/{id}/search_web"`,
	} {
		if !strings.Contains(body, route) {
			t.Errorf("%s does not register %q; the closed-set tool has no route (Gate G2)", handlers, route)
		}
	}
}

// findFileContaining returns the first .go file (non-test) under
// dir whose contents include needle. Used by Gate G2 to discover
// the handler file for each closed-set tool.
func findFileContaining(dir, needle string) (string, string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(string(raw), needle) {
			return path, string(raw), true
		}
	}
	return "", "", false
}

// jobpodArgsDir is the directory whose source files produce the
// `podman run` argv for a Job Pod. Any flag, mount, or escape
// vector that ends up in a running pod must originate in one of
// the .go files here. Gate G2 greps this directory structurally
// because the buildArgs function is build-tag-split
// (linux || darwin) and a CI host may not match the operator's
// platform. The text-search form of the test is platform-agnostic.
const jobpodArgsDir = "../../internal/jobpod"

// TestGateG2JobPodArgvCannotEscape is the M2-T6 structural backstop
// for the §21 containment boundary. Every flag, mount, and source
// that ends up in a `podman run` argv for a Job Pod must be added
// by one of three files: args_common.go, args_linux.go,
// args_darwin.go. This test greps those three files for the
// known-bad substrings that would re-enable a pod escape and
// fails the build if any of them appear.
//
// The complement is TestBuildArgs_PackageFlagsCannotBypassHardening
// in internal/jobpod/args_common_test.go, which asserts the
// *runtime* argv (the result of buildArgs) does not contain the
// most common bypasses. That test is build-tagged
// (linux || darwin) and is the runtime defense. This test is
// the source-tree defense: it runs on every push, on every
// platform, with no build tags, and catches a forbidden flag
// before it ever reaches a running pod.
//
// Forbidden substrings (intentionally overlapping with the runtime
// test, but with a wider net — slirp4netns, AppArmor, bind-mount
// sources into host-private directories):
//
//	--net=slirp4netns        // podman network mode that bridges
//	                          //   to the host's network stack
//	--network=slirp4netns    // long form of the same bypass
//	podman.sock              // any string that would mount the
//	                          //   podman socket into the pod
//	/home/                   // bind-mount source for user homes
//	/root/                   // bind-mount source for root home
//	/Users/                  // macOS user home prefix
//	/etc/passwd              // bind-mount source for shadow file
//	.state/                  // bind-mount source for the SQLite
//	                          //   state directory
//	apparmor=unconfined      // disables AppArmor on Linux hosts
//
// The list is intentionally a denylist of *substrings* that would
// have to appear in the argv source code to be effective. A
// comment in the source mentioning "forbidden --net=slirp4netns"
// would also fail the test, which is the desired property: the
// forbidden flag must never even be discussed in the argv source.
func TestGateG2JobPodArgvCannotEscape(t *testing.T) {
	entries, err := os.ReadDir(jobpodArgsDir)
	if err != nil {
		t.Fatalf("read %s: %v", jobpodArgsDir, err)
	}
	forbidden := []string{
		"--net=slirp4netns",
		"--network=slirp4netns",
		"podman.sock",
		"/home/",
		"/root/",
		"/Users/",
		"/etc/passwd",
		"state/", // matches state/ as a bind-mount source; the
		// token-dir lives at <state>/tokens, which is created
		// by the manager at runtime, not in the argv source.
		"apparmor=unconfined",
	}
	for _, e := range entries {
		name := e.Name()
		// We only care about the three argv-construction source
		// files. The test files (args_*_test.go) are deliberately
		// excluded because the runtime test there is the
		// behavioral companion, not the structural backstop.
		if !strings.HasPrefix(name, "args_") || !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(jobpodArgsDir, name)
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatalf("read %s: %v", path, rerr)
		}
		body := string(raw)
		for _, bad := range forbidden {
			if strings.Contains(body, bad) {
				t.Errorf("%s contains forbidden substring %q; "+
					"the M2-T6 argv construction must not enable "+
					"pod escape (Gate G2)", path, bad)
			}
		}
	}
}

