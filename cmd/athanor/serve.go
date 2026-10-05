package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tcs76321/athanor/internal/api"
	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/control"
	"github.com/tcs76321/athanor/internal/corrections"
	"github.com/tcs76321/athanor/internal/engine"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/internalapi"
	"github.com/tcs76321/athanor/internal/internalapi/runner"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/jobpod"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/logging"
	"github.com/tcs76321/athanor/internal/power"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/scheduler"
	"github.com/tcs76321/athanor/internal/server"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

// daemonFlags holds the serve command's flag set.
type daemonFlags struct {
	configPath string
	addr       string
	stateDir   string
	version    bool
}

// serveFlags parses daemon flags; extra args after "serve" are included.
func serveFlags(args []string) *daemonFlags {
	f := &daemonFlags{}
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.StringVar(&f.configPath, "config", "config.yaml", "path to config.yaml")
	fs.StringVar(&f.addr, "addr", "127.0.0.1:7420", "HTTP listen address (loopback only, §21.8)")
	fs.StringVar(&f.stateDir, "state-dir", "state", "state directory (database, logs, backups)")
	fs.BoolVar(&f.version, "version", false, "print version and exit")
	if len(args) > 0 && args[0] == "serve" {
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "athanor:", err)
		os.Exit(2)
	}
	return f
}

// runServe is the serve entry point.
func runServe(f *daemonFlags) {
	if f.version {
		fmt.Println("athanor", version)
		return
	}
	if err := run(f.configPath, f.addr, f.stateDir); err != nil {
		fmt.Fprintln(os.Stderr, "athanor:", err)
		os.Exit(1)
	}
}

