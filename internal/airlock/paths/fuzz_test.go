package paths_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/airlock/paths"
)

// FuzzResolve asserts the §21.3 path-arithmetic layer never panics and
// never returns a path that escapes root, for arbitrary (root, rel).
//
// The containment assertion only applies when root is absolute: "under
// root" is meaningless for a relative root, so those inputs only have to
// not panic. Any error must be one of the package's documented sentinels.
func FuzzResolve(f *testing.F) {
	seeds := []struct{ root, rel string }{
		{"/tmp/root", "a/b.txt"},
		{"/tmp/root", "../escape"},
		{"/tmp/root", "/abs"},
		{"/tmp/root", "a\x00b"},
		{"/tmp/root", "a/../../b"},
		{"/tmp/root", "."},
		{"/tmp/root", "sub/./file"},
		{"/tmp/root", "sub/../sibling"},
	}
	for _, s := range seeds {
		f.Add(s.root, s.rel)
	}
	f.Fuzz(func(t *testing.T, root, rel string) {
		joined, err := paths.Resolve(root, rel)
		if err != nil {
			switch {
			case errors.Is(err, paths.ErrAbsolute),
				errors.Is(err, paths.ErrTraversal),
				errors.Is(err, paths.ErrInvalid):
				return
			default:
				t.Fatalf("Resolve(%q, %q) returned unexpected error %v", root, rel, err)
			}
		}
		if !filepath.IsAbs(root) {
			return
		}
		// "Under root" via a relative path: anything starting with
		// "../" has escaped. filepath.Rel handles root == "/" (where a
		// naive string-prefix check would false-positive).
		relToRoot, relErr := filepath.Rel(filepath.Clean(root), filepath.Clean(joined))
		if relErr != nil ||
			relToRoot == ".." ||
			strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
			t.Fatalf("Resolve(%q, %q) = %q escapes root %q", root, rel, joined, root)
		}
	})
}
