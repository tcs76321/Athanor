package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/doctor"
)

// runStart is the M7-T7 onboarding path. The default is the Core Pod
// (ADR-0078); -host runs the host binary as a development escape hatch.
func runStart(args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	configPath := fs.String("config", "config.yaml", "path to config.yaml")
	stateDir := fs.String("state-dir", "state", "state directory (host mode / doctor)")
	addr := fs.String("addr", defaultCoreAddr, "HTTP listen address")
	container := fs.Bool("container", false, "host mode: allow a wildcard bind behind a loopback-only published port")
	skipDoctor := fs.Bool("skip-doctor", false, "skip the pre-flight checks")
	skipModelAudit := fs.Bool("skip-model-audit", false, "do not enforce the measured model-fit audit")
	host := fs.Bool("host", false, "run the host daemon (development escape hatch, not the supported runtime)")
	coreImage := fs.String("core-image", defaultCoreImage, "Core Pod image")
	containerName := fs.String("container-name", defaultCoreName, "Core Pod container name")
	stateVol := fs.String("state-volume", defaultStateVol, "Core Pod state volume")
	workVol := fs.String("workspace-volume", defaultWorkVol, "Core Pod workspace volume")
	reposDir := fs.String("repos-dir", defaultReposDir(), "Core Pod: host directory mounted at the same path for repositories")
	scratchHost := fs.String("scratch-host-dir", "", "Core Pod: host-visible scratch directory (default: job_pod.scratch_host_dir)")
	socket := fs.String("socket", "", "Podman socket path (default: resolve from the machine)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if !*skipDoctor {
		doctorCfg := cfg
		if !*host {
			// The Core Pod reaches the host Ollama through
			// host.containers.internal; the host-side doctor probes
			// the loopback endpoint instead.
			doctorCfg = hostDoctorConfig(cfg)
		}
		rep := doctor.Run(context.Background(), doctorCfg, doctor.Options{StateDir: *stateDir}, newOSProbes())
		printReport(rep)
		if !rep.OK() {
			_, _, fail := rep.Counts()
			return fmt.Errorf("start: doctor found %d failing check(s); fix them or re-run with -skip-doctor", fail)
		}
	}
	modelAuditJSON := ""
	if !*skipModelAudit {
		hostMem, _ := newOSProbes().TotalMemoryBytes(context.Background())
		audit, err := checkModelAudit(modelAuditPath(*configPath), cfg, int64(hostMem))
		if err != nil {
			return err
		}
		if audit.GeneratedAt != "" {
			if raw, jerr := json.Marshal(audit); jerr == nil {
				modelAuditJSON = string(raw)
			}
		}
	}
	if *host {
		return run(*configPath, *addr, *stateDir, *container)
	}

	client := NewExecClient()
	sock, err := coreSocketPath(context.Background(), client, *socket)
	if err != nil {
		return err
	}
	if !allowlistContains(cfg.Network.ExternalAPIHostAllowlist, *addr) {
		return fmt.Errorf("network.external_api_host_allowlist does not include %s; add it to the config or use the default 127.0.0.1:7420 (the Core would start but reject every request)", *addr)
	}
	scratch := *scratchHost
	if scratch == "" {
		scratch = cfg.JobPod.ScratchHostDir
	}
	opts, err := coreOptionsFromConfig(cfg.JobPod.TokenHostDir, scratch, *reposDir, *configPath, *addr,
		*coreImage, *containerName, *stateVol, *workVol, sock)
	if err != nil {
		return err
	}
	// The Core cannot see the macOS host's memory through the Podman VM. Pass
	// the physical host capacity so model-fit budgets use the right number;
	// Job Pod limits still use the runtime's own memory.
	if mem, ok := newOSProbes().TotalMemoryBytes(context.Background()); ok {
		opts.HostMemoryBytes = mem
	}
	opts.ModelAudit = modelAuditJSON
	// The Core Pod cannot see the host GPU/NPU. Probe once on the host and
	// hand the result to the Core for resource planning and the UI.
	gpu, _ := newOSProbes().GPU(context.Background())
	if gpu.Name != "" || gpu.NPU != "" {
		if raw, jerr := json.Marshal(gpu); jerr == nil {
			opts.AcceleratorJSON = string(raw)
		}
	}
	if err := coreUp(context.Background(), opts); err != nil {
		return err
	}
	fmt.Printf("Core Pod %s is healthy at http://%s\n", opts.Name, opts.Addr)
	return nil
}

// hostDoctorConfig returns a copy of cfg whose Ollama URL is the host
// loopback, so the host-side doctor can probe the same server the Core Pod
// reaches through host.containers.internal.
func hostDoctorConfig(cfg *config.Config) *config.Config {
	if !strings.Contains(cfg.Inference.OllamaURL, "host.containers.internal") {
		return cfg
	}
	out := *cfg
	out.Inference.OllamaURL = strings.Replace(cfg.Inference.OllamaURL, "host.containers.internal", "127.0.0.1", 1)
	return &out
}

func defaultReposDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "athanor", "repos")
}

// allowlistContains reports whether addr is in the external-API Host
// allowlist. An empty list disables the check (the documented test escape
// hatch), so it accepts any address.
func allowlistContains(list []string, addr string) bool {
	if len(list) == 0 {
		return true
	}
	for _, entry := range list {
		if entry == addr {
			return true
		}
	}
	return false
}
