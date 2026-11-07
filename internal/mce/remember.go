package mce

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/tcs76321/athanor/internal/store"
)

// JobSummaryTemplateVersion identifies the deterministic job-summary memo.
const JobSummaryTemplateVersion = "job-summary-v1"

// RememberJobSummary writes a bounded, deterministic memo describing a
// completed job into the project's compacted memory. It is not an LLM
// compaction: the engine already has the job notebook, and this is a
// content-addressed copy of the useful part, so a later job's retrieval can
// find it. The (kind, input_hash) unique key makes the write idempotent across
// recovery and duplicate terminal finalization.
func (c *CompactStore) RememberJobSummary(ctx context.Context, projectID, jobID, summary string) (bool, error) {
	if c == nil || c.db == nil {
		return false, nil
	}
	if projectID == "" || jobID == "" || summary == "" {
		return false, nil
	}
	summary = trimSummary(summary, 4000)
	kind := CompactionDeterministic
	inputHash := sha256.Sum256([]byte(string(kind) + "\x00" + JobSummaryTemplateVersion + "\x00" + jobID + "\x00" + summary))
	hash := hex.EncodeToString(inputHash[:])
	id := compactionID(kind, hash)
	res, err := c.db.DB().ExecContext(ctx,
		`INSERT INTO compacted_memory
		    (id, kind, profile_type, profile_state, input_hash, template_version,
		     persona, temperature, project_id, job_id, source_bytes, compacted_bytes, content)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(kind, input_hash) DO NOTHING`,
		id, string(kind), "job_summary", "terminal", hash, JobSummaryTemplateVersion,
		"engine", CompactionTemperature, projectID, jobID,
		len(summary), len(summary), summary)
	if err != nil {
		return false, fmt.Errorf("mce: remember job summary: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("mce: remember job summary rows: %w", err)
	}
	if n == 0 {
		return false, nil
	}
	if _, err := c.db.AppendEvent(ctx, store.Event{
		Category:  "context",
		ProjectID: projectID, JobID: jobID,
		Data: map[string]any{
			"event": "memory_remembered", "kind": string(kind),
			"bytes": len(summary), "template": JobSummaryTemplateVersion,
		},
	}); err != nil {
		return true, fmt.Errorf("mce: audit job summary: %w", err)
	}
	return true, nil
}

func trimSummary(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}
