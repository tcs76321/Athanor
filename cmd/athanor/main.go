// Command athanor is the daemon entry point and CLI (M1-T7).
//
// Boot sequence (daemon): load config (falling back to built-in defaults
// when the file is absent) → init logging → open store → run migrations →
// load kill switch → wire engine → serve on loopback → recover active
// jobs → graceful shutdown on SIGINT/SIGTERM.
//
// Client subcommands talk to a running daemon over the loopback HTTP API:
//
//	athanor                        run the daemon (same as `serve`)
//	athanor init                   write a config.yaml with every default
//	athanor project create         create a project with its first goal
//	athanor goal submit            add a goal and start its job
//	athanor job watch              stream a job's progress to completion
//	athanor artifacts              list a project's artifacts
//	athanor freeze / unfreeze      drive the kill switch (§22)
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/config"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "0.0.0-dev"

const shutdownTimeout = 10 * time.Second

// loadConfig resolves the daemon configuration. A missing config file is
// not an error on a fresh clone: the daemon falls back to the built-in
// defaults (M1-T7 acceptance: fresh clone → running daemon, no manual
// steps). A file that exists but is malformed or invalid still fails
// loudly — only explicit absence falls back.
func loadConfig(path string) (*config.Config, error) {
	cfg, err := config.Load(path)
	if errors.Is(err, config.ErrFileNotFound) {
		def, derr := config.Default()
		if derr != nil {
			return nil, derr
		}
		fmt.Printf("athanor: no config at %s — using built-in defaults (see config.example.yaml)\n", path)
		return def, nil
	}
	return cfg, err
}

func main() {
	args := os.Args[1:]
	// No args, explicit "serve", or leading daemon flags (e.g.
	// `athanor -addr :9000`, the `make run` form) all run the daemon.
	if len(args) == 0 || args[0] == "serve" || strings.HasPrefix(args[0], "-") {
		runServe(serveFlags(args))
		return
	}

	var err error
	switch args[0] {
	case "init":
		err = runInit(args[1:])
	case "doctor":
		err = runDoctor(args[1:])
	case "alarms":
		err = runAlarms(args[1:])
	case "backup":
		err = runBackup(args[1:])
	case "restore":
		err = runRestore(args[1:])
	case "project":
		err = runProject(args[1:])
	case "goal":
		err = runGoal(args[1:])
	case "job":
		err = runJob(args[1:])
	case "artifacts":
		err = runArtifacts(args[1:])
	case "export":
		err = runExport(args[1:])
	case "index":
		err = runIndex(args[1:])
	case "hitl":
		err = runHITL(args[1:])
	case "push":
		err = runPush(args[1:])
	case "corrections":
		err = runCorrections(args[1:])
	case "reject":
		err = runReject(args[1:])
	case "strategy":
		err = runStrategy(args[1:])
	case "freeze":
		err = runFreeze(args[1:])
	case "unfreeze":
		err = runUnfreeze(args[1:])
	case "-h", "--help", "help":
		usage()
	case "-v", "--version", "version":
		fmt.Println("athanor", version)
	default:
		fmt.Fprintf(os.Stderr, "athanor: unknown command %q\n\n", args[0])
		usage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "athanor:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`usage: athanor [command]

commands:
  (default) serve    run the daemon: -config, -addr, -state-dir, -version
  init               write ./config.yaml containing every default value
  doctor             check the host, backend, and config (§30.2)
  alarms             list active alarms [-resolve <id>] (§22.3)
  backup             write a database snapshot [-state-dir state] [-keep N]
  restore            restore a snapshot -from <file> -force (daemon stopped)
  project create     -name -archetype -goal [-criteria] [-addr]
  goal submit        -project -goal [-criteria] [-addr]
  job watch          -job [-timeout] [-addr]   (streams progress, prints artifact)
  artifacts          -project [-addr]          (lists a project's artifacts)
  index              -project [-path] [-addr]  (index a project's repository)
  freeze             [-addr]                   (§22 kill switch)
  unfreeze           -reason "..." [-addr]     (requires a reason, logged)
  version            print version
`)
}
