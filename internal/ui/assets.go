package ui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// assetsFS carries the UI's CSS, JavaScript, and the vendored HTMX runtime.
// Everything is served locally: the UI must work offline and the CSP forbids
// remote scripts/styles.
//
//go:embed assets/*
var assetsFS embed.FS

// handleAssets serves /ui/assets/* from the embedded filesystem. The path is
// validated with fs.ValidPath so a traversal segment can never escape the
// embedded root; unknown files are 404.
func (u *UI) handleAssets(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/ui/assets/")
	if name == "" || !fs.ValidPath(name) || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(assetsFS, path.Join("assets", name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case strings.HasSuffix(name, ".svg"):
		w.Header().Set("Content-Type", "image/svg+xml")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
