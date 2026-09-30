// M4-T4: wire the §21.3 egress pipeline into the daemon
// boot sequence. The exporter polls the event log for
// `event=status, to=accepted` rows and exports each
// accepted artifact to <state>/workspace/exports/.
// The engine is unchanged: the exporter is an observer,
// not a driver (ADR-0015 §7 "Egress is a subscriber,
// not an engine hook").
//
// The exporter uses the same per-pipeline scanner
// registry as the ingress pipeline. The egress list
// (size + zipbomb + clamav + yara) excludes
// prompt-injection scanners (ADR-0015 §"Trust
// boundaries, not all text"). M4-T4 adds the export
// glue; the scanner list was wired in M4-T3.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/tcs76321/athanor/internal/airlock/egress"
	"github.com/tcs76321/athanor/internal/airlock/scanner"
	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

// startEgress constructs and starts the egress Exporter.
// The exporter's poll loop is bound to the daemon's
// lifetime via the run() context; the caller's defer
// stops the loop on shutdown. Returns the running
// Exporter (so callers can call ExportOne directly for
// the manual `athanor export` CLI subcommand) and the
// constructed registry (so tests can inspect it).
func startEgress(
	ctx context.Context,
	stateDir string,
	store_ *store.Store,
	artifactStore *artifact.Store,
	projectRepo *project.Repo,
	registry *scanner.Registry,
	logger *slog.Logger,
) (*egress.Exporter, error) {
	workspace := filepath.Join(stateDir, "workspace")
	exp, err := egress.New(egress.Options{
		WorkspaceRoot: workspace,
		Registry:      registry,
		ArtifactStore: artifactStore,
		ProjectRepo:   projectRepo,
		Store:         store_,
		PollInterval:  5 * time.Second,
		Logger:        logger,
	})
	if err != nil {
		return nil, fmt.Errorf("starting egress exporter: %w", err)
	}
	go exp.Start(ctx)
	logger.Info("airlock egress started", "workspace", workspace)
	return exp, nil
}
