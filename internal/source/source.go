// Package source loads a project's local source material — declared source
// files and the news wire — through the §21.3 path-containment library.
//
// Before this package, two call sites (`internal/firstuse` and
// `internal/engine/contextasm`) each hardcoded `sources/` and `wire/` and read
// them with os.ReadFile. That duplicated the layout knowledge and followed a
// symlink out of the project repository. Here the layout is data, every read
// goes through airlock/paths, and the result carries a manifest so a missing or
// rejected source is visible instead of silent.
package source

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tcs76321/athanor/internal/airlock/paths"
	"github.com/tcs76321/athanor/internal/prompt"
)

// Kind is how a source is obtained.
type Kind string

const (
	// KindFile is one file under the project repository.
	KindFile Kind = "file"
	// KindDir is a directory of text files under the project repository.
	KindDir Kind = "dir"
	// KindURL is an http(s) source the engine fetches through the gateway.
	// It is never read from disk.
	KindURL Kind = "url"
	// KindQuery is a search query the research sub-step may run. It is not
	// read from disk.
	KindQuery Kind = "query"
)

// Spec is one source to load or pass through.
type Spec struct {
	Kind     Kind
	Location string // repository-relative path, URL, or query
	Name     string // optional display name; Location when empty
}

// Limits bounds one load. A zero field is replaced by DefaultLimits.
type Limits struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

// DefaultLimits is the 0.x source-read envelope: enough for a day's wire or a
// handful of research notes, small enough that a stray large file cannot bloat
// the prompt or the process.
func DefaultLimits() Limits {
	return Limits{MaxFiles: 200, MaxFileBytes: 512 << 10, MaxTotalBytes: 2 << 20}
}

// Loaded is one source that was read.
type Loaded struct {
	Name  string
	Kind  Kind
	Text  string
	Bytes int64
	Hash  string
}

// Skipped is one source that was not read, with the reason. A skipped source is
// best-effort: a dead local file must not fail a cycle, but it must be visible.
type Skipped struct {
	Name   string
	Reason string
}

// Manifest is the result of one load.
type Manifest struct {
	Loaded  []Loaded
	Skipped []Skipped
}

// ErrTooLarge reports a file or total that exceeds the load envelope.
var ErrTooLarge = errors.New("source: exceeds the byte cap")

// ProjectSpecs builds the default specs for a project: declared sources are
// files under `sources/` (or URLs when they carry an http(s) scheme), and each
// directory is walked for text files. Callers that support custom directories
// pass them in dirs; the default is `wire`.
func ProjectSpecs(sources []string, dirs []string) []Spec {
	out := make([]Spec, 0, len(sources)+len(dirs))
	for _, raw := range sources {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if isHTTPURL(raw) {
			out = append(out, Spec{Kind: KindURL, Location: raw, Name: raw})
			continue
		}
		// Preserve the 0.0.0 layout: a bare name is a file under sources/.
		out = append(out, Spec{
			Kind:     KindFile,
			Location: filepath.Join("sources", filepath.Base(raw)),
			Name:     raw,
		})
	}
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir != "" {
			out = append(out, Spec{Kind: KindDir, Location: dir, Name: dir})
		}
	}
	return out
}

// LoadLocal reads every file/dir spec under root. URL and query specs are
// ignored (they are handled by the gateway path). A missing file is recorded
// in Skipped; a containment rejection is recorded and never followed.
func LoadLocal(root string, specs []Spec, limits Limits) (Manifest, error) {
	limits = withDefaults(limits)
	if strings.TrimSpace(root) == "" {
		return Manifest{}, nil
	}

	var m Manifest
	total := int64(0)
	for _, spec := range specs {
		switch spec.Kind {
		case KindURL, KindQuery:
			continue
		case KindFile:
			loaded, skip, err := readOne(root, spec, limits)
			if err != nil {
				return Manifest{}, err
			}
			if skip != "" {
				m.Skipped = append(m.Skipped, Skipped{Name: nameOf(spec), Reason: skip})
				continue
			}
			if total+loaded.Bytes > limits.MaxTotalBytes {
				m.Skipped = append(m.Skipped, Skipped{Name: nameOf(spec), Reason: "total byte cap reached"})
				continue
			}
			total += loaded.Bytes
			m.Loaded = append(m.Loaded, loaded)
		case KindDir:
			files, skipped, err := readDir(root, spec, limits, &total)
			if err != nil {
				return Manifest{}, err
			}
			m.Loaded = append(m.Loaded, files...)
			m.Skipped = append(m.Skipped, skipped...)
		default:
			m.Skipped = append(m.Skipped, Skipped{Name: nameOf(spec), Reason: "unknown source kind"})
		}
	}
	sort.Slice(m.Loaded, func(i, j int) bool { return m.Loaded[i].Name < m.Loaded[j].Name })
	sort.Slice(m.Skipped, func(i, j int) bool { return m.Skipped[i].Name < m.Skipped[j].Name })
	return m, nil
}

