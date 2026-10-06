package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/tcs76321/athanor/internal/doctor"
)

// runStart is the M7-T7 onboarding path: run the §30.2 doctor, refuse on a
// hard prerequisite failure, then boot the daemon. It is what `install.sh`
// points a fresh machine at.
func runStart(args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	configPath := fs.String("config", "config.yaml", "path to config.yaml")
	stateDir := fs.String("state-dir", "state", "state directory")
	addr := fs.String("addr", "127.0.0.1:7420", "HTTP listen address")
	skipDoctor := fs.Bool("skip-doctor", false, "skip the pre-flight checks and serve immediately")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*skipDoctor {
		cfg, err := loadConfig(*configPath)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		rep := doctor.Run(context.Background(), cfg, doctor.Options{StateDir: *stateDir}, newOSProbes())
		printReport(rep)
		if !rep.OK() {
			_, _, fail := rep.Counts()
			return fmt.Errorf("start: doctor found %d failing check(s); fix them or re-run with -skip-doctor", fail)
		}
	}
	return run(*configPath, *addr, *stateDir)
}
