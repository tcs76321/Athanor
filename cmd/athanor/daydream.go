package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/corrections"
	"github.com/tcs76321/athanor/internal/daydream"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/power"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/strategy"
)

// M5-T6 minimal daydream driver (ARCHITECTURE §17.1–§17.2; ADR-0025 §6).
//
// One §17.1 action — Memory Consolidation — runs on a power/idle-gated loop:
// when the daemon is idle, daydreaming is allowed, and the kill switch is not
// frozen, a pass compacts terminal-job event logs (deterministic) and accepted
// documentation artifacts (semantic) at Temp 0.0. The full Daydream Engine
// (the other five actions, the §17.3 DaydreamLog, and the OS AC/battery
// watcher) is M7-T1/T2.
//
// The loop is off by default: the default power profile is interactive, whose
// limits set AllowDaydreaming=false. Enabling autonomy turns it on.

const (
	// defaultConsolidationBatch bounds how many items one source contributes
	// to a single pass.
	defaultConsolidationBatch = 20
	// minDaydreamInterval floors the loop cadence so a tiny idle_resume_after
	// cannot spin it.
	minDaydreamInterval = time.Minute
	// defaultDaydreamWallTime bounds a pass when the config leaves the limit
	// unset.
	defaultDaydreamWallTime = 30 * time.Minute
	// feedbackReviewMinProjects is how many distinct projects a
	// project-scoped rule must recur in before Feedback Review proposes it
	// globally.
	feedbackReviewMinProjects = 2
	// defaultMineCohortSize / defaultMineAcceptDelta mirror the §29
	// strategy_analysis defaults when the operator left them unset.
	defaultMineCohortSize  = 20
	defaultMineAcceptDelta = 0.15
	// proactiveDocMaxFiles bounds the repository listing sent to the model.
	proactiveDocMaxFiles = 40
)

// daydreamPower is the slice of *power.PowerManager the loop consults.
type daydreamPower interface{ GetLimits() power.Limits }

// daydreamFreezer is the §22 kill-switch surface (the engine's Freezer shape).
type daydreamFreezer interface{ Frozen() bool }

// daydreamIndexer is the M5-T8 repository-indexing slice the loop consults
// (ADR-0028 §6). One bounded pass per project per tick, so exploration yields
// to real work like every other daydream action.
type daydreamIndexer interface {
	IndexOnce(ctx context.Context, projectID, path string) (mce.IndexResult, error)
}

// daydreamGenerator is the LLM seam for the Proactive Documentation action
// (§17.1). The production adapter wraps llm.Client in the `main` persona.
type daydreamGenerator interface {
	Generate(ctx context.Context, prompt string) (string, error)
}

// daydreamDeps is everything the loop consults, so serve.go reads at the call
// site rather than through a long positional constructor.
type daydreamDeps struct {
	cfg         *config.Config
	jobs        *job.Repository
	events      *store.Store
	artifacts   *artifact.Store
	compactions *mce.CompactStore
	compactor   mce.Compactor
	power       daydreamPower
	freezer     daydreamFreezer
	// projects + indexer drive the §17.1 Repository Exploration action
	// (M5-T8). Both nil disables it; the M5-T6 consolidation tests construct
	// a runner without them.
	projects *project.Repo
	indexer  daydreamIndexer
	// logs persists the §17.3 DaydreamLog rows (M7-T2). nil disables logging.
	logs *daydream.Repo
	// corrections and strategy drive the Feedback Review / Strategy Mining
	// actions (M7-T2). nil disables the matching action.
	corrections *corrections.Repo
	strategy    *strategy.Repo
	// generator is the LLM seam for Proactive Documentation (M7-T2). nil
	// disables that action.
	generator daydreamGenerator
	log       *slog.Logger
}

// daydreamRunner owns the loop's lifecycle.
type daydreamRunner struct {
	deps   daydreamDeps
	cancel context.CancelFunc
	done   chan struct{}
}

// startDaydream launches the memory-consolidation loop. It always returns a
// runner; every pass is independently gated, so a disabled, frozen, or busy
// daemon simply does nothing.
func startDaydream(parent context.Context, deps daydreamDeps) *daydreamRunner {
	if deps.log == nil {
		deps.log = slog.Default()
	}
	ctx, cancel := context.WithCancel(parent)
	r := &daydreamRunner{deps: deps, cancel: cancel, done: make(chan struct{})}
	go r.loop(ctx)
	return r
}

