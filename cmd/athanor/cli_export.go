// M4-T4: `athanor export` CLI subcommand. Talks to a
// running daemon over the loopback HTTP API and asks
// the egress exporter to export one artifact by ID.
// Returns the daemon's structured response; the
// caller (the CLI) prints it.
//
// The HTTP path is the daemon's external loopback route
// `POST /exports/<id>` (internal/api). (An earlier
// revision posted to a non-existent `/internal/v1/exports/`
// path, so the subcommand always 404'd.) The route returns
// 200 with the export path on success, 409 when frozen
// (and 503 when no exporter is wired), or a JSON error.
// The CLI surfaces the error verbatim.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

// runExport handles `athanor export -artifact <id>`.
func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	addr := fs.String("addr", defaultAddr, "daemon address")
	artifactID := fs.String("artifact", "", "artifact ID to export (required)")
	timeoutSec := fs.Int("timeout", 60, "request timeout in seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *artifactID == "" {
		return fmt.Errorf("-artifact is required")
	}
	url := fmt.Sprintf("%s/exports/%s", *addr, *artifactID)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSec)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("daemon unreachable at %s (is `athanor serve` running?): %w", *addr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := readAll(resp.Body, 1<<20)
	if resp.StatusCode >= 300 {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &errBody)
		if errBody.Error != "" {
			return fmt.Errorf("%s", errBody.Error)
		}
		return fmt.Errorf("daemon returned %s: %s", resp.Status, body)
	}
	var out struct {
		Path     string `json:"path"`
		Exported bool   `json:"exported"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	if out.Exported {
		fmt.Printf("exported to %s\n", out.Path)
	} else {
		fmt.Printf("no-op: %s\n", out.Path)
	}
	return nil
}

func readAll(r interface{ Read(p []byte) (int, error) }, n int64) []byte {
	buf := make([]byte, 0, n)
	tmp := make([]byte, 4096)
	for {
		if int64(len(buf)) >= n {
			return buf
		}
		nr, err := r.Read(tmp)
		if nr > 0 {
			buf = append(buf, tmp[:nr]...)
		}
		if err != nil {
			return buf
		}
	}
}

// Ensure os is imported even when this file is read
// in isolation by tooling (the package main file's
// os import covers the same symbol).
var _ = os.Stderr