// run boots the daemon and blocks until an OS signal or server failure.
func run(configPath, addr, stateDir string) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logMgr, err := logging.New(logging.Options{
		Dir:        filepath.Join(stateDir, "logs"),
		Level:      cfg.Logging.Level,
		Categories: cfg.Logging.Categories,
	})
	if err != nil {
		return fmt.Errorf("initialising logging: %w", err)
	}
	defer func() { _ = logMgr.Close() }()

	st, err := store.Open(filepath.Join(stateDir, "athanor.db"))
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer func() { _ = st.Close() }()

	// ADR-0026 §1: the MCE retrieval index (migration 0013) is an FTS5
	// virtual table. FTS5 is a build-tag feature of mattn/go-sqlite3, so a
	// tag-less binary would fail mid-migration with "no such module: fts5".
	// Probe before migrating and fail with a named, actionable error.
	if err := store.CheckFTS5(context.Background(), st.DB()); err != nil {
		return err
	}

	if err := store.Migrate(st.DB(), migrations.FS, filepath.Join(stateDir, "backups")); err != nil {
		return fmt.Errorf("migrating database: %w", err)
	}

	// Kill switch (M1-T6, §22): loads persisted frozen state so a restart
	// inherits freeze status, never resets it.
	killSwitch, err := control.NewKillSwitch(st)
	if err != nil {
		return fmt.Errorf("loading kill switch state: %w", err)
	}

	// Wire the walking skeleton (M1): personas → LLM client → engine.
	registry, err := llm.NewRegistry(cfg.Personas)
	if err != nil {
		return fmt.Errorf("building persona registry: %w", err)
	}
	// M1-T8.4: PowerManager is the live source of the engine's
	// concurrency cap. M6 will add an OS watcher that switches
	// profiles; for now it stays at the default (interactive).
	powerMgr := power.NewPowerManager(nil)
	// M2-T2: Job Pod manager. Owns the lifecycle of every Podman
	// Job Pod. Sweep runs at boot to clean up after a crash or
	// kill -9. M2-T4b wired the engine↔pod lifecycle (ADR-0024): the
	// engine's pod-lifecycle seam starts a per-job pod and stops it at
	// terminal state, and the internal API execs into it; the runner's
	// TokenFor resolves the per-job token from this manager.
	podMgr := jobpod.New(NewExecClient(), killSwitch, filepath.Join(stateDir, "tokens"))
	if res, err := podMgr.Sweep(context.Background()); err != nil {
		// Sweep is opportunistic: a missing podman or a not-yet-
		// started machine is logged, not fatal.
		fmt.Fprintf(os.Stderr, "athanor: jobpod sweep failed: %v\n", err)
	} else if res.Removed > 0 {
		fmt.Printf("athanor: swept %d orphan pod(s) at startup\n", res.Removed)
	}
	// Resolve the loopback listen address once, before any
	// component that needs it. The engine's ToolRunner (M2-T4)
	// uses it as the base URL for the internal API.
	loopAddr, err := server.LocalhostAddr(addr)
	if err != nil {
		return err
	}
	// Construct the artifact store and project repo
	// once; both the engine and the egress pipeline
	// (M4-T4) need them. Sharing the same instances
	// ensures the egress loop sees engine writes
	// (the artifact store is the source of truth).
	artifactStore := artifact.NewStore(st, filepath.Join(stateDir, "artifacts"))
	projectRepo := project.NewRepo(st)
	// One LLM client shared by the engine and the MCE summarizer, so both
	// talk to the same Ollama endpoint through the same connection pool.
	llmClient := llm.NewClient(cfg.Inference.OllamaURL, nil)
	// M5-T2.7: MCE dormant chunk store + wide-persona summarizer. Built
	// before the engine (M5-T5.6) because the engine's §10.5 assembling
	// reads the MCE working set through the ContextProvider adapter.
	// A database without migration 0009 fails here rather than at first
	// ingest.
	mceRT, err := startMCE(st, registry, llmClient, cfg.ContextEngine)
	if err != nil {
		return fmt.Errorf("starting mce: %w", err)
	}
	slog.Info("mce runtime ready",
		"lossless_swapping", mceRT.LosslessSwapping,
		"summarizer_persona", mceRT.SummarizerPersona,
		"chunk_store", mceRT.Store != nil,
		"summarizer", mceRT.Summarizer != nil,
		"compaction_store", mceRT.CompactStore != nil,
		"compactor", mceRT.Compactor != nil)
	eng := engine.New(cfg, st,
		job.NewRepository(st),
		projectRepo,
		artifactStore,
		// M3-T1: EvaluationRecord repository (§19). The engine
		// persists one record per candidate artifact during
		// the evaluating phase; the comparison phase reads
		// them back.
		evaluation.NewRepo(st),
		llmClient,
		registry,
		killSwitch,
		powerMgr,
		// M2-T4: the engine's window onto the internal API.
		// It authenticates each call with the per-job bearer
		// token retrieved from the Job Pod manager, so the
		// engine is just another client of the loopback
		// internal API (ADR-0009 D5).
		runner.New("http://"+loopAddr, podMgr),
		// M5-T5.6: the §10.4 eviction seam is now the real §10.5
		// ladder (ADR-0023 §6) — one step down the tier order per
		// critical-pressure call, recorded in the job's suppression
		// row. It stays stateless; the engine persists the result.
		engine.NewLadderEvictor(),
		// M5-T5.6: the MCE working set (§10.1 active chunk +
		// Dormant Index) behind the same lossless-swapping gate as
		// ingestion and the context_swap route.
		newContextProvider(mceRT.Store, mceRT.LosslessSwapping),
		// M2-T4b.5: the ADR-0024 §2 Job Pod lifecycle seam. It starts a
		// long-lived idle pod before the code-archetype sub-steps and
		// stops it at terminal state, so the runner's per-job token
		// (TokenFor) exists when the internal API execs into it.
		newPodLifecycle(podMgr, cfg),
	)
	// F3-T5 (§14, ADR-0030): Git-as-undo. When an artifact is accepted
	// and the project has a repository_path that is a clean git worktree,
	// record it on an agent branch. Best-effort; never pushes. M6-T5
	// (ADR-0036) reuses the same adapter for the HITL-gated push.
	gitAdapter := gitClient{}
	eng.SetGitCommitter(gitAdapter)
	// M6-T2 (ADR-0033): the dependency scheduler. It starts the ready
	// leaves of a decomposed goal and advances the graph as jobs finish;
	// the engine notifies it through the terminal seam below.
	sched := scheduler.New(projectRepo, job.NewRepository(st), eng, st)
	sched.SetMaxTaskRetries(cfg.Execution.MaxTaskRetriesValue())
	eng.SetOnJobTerminal(sched.OnJobTerminal)
	// M6-T4 (ADR-0035): the HITL queue. The scheduler's Escalator seam
	// turns an exhausted task into a request; the service waits resumes,
	// fails on rejection, and denies on expiry.
	hitlRepo := hitl.NewRepo(st)
	hitlSvc := hitl.NewService(hitlRepo, job.NewRepository(st), eng, cfg.HITL.DefaultTTL.D())
	sched.SetEscalator(hitlEscalator{repo: hitlRepo})
	// M6-T5 (ADR-0036): approval of a git_push request runs the push.
	hitlSvc.SetApprover(hitl.TypeGitPush,
		gitPushApprover{git: gitAdapter, projects: projectRepo, events: st}.Approve)
	// M6-T6 (ADR-0037): CorrectionRecords. The engine reports phase failures
	// through the sink; the API serves the §18.4 rejection form and lists.
	correctionsRepo := corrections.NewRepo(st)
	eng.SetCorrectionSink(correctionsRepo)
	eng.SetCorrectionSource(correctionsRepo)
	srv := server.New(version)
	srv.SetControl(killSwitch)
	externalAPI := api.New(projectRepo, job.NewRepository(st),
		artifactStore,
		eng, killSwitch, st)
	// M5-T8: the repository indexer behind POST /projects/{id}/index
	// (ADR-0028 §6). It shares the MCE runtime's chunk store and summarizer.
	indexer := newMCEIndexer(mceRT, st, llmClient, cfg.ContextEngine)
	externalAPI.SetIndexRunner(indexer)
	// M6-T1: the explicit DAG decomposer behind POST /projects/{id}/decompose
	// (ADR-0032). It shares the LLM client and persona registry with the
	// engine; it produces and validates a task graph but does not enqueue
	// jobs (the M6-T2 scheduler will).
	externalAPI.SetDecomposer(newDecomposer(cfg, llmClient, registry, projectRepo, st))
	// M6-T2: the scheduler, plus the execution.dag_decomposition gate that
	// switches goal submission to decompose-then-schedule (ADR-0033 §5).
	externalAPI.SetScheduler(sched)
	externalAPI.SetDAGScheduling(cfg.Execution.DAGDecomposition)
	externalAPI.SetHITL(hitlSvc)
	externalAPI.SetPusher(gitPusher{projects: projectRepo, hitl: hitlRepo})
	externalAPI.SetCorrections(correctionsRepo)
	externalAPI.Register(srv.Mux())
	// M2-T3 + M2-T4: internal API for Job Pods. Same loopback HTTP
	// server, different path prefix (/internal/v1/), every route
	// wrapped in authMiddleware. Token store is the jobpod.Manager's
	// in-memory map of podID → token, adapted to the
	// internalapi.TokenStore shape (ErrNotFound → ErrTokenNotFound).
	// The tool envelope lookup is the project repo, which loads
	// the per-task override and falls back to the config default.
	// Structural proof is Gate G2 (internal/gate/gate_g2_test.go).
	defaultEnv, err := cfg.JobPodEnvelope()
	if err != nil {
		return fmt.Errorf("resolving job_pod.default_tools: %w", err)
	}
	// M4-T7 (ADR-0019 §2): the gateway-backed tools dispatch through
	// the adapter over GatewayParts — the §21.5 Client + Reader
	// constructed below. Failures to construct the gateway are
	// fatal: a daemon that boots without a gateway is, by
	// construction, a daemon that cannot reach the internet safely.
	// The Reader half of the bundle is constructed (and its
	// injection heuristic validated) here so a reader-mode
	// misconfiguration fails at boot, not at first fetch.
	gw, err := startGateway(stateDir, st, cfg.Network, slog.Default())
	if err != nil {
		return fmt.Errorf("starting gateway: %w", err)
	}
	if cfg.JobPod.Image == "" {
		fmt.Fprintln(os.Stderr, "athanor: warning: job_pod.image is empty — Job Pod tool execution "+
			"(execute_code / run_tests / lint) will refuse until an image is configured "+
			"(see config.example.yaml; ADR-0024 §6)")
	}
	internalapi.New(tokenStoreAdapter{podMgr}, project.NewRepo(st), st,
		project.NewRepo(st), defaultEnv,
		newGatewayToolAdapter(gw, cfg.Network.SearchEngineURLTemplate),
		// M5-T3.3: the context_swap adapter over the MCE chunk store built
		// above. A daemon with lossless swapping disabled still constructs
		// it; every swap then returns ErrSwapDisabled (501).
		newContextSwapAdapter(mceRT.Store, mceRT.LosslessSwapping),
		// M2-T4b.4: the PodExecutor over the Job Pod manager. An empty
		// job_pod.image makes every pod-executed route answer 503 (the
		// boot warning above says so); the engine lifecycle seam (T4b.5)
		// is what actually starts the pod this adapter execs into.
		newPodExecutor(podMgr, cfg.JobPod.Image),
		// M5-T7.7: the query_memory adapter over the MCE retriever. It is
		// full-text only unless context_engine.memory_embedding_model is
		// set (ADR-0026 §3), so the route is live without embeddings.
		newMemoryQuerier(st, llmClient, cfg.ContextEngine),
	).Register(srv.Mux())

	// M4-T2: ingress pipeline. Watches <state>/workspace/inbox,
	// routes new files through airlock/paths + airlock/scanner,
	// disposes to .processed/ or quarantine/. The watcher is
	// bound to the daemon's lifetime; deferred Close drains
	// the pending queue before the process exits. The same
	// scanner registry is reused for the egress pipeline
	// below (M4-T4) — one registry, two pipelines.
	ingWatcher, scannerReg, err := startIngress(context.Background(), stateDir, st, killSwitch,
		ingressConfig{
			AirlockEnabled:       config.Val(cfg.Airlock.Enabled, true),
			MaxIngressBytes:      cfg.Airlock.MaxIngressBytes,
			MaxUncompressedRatio: cfg.Airlock.MaxUncompressedRatio,
			MaxZipEntries:        cfg.Airlock.MaxZipEntries,
			YaraRuleSet:          cfg.Airlock.YaraRuleSet,
		},
		slog.Default(),
	)
	if err != nil {
		return fmt.Errorf("starting ingress: %w", err)
	}
	if ingWatcher != nil {
		defer func() { _ = ingWatcher.Close() }()
	}

	// M4-T4: egress pipeline. Subscribes to the event log
	// for accepted artifacts and exports them to
	// <state>/workspace/exports/. The engine is unchanged:
	// the exporter is an observer, not a driver (ADR-0015
	// §"Egress is a subscriber, not an engine hook"). The
	// poll goroutine stops on ctx cancel (daemon shutdown).
	exporter, err := startEgress(
		context.Background(), stateDir, st,
		artifactStore, projectRepo, scannerReg, slog.Default(),
	)
	if err != nil {
		return fmt.Errorf("starting egress: %w", err)
	}
	// Wire the exporter into the external API so the
	// `athanor export` CLI (and any future operator UI)
	// can drive synchronous exports. The interface
	// keeps the API package free of the egress import
	// (Gate G1 keeps the dependency graph narrow).
	externalAPI.SetManualExporter(exporter)

	// M4-T5: §21.5 Internet Gated Reader. The gateway
	// is the *only* door between the Core and the
	// public internet (ADR-0017 §1). It is
	// constructed at daemon boot and held ready for
	// the M4-T7 tool envelope; no callers exist
	// yet, but the construction is the structural
	// proof that the config + Resolver + audit-log
	// wiring is sound. Failures to construct the
	// gateway are fatal: a daemon that boots
	// without a gateway is, by construction, a
	// daemon that cannot reach the internet safely
	// (and the operator's first `network` event
	// would be the absence of a `fetched` row).
	// M4-T7: the gateway bundle is constructed above (before the
	// internal API registration) and threaded into the tool adapter
	// (ADR-0019 §2); a future contributor who refactors serve.go and
	// drops the gateway construction trips a CI boot test (added in
	// T5.3).

	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ln, err := net.Listen("tcp", loopAddr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", loopAddr, err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(ln) }()

	// Dogfood the EventLog: record startup in the append-only audit trail.
	if _, err := st.AppendEvent(context.Background(), store.Event{
		Category: "jobs",
		Data:     map[string]any{"event": "startup", "version": version},
	}); err != nil {
		return fmt.Errorf("recording startup event: %w", err)
	}

	// §23.6: resume any job that was mid-flight when the daemon died.
	// M2-T4b (ADR-0024) closed the former gap here: the engine↔pod
	// lifecycle seam starts a pod for `code`-archetype jobs and the
	// internal API dispatches execute_code / run_tests / lint into it
	// (503 until job_pod.image is set). A recovered job resumes with the
	// same dispatch.
	//
	// M6-T4 (ADR-0035): the HITL expiry sweep runs alongside recovery so an
	// unanswered request is denied on schedule.
	hitlCtx, cancelHITL := context.WithCancel(context.Background())
	defer cancelHITL()
	startHITLExpiry(hitlCtx, hitlSvc, cfg.HITL.ExpiryInterval.D(), slog.Default())

	eng.Recover(context.Background())

	// M6-T2 (ADR-0033 §4): reconcile decomposed graphs after a restart.
	// Recover restarts in-flight jobs; this pass applies any terminal job
	// outcomes recorded before the crash and schedules newly-ready leaves.
	if goals, err := projectRepo.NonTerminalGoalIDs(context.Background()); err != nil {
		slog.Error("scheduler: listing goals to reconcile", "err", err)
	} else {
		for _, goalID := range goals {
			if err := sched.Reconcile(context.Background(), goalID); err != nil {
				slog.Error("scheduler: reconcile", "goal", goalID, "err", err)
			}
		}
	}

	// M5-T6: the minimal daydream memory-consolidation loop (ADR-0025 §6). It
	// is off by default (the interactive power profile disallows daydreaming)
	// and every pass is gated by config, power, the kill switch, and idle
	// state. Stopped on daemon shutdown.
	daydream := startDaydream(context.Background(), daydreamDeps{
		cfg:         cfg,
		jobs:        job.NewRepository(st),
		events:      st,
		artifacts:   artifactStore,
		compactions: mceRT.CompactStore,
		compactor:   mceRT.Compactor,
		power:       powerMgr,
		freezer:     killSwitch,
		projects:    projectRepo,
		indexer:     indexer,
		log:         slog.Default(),
	})
	defer daydream.Close()

	fmt.Printf("athanor %s listening on http://%s\n", version, loopAddr)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case sig := <-stop:
		fmt.Printf("received %s — shutting down\n", sig)
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	}

	if _, err := st.AppendEvent(context.Background(), store.Event{
		Category: "jobs",
		Data:     map[string]any{"event": "shutdown"},
	}); err != nil {
		fmt.Fprintf(os.Stderr, "athanor: recording shutdown event: %v\n", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutting down http server: %w", err)
	}
	return nil
}
