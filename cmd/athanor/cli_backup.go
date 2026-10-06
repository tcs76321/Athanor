package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tcs76321/athanor/internal/backup"
	"github.com/tcs76321/athanor/internal/store"
)

// runBackup forces one §23.4 snapshot + retention prune. It operates directly
// on the state directory (the daemon should be stopped: it holds the database
// open).
func runBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	stateDir := fs.String("state-dir", "state", "state directory")
	keep := fs.Int("keep", 10, "retain the newest N snapshots (0 = keep all)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := os.MkdirAll(*stateDir, 0o700); err != nil {
		return err
	}
	dbPath := filepath.Join(*stateDir, "athanor.db")
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	backupDir := filepath.Join(*stateDir, "backups")
	path, err := backup.Snapshot(st.DB(), backupDir, st.Version())
	if err != nil {
		return err
	}
	removed, err := backup.Prune(backupDir, *keep)
	if err != nil {
		return err
	}
	fmt.Printf("backup written: %s\n", path)
	if len(removed) > 0 {
		fmt.Printf("pruned %d old snapshot(s)\n", len(removed))
	}
	return nil
}

// runRestore replaces the state database with a snapshot. It requires -force
// (an explicit acknowledgment) and a stopped daemon.
func runRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	from := fs.String("from", "", "snapshot path to restore (required)")
	stateDir := fs.String("state-dir", "state", "state directory")
	force := fs.Bool("force", false, "acknowledge overwriting the current database")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" {
		return fmt.Errorf("restore: -from <snapshot> is required")
	}
	if !*force {
		return fmt.Errorf("restore: refusing to overwrite the database without -force (stop the daemon first)")
	}
	dbPath := filepath.Join(*stateDir, "athanor.db")
	if err := backup.Restore(*from, dbPath); err != nil {
		return err
	}
	fmt.Printf("restored %s from %s\n", dbPath, *from)
	return nil
}
