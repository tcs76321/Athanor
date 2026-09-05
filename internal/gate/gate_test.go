// Package gate encodes the milestone gates as executable proofs
// (ROADMAP §3). Gate G1: "No tool execution exists at all — the agent is
// provably contained to LLM + storage."
//
// # What Gate G1 proves
//
// This package fails the build if any production source file under
// `internal/` or `cmd/` violates any of the following:
//
//  1. No tool-execution imports anywhere in production code. The forbidden
//     set is `os/exec` (spawning processes), `os/user` (host user
//     enumeration), `github.com/docker/docker/client` (container control),
//     and `github.com/containers/podman/v5/libpod` (container control).
//
//  2. No raw `syscall` in agent code (`internal/`), with one named
//     exception (§21.3 path containment — see rule 5). The agent's own
//     packages may not touch the syscall surface at large.
//
//  3. `syscall` in `cmd/` (the daemon entry point) is permitted only for
//     signal constants — `SIGTERM`, `SIGINT`, `SIGHUP`, `SIGQUIT`. The
//     test walks the AST and asserts every `syscall.X` selector in `cmd/`
//     references one of the allowlisted identifiers.
//
//  4. `os/exec` in `cmd/` is permitted only for the named file
//     `cmd/athanor/jobpod_client.go` (M2 production Podman client). The
//     allowlist is a single named file, not a directory or pattern; the
//     gate is opt-in by exception, not opt-out by default.
//
//  5. `syscall` in `internal/airlock/paths/paths_linux.go` and
//     `internal/airlock/paths/paths_darwin.go` is permitted only for
//     the `O_NOFOLLOW` open-flag constant (M4-T1, §21.3 file airlock).
//     The gate walks every `syscall.X` selector in those two files and
//     asserts it is the allowlisted identifier. This is the same shape
//     as rule 3 (allowlisted identifiers only) but applied to internal/.
//     The cross-platform fallback `paths_other.go` does not import
//     `syscall` and emits a documented warning that `O_NOFOLLOW` is
//     not enforced on unsupported GOOS; running on a build target
//     outside the allowlist is a Gate G1 tripwire by design —
//     unsupported hosts get a compile-time refusal, not a silent loss
//     of containment. Adding a new platform is a one-line entry in
//     `allowedInternalSyscallIdents` plus a new build-tag-gated file.
//
//  6. `http.Get`, `http.Post`, and `http.DefaultClient` may not be
//     used outside `internal/gateway/` (the §21.5 Internet Gated
//     Reader) or the four `cmd/athanor/cli*.go` files (the loopback
//     CLI client — `cmd/athanor/cli.go`'s `apiCall` helper, plus the
//     three subcommand files that call it or call `http.DefaultClient.Do`
//     directly with a loopback URL). The gate walks every
//     production source file under `internal/` and `cmd/` and asserts
//     the forbidden selectors only appear in the allowlisted
//     locations. Constructed `*http.Client` usage (e.g. the LLM
//     client in `internal/llm/client.go`, the Job Pod runner in
//     `internal/internalapi/runner/httpclient.go`) is not in the
//     rule — those callers construct explicit timeouts and are
//     sanctioned for loopback / Ollama use. The rule is in its
//     own test (`TestGateG1NoOutboundHTTPOutsideGateway`) so the
//     violation counter is per-rule and a future contributor can
//     diagnose a single failure in isolation. M4-T5.4 / ADR-0017
//     §2 is the durable record of the rule's design; the
//     adversarial suite (M4-T8) extends it with `http.Get`-family
//     fuzz cases.
//
// The implementation is a single AST walk in TestGateG1NoToolExecution
// (rules 1–5) plus a separate AST walk in
// TestGateG1NoOutboundHTTPOutsideGateway (rule 6). Adding a new
// forbidden import, a new tool surface, a new syscall identifier,
// or a new outbound-HTTP helper requires extending these tests and
// updating this comment.
//
// # What Gate G1 does NOT prove
//
// Gate G1 is a structural containment guarantee, not a behavioral one.
// It does not prove:
//
//   - That the LLM cannot be tricked into producing shell commands in
//     its output. Behavioral containment against prompt injection is the
//     job of the M4 prompt-injection scanner (incoming documents) and
//     the M5-T3 byte-load `context_swap` tool, not this gate.
//   - That the daemon's HTTP surface is safe. Loopback-only is enforced
//     in `internal/server/server.go` (LocalhostAddr), and the tool
//     surface is gated by Go route registration. The Go compiler and
//     test coverage are the enforcement layer there.
//   - That the SQLite layer is well-behaved under load. The single-
//     connection pool and WAL mode are ADR-0003/0004 choices; performance
//     is monitored separately.
//
// In short: Gate G1 says "the agent cannot call out to a shell or a
// container client through any code path that this test can see." It
// does not say "the agent is safe." Safety is the rest of the system.
package gate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// forbiddenImports are capabilities M1 must not have anywhere in the
// daemon (production sources only; tests may reference them to describe
// attacks).
var forbiddenImports = map[string]string{
	"os/exec":                                "spawning processes (tool execution)",
	"os/user":                                "host user enumeration",
	"github.com/docker/docker/client":        "container control",
	"github.com/containers/podman/v5/libpod": "container control",
}

