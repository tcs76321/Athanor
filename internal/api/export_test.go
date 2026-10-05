package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/tcs76321/athanor/internal/artifact"
)

// fakeExporter stands in for the egress exporter so the manual-export
// route's error mapping can be exercised without spinning up the scanner
// pipeline.
type fakeExporter struct {
	path     string
	exported bool
	err      error
}

func (f *fakeExporter) ExportOne(_ context.Context, _ string) (string, bool, error) {
	return f.path, f.exported, f.err
}

func postExport(t *testing.T, url string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// TestExportRouteMissingArtifactIs404 pins the sentinel mapping: the
// exporter wraps artifact.ErrNotFound, and the route must translate that
// to 404, not 500. The pre-F3 helper compared error strings against
// "artifact: not found", which never matched the real sentinel
// ("artifact not found").
func TestExportRouteMissingArtifactIs404(t *testing.T) {
	h := newHarness(t)
	h.api.SetManualExporter(&fakeExporter{
		err: fmt.Errorf("get artifact ghost: %w", artifact.ErrNotFound),
	})
	status, raw := postExport(t, h.ts.URL+"/exports/ghost")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", status, raw)
	}
}

// TestExportRouteHardErrorIs500 is the complement: a non-sentinel failure
// must not be silently reclassified as "not found".
func TestExportRouteHardErrorIs500(t *testing.T) {
	h := newHarness(t)
	h.api.SetManualExporter(&fakeExporter{err: errors.New("scanner subprocess hung")})
	status, _ := postExport(t, h.ts.URL+"/exports/ghost")
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
}

// TestExportRouteSuccessIs200 guards the happy path against the mapping
// becoming too eager.
func TestExportRouteSuccessIs200(t *testing.T) {
	h := newHarness(t)
	h.api.SetManualExporter(&fakeExporter{path: "/tmp/export", exported: true})
	status, _ := postExport(t, h.ts.URL+"/exports/abc")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
}
