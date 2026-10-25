package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tcs76321/athanor/internal/backup"
	"github.com/tcs76321/athanor/internal/store"
)

// corePodStateDir is the Core Pod's state mount.
const corePodStateDir = "/var/lib/athanor/state"

// containerBackupExecArgs runs the backup inside a live Core Pod.
func containerBackupExecArgs(name string, keep int) []string {
	return []string{"exec", name, "athanor", "backup", "-state-dir", corePodStateDir, "-keep", strconv.Itoa(keep)}
}

// containerBackupRunArgs runs the backup in a one-off container when the Core
// Pod is stopped (the state volume is mounted directly).
func containerBackupRunArgs(volume, image string, keep int) []string {
	return []string{
		"run", "--rm",
		"-v", volume + ":/state",
		image, "backup", "-state-dir", "/state", "-keep", strconv.Itoa(keep),
	}
}

// containerRestoreRunArgs restores a host snapshot into the state volume via a
// one-off container. The snapshot's directory is mounted read-only at /in.
func containerRestoreRunArgs(volume, image, hostDir, base string) []string {
	return []string{
		"run", "--rm",
		"-v", volume + ":/state",
		"-v", hostDir + ":/in:ro",
		image, "restore", "-from", "/in/" + base, "-state-dir", "/state", "-force",
	}
}

// runBackup forces one §23.4 snapshot + retention prune. With -container it
// runs inside the Core Pod (or in a one-off container over the state volume
// when the Core is stopped); without it, it operates on a host state directory
// (the host development path).
func runBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	stateDir := fs.String("state-dir", "state", "state directory (host mode)")
	keep := fs.Int("keep", 10, "retain the newest N snapshots (0 = keep all)")
	container := fs.String("container", "", "Core Pod container name (e.g. athanor-core); omit for host mode")
	stateVolume := fs.String("state-volume", "athanor-state", "Core Pod state volume (container mode)")
	image := fs.String("image", "athanor-core:latest", "Core image for a stopped-container backup (container mode)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *container != "" {
		return runContainerBackup(context.Background(), *container, *stateVolume, *image, *keep)
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

func runContainerBackup(ctx context.Context, name, volume, image string, keep int) error {
	client := NewExecClient()
	out, stderr, err := client.Run(ctx, containerBackupExecArgs(name, keep)...)
	if err == nil {
		fmt.Print(string(out))
		fmt.Printf("snapshot is inside the %s volume; copy it out with:\n  podman cp %s:%s/backups/<snapshot>.db .\n", volume, name, corePodStateDir)
		return nil
	}
	// The Core is stopped (or absent): run a one-off container against the
	// named volume. A missing image is reported by podman.
	out, stderr2, runErr := client.Run(ctx, containerBackupRunArgs(volume, image, keep)...)
	if runErr != nil {
		return fmt.Errorf("container backup failed (exec: %v; one-off run: %v): %s", err, runErr, strings.TrimSpace(string(stderr2)))
	}
	fmt.Print(string(out))
	_ = stderr
	return nil
}

// runRestore replaces the state database with a snapshot. It requires -force
// (an explicit acknowledgment). With -container it restores through a one-off
// container over the named state volume; the Core container must be stopped.
func runRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	from := fs.String("from", "", "snapshot path to restore (required)")
	stateDir := fs.String("state-dir", "state", "state directory (host mode)")
	force := fs.Bool("force", false, "acknowledge overwriting the current database")
	container := fs.String("container", "", "Core Pod container name (e.g. athanor-core); omit for host mode")
	stateVolume := fs.String("state-volume", "athanor-state", "Core Pod state volume (container mode)")
	image := fs.String("image", "athanor-core:latest", "Core image for the one-off restore (container mode)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" {
		return fmt.Errorf("restore: -from <snapshot> is required")
	}
	if !*force {
		return fmt.Errorf("restore: refusing to overwrite the database without -force (stop the daemon first)")
	}
	if *container != "" {
		return runContainerRestore(context.Background(), *container, *stateVolume, *image, *from)
	}
	dbPath := filepath.Join(*stateDir, "athanor.db")
	if err := backup.Restore(*from, dbPath); err != nil {
		return err
	}
	fmt.Printf("restored %s from %s\n", dbPath, *from)
	return nil
}

func runContainerRestore(ctx context.Context, name, volume, image, from string) error {
	abs, err := filepath.Abs(from)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("restore: reading snapshot: %w", err)
	}
	client := NewExecClient()
	if out, _, err := client.Run(ctx, "inspect", "--format", "{{.State.Running}}", name); err == nil &&
		strings.TrimSpace(string(out)) == "true" {
		return fmt.Errorf("restore: Core container %s is running; stop it first (podman stop %s)", name, name)
	}
	dir, base := filepath.Split(abs)
	dir = filepath.Clean(dir)
	if _, stderr, err := client.Run(ctx, containerRestoreRunArgs(volume, image, dir, base)...); err != nil {
		return fmt.Errorf("container restore failed: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	fmt.Printf("restored %s into the %s volume via %s\n", base, volume, image)
	return nil
}
