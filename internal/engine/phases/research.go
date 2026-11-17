package phases

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/prompt"
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
	// maxResearchQueries bounds the discovery queries one research block
	// may declare. Each is a gateway fetch of the engine page; three is
	// enough breadth without turning divergence into a crawl.
	maxResearchQueries = 3
	// searchQueryMarker is the compile-time marker the workflow template
	// writes for a research block's `queries` config.
	searchQueryMarker = "SEARCH QUERIES:"
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

// extractSearchQueries pulls the list after SEARCH QUERIES: out of a compiled
// research description. Queries are bullet lines; a blank line or a
// non-bullet line ends the list. Only the first marker block is read.
func extractSearchQueries(text string, max int) []string {
	if max <= 0 {
		return nil
	}
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(strings.ToUpper(line), searchQueryMarker) {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	var out []string
	for _, line := range lines[start+1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(out) > 0 {
				break
			}
			continue
		}
		if !strings.HasPrefix(trimmed, "-") {
			break
		}
		q := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		if q == "" {
			continue
		}
		out = append(out, q)
		if len(out) >= max {
			break
		}
	}
	return out
}

// allowsTool reports whether the task's envelope grants tool by name. The
// block compiler writes the closed-set names into AllowedTools; an empty list
// means "daemon default", which is handled by the internal API, not here.
func allowsTool(t project.Task, tool string) bool {
	for _, name := range t.AllowedTools {
		if name == tool {
			return true
		}
	}
	return false
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
func (d *Deps) researchContext(ctx context.Context, j job.Job, p project.Project, t project.Task, upstream string) (string, error) {
	local := localSourceBlock(t.Description)
	// The typed urls port (research.sources -> fetch.urls) has no separate
	// data plane; the upstream block's accepted output is the handoff. Bound
	// it before extraction so one long dependency cannot become a URL dump.
	if len(upstream) > maxSourceBlockChars {
		upstream = upstream[:maxSourceBlockChars]
	}
	urls := extractSourceURLs(t.Description+"\n"+p.Goal+"\n"+upstream, maxResearchSources)
	queries := extractSearchQueries(t.Description, maxResearchQueries)
	// A research task with search_web granted but no explicit query list
	// searches its own title + goal once, when the operator configured a
	// search engine. This is what makes a research block useful out of the
	// box without hardcoding a topic in the template.
	if len(queries) == 0 && allowsTool(t, "search_web") && d.Cfg != nil &&
		strings.TrimSpace(d.Cfg.Network.SearchEngineURLTemplate) != "" {
		if q := strings.TrimSpace(t.Title + " " + p.Goal); q != "" {
			queries = []string{q}
		}
	}
	if len(urls) == 0 && len(queries) == 0 {
		return local, nil
	}
	d.audit(ctx, j.ID, map[string]any{
		"event":   "research_start",
		"sources": len(urls),
		"queries": len(queries),
	})
	if d.runner() == nil {
		d.audit(ctx, j.ID, map[string]any{
			"event":   "research_fetch",
			"skipped": true,
			"reason":  "no ToolRunner wired (dev mode)",
		})
		return local, nil
	}

	// Bound the whole sequential discovery/fetch loop by the diverging phase
	// budget. The LLM calls are bounded in phases.go and the pod tool calls
	// in toolContext; the fetches were the one unbounded surface — a slow or
	// hanging source stalled the entire divergence phase (and job)
	// indefinitely.
	fctx, cancel := d.researchBudget(ctx)
	defer cancel()

	// Source discovery (ADR-0140): a research block may carry search queries.
	// The engine runs them through the gateway and treats the top hits as
	// additional sources; a query failure is a soft-fail. Result URLs are
	// still fetched through the same policy, so a search engine grant does
	// not grant the result hosts.
	seen := make(map[string]bool, len(urls))
	for _, u := range urls {
		seen[u] = true
	}
	if allowsTool(t, "search_web") {
		for _, q := range queries {
			if len(urls) >= maxResearchSources || fctx.Err() != nil {
				break
			}
			d.audit(ctx, j.ID, map[string]any{
				"event": "research_search", "query": q, "outcome": "start",
			})
			res, err := d.runner().SearchWeb(fctx, j.ID, toolenvelope.SearchWebRequest{Query: q})
			if err != nil {
				d.audit(ctx, j.ID, map[string]any{
					"event": "research_search", "query": q, "outcome": "error",
					"detail": err.Error(),
				})
				continue
			}
			added := 0
			for _, hit := range res.Results {
				if len(urls) >= maxResearchSources {
					break
				}
				if hit.URL == "" || seen[hit.URL] {
					continue
				}
				seen[hit.URL] = true
				urls = append(urls, hit.URL)
				added++
				d.audit(ctx, j.ID, map[string]any{
					"event": "research_source", "url": hit.URL, "query": q,
				})
			}
			d.audit(ctx, j.ID, map[string]any{
				"event": "research_search", "query": q, "outcome": "completed",
				"results": len(res.Results), "added": added,
			})
		}
	}
	if len(urls) == 0 {
		return local, nil
	}

	var b strings.Builder
	for _, u := range urls {
		if fctx.Err() != nil {
			d.audit(ctx, j.ID, map[string]any{
				"event": "research_fetch", "url": u, "outcome": "deadline_exceeded",
			})
			break
		}
		res, err := d.runner().FetchURL(fctx, j.ID, toolenvelope.FetchURLRequest{URL: u})
		switch {
		case errors.Is(err, toolenvelope.ErrToolDisallowed):
			d.audit(ctx, j.ID, map[string]any{
				"event": "research_fetch", "url": u, "outcome": "disallowed",
			})
			continue
		case errors.Is(err, toolenvelope.ErrPolicyDenied):
			// The tool was in the envelope but the gateway policy
			// refused the host (known-issues O1). Audited distinctly
			// so a denial is not misread as an envelope miss.
			d.audit(ctx, j.ID, map[string]any{
				"event": "research_fetch", "url": u, "outcome": "denied",
			})
			continue
		case err != nil:
			d.audit(ctx, j.ID, map[string]any{
				"event": "research_fetch", "url": u, "outcome": "error",
				"detail": err.Error(),
			})
			continue
		case res.Markdown == "":
			d.audit(ctx, j.ID, map[string]any{
				"event": "research_fetch", "url": u, "outcome": "empty",
				"mode": res.Mode, "status": res.StatusCode,
			})
			continue
		}
		md := res.Markdown
		if len(md) > maxSourceBlockChars {
			md = md[:maxSourceBlockChars] + "\n…[source truncated at the prompt-side budget]"
		}
		d.audit(ctx, j.ID, map[string]any{
			"event": "research_fetch", "url": u, "outcome": "fetched",
			"mode": res.Mode, "bytes": len(md), "truncated": res.Truncated,
		})
		// External bytes are evidence, not instructions: frame the fetched
		// markdown as a labeled untrusted block (W2 trust boundary).
		fmt.Fprintf(&b, "\n\n%s\n", prompt.Untrusted("fetched source: "+u, md))
	}
	if b.Len() == 0 {
		return local, nil
	}
	fetched := "RESEARCH SOURCES (fetched through the gateway — ground claims in these and cite them):\n" + b.String()
	if local != "" {
		return local + "\n\n" + fetched, nil
	}
	return fetched, nil
}

// localSourceBlock returns the local source material embedded in a task
// description by the cycle compiler, bounded to the research prompt budget.
func localSourceBlock(description string) string {
	const marker = "SOURCE MATERIAL"
	idx := strings.Index(description, marker)
	if idx < 0 {
		return ""
	}
	block := strings.TrimSpace(description[idx:])
	if len(block) > maxSourceBlockChars*2 {
		block = block[:maxSourceBlockChars*2] + "\n…[local source truncated at the prompt-side budget]"
	}
	return block
}

// researchBudget bounds the sequential research fetch loop by the diverging
// phase's wall-time budget (5-minute cap, mirroring toolContext). The
// returned cancel must be deferred by the caller.
func (d *Deps) researchBudget(parent context.Context) (context.Context, context.CancelFunc) {
	budget := 5 * time.Minute
	if d.Cfg != nil {
		if b, ok := d.Cfg.Execution.PhaseBudget(string(llm.PhaseDiverging)); ok && b > 0 && b < budget {
			budget = b
		}
	}
	return context.WithTimeout(parent, budget)
}
