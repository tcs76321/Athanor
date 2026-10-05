// Package logging provides category-tagged structured JSON logging to
// state/logs/ with size-based rotation (ARCHITECTURE §28).
//
// Every event carries a "category" attribute from the closed set in
// config.Categories (§28.1). The SQLite event log (M0-T6) is the
// append-only audit trail; file logging is a separate substrate.
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxSizeBytes = 10 << 20 // 10 MiB per active log file
	defaultKeepFiles    = 5        // rotated files retained per category
	filePerm            = 0o600
)

// Options controls logger construction.
type Options struct {
	// Dir is the log directory, e.g. <home>/state/logs.
	Dir string
	// Level is the minimum enabled level ("debug", "info", "warn", "error").
	Level string
	// Categories is the set of enabled categories; empty enables all known.
	Categories []string
	// MaxSizeBytes overrides the rotation threshold (tests).
	MaxSizeBytes int64
	// KeepFiles overrides how many rotated files are retained (tests).
	KeepFiles int
}

// Manager owns the rotating sinks and hands out per-category loggers.
type Manager struct {
	opts   Options
	mu     sync.Mutex
	sinks  map[string]*rotatingWriter // one sink per category file
	level  slog.Level
	closed bool
}

// New creates the log directory and returns a Manager. Close must be
// called to flush and release file handles.
func New(opts Options) (*Manager, error) {
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating log dir: %w", err)
	}
	m := &Manager{opts: opts, sinks: map[string]*rotatingWriter{}}
	switch strings.ToLower(opts.Level) {
	case "", "info":
		m.level = slog.LevelInfo
	case "debug":
		m.level = slog.LevelDebug
	case "warn":
		m.level = slog.LevelWarn
	case "error":
		m.level = slog.LevelError
	default:
		return nil, fmt.Errorf("unknown log level %q", opts.Level)
	}
	return m, nil
}

// categoryEnabled reports whether cat may emit events.
func (m *Manager) categoryEnabled(cat string) bool {
	if len(m.opts.Categories) == 0 {
		return true
	}
	for _, c := range m.opts.Categories {
		if c == cat {
			return true
		}
	}
	return false
}

// Logger returns a *slog.Logger writing JSON events to the given
// category's file under Dir. Unknown or disabled categories are rejected —
// the category set is closed by ARCHITECTURE §28.1.
func (m *Manager) Logger(category string) (*slog.Logger, error) {
	if !m.categoryEnabled(category) {
		return nil, fmt.Errorf("category %q is not in the enabled set", category)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, fmt.Errorf("logging manager is closed")
	}
	w, ok := m.sinks[category]
	if !ok {
		maxSize := m.opts.MaxSizeBytes
		if maxSize <= 0 {
			maxSize = defaultMaxSizeBytes
		}
		keep := m.opts.KeepFiles
		if keep <= 0 {
			keep = defaultKeepFiles
		}
		var err error
		w, err = newRotatingWriter(filepath.Join(m.opts.Dir, category+".log"), maxSize, keep)
		if err != nil {
			return nil, err
		}
		m.sinks[category] = w
	}
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: m.level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().UTC().Format(time.RFC3339Nano))
			}
			return a
		},
	})
	return slog.New(h).With(slog.String("category", category)), nil
}

// Close flushes and closes all open sinks.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	var firstErr error
	for cat, w := range m.sinks {
		if err := w.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("closing %s log: %w", cat, err)
		}
	}
	m.sinks = map[string]*rotatingWriter{}
	return firstErr
}
