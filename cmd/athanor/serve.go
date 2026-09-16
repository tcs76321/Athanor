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
	"github.com/tcs76321/athanor/internal/engine"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/internalapi"
	"github.com/tcs76321/athanor/internal/internalapi/runner"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/jobpod"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/logging"
	"github.com/tcs76321/athanor/internal/power"
	"github.com/tcs76321/athanor/internal/project"
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
	// kill -9. Engine wiring is M2-T3/M2-T4 territory; this is
	// the boot-time integration only.
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
	eng := engine.New(cfg, st,
		job.NewRepository(st),
		projectRepo,
		artifactStore,
		// M3-T1: EvaluationRecord repository (§19). The engine
		// persists one record per candidate artifact during
		// the evaluating phase; the comparison phase reads
		// them back.
		evaluation.NewRepo(st),
		llm.NewClient(cfg.Inference.OllamaURL, nil),
		registry,
		killSwitch,
		powerMgr,
		// M2-T4: the engine's window onto the internal API.
		// It authenticates each call with the per-job bearer
		// token retrieved from the Job Pod manager, so the
		// engine is just another client of the loopback
		// internal API (ADR-0009 D5).
		runner.New("http://"+loopAddr, podMgr),
	)
	srv := server.New(version)
	srv.SetControl(killSwitch)
	externalAPI := api.New(projectRepo, job.NewRepository(st),
		artifactStore,
		eng, killSwitch, st)
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
	internalapi.New(tokenStoreAdapter{podMgr}, project.NewRepo(st), st,
		project.NewRepo(st), defaultEnv, nil).Register(srv.Mux())
	// The M4-T7.4 commit replaces the nil gateway with the
	// cmd/athanor adapter over GatewayParts (ADR-0019 §2); until
	// then the fetch_url / search_web routes respond 503.

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
	gw, err := startGateway(stateDir, st, cfg.Network, slog.Default())
	if err != nil {
		return fmt.Errorf("starting gateway: %w", err)
	}
	// The gateway itself owns no goroutines; the
	// "no-op at boot" pattern is that the variable
	// is held so the wiring is exercised on every
	// daemon start. T7 will thread it into the
	// tool envelope; until then, a future
	// contributor who refactors serve.go and
	// drops the gateway construction trips a CI
	// boot test (added in T5.3). The Reader half
	// of the bundle is constructed (and its
	// injection heuristic validated) here so a
	// reader-mode misconfiguration fails at boot,
	// not at first fetch.
	_ = gw

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
	// M2-T5 known limitation: a synthesizing job whose pod was running
	// when the daemon died will fail to resume here, because the new
	// daemon's jobpod.Manager is empty and TokenFor returns ErrNotFound.
	// M3 wires the engine to call podMgr.Start for recovered jobs.
	eng.Recover(context.Background())

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
