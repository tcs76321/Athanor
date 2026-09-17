package division

import (
	"os"
	"path/filepath"
	"strings"
)

// Source is one corpus file: its content plus the metadata strategies need.
type Source struct {
	FilePath string // relative path (used in chunk IDs)
	Lang     string
	Src      []byte
}

// LangFor maps a filename to a corpus language; "" means unsupported.
func LangFor(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js":
		return "javascript"
	case ".md":
		return "markdown"
	default:
		return ""
	}
}

// LoadDir walks root recursively and returns every file whose extension
// maps to a corpus language. Files that fail to read are skipped with a
// returned error — the corpus must be complete for the property test.
func LoadDir(root string) ([]Source, error) {
	var out []Source
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		lang := LangFor(d.Name())
		if lang == "" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, Source{FilePath: filepath.ToSlash(rel), Lang: lang, Src: src})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// LoadCorpus returns the spike corpus:
//
//   - testdata/   — committed fixtures (Go, Python, JavaScript, Markdown,
//     plus edge/ and broken/ adversarial cases)
//   - ../../internal — this repository's own Go sources, the "real repo"
//     half of the corpus (the acceptance criteria say real repos)
//
// The repo walk is relative to the package directory (tests run with the
// package dir as CWD) and degrades to fixtures-only if the checkout is
// not present, so the spike still works vendored elsewhere.
func LoadCorpus() ([]Source, error) {
	fixtures, err := LoadDir("testdata")
	if err != nil {
		return nil, err
	}
	var all []Source
	all = append(all, fixtures...)
	repo, repoErr := LoadDir(filepath.Join("..", "..", "internal"))
	if repoErr == nil {
		all = append(all, repo...)
	}
	return all, nil
}
