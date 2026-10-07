package toolenvelope

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

// File is one entry of a multi-file code candidate (ADR-0065). Path is a
// container-relative path; Content is the file's text. A code candidate is a
// []File stored in one artifact blob, so versioning and hashing are unchanged.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Errors returned by the file-tree helpers.
var (
	ErrEmptyPath     = errors.New("toolenvelope: file path is empty")
	ErrAbsolutePath  = errors.New("toolenvelope: file path must be relative")
	ErrPathTraversal = errors.New("toolenvelope: file path escapes the root")
	ErrNullByte      = errors.New("toolenvelope: file path contains a NULL byte")
	ErrDuplicatePath = errors.New("toolenvelope: duplicate file path")
)

// SanitizeRelPath validates and normalizes a container-relative file path. It
// is the §21.3 containment rule applied to paths *inside* a Job Pod scratch
// dir: reject absolute paths, `..` traversal, NULL bytes, and the empty path;
// collapse `.`/duplicate separators. The result is a clean relative path that
// never begins with `../` or `/`. Pure, stdlib only.
func SanitizeRelPath(p string) (string, error) {
	if p == "" {
		return "", ErrEmptyPath
	}
	if strings.ContainsRune(p, 0) {
		return "", ErrNullByte
	}
	// Reject Windows-style absolute too; the pod is Linux but the guard is
	// cheap and the intent is "no root escape regardless of host".
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) || strings.Contains(p, ":") {
		return "", fmt.Errorf("%w: %q", ErrAbsolutePath, p)
	}
	clean := path.Clean(p)
	if clean == "." || clean == ".." || clean == "/" {
		return "", fmt.Errorf("%w: %q", ErrPathTraversal, p)
	}
	if strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("%w: %q", ErrPathTraversal, p)
	}
	return clean, nil
}

// ValidateFiles sanitizes every path (returning the cleaned tree) and rejects
// duplicates. An empty tree is valid (a candidate may produce nothing).
func ValidateFiles(files []File) ([]File, error) {
	seen := make(map[string]bool, len(files))
	out := make([]File, 0, len(files))
	for _, f := range files {
		p, err := SanitizeRelPath(f.Path)
		if err != nil {
			return nil, err
		}
		if seen[p] {
			return nil, fmt.Errorf("%w: %q", ErrDuplicatePath, p)
		}
		seen[p] = true
		out = append(out, File{Path: p, Content: f.Content})
	}
	return out, nil
}

// EncodeFiles serializes a file tree to the JSON manifest stored as a code
// artifact's content. Paths are validated first, so an unencodable tree never
// reaches the store. The order is canonical (sorted by path) so equal trees
// hash identically.
func EncodeFiles(files []File) ([]byte, error) {
	clean, err := ValidateFiles(files)
	if err != nil {
		return nil, err
	}
	sort.Slice(clean, func(i, j int) bool { return clean[i].Path < clean[j].Path })
	return json.Marshal(struct {
		Files []File `json:"files"`
	}{Files: clean})
}

// DecodeFiles parses a code artifact's JSON manifest back into a tree. It
// validates paths too, so a hand-edited or hostile blob cannot direct a write
// outside the scratch dir.
func DecodeFiles(blob []byte) ([]File, error) {
	var m struct {
		Files []File `json:"files"`
	}
	if err := json.Unmarshal(blob, &m); err != nil {
		return nil, fmt.Errorf("toolenvelope: decoding file tree: %w", err)
	}
	return ValidateFiles(m.Files)
}

// IsTreeManifest reports whether blob looks like a JSON file-tree manifest
// (rather than raw source). Used to keep the single-file shorthand working:
// a legacy/manifest artifact is a tree, anything else is one `solution.py`.
func IsTreeManifest(blob []byte) bool {
	trimmed := strings.TrimSpace(string(blob))
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	var m struct {
		Files []File `json:"files"`
	}
	return json.Unmarshal(blob, &m) == nil && m.Files != nil
}
