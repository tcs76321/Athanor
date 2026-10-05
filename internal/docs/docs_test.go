// Package docs keeps the repository's own cross-links honest (F3-T7).
//
// README.md, ARCHITECTURE.md, ROADMAP.md, AGENTS.md, and docs/ are
// densely cross-linked; a renamed file or a typo silently produces a
// dead link that no compiler catches. This test walks every Markdown
// file in the repo and asserts that each relative link target resolves
// to a real path on disk.
//
// It deliberately does NOT check external URLs (that needs the network
// and is flaky) or anchors (a heading rename is a docs concern, not a
// build-break). It strips fenced code blocks and inline code spans first,
// so example syntax inside a fence is not mistaken for a real link.
package docs

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// linkTargetRe matches the target of an inline Markdown link or image,
// i.e. the ... in ](...). It is intentionally permissive; the
// classification below rejects non-file targets.
var linkTargetRe = regexp.MustCompile(`\]\(([^)]+)\)`)

// inlineCodeRe matches a single-backtick inline code span on one line.
var inlineCodeRe = regexp.MustCompile("`[^`]*`")

// TestRelativeMarkdownLinksResolve is the checker.
func TestRelativeMarkdownLinksResolve(t *testing.T) {
	root, err := findModuleRoot()
	if err != nil {
		t.Fatalf("finding module root: %v", err)
	}

	var broken []string
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip VCS/runtime/build output; every other directory
			// is fair game so docs/ and spikes/ are covered.
			switch d.Name() {
			case ".git", "bin", "state", "workspace", "backups", "node_modules":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(root, path)
		for _, target := range extractFileTargets(stripCode(string(raw))) {
			resolved := filepath.Join(filepath.Dir(path), filepath.FromSlash(target))
			if _, serr := os.Stat(resolved); serr != nil {
				broken = append(broken, fmt.Sprintf("%s → %s", rel, target))
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking Markdown: %v", walkErr)
	}
	if len(broken) > 0 {
		t.Fatalf("%d broken relative Markdown link(s):\n  %s", len(broken), strings.Join(broken, "\n  "))
	}
}

// stripCode removes fenced code blocks (``` or ~~~) and single-backtick
// inline spans so example syntax is not treated as a link.
func stripCode(md string) string {
	var b strings.Builder
	inFence := false
	var fence string
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inFence && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
			inFence = true
			fence = trimmed[:3]
			continue
		}
		if inFence {
			if strings.HasPrefix(trimmed, fence) {
				inFence = false
			}
			continue
		}
		b.WriteString(inlineCodeRe.ReplaceAllString(line, ""))
		b.WriteByte('\n')
	}
	return b.String()
}

// TestExtractFileTargets is the unit-level proof that the extraction is
// not a no-op and that the skip rules behave. The integration test above
// asserts the tree has no broken links; this test asserts the extractor
// actually finds links and classifies them.
func TestExtractFileTargets(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"plain relative", "[a](docs/x.md)", []string{"docs/x.md"}},
		{"fragment stripped", "[a](docs/x.md#sec)", []string{"docs/x.md"}},
		{"url skipped", "[a](https://example.com/x.md)", nil},
		{"anchor only skipped", "[a](#sec)", nil},
		{"absolute path skipped", "[a](/Users/me/x.md)", nil},
		{"title stripped", `[a](docs/x.md "Title")`, []string{"docs/x.md"}},
		{"fenced code ignored", "```\n[a](missing.md)\n```\n[b](real.md)", []string{"real.md"}},
		{"inline code ignored", "use `[a](missing.md)` then [b](real.md)", []string{"real.md"}},
		{"parent link kept", "[a](../docs/x.md)", []string{"../docs/x.md"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractFileTargets(stripCode(c.in))
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("extractFileTargets(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// extractFileTargets returns the relative file targets in a link list,
// dropping absolute URLs, anchors, and anything that is not a plausible
// repo-relative path.
func extractFileTargets(md string) []string {
	var out []string
	for _, m := range linkTargetRe.FindAllStringSubmatch(md, -1) {
		target := strings.TrimSpace(m[1])
		// Drop an optional Markdown title: ](path "title").
		if i := strings.IndexAny(target, " \t"); i >= 0 {
			target = target[:i]
		}
		// Drop a fragment.
		if i := strings.IndexByte(target, '#'); i >= 0 {
			target = target[:i]
		}
		if !isRelativeFileTarget(target) {
			continue
		}
		out = append(out, target)
	}
	return out
}

// isRelativeFileTarget reports whether target should be existence-checked.
// It rejects empty targets, fragments, absolute URLs, and absolute
// filesystem paths (the docs use `/Users/...`-style examples that are
// host-specific, and a leading slash would otherwise be read as
// repo-relative).
func isRelativeFileTarget(target string) bool {
	if target == "" || strings.HasPrefix(target, "/") {
		return false
	}
	lower := strings.ToLower(target)
	for _, scheme := range []string{"http://", "https://", "mailto:", "tel:", "ftp://"} {
		if strings.HasPrefix(lower, scheme) {
			return false
		}
	}
	if strings.ContainsAny(target, "<>") || strings.Contains(target, "://") {
		return false
	}
	return true
}

// findModuleRoot walks up from the test's working directory to the
// directory containing go.mod.
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
