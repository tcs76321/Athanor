package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// freeBytes returns the free space on the filesystem holding path.
func freeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// dbBytes returns the total on-disk size of a state dir's SQLite files
// (database + WAL + shared memory).
func dbBytes(stateDir string) int64 {
	var total int64
	for _, suffix := range []string{"athanor.db", "athanor.db-wal", "athanor.db-shm"} {
		if fi, err := os.Stat(filepath.Join(stateDir, suffix)); err == nil {
			total += fi.Size()
		}
	}
	return total
}

// rssKB returns the resident set size (KiB) of a pid using `ps`.
func rssKB(pid int) (int64, error) {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
}

// soakSampler periodically records the daemon's RSS, the SQLite file
// sizes, and free disk while an arm runs. The samples append to
// <runDir>/soak.csv so a long run leaves evidence of whether memory or
// disk drifted (the informal M7-T9 preview).
type soakSampler struct {
	stop chan struct{}
	done chan struct{}
}

// startSoak samples every interval until stopAndWait is called.
func startSoak(runDir string, interval time.Duration, pid int, stateDir string) *soakSampler {
	s := &soakSampler{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		f, err := os.Create(filepath.Join(runDir, "soak.csv"))
		if err != nil {
			return
		}
		defer func() { _ = f.Close() }()
		w := csv.NewWriter(f)
		defer w.Flush()
		_ = w.Write([]string{"ts", "rss_kb", "db_bytes", "free_gb"})
		sample := func() {
			rss, _ := rssKB(pid)
			free, _ := freeBytes(stateDir)
			_ = w.Write([]string{
				time.Now().UTC().Format(time.RFC3339),
				strconv.FormatInt(rss, 10),
				strconv.FormatInt(dbBytes(stateDir), 10),
				fmt.Sprintf("%.1f", float64(free)/(1<<30)),
			})
			w.Flush()
		}
		sample()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				sample()
				return
			case <-t.C:
				sample()
			}
		}
	}()
	return s
}

func (s *soakSampler) stopAndWait() {
	if s == nil {
		return
	}
	close(s.stop)
	<-s.done
}

// orphanPods returns the names of any surviving athanor Job Pods. The
// M2-T5 teardown should always leave zero; a non-empty result after an
// arm is a containment regression worth surfacing.
func orphanPods() ([]string, error) {
	out, err := exec.Command("podman", "ps", "-a",
		"--filter", "name=athanor-job-", "--format", "{{.Names}}").Output()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}
