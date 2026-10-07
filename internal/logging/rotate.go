package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// rotatingWriter is an io.WriteCloser that rotates its file once it
// exceeds maxSize bytes, retaining at most keep rotated copies named
// <name>.log.<unixnano>.
type rotatingWriter struct {
	mu      sync.Mutex
	path    string
	f       *os.File
	size    int64
	maxSize int64
	keep    int
}

func newRotatingWriter(path string, maxSize int64, keep int) (*rotatingWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, filePerm)
	if err != nil {
		return nil, fmt.Errorf("opening log file: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("stating log file: %w", err)
	}
	return &rotatingWriter{path: path, f: f, size: info.Size(), maxSize: maxSize, keep: keep}, nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return 0, os.ErrClosed
	}
	if w.size+int64(len(p)) > w.maxSize {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) rotateLocked() error {
	rotated := fmt.Sprintf("%s.%d", w.path, time.Now().UnixNano())
	if err := w.f.Close(); err != nil {
		return fmt.Errorf("closing during rotation: %w", err)
	}
	if err := os.Rename(w.path, rotated); err != nil && !os.IsNotExist(err) {
		// Rename failed: reopen the original so logging keeps working
		// instead of leaving w.f nil (which would make every later Write
		// fail with os.ErrClosed permanently).
		if f, rerr := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, filePerm); rerr == nil {
			w.f = f
		}
		return fmt.Errorf("rotating log: %w", err)
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, filePerm)
	if err != nil {
		// Reopen of the fresh path failed; fall back to the rotated file so
		// the process keeps logging rather than going permanently silent.
		if rf, rerr := os.OpenFile(rotated, os.O_CREATE|os.O_WRONLY|os.O_APPEND, filePerm); rerr == nil {
			w.f = rf
		}
		return fmt.Errorf("reopening log after rotation: %w", err)
	}
	w.f = f
	w.size = 0
	w.pruneLocked()
	return nil
}

// pruneLocked removes the oldest rotated copies beyond the retention count.
func (w *rotatingWriter) pruneLocked() {
	matches, _ := filepath.Glob(w.path + ".*")
	if len(matches) <= w.keep {
		return
	}
	sort.Strings(matches) // unixnanos sort lexically oldest-first
	for _, old := range matches[:len(matches)-w.keep] {
		_ = os.Remove(old) // pruning is best-effort; nothing sensible to do on failure
	}
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