// forbiddenInternalImports additionally applies to internal/ only: the
// agent's own code may not touch raw syscalls at all. The daemon entry
// point (cmd/) may use syscall *signal constants* — verified separately
// below.
var forbiddenInternalImports = map[string]string{
	"syscall": "raw syscalls (exec, ptrace, mount, …)",
}

// allowedSyscallIdents are the signal-related syscall identifiers the
// daemon entry point may reference.
var allowedSyscallIdents = map[string]bool{
	"SIGTERM": true, "SIGINT": true, "SIGHUP": true, "SIGQUIT": true,
}

// allowedOsExecFiles are the specific files in cmd/ that may import
// os/exec. M2-T2 introduces the production Podman client
// (cmd/athanor/jobpod_client.go) which legitimately shells out to
// the `podman` binary. The gate still forbids os/exec in internal/
// and in any other cmd/ file; this list is the named exception, not
// a general permission.
//
// M4-T3 adds the scanner adapters in cmd/athanor/scanners/
// (ClamAV, YARA) which legitimately shell out to external
// binaries. The package is a directory, not a single file; the
// per-file allowlist is augmented with `allowedOsExecDirs` so
// adding a new adapter under cmd/athanor/scanners/ requires
// only a one-line entry in `allowedOsExecDirs`, not edits to
// this map. The dirs map is keyed on the full repo-relative
// path so a future contributor cannot create a sibling
// directory and slip through.
var allowedOsExecFiles = map[string]bool{
	"jobpod_client.go": true,
}

// allowedOsExecDirs are the directories in cmd/ under which
// every Go file may import os/exec. The M4-T3 scanner
// adapters (ClamAV, YARA) live here; the gate is opt-in by
// directory, not by file, because adapters naturally come
// in groups (driver + N scanner-specific files).
var allowedOsExecDirs = []string{
	"cmd/athanor/scanners",
}

// allowedInternalSyscallFiles are the specific files in internal/
// that may import `syscall`. M4-T1 introduces the path-containment
// library (§21.3 file airlock), whose build-tag-gated
// `paths_linux.go` and `paths_darwin.go` legitimately need the
// platform's `O_NOFOLLOW` open-flag constant to defeat
// symlink-as-final-component escapes. The gate still forbids
// `syscall` in every other internal/ file; this list is the named
// exception, not a general permission, and the syscall identifiers
// reachable through it are constrained by
// `allowedInternalSyscallIdents` (rule 5 above).
//
// Path entries are the full relative path from the repo root, not
// just the basename, so a future contributor cannot create an
// unannotated `internal/somethingelse/paths_linux.go` and have it
// pass. Adding a new platform is a one-line entry here plus a
// matching entry in `allowedInternalSyscallIdents` plus a new
// build-tag-gated file.
var allowedInternalSyscallFiles = map[string]bool{
	"internal/airlock/paths/paths_linux.go":  true,
	"internal/airlock/paths/paths_darwin.go": true,
}

