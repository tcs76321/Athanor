// Internal-only test for the helper functions added alongside the
// M4-T1 Gate G1 extension. The main gate test exercises the
// positive path; this file pins the helper contracts (path
// normalization, sorted identifier list) so a future refactor
// cannot silently change the repo-relative path shape or the
// error-message format without breaking this test.
package gate

import (
	"reflect"
	"sort"
	"testing"
)

func TestRelPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "internal file at the root",
			in:   "../../internal/foo/bar.go",
			want: "internal/foo/bar.go",
		},
		{
			name: "internal deep file",
			in:   "../../internal/airlock/paths/paths_linux.go",
			want: "internal/airlock/paths/paths_linux.go",
		},
		{
			name: "cmd file",
			in:   "../../cmd/athanor/main.go",
			want: "cmd/athanor/main.go",
		},
		{
			name: "no internal-or-cmd segment returns cleaned input unchanged",
			in:   "../../oops.txt",
			want: "../../oops.txt", // filepath.Clean preserves leading ../ segments
		},
		{
			name: "first segment wins on internal/cmd ambiguity",
			in:   "../../cmd/with/internal/inside.go",
			want: "cmd/with/internal/inside.go",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := relPath(tc.in)
			if got != tc.want {
				t.Errorf("relPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestAllowedInternalSyscallIdentsKeyList(t *testing.T) {
	got := allowedInternalSyscallIdentsKeyList()
	want := []string{"O_NOFOLLOW"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("allowedInternalSyscallIdentsKeyList() = %v, want %v", got, want)
	}
	// Sorted invariant: any future identifier must be added to the
	// expected slice in the correct sort position. The test fails
	// before the Gate G1 walk would notice a missing entry.
}

// TestRule6ForbiddenIdentsPinned pins the
// `forbiddenOutboundHTTPIdents` map: the set is
// `http.Get`, `http.Post`, and `http.DefaultClient`,
// and nothing else. Adding a new identifier is a
// deliberate policy change; this test is the
// tripwire.
func TestRule6ForbiddenIdentsPinned(t *testing.T) {
	want := []string{"DefaultClient", "Get", "Post"}
	var got []string
	for k := range forbiddenOutboundHTTPIdents {
		got = append(got, k)
	}
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("forbiddenOutboundHTTPIdents = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("forbiddenOutboundHTTPIdents[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestRule6AllowedLocationsPinned pins the
// `allowedOutboundHTTPLocations` map: the four
// `cmd/athanor/cli*.go` files, and nothing else.
// The list is the loopback CLI client surface; a
// future contributor who adds a new `cli_*.go`
// file must also add it to this map (or, better,
// fold the new call into `cli.go`'s `apiCall`
// helper and not add a new file).
func TestRule6AllowedLocationsPinned(t *testing.T) {
	want := []string{
		"cmd/athanor/cli.go",
		"cmd/athanor/cli_control.go",
		"cmd/athanor/cli_export.go",
		"cmd/athanor/cli_project.go",
	}
	var got []string
	for k := range allowedOutboundHTTPLocations {
		got = append(got, k)
	}
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("allowedOutboundHTTPLocations = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("allowedOutboundHTTPLocations[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestIsUnderGatewayPackage pins the
// `isUnderGatewayPackage` helper. The test covers
// the four cases the rule's walk relies on:
//
//   1. The gateway package's own files (allowed).
//   2. A subdirectory of the gateway (allowed).
//   3. A sibling package under `internal/` (denied).
//   4. A `cmd/` file (denied, even though the
//      package is at the same Go-path level as
//      `internal/gateway`).
//
// The repo-relative path form is the one
// `relPath` produces; the test exercises the
// helper directly because `relPath` itself is
// pinned separately.
func TestIsUnderGatewayPackage(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		{"internal/gateway/client.go", true},
		{"internal/gateway/dial.go", true},
		{"internal/gateway/subdir/foo.go", true},
		{"internal/airlock/paths/paths.go", false},
		{"internal/llm/client.go", false},
		{"cmd/athanor/serve.go", false},
		{"cmd/athanor/gateway.go", false}, // daemon, not the gateway
	}
	for _, c := range cases {
		t.Run(c.rel, func(t *testing.T) {
			if got := isUnderGatewayPackage(c.rel); got != c.want {
				t.Errorf("isUnderGatewayPackage(%q) = %v, want %v", c.rel, got, c.want)
			}
		})
	}
}
