package mce

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"path/filepath"

	"github.com/tcs76321/athanor/internal/airlock/paths"
	"github.com/tcs76321/athanor/internal/mce/division"
)

// Repository walker (ROADMAP M5-T8; ADR-0028 §3).
//
// Discover enumerates the files a repository indexer should consider. It is
// deliberately conservative: only the languages the divider parses are
// returned, generated/vendor/VCS directories are skipped, and symlinks,
// non-regular files, binaries, and oversized sources are excluded. The
// result is deterministic (filepath.WalkDir walks lexically), so a pass is
// reproducible and tests are stable.

// binarySniffBytes is how many leading bytes are inspected for a NUL to
// classify a file as binary.
const binarySniffBytes = 512

// FileRef is one repository file considered for indexing.
type FileRef struct {
	RelPath   string
	Lang      string
	Size      int64
	MTimeUnix int64
}

// WalkOptions bounds discovery.
type WalkOptions struct {
	// ExcludeDirs are extra directory names skipped, additive to the
	// built-in set (VCS, dependency, and build-output directories).
	ExcludeDirs []string
	// MaxSourceBytes skips a file larger than this. Zero or negative means
	// unlimited (the caller's division bound still applies at ingest).
	MaxSourceBytes int64
}

// builtinSkipDirs are directory names never walked.
func builtinSkipDirs() map[string]bool {
	return map[string]bool{
		".git": true, ".hg": true, ".svn": true,
		"node_modules": true, "vendor": true,
		"target": true, "dist": true, "build": true,
		".venv": true, "venv": true, "__pycache__": true,
		".idea": true, ".vscode": true,
	}
}

// Discover walks root and returns the indexable files under it, ordered by
// path. A missing or unreadable root is an error; an unreadable subdirectory
// is skipped. Symlinks are never followed.
func Discover(root string, opts WalkOptions) ([]FileRef, error) {
	skip := builtinSkipDirs()
	for _, d := range opts.ExcludeDirs {
		if d != "" {
			skip[d] = true
		}
	}

	var out []FileRef
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != root && skip[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		// Never index a symlink or any non-regular file (device, FIFO,
		// socket). WalkDir does not follow symlinked directories; this
		// also drops a symlinked file at any level.
		if !d.Type().IsRegular() {
			return nil
		}
		lang := division.LangFor(d.Name())
		if lang == "" {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if opts.MaxSourceBytes > 0 && info.Size() > opts.MaxSourceBytes {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		binary, berr := isBinary(root, rel)
		if berr != nil {
			return nil // unreadable: skip rather than abort the walk
		}
		if binary {
			return nil
		}
		out = append(out, FileRef{
			RelPath:   rel,
			Lang:      lang,
			Size:      info.Size(),
			MTimeUnix: info.ModTime().Unix(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// isBinary reports whether rel (under root) has a NUL byte in its first
// binarySniffBytes. The open goes through §21.3 path containment so a
// symlink at the final component is refused at the kernel level.
func isBinary(root, rel string) (bool, error) {
	f, err := paths.OpenNoFollow(root, rel)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, binarySniffBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, err
	}
	return bytes.IndexByte(buf[:n], 0) >= 0, nil
}