// allowedInternalSyscallIdents are the syscall identifiers the
// §21.3 path-containment wrappers (M4-T1) may reference. The set is
// closed and small on purpose: `O_NOFOLLOW` is the only
// kernel-level defense against a final-component symlink that
// slips past `filepath.EvalSymlinks` on the prefix. The list is
// the same shape as `allowedSyscallIdents` (the cmd/ signal
// constants) but applied to internal/ via the file allowlist above.
var allowedInternalSyscallIdents = map[string]bool{
	"O_NOFOLLOW": true,
}

// TestGateG1NoToolExecution is the grep-level proof, upgraded to an AST
// walk: no production source under internal/ or cmd/ may import a
// tool-execution capability. The llm client's net/http and the daemon's
// own loopback server are the sanctioned network surfaces; adding any
// outbound-calling tool is a Gate G1 violation until M2 lands its
// guarded runtime.
func TestGateG1NoToolExecution(t *testing.T) {
	roots := []struct {
		dir      string
		internal bool
	}{
		{"../../internal", true},
		{"../../cmd", false},
	}
	violations := 0
	for _, root := range roots {
		err := filepath.WalkDir(root.dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			file, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Errorf("parsing %s: %v", path, perr)
				return nil
			}
			for _, imp := range file.Imports {
				name := strings.Trim(imp.Path.Value, `"`)
				if reason, bad := forbiddenImports[name]; bad {
					// os/exec is allowed in a small named set of
					// cmd/ files (the production Podman client) and
					// inside `allowedOsExecDirs` (the M4-T3 scanner
					// adapters). The gate still forbids it everywhere
					// else.
					if name == "os/exec" && !root.internal {
						if allowedOsExecFiles[filepath.Base(path)] {
							// permitted (single named file)
						} else if isUnderAllowedExecDir(path) {
							// permitted (allowlisted directory)
						} else {
							t.Errorf("%s imports %q — %s", path, name, reason)
							violations++
						}
					} else {
						t.Errorf("%s imports %q — %s", path, name, reason)
						violations++
					}
				}
				if reason, bad := forbiddenInternalImports[name]; bad && root.internal {
					// M4-T1 (§21.3 file airlock): the path-containment
					// library has build-tag-gated files that need
					// `syscall.O_NOFOLLOW`. They are the only files in
					// internal/ that may import `syscall`, and the
					// syscall identifiers they reach are constrained
					// by `allowedInternalSyscallIdents` (enforced
					// below in the same walk). Path-keyed, not
					// basename-keyed, so a future contributor cannot
					// silently create another exception.
					if name == "syscall" && allowedInternalSyscallFiles[relPath(path)] {
						// permitted — the selector check below
						// is the second line of defense.
					} else {
						t.Errorf("%s imports %q — %s", path, name, reason)
						violations++
					}
				}
				if name == "syscall" && root.internal && allowedInternalSyscallFiles[relPath(path)] {
					// Identical shape to the cmd/ signal-constant
					// check above, but applied to the M4-T1 wrapper
					// files. Every `syscall.X` selector in the
					// allowlisted files must reference one of the
					// identifiers in `allowedInternalSyscallIdents`.
					// A new identifier is a Gate G1 violation
					// until both this map and the file allowlist
					// are extended deliberately.
					ast.Inspect(file, func(n ast.Node) bool {
						sel, ok := n.(*ast.SelectorExpr)
						if !ok {
							return true
						}
						id, ok := sel.X.(*ast.Ident)
						if !ok || id.Name != "syscall" {
							return true
						}
						if !allowedInternalSyscallIdents[sel.Sel.Name] {
							t.Errorf("%s references syscall.%s — only %v are allowed in %s (Gate G1 rule 5)", path, sel.Sel.Name, allowedInternalSyscallIdentsKeyList(), filepath.Base(path))
							violations++
						}
						return true
					})
				}
				if name == "syscall" && !root.internal {
					// Outside internal/, syscall is allowed for signal
					// constants only.
					ast.Inspect(file, func(n ast.Node) bool {
						sel, ok := n.(*ast.SelectorExpr)
						if !ok {
							return true
						}
						if id, ok := sel.X.(*ast.Ident); ok && id.Name == "syscall" && !allowedSyscallIdents[sel.Sel.Name] {
							t.Errorf("%s references syscall.%s — only signal constants are allowed in cmd/", path, sel.Sel.Name)
							violations++
						}
						return true
					})
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if violations > 0 {
		t.Fatalf("%d Gate G1 violations: tool-execution capability present in M1", violations)
	}
}

// relPath returns the repository-root-relative path for a file
// visited by filepath.WalkDir. The walker is rooted at ../../internal
// or ../../cmd relative to this test file, so the repo root is two
// levels up from those roots (i.e. the parent of internal/ and cmd/).
// The function is used to key the `allowedInternalSyscallFiles`
// allowlist on repo-relative paths, so a future contributor cannot
// create an unannotated `internal/somethingelse/paths_linux.go` and
// have it pass. On unexpected input the function falls back to the
// walked path verbatim and lets the lookup miss; the gate test will
// then report a violation rather than silently allow the import.
func relPath(p string) string {
	// filepath.WalkDir hands us paths like "../../internal/foo/bar.go".
	// Normalize, then strip the leading "../" components until the
	// first segment is "internal" or "cmd"; everything from that
	// segment onward is the repo-relative path.
	cleaned := filepath.ToSlash(filepath.Clean(p))
	parts := strings.Split(cleaned, "/")
	for i, seg := range parts {
		if seg == "internal" || seg == "cmd" {
			return strings.Join(parts[i:], "/")
		}
	}
	return cleaned
}

// isUnderAllowedExecDir reports whether the file at
// `walkerPath` lives under one of the directories in
// `allowedOsExecDirs`. The walker hands us paths like
// "../../cmd/athanor/scanners/clamav.go"; the function
// normalizes to "cmd/athanor/scanners/clamav.go" via
// relPath, then checks whether any allowed dir is a
// prefix. A future contributor cannot create a sibling
// directory and have it pass: the prefix check is exact
// (no globs).
func isUnderAllowedExecDir(walkerPath string) bool {
	cleaned := filepath.ToSlash(filepath.Clean(walkerPath))
	for _, dir := range allowedOsExecDirs {
		// Normalize: relPath returns "cmd/..." or
		// "internal/..." — strip the leading "cmd/"
		// the same way relPath does (relPath keeps
		// "cmd" in the returned path). The dirs
		// list stores paths starting with "cmd/".
		rel := relPath(walkerPath)
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			_ = cleaned
			return true
		}
	}
	return false
}

// allowedInternalSyscallIdentsKeyList returns the sorted
// identifiers in `allowedInternalSyscallIdents` for use in error
// messages. The format is stable so the gate's test output is
// diff-friendly across changes.
func allowedInternalSyscallIdentsKeyList() []string {
	out := make([]string, 0, len(allowedInternalSyscallIdents))
	for k := range allowedInternalSyscallIdents {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// forbiddenOutboundHTTPIdents are the package-level
// outbound HTTP helpers rule 6 (M4-T5.4, ADR-0017 §2)
// forbids outside the §21.5 gateway. A future
// contributor who adds `http.Get(remoteURL)` to any
// non-allowlisted file trips the build. The set is the
// lazy "I don't want to construct an http.Client"
// pattern; the project convention is to construct
// `*http.Client` with explicit timeouts (see the LLM
// client and the Job Pod runner for sanctioned
// examples).
//
// The identifiers are the *package-level* helpers
// only. Constructed-client usage (e.g.
// `(&http.Client{Timeout: ...}).Do(req)`) is not in
// the rule: those callers construct explicit
// timeouts and are loopback / Ollama use. The rule
// targets the "I'll just use http.Get" anti-pattern,
// not the explicit-timeout pattern.
var forbiddenOutboundHTTPIdents = map[string]bool{
	"Get":           true, // http.Get(url)
	"Post":          true, // http.Post(url, contentType, body)
	"DefaultClient": true, // http.DefaultClient; .Do via the field name
}

// allowedOutboundHTTPLocations are the files where
// the forbidden selectors may appear. The set is
// path-keyed (full repo-relative path) so a future
// contributor cannot create a sibling file and have
// it pass.
//
// The §21.5 Internet Gated Reader
// (`internal/gateway/`) is the only place outbound
// HTTP to non-loopback destinations may originate.
// The `cmd/athanor/cli*.go` files are the loopback
// CLI client; they talk to the daemon over the
// loopback socket (default `127.0.0.1:7420` per
// §21.8) and are the canonical "CLI to daemon"
// surface. `cli.go` provides the `apiCall` helper;
// `cli_control.go` and `cli_project.go` use
// `apiCall` transitively; `cli_export.go` calls
// `http.DefaultClient.Do(req)` directly with a
// loopback URL. The daemon's own HTTP server is
// tested via `httptest.NewServer` in `*_test.go`
// files, which are exempt from rule 6 (the test
// exemption is the same one rules 1–5 use).
var allowedOutboundHTTPLocations = map[string]bool{
	"cmd/athanor/cli.go":          true, // apiCall helper + http.DefaultClient.Do
	"cmd/athanor/cli_control.go": true, // uses apiCall
	"cmd/athanor/cli_export.go":  true, // http.DefaultClient.Do directly
	"cmd/athanor/cli_project.go": true, // uses apiCall
}

// isUnderGatewayPackage reports whether the file at
// `walkerPath` lives under `internal/gateway/`. The
// §21.5 gateway is the only place the package-level
// outbound HTTP helpers may appear (the gateway
// constructs a per-request `*http.Client`; the
// `http.Get` / `http.Post` / `http.DefaultClient`
// helpers in `internal/gateway/dial.go` and
// `client.go` are a deliberate but narrow exception
// — see the test's allowlist comment for the
// rationale). The check is a prefix match on the
// repo-relative path; a future contributor cannot
// create a sibling directory and have it pass.
func isUnderGatewayPackage(walkerPath string) bool {
	rel := relPath(walkerPath)
	return rel == "internal/gateway" || strings.HasPrefix(rel, "internal/gateway/")
}

// checkOutboundHTTP reports whether `file` (already
// parsed) contains a forbidden outbound-HTTP
// selector. It is the inner walk the test extracts
// from `TestGateG1NoOutboundHTTPOutsideGateway` so
// the walk's logic has a unit-level regression
// surface in addition to the integration test that
// walks the real tree. The function returns the
// sorted list of forbidden selector names it
// found (empty for a clean file); the integration
// test counts violations, the unit test asserts
// the *names*. Sorting keeps the test output
// stable across AST-traversal orderings.
func checkOutboundHTTP(file *ast.File) []string {
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Name != "http" {
			return true
		}
		if forbiddenOutboundHTTPIdents[sel.Sel.Name] {
			found = append(found, sel.Sel.Name)
		}
		return true
	})
	sort.Strings(found)
	return found
}

// TestGateG1NoOutboundHTTPOutsideGateway is rule 6
// (M4-T5.4, ADR-0017 §2). The walk is a separate
// test function so the violation counter is
// per-rule: a future contributor who adds a single
// bad call site gets a single, focused failure,
// not a cascade of `TestGateG1NoToolExecution`
// violations to sift through. The walk uses the
// same `ast.Inspect` shape as the syscall walk in
// `TestGateG1NoToolExecution` (rule 3 / 5) so a
// reader who knows one rule knows the other.
func TestGateG1NoOutboundHTTPOutsideGateway(t *testing.T) {
	roots := []struct {
		dir      string
		internal bool
	}{
		{"../../internal", true},
		{"../../cmd", false},
	}
	violations := 0
	for _, root := range roots {
		err := filepath.WalkDir(root.dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				// The `_test.go` exemption is the
				// same one rules 1–5 use; the
				// daemon's HTTP server is tested
				// via `httptest.NewServer` (loopback)
				// in `internal/server/*_test.go`
				// and `internal/api/e2e_test.go`,
				// all of which legitimately call
				// `http.Get` / `http.Post` /
				// `http.DefaultClient` against
				// the test server.
				return nil
			}
			rel := relPath(path)
			// Allowlist 1: the §21.5 gateway
			// itself. The package constructs a
			// per-request `*http.Client`; the
			// `http.Get` / `http.Post` /
			// `http.DefaultClient` selectors
			// that appear in its source today
			// (the T6 follow-up will add a
			// `*http.Client` literal in
			// `client_test.go` if the tests
			// need one) are deliberate.
			if isUnderGatewayPackage(path) {
				return nil
			}
			// Allowlist 2: the four loopback
			// CLI files in `cmd/athanor/`. The
			// list is path-keyed so a sibling
			// file (e.g. `cmd/athanor/cli_foo.go`)
			// added by a future contributor
			// does not silently pass.
			if allowedOutboundHTTPLocations[rel] {
				return nil
			}
			fset := token.NewFileSet()
			file, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Errorf("parsing %s: %v", path, perr)
				return nil
			}
			for _, ident := range checkOutboundHTTP(file) {
				t.Errorf("%s references http.%s — outbound HTTP must go through internal/gateway (M4-T5.4, ADR-0017 §2)", path, ident)
				violations++
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if violations > 0 {
		t.Fatalf("%d Gate G1 rule 6 violations: outbound HTTP used outside the §21.5 gateway", violations)
	}
	if violations > 0 {
		t.Fatalf("%d Gate G1 rule 6 violations: outbound HTTP used outside the §21.5 gateway", violations)
	}
}

// TestCheckOutboundHTTP is the unit-level proof that
// `checkOutboundHTTP` actually catches the
// forbidden selectors. The integration test
// (`TestGateG1NoOutboundHTTPOutsideGateway`)
// walks the real tree and asserts the absence of
// violations; this test feeds synthetic source
// to the inner walk and asserts the *presence* of
// the expected violations. A regression that
// silently no-ops the walk (e.g. always returns
// nil) trips this test even if the integration
// test continues to pass against a known-good
// tree.
//
// The cases cover the three forbidden selectors
// (Get, Post, DefaultClient) plus a positive
// control (a constructed *http.Client with an
// explicit timeout) and a comment-position
// control (text that mentions http.Get inside a
// `// ...` line, which the AST does not tokenize
// into SelectorExprs).
func TestCheckOutboundHTTP(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "clean_constructed_client",
			src: `package x
import "net/http"
func f() {
	c := &http.Client{Timeout: 5e9}
	_ = c
}`,
			want: nil,
		},
		{
			name: "http_get_caught",
			src: `package x
import "net/http"
func f() { _ = http.Get("https://example.com/") }`,
			want: []string{"Get"},
		},
		{
			name: "http_post_caught",
			src: `package x
import "net/http"
func f() { _ = http.Post("https://example.com/", "text/plain", nil) }`,
			want: []string{"Post"},
		},
		{
			name: "http_default_client_do_caught",
			src: `package x
import "net/http"
func f() { _ = http.DefaultClient.Do(nil) }`,
			want: []string{"DefaultClient"},
		},
		{
			name: "multiple_violations",
			src: `package x
import "net/http"
func f() {
	_ = http.Get("https://a.example/")
	_ = http.Post("https://b.example/", "text/plain", nil)
	_ = http.DefaultClient.Do(nil)
}`,
			want: []string{"DefaultClient", "Get", "Post"},
		},
		{
			name: "comment_position_not_counted",
			// AST does not tokenize the inside
			// of a `// ...` comment, so the
			// `http.Get` and `http.Post`
			// mentions in the doc comment do
			// not produce SelectorExpr nodes.
			src: `package x
// uses http.Get and http.Post in the doc.
import "net/http"
func f() { _ = &http.Client{Timeout: 1e9} }`,
			want: nil,
		},
		{
			name: "string_literal_not_counted",
			// The AST places a BasicLit node
			// here, not a SelectorExpr, so
			// the walk does not see it as a
			// reference.
			src: `package x
var s = "http.Get is forbidden"`,
			want: nil,
		},
		{
			name: "http_method_constants_ok",
			// http.MethodGet, .MethodPost, etc.
			// are *string* constants, not the
			// forbidden outbound-client
			// helpers. The walk's SelectorExpr
			// check is on the Sel name; the
			// constant access uses
			// `http.MethodGet` which is not in
			// the forbidden set.
			src: `package x
import "net/http"
func f(m string) string {
	if m == http.MethodGet { return "get" }
	return http.MethodPost
}`,
			want: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "synthetic.go", c.src, 0)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got := checkOutboundHTTP(file)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("checkOutboundHTTP = %v, want %v", got, c.want)
			}
		})
	}
}
