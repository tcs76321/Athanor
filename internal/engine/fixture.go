package engine

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// Fixture read bounds (ADR-0065 §6). A fixture is a curated starter tree, not
// a repository: cap the file count, per-file size, and total size so a stray
// large directory cannot bloat the prompt or the pod.
const (
	fixtureMaxFiles      = 200
	fixtureMaxFileBytes  = 1 << 20 // 1 MiB per file
	fixtureMaxTotalBytes = 8 << 20 // 8 MiB total
)

// readFixtureTree reads a starter tree into a text file tree (ADR-0065). It is
// containment-safe: it never follows a symlink, skips VCS/dependency
// directories, and enforces the size/length bounds above. A non-regular,
// oversized, or binary entry is skipped (a fixture is curated text). Result
// paths are relative to root and sanitized.
func readFixtureTree(root string) ([]toolenvelope.File, error) {
	canon, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("fixture root %s: %w", root, err)
	}
	info, err := os.Stat(canon)
	if err != nil {
		return nil, fmt.Errorf("fixture root %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("fixture root %s is not a directory", root)
	}
	var out []toolenvelope.File
	var total int
	err = filepath.WalkDir(canon, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "__pycache__", ".venv", ".mypy_cache", ".ruff_cache":
				if p != canon {
					return fs.SkipDir
				}
			}
			return nil
		}
		// WalkDir does not descend a symlinked directory, and a symlinked
		// file is skipped outright: never follow a symlink.
		if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		if len(out) >= fixtureMaxFiles {
			return fmt.Errorf("fixture %s exceeds %d files", root, fixtureMaxFiles)
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if len(data) > fixtureMaxFileBytes || isBinary(data) {
			return nil
		}
		// Defense in depth: a file reached through a symlinked parent could
		// resolve outside the root. Require containment after resolution.
		resolved, rerr := filepath.EvalSymlinks(p)
		if rerr != nil {
			return rerr
		}
		if resolved != canon && !strings.HasPrefix(resolved, canon+string(os.PathSeparator)) {
			return fmt.Errorf("fixture file %s escapes %s", p, canon)
		}
		rel, rerr := filepath.Rel(canon, p)
		if rerr != nil {
			return rerr
		}
		clean, serr := toolenvelope.SanitizeRelPath(filepath.ToSlash(rel))
		if serr != nil {
			return serr
		}
		total += len(data)
		if total > fixtureMaxTotalBytes {
			return fmt.Errorf("fixture %s exceeds %d total bytes", root, fixtureMaxTotalBytes)
		}
		out = append(out, toolenvelope.File{Path: clean, Content: string(data)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// isBinary reports whether data looks binary (a NUL in the first 8 KiB).
func isBinary(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
	}
	for i := 0; i < n; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

// renderFixture renders a fixture tree into a prompt block (ADR-0065). It uses
// the same `=== FILE: path ===` marker the code instruction asks the model to
// emit, so the model sees the convention it should use and a round trip is
// natural.
func renderFixture(files []toolenvelope.File) string {
	var b strings.Builder
	b.WriteString("STARTER FILES (the task provides these; modify them in place, keep the public API unless asked otherwise):\n")
	for _, f := range files {
		fmt.Fprintf(&b, "\n=== FILE: %s ===\n%s\n", f.Path, f.Content)
	}
	return b.String()
}

// overlayFiles merges a candidate tree over a fixture tree: a candidate file
// with the same path replaces the fixture file; a fixture-only file is kept.
// The fixture order is preserved and candidate-only files are appended, so the
// result is deterministic.
func overlayFiles(base, over []toolenvelope.File) []toolenvelope.File {
	out := make([]toolenvelope.File, len(base))
	copy(out, base)
	idx := make(map[string]int, len(out))
	for i, f := range out {
		idx[f.Path] = i
	}
	for _, f := range over {
		if i, ok := idx[f.Path]; ok {
			out[i] = f
			continue
		}
		idx[f.Path] = len(out)
		out = append(out, f)
	}
	return out
}
