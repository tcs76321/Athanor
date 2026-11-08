// Package repair owns the pure policy for the self-refine operation: whether
// an attempt is allowed, which candidate to repair, the targeted prompt, and
// the single-file observation request. The orchestration — the model call, the
// artifact write, and the re-evaluation — stays in the phase package, which
// already owns evaluateCandidate. Keeping that loop there is what lets this
// package stay a leaf (ADR-0131).
package repair

import (
	"fmt"
	"strings"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/engine/acceptance"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/prompt"
)

// Budget is the self-refine allowance for one job.
type Budget struct {
	Enabled bool
	Max     int
	Used    int
}

// Allows reports whether another repair may run.
func (b Budget) Allows() bool {
	return b.Enabled && b.Max > 0 && b.Used < b.Max
}

// Passed reports whether an evaluation record passed every acceptance edge.
func Passed(rec evaluation.Record) bool {
	return rec.PassedTests && len(rec.MissingCriteria) == 0 && len(rec.SecurityIssues) == 0
}

// BestCandidate returns the job's highest-scoring evaluated proposal. Among
// multiple records for one artifact it takes the highest score. ok is false
// when there is nothing evaluated to repair.
func BestCandidate(candidates []artifact.Artifact, records []evaluation.Record) (artifact.Artifact, evaluation.Record, bool) {
	if len(candidates) == 0 || len(records) == 0 {
		return artifact.Artifact{}, evaluation.Record{}, false
	}
	bestByArtifact := map[string]evaluation.Record{}
	for _, r := range records {
		if cur, ok := bestByArtifact[r.ArtifactID]; !ok || r.Score > cur.Score {
			bestByArtifact[r.ArtifactID] = r
		}
	}
	var best artifact.Artifact
	var bestRec evaluation.Record
	found := false
	for _, c := range candidates {
		r, ok := bestByArtifact[c.ID]
		if !ok {
			continue
		}
		if !found || r.Score > bestRec.Score {
			best, bestRec, found = c, r, true
		}
	}
	return best, bestRec, found
}

// Instructions composes the targeted-repair prompt: the evaluator's recorded
// failures plus an instruction to change only what is necessary and return the
// full artifact.
func Instructions(rec evaluation.Record, capability, language string) string {
	var b strings.Builder
	b.WriteString("SELF-REFINE. The candidate above failed acceptance. Rewrite it to fix exactly these failures:\n")
	if len(rec.MissingCriteria) > 0 {
		fmt.Fprintf(&b, "  missing criteria: %v\n", rec.MissingCriteria)
	}
	if len(rec.FailedTests) > 0 {
		fmt.Fprintf(&b, "  failed tests: %v\n", rec.FailedTests)
	}
	if len(rec.SecurityIssues) > 0 {
		fmt.Fprintf(&b, "  security issues: %v\n", rec.SecurityIssues)
	}
	if rec.Summary != "" {
		fmt.Fprintf(&b, "  evaluator summary: %q\n", rec.Summary)
	}
	b.WriteString("\nKeep what already works; change only what is necessary. Output the full corrected artifact.")
	if capability == project.CapabilityCode {
		b.WriteString(" " + prompt.CodeInstruction(language))
	}
	return b.String()
}

// ObservationRequest is the one-file observation the model may ask for before
// a repair. The engine executes the read; the model never gets a general tool
// loop (ADR-0113).
type ObservationRequest struct {
	ReadFile *string `json:"read_file"`
}

// ParseObservation extracts a requested path from the model's observation
// response. requested is false for an explicit null; a malformed response is an
// error the caller audits and ignores.
func ParseObservation(content string) (path string, requested bool, err error) {
	req, err := acceptance.ParseVerdict[ObservationRequest](content)
	if err != nil {
		return "", false, err
	}
	if req.ReadFile == nil {
		return "", false, nil
	}
	path = strings.TrimSpace(*req.ReadFile)
	if path == "" {
		return "", false, nil
	}
	return path, true, nil
}
