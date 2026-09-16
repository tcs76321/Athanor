package engine

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// The M4-T7 research sub-step (ADR-0019 §7, as amended): URLs the
// task author wrote into the task description (and the project goal)
// are fetched through the §21.5 gateway before divergence, and the
// extracted markdown is injected into the divergence prompt as
// attributed, bounded blocks.
//
// The original ADR said the planner would emit a `sources` array.
// Reality: the M1 planner output is free text and is not persisted —
// there is no structured plan schema to hang `sources` on, and
// building one is M5-scale prompt-schema work. The task-declared URL
// is the deterministic equivalent: the human wrote the URLs, the
// engine fetches exactly those (nothing scraped from LLM prose),
// and the per-task `fetch_url` envelope override gates the whole
// sub-step. The ADR §7 amendment records the pivot.
//
// Every failure soft-fails (the M1 precedent): a denied, disallowed,
// or unreachable source is a containment/availability event in the
// audit log, never a job failure. Research context is an
// enhancement; a job must not die because a documentation site is
// down.

const (
	// maxResearchSources bounds how many URLs one task's prose may
	// contribute. A research task cites a handful of sources; more
	// is a URL dump and the per-phase budget would eat it.
	maxResearchSources = 8
	// maxSourceBlockChars bounds one source's injected markdown.
	// The gateway already capped the fetch at network.max_response_bytes;
	// this is the prompt-side budget so one huge page cannot crowd
	// out the task itself.
	maxSourceBlockChars = 8000
)

// sourceURLRe matches absolute http(s) URLs in task prose. The
// character class excludes whitespace and the delimiters that would
// end a URL in markup; trailing punctuation is stripped by
// extractSourceURLs.
var sourceURLRe = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `\[\]]+`)

// extractSourceURLs pulls the task-declared research URLs out of
// free text: deduped, trailing prose punctuation stripped, capped at
// max. Extraction is deliberately conservative — only what the
// author explicitly wrote as a URL is fetched; nothing is scraped
// from LLM output.
func extractSourceURLs(text string, max int) []string {
	if max <= 0 {
		return nil
	}
	matches := sourceURLRe.FindAllString(text, -1)
	seen := map[string]bool{}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		m = strings.TrimRight(m, ".,;:!?)]}")
		if len(m) <= len("https://x") {
			continue
		}
		if seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
		if len(out) >= max {
			break
		}
	}
	return out
}

// researchContext fetches the task's declared sources through the
// gateway (via the runner's envelope-gated fetch_url route) and
// returns the combined, attributed markdown block for the divergence
// prompt. Empty string means "no sources declared" or "nothing
// fetched" — both are silent no-ops apart from the audit rows.
//
// The sub-step is synchronous and sequential (ADR-0019 §7): one
// runner.FetchURL per source, each soft-failing into its own
// `research_fetch` audit row. A crash mid-divergence re-runs the
// whole phase (the M3-T6 recovery shape), so the fetches re-run too
// — they are idempotent downloads, gateway-audited and rate-limited.
func (e *Engine) researchContext(ctx context.Context, j job.Job, p project.Project, t project.Task) (string, error) {
	urls := extractSourceURLs(t.Description+"\n"+p.Goal, maxResearchSources)
	if len(urls) == 0 {
		return "", nil
	}
	e.audit(ctx, j.ID, map[string]any{
		"event":   "research_start",
		"sources": len(urls),
	})
	if e.runner == nil {
		e.audit(ctx, j.ID, map[string]any{
			"event":  "research_fetch",
			"skipped": true,
			"reason": "no ToolRunner wired (dev mode)",
		})
		return "", nil
	}

	var b strings.Builder
	for _, u := range urls {
		res, err := e.runner.FetchURL(ctx, j.ID, toolenvelope.FetchURLRequest{URL: u})
		switch {
		case errors.Is(err, toolenvelope.ErrToolDisallowed):
			e.audit(ctx, j.ID, map[string]any{
				"event": "research_fetch", "url": u, "outcome": "disallowed",
			})
			continue
		case err != nil:
			e.audit(ctx, j.ID, map[string]any{
				"event": "research_fetch", "url": u, "outcome": "error",
				"detail": err.Error(),
			})
			continue
		case res.Markdown == "":
			e.audit(ctx, j.ID, map[string]any{
				"event": "research_fetch", "url": u, "outcome": "empty",
				"mode": res.Mode, "status": res.StatusCode,
			})
			continue
		}
		md := res.Markdown
		if len(md) > maxSourceBlockChars {
			md = md[:maxSourceBlockChars] + "\n…[source truncated at the prompt-side budget]"
		}
		e.audit(ctx, j.ID, map[string]any{
			"event": "research_fetch", "url": u, "outcome": "fetched",
			"mode": res.Mode, "bytes": len(md), "truncated": res.Truncated,
		})
		fmt.Fprintf(&b, "\n\n### Source: %s\n\n%s\n", u, md)
	}
	if b.Len() == 0 {
		return "", nil
	}
	return "RESEARCH SOURCES (fetched through the gateway — ground claims in these and cite them):\n" + b.String(), nil
}