func readOne(root string, spec Spec, limits Limits) (Loaded, string, error) {
	name := nameOf(spec)
	if spec.Location == "" {
		return Loaded{}, "empty location", nil
	}
	data, reason, err := readContained(root, spec.Location, limits.MaxFileBytes)
	if err != nil {
		return Loaded{}, "", err
	}
	if reason != "" {
		return Loaded{}, reason, nil
	}
	return makeLoaded(name, KindFile, data), "", nil
}

func readDir(root string, spec Spec, limits Limits, total *int64) ([]Loaded, []Skipped, error) {
	dir, err := paths.Resolve(root, spec.Location)
	if err != nil {
		return nil, []Skipped{{Name: nameOf(spec), Reason: reason(err)}}, nil
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, []Skipped{{Name: nameOf(spec), Reason: "not found"}}, nil
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, []Skipped{{Name: nameOf(spec), Reason: "symlink refused"}}, nil
	}
	if !info.IsDir() {
		return nil, []Skipped{{Name: nameOf(spec), Reason: "not a directory"}}, nil
	}

	var loaded []Loaded
	var skipped []Skipped
	count := 0
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			skipped = append(skipped, Skipped{Name: p, Reason: "walk error"})
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			skipped = append(skipped, Skipped{Name: p, Reason: "symlink refused"})
			return nil
		}
		if !d.Type().IsRegular() {
			skipped = append(skipped, Skipped{Name: p, Reason: "not a regular file"})
			return nil
		}
		if !isTextFile(p) {
			return nil
		}
		if count >= limits.MaxFiles {
			skipped = append(skipped, Skipped{Name: p, Reason: "file-count cap reached"})
			return fs.SkipAll
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			skipped = append(skipped, Skipped{Name: p, Reason: "path resolution failed"})
			return nil
		}
		if *total >= limits.MaxTotalBytes {
			skipped = append(skipped, Skipped{Name: rel, Reason: "total byte cap reached"})
			return fs.SkipAll
		}
		data, reason, rerr := readContained(root, rel, limits.MaxFileBytes)
		if rerr != nil {
			return rerr
		}
		if reason != "" {
			skipped = append(skipped, Skipped{Name: rel, Reason: reason})
			return nil
		}
		loadedThis := makeLoaded(filepath.ToSlash(rel), KindDir, data)
		if *total+loadedThis.Bytes > limits.MaxTotalBytes {
			skipped = append(skipped, Skipped{Name: rel, Reason: "total byte cap reached"})
			return fs.SkipAll
		}
		*total += loadedThis.Bytes
		count++
		loaded = append(loaded, loadedThis)
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("source: walking %s: %w", spec.Location, err)
	}
	return loaded, skipped, nil
}

