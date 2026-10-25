package main

import (
	"strings"
	"testing"
)

func TestContainerBackupArgs(t *testing.T) {
	got := strings.Join(containerBackupExecArgs("athanor-core", 5), " ")
	for _, want := range []string{
		"exec athanor-core athanor backup",
		"-state-dir /var/lib/athanor/state",
		"-keep 5",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("exec argv missing %q in %q", want, got)
		}
	}

	run := strings.Join(containerBackupRunArgs("athanor-state", "athanor-core:latest", 3), " ")
	for _, want := range []string{
		"run --rm",
		"-v athanor-state:/state",
		"athanor-core:latest backup -state-dir /state -keep 3",
	} {
		if !strings.Contains(run, want) {
			t.Errorf("one-off argv missing %q in %q", want, run)
		}
	}
}

func TestContainerRestoreArgs(t *testing.T) {
	got := strings.Join(containerRestoreRunArgs("athanor-state", "athanor-core:latest", "/home/u/backups", "snap.db"), " ")
	for _, want := range []string{
		"run --rm",
		"-v athanor-state:/state",
		"-v /home/u/backups:/in:ro",
		"restore -from /in/snap.db -state-dir /state -force",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("restore argv missing %q in %q", want, got)
		}
	}
}
