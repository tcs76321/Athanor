package main

import (
	"flag"
	"fmt"
	"net/http"
)

// indexResult mirrors the daemon's IndexSummary JSON.
type indexResult struct {
	Discovered int `json:"discovered"`
	Indexed    int `json:"indexed"`
	Skipped    int `json:"skipped"`
	Pruned     int `json:"pruned"`
	Chunks     int `json:"chunks"`
	Summarized int `json:"summarized"`
	Embedded   int `json:"embedded"`
	Failed     int `json:"failed"`
}

// runIndex drives the M5-T8 repository indexer for one project (ADR-0028 §6).
func runIndex(args []string) error {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	projectID := fs.String("project", "", "project id (required)")
	path := fs.String("path", "", "repository root (overrides the project's repository_path)")
	addr := fs.String("addr", defaultAddr, "daemon address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectID == "" {
		return fmt.Errorf("-project is required")
	}
	var out indexResult
	if err := apiCall(http.MethodPost, *addr+"/projects/"+*projectID+"/index",
		map[string]any{"path": *path}, &out); err != nil {
		return err
	}
	fmt.Printf("indexed %d, skipped %d, pruned %d, failed %d (%d chunks, %d summaries, %d embeddings)\n",
		out.Indexed, out.Skipped, out.Pruned, out.Failed,
		out.Chunks, out.Summarized, out.Embedded)
	return nil
}