// Close stops the loop and waits for it to exit.
func (r *daydreamRunner) Close() {
	r.cancel()
	<-r.done
}

// interval returns the loop cadence, floored.
func (r *daydreamRunner) interval() time.Duration {
	d := r.deps.cfg.Power.IdleResumeAfter.D()
	if d < minDaydreamInterval {
		return minDaydreamInterval
	}
	return d
}

// loop runs one pass per tick until ctx is cancelled.
func (r *daydreamRunner) loop(ctx context.Context) {
	defer close(r.done)
	t := time.NewTicker(r.interval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.pass(ctx); err != nil {
				r.deps.log.Warn("daydream pass failed", "err", err)
			}
		}
	}
}

// pass runs at most one pass of each daydream action when every gate allows
// it: configuration, the power profile, the kill switch, and idle state.
func (r *daydreamRunner) pass(ctx context.Context) error {
	if !config.Val(r.deps.cfg.Power.DaydreamOnIdle, true) {
		return nil
	}
	if r.deps.power != nil && !r.deps.power.GetLimits().AllowDaydreaming {
		return nil
	}
	if r.deps.freezer != nil && r.deps.freezer.Frozen() {
		return nil
	}
	active, err := r.deps.jobs.Active(ctx)
	if err != nil {
		return fmt.Errorf("daydream: listing active jobs: %w", err)
	}
	if len(active) > 0 {
		return nil // real work is queued; §17.2 "yield immediately"
	}

	// Both actions share one §17.2 wall-time budget.
	timeout := time.Duration(r.deps.cfg.Power.DaydreamMaxWallTimeMinutes) * time.Minute
	if timeout <= 0 {
		timeout = defaultDaydreamWallTime
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := r.consolidate(ctx); err != nil {
		return err
	}
	if err := r.exploreRepositories(ctx); err != nil {
		return err
	}
	if err := r.feedbackReview(ctx); err != nil {
		return err
	}
	if err := r.strategyMining(ctx); err != nil {
		return err
	}
	return r.proactiveDocumentation(ctx)
}

// consolidate runs one bounded pass over both §10.3 sources and audits it.
func (r *daydreamRunner) consolidate(ctx context.Context) error {
	start := time.Now()
	sources := []struct {
		name   string
		source mce.MemorySource
	}{
		{"deterministic", &terminalJobSource{jobs: r.deps.jobs, events: r.deps.events, compactions: r.deps.compactions}},
		{"semantic", &acceptedDocSource{artifacts: r.deps.artifacts, compactions: r.deps.compactions}},
	}
	var total mce.ConsolidationResult
	for _, s := range sources {
		res, err := mce.NewConsolidator(r.deps.compactions, r.deps.compactor, s.source).
			RunOnce(ctx, defaultConsolidationBatch)
		if err != nil {
			return fmt.Errorf("daydream %s: %w", s.name, err)
		}
		total.Processed += res.Processed
		total.Compacted += res.Compacted
		total.Cached += res.Cached
		total.Skipped += res.Skipped
		total.TooLarge += res.TooLarge
		total.Failed += res.Failed
		total.BytesBefore += res.BytesBefore
		total.BytesAfter += res.BytesAfter
	}
	if total.Processed == 0 {
		return nil // caught up; no audit noise
	}
	if err := r.audit(ctx, "memory_consolidation", map[string]any{
		"processed":    total.Processed,
		"compacted":    total.Compacted,
		"cached":       total.Cached,
		"skipped":      total.Skipped,
		"too_large":    total.TooLarge,
		"failed":       total.Failed,
		"bytes_before": total.BytesBefore,
		"bytes_after":  total.BytesAfter,
	}); err != nil {
		return err
	}
	tokensSaved := int(total.BytesBefore-total.BytesAfter) / 4
	if tokensSaved < 0 {
		tokensSaved = 0
	}
	r.recordDaydream(ctx, daydream.ActionMemoryConsolidation, "security", start,
		nil, nil, nil, total.Processed, tokensSaved, 0)
	return nil
}

// exploreRepositories is the §17.1 Repository Exploration action (M5-T8;
// ADR-0028 §6): one bounded indexing pass per project with a repository, so
// the single-connection database and the model are touched for at most one
// batch before the loop yields.
func (r *daydreamRunner) exploreRepositories(ctx context.Context) error {
	if r.deps.indexer == nil || r.deps.projects == nil {
		return nil
	}
	start := time.Now()
	projects, err := r.deps.projects.WithRepository(ctx, 0)
	if err != nil {
		return fmt.Errorf("daydream: listing repositories: %w", err)
	}
	var indexed, pruned, failed int
	for _, p := range projects {
		res, err := r.deps.indexer.IndexOnce(ctx, p.ID, p.RepositoryPath)
		if err != nil {
			log := r.deps.log
			if log == nil {
				log = slog.Default()
			}
			log.Warn("daydream: repository exploration failed", "project", p.ID, "err", err)
			continue
		}
		indexed += res.Indexed
		pruned += res.Pruned
		failed += res.Failed
	}
	if indexed == 0 && pruned == 0 && failed == 0 {
		return nil // caught up; no audit noise
	}
	if err := r.audit(ctx, "repository_exploration", map[string]any{
		"projects": len(projects),
		"indexed":  indexed,
		"pruned":   pruned,
		"failed":   failed,
	}); err != nil {
		return err
	}
	r.recordDaydream(ctx, daydream.ActionRepoExploration, "wide", start,
		nil, nil, nil, indexed, 0, indexed)
	return nil
}

// feedbackReview is the §17.1 Feedback Review action (M7-T2): a rule that
// recurs across two or more projects is proposed as a global CorrectionRecord
// (the §18.1 twin of Strategy Mining). It is idempotent — an existing global
// correction with the same rule suppresses re-proposal.
func (r *daydreamRunner) feedbackReview(ctx context.Context) error {
	if r.deps.corrections == nil {
		return nil
	}
	start := time.Now()
	recs, err := r.deps.corrections.Active(ctx)
	if err != nil {
		return fmt.Errorf("daydream feedback_review: %w", err)
	}
	type key struct{ rule, category string }
	byRule := map[key]map[string]bool{}
	existingGlobal := map[key]bool{}
	for _, rec := range recs {
		if rec.DerivedRule == "" {
			continue
		}
		k := key{rec.DerivedRule, rec.Category}
		if rec.Scope == corrections.ScopeGlobal {
			existingGlobal[k] = true
			continue
		}
		if byRule[k] == nil {
			byRule[k] = map[string]bool{}
		}
		byRule[k][rec.ProjectID] = true
	}
	var proposed []string
	for k, projects := range byRule {
		if len(projects) < feedbackReviewMinProjects || existingGlobal[k] {
			continue
		}
		rec, err := r.deps.corrections.Capture(ctx, corrections.CaptureInput{
			Source:       corrections.SourceFeedbackReview,
			Scope:        corrections.ScopeGlobal,
			Category:     k.category,
			Severity:     corrections.SeverityLow,
			DerivedRule:  k.rule,
			UserFeedback: fmt.Sprintf("Rule recurred across %d projects; propose it globally.", len(projects)),
			Detail:       "daydream feedback_review",
		})
		if err != nil {
			r.logger().Warn("daydream: feedback review capture failed", "err", err)
			continue
		}
		proposed = append(proposed, rec.ID)
	}
	if len(proposed) == 0 {
		return nil
	}
	if err := r.audit(ctx, "feedback_review", map[string]any{"proposed": len(proposed)}); err != nil {
		return err
	}
	r.recordDaydream(ctx, daydream.ActionFeedbackReview, "security", start, nil, proposed, nil, 0, 0, 0)
	return nil
}

// strategyMining is the §17.1 Strategy Mining action (M7-T2): deterministic
// §13.4 aggregation over StrategyOutcomes, producing proposed insights. It
// invents no numbers (Mine does the math) and never promotes.
func (r *daydreamRunner) strategyMining(ctx context.Context) error {
	if r.deps.strategy == nil || !config.Val(r.deps.cfg.StrategyAnalysis.Enabled, true) {
		return nil
	}
	opts := strategy.MineOptions{
		MinCohortSize:      r.deps.cfg.StrategyAnalysis.MinCohortSize,
		MinAcceptRateDelta: r.deps.cfg.StrategyAnalysis.MinAcceptRateDelta,
	}
	if opts.MinCohortSize <= 0 {
		opts.MinCohortSize = defaultMineCohortSize
	}
	if opts.MinAcceptRateDelta <= 0 {
		opts.MinAcceptRateDelta = defaultMineAcceptDelta
	}
	start := time.Now()
	proposed, err := r.deps.strategy.Mine(ctx, opts)
	if err != nil {
		return fmt.Errorf("daydream strategy_mining: %w", err)
	}
	if len(proposed) == 0 {
		return nil
	}
	ids := make([]string, 0, len(proposed))
	for _, ins := range proposed {
		ids = append(ids, ins.ID)
	}
	if err := r.audit(ctx, "strategy_mining", map[string]any{"proposed": len(ids)}); err != nil {
		return err
	}
	r.recordDaydream(ctx, daydream.ActionStrategyMining, "security", start, nil, nil, ids, 0, 0, 0)
	return nil
}

// proactiveDocumentation is the §17.1 Proactive Documentation action (M7-T2):
// for the first repository-backed project with no document artifact yet, ask
// the `main` persona for a draft README and persist it as a draft. Draft-only,
// never committed, and idempotent per project (once the draft exists,
// ListByProject shows a document and the project is skipped).
func (r *daydreamRunner) proactiveDocumentation(ctx context.Context) error {
	if r.deps.generator == nil || r.deps.projects == nil || r.deps.artifacts == nil {
		return nil
	}
	start := time.Now()
	projects, err := r.deps.projects.WithRepository(ctx, 1)
	if err != nil {
		return fmt.Errorf("daydream proactive_documentation: %w", err)
	}
	var produced []string
	for _, p := range projects {
		arts, err := r.deps.artifacts.ListByProject(ctx, p.ID)
		if err != nil {
			r.logger().Warn("daydream: listing artifacts", "project", p.ID, "err", err)
			continue
		}
		hasDoc := false
		for _, a := range arts {
			if a.Kind == artifact.KindDocument {
				hasDoc = true
				break
			}
		}
		if hasDoc {
			continue
		}
		content, err := r.deps.generator.Generate(ctx, buildDocPrompt(p, listRepoFiles(p.RepositoryPath, proactiveDocMaxFiles)))
		if err != nil {
			r.logger().Warn("daydream: doc generation failed", "project", p.ID, "err", err)
			continue
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		art, err := r.deps.artifacts.CreateDraft(ctx, p.ID, artifact.KindDocument, []byte(content))
		if err != nil {
			r.logger().Warn("daydream: persisting doc draft", "project", p.ID, "err", err)
			continue
		}
		produced = append(produced, art.ID)
	}
	if len(produced) == 0 {
		return nil
	}
	if err := r.audit(ctx, "proactive_documentation", map[string]any{"artifacts": len(produced)}); err != nil {
		return err
	}
	r.recordDaydream(ctx, daydream.ActionProactiveDocumentation, "main", start, produced, nil, nil, 0, 0, 0)
	return nil
}

// recordDaydream persists one §17.3 row. It is nil-safe: a runner without a
// log repo (unit tests) simply skips persistence.
func (r *daydreamRunner) recordDaydream(ctx context.Context, action, persona string, start time.Time,
	artifacts, correctionsIDs, insights []string, chunks, tokensSaved, dormantEntries int) {
	if r.deps.logs == nil {
		return
	}
	if _, err := r.deps.logs.Insert(ctx, daydream.Log{
		Action: action, PersonaUsed: persona, StartedAt: start, FinishedAt: time.Now(),
		ArtifactsProduced: artifacts, CorrectionsProposed: correctionsIDs, InsightsProposed: insights,
		ChunksProcessed: chunks, TokensSaved: tokensSaved, DormantIndexEntriesAdded: dormantEntries,
	}); err != nil {
		r.logger().Warn("daydream: persisting log", "action", action, "err", err)
	}
}

func (r *daydreamRunner) logger() *slog.Logger {
	if r.deps.log != nil {
		return r.deps.log
	}
	return slog.Default()
}

// listRepoFiles returns up to max repository-relative file paths, skipping
// VCS/build directories. The walk is bounded so a huge repository cannot stall
// a daydream pass.
func listRepoFiles(root string, max int) []string {
	var out []string
	if root == "" {
		return out
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", ".athanor", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		out = append(out, rel)
		if len(out) >= max {
			return fs.SkipAll
		}
		return nil
	})
	return out
}

// buildDocPrompt builds the Proactive Documentation request from a project and
// a bounded file listing.
func buildDocPrompt(p project.Project, files []string) string {
	return fmt.Sprintf(
		"Write a concise README.md (markdown) for the project %q.\n\n"+
			"Goal: %s\n\nRepository files:\n%s\n\n"+
			"Document the project's purpose, structure, and how to build and run it. "+
			"If you are unsure about a detail, say so plainly rather than inventing it. "+
			"Return only the markdown document.",
		p.Name, p.Goal, strings.Join(files, "\n"))
}

// audit appends a per-pass `daydream` event.
func (r *daydreamRunner) audit(ctx context.Context, event string, data map[string]any) error {
	data["event"] = event
	if _, err := r.deps.events.AppendEvent(ctx, store.Event{
		Category: "daydream",
		Data:     data,
	}); err != nil {
		return fmt.Errorf("daydream: audit: %w", err)
	}
	return nil
}

// terminalJobSource yields one deterministic-compaction item per terminal job
// whose event log has not been consolidated (log + episodic, §10.3). The event
// log is never mutated — compaction produces a derived memo (ADR-0025 §4).
type terminalJobSource struct {
	jobs        *job.Repository
	events      *store.Store
	compactions *mce.CompactStore
}

func (s *terminalJobSource) Next(ctx context.Context, limit int) ([]mce.MemoryItem, error) {
	jobs, err := s.jobs.Terminal(ctx, limit*4)
	if err != nil {
		return nil, err
	}
	var out []mce.MemoryItem
	for _, j := range jobs {
		if len(out) >= limit {
			break
		}
		done, err := s.compactions.HasJob(ctx, j.ID, mce.CompactionDeterministic)
		if err != nil {
			return nil, err
		}
		if done {
			continue
		}
		evs, err := s.events.QueryEvents(ctx, store.EventFilter{JobID: j.ID})
		if err != nil {
			return nil, err
		}
		if len(evs) == 0 {
			continue
		}
		content := joinEventLog(evs)
		out = append(out, mce.MemoryItem{
			Content:    []byte(content),
			Profile:    mce.Profile{Type: mce.EpistemicLog, State: mce.TemporalEpisodic},
			Ref:        mce.SourceRef{JobID: j.ID, ProjectID: j.ProjectID, RelPath: j.ID},
			SourceHash: contentHash(content),
		})
	}
	return out, nil
}

// acceptedDocSource yields one semantic-compaction item per accepted
// documentation artifact not yet consolidated (documentation + archival,
// §10.3). The artifact bytes are untouched.
type acceptedDocSource struct {
	artifacts   *artifact.Store
	compactions *mce.CompactStore
}

func (s *acceptedDocSource) Next(ctx context.Context, limit int) ([]mce.MemoryItem, error) {
	arts, err := s.artifacts.ListAccepted(ctx, limit*4)
	if err != nil {
		return nil, err
	}
	var out []mce.MemoryItem
	for _, art := range arts {
		if len(out) >= limit {
			break
		}
		if art.Kind != artifact.KindDocument {
			continue // code is divided, never compacted (§10.3)
		}
		done, err := s.compactions.HasSource(ctx, art.ContentHash, mce.CompactionSemantic)
		if err != nil {
			return nil, err
		}
		if done {
			continue
		}
		content, err := s.artifacts.ReadContent(ctx, art.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, mce.MemoryItem{
			Content:    content,
			Profile:    mce.Profile{Type: mce.EpistemicDocumentation, State: mce.TemporalArchival},
			Ref:        mce.SourceRef{ProjectID: art.ProjectID, JobID: art.JobID, RelPath: art.StoragePath},
			SourceHash: art.ContentHash,
		})
	}
	return out, nil
}

// joinEventLog renders an event slice as compactable text: timestamp, category,
// and the JSON data, one event per line.
func joinEventLog(evs []store.EventRecord) string {
	var b strings.Builder
	for _, e := range evs {
		fmt.Fprintf(&b, "%s %s %s\n", e.TS.Format(time.RFC3339), e.Category, e.DataJSON)
	}
	return b.String()
}

// contentHash is the origin hash a source uses as its already-consolidated
// marker.
func contentHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