// readContained opens one repository-relative file with O_NOFOLLOW, rejects a
// non-regular file, and reads at most max bytes + 1 to detect an over-cap file.
// A reason (non-empty) means "skip this source"; an error means the load itself
// failed and the caller should stop.
func readContained(root, rel string, max int64) ([]byte, string, error) {
	// Resolve + Lstat first so a symlink is a named skip instead of the
	// kernel's ELOOP from O_NOFOLLOW (which would look like a load failure).
	resolved, rerr := paths.Resolve(root, rel)
	if rerr != nil {
		return nil, reason(rerr), nil
	}
	info, lerr := os.Lstat(resolved)
	if lerr != nil {
		if os.IsNotExist(lerr) {
			return nil, "not found", nil
		}
		return nil, "", fmt.Errorf("source: stat %s: %w", rel, lerr)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, "symlink refused", nil
	}
	f, err := paths.OpenNoFollow(root, rel)
	if err != nil {
		switch {
		case errors.Is(err, paths.ErrNotFound):
			return nil, "not found", nil
		case errors.Is(err, paths.ErrSymlinkEscape), errors.Is(err, paths.ErrTraversal),
			errors.Is(err, paths.ErrAbsolute), errors.Is(err, paths.ErrInvalid):
			return nil, reason(err), nil
		case errors.Is(err, paths.ErrDevice), errors.Is(err, paths.ErrSetUID),
			errors.Is(err, paths.ErrExecutable):
			return nil, reason(err), nil
		default:
			return nil, "", fmt.Errorf("source: opening %s: %w", rel, err)
		}
	}
	defer func() { _ = f.Close() }()
	info, err = f.Stat()
	if err != nil {
		return nil, "", fmt.Errorf("source: stat %s: %w", rel, err)
	}
	if !info.Mode().IsRegular() {
		return nil, "not a regular file", nil
	}
	if info.Size() > max {
		return nil, "file byte cap exceeded", nil
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, "", fmt.Errorf("source: reading %s: %w", rel, err)
	}
	if int64(len(data)) > max {
		return nil, "file byte cap exceeded", nil
	}
	if isBinary(data) {
		return nil, "binary content", nil
	}
	return data, "", nil
}

func makeLoaded(name string, kind Kind, data []byte) Loaded {
	sum := sha256.Sum256(data)
	return Loaded{
		Name: name, Kind: kind, Text: string(data),
		Bytes: int64(len(data)), Hash: hex.EncodeToString(sum[:]),
	}
}

// SourceTexts returns the loaded text keyed by display name for the workflow
// compiler, which injects authoritative source text into research/fetch blocks.
func (m Manifest) SourceTexts() map[string]string {
	if len(m.Loaded) == 0 {
		return nil
	}
	out := make(map[string]string, len(m.Loaded))
	for _, l := range m.Loaded {
		out[l.Name] = l.Text
	}
	return out
}

// PromptBlock renders the loaded sources as labeled untrusted blocks, bounded
// by maxTotal bytes. Per-file text is truncated with a visible marker so the
// model knows it is incomplete.
func (m Manifest) PromptBlock(maxTotal int) string {
	if len(m.Loaded) == 0 {
		return ""
	}
	if maxTotal <= 0 {
		maxTotal = 1 << 30
	}
	var b strings.Builder
	total := 0
	for _, l := range m.Loaded {
		text := strings.TrimSpace(l.Text)
		if text == "" {
			continue
		}
		if remaining := maxTotal - total; remaining <= 0 {
			break
		} else if len(text) > remaining {
			text = text[:remaining] + "\n…[source truncated at the prompt budget]"
		}
		block := prompt.Untrusted(l.Name, text)
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(block)
		total += len(block)
	}
	return b.String()
}

func withDefaults(l Limits) Limits {
	if l.MaxFiles <= 0 {
		l.MaxFiles = DefaultLimits().MaxFiles
	}
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = DefaultLimits().MaxFileBytes
	}
	if l.MaxTotalBytes <= 0 {
		l.MaxTotalBytes = DefaultLimits().MaxTotalBytes
	}
	return l
}

func nameOf(spec Spec) string {
	if strings.TrimSpace(spec.Name) != "" {
		return spec.Name
	}
	return spec.Location
}

func isHTTPURL(s string) bool {
	l := strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

func isTextFile(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".md", ".markdown", ".txt", ".rst", ".json", ".yaml", ".yml", ".csv":
		return true
	default:
		return false
	}
}

func isBinary(data []byte) bool {
	n := len(data)
	if n > 512 {
		n = 512
	}
	for _, b := range data[:n] {
		if b == 0 {
			return true
		}
	}
	return false
}

func reason(err error) string {
	switch {
	case errors.Is(err, paths.ErrAbsolute):
		return "absolute path refused"
	case errors.Is(err, paths.ErrTraversal):
		return "traversal refused"
	case errors.Is(err, paths.ErrSymlinkEscape):
		return "symlink escape refused"
	case errors.Is(err, paths.ErrDevice):
		return "device node refused"
	case errors.Is(err, paths.ErrSetUID):
		return "setuid file refused"
	case errors.Is(err, paths.ErrExecutable):
		return "executable refused"
	default:
		return "rejected"
	}
}
