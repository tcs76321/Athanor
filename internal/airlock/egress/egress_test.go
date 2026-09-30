// M4-T4 egress tests. The two structural ACs are:
//
//  1. Egress of a poisoned tree fails closed (clamav/yara
//     would catch a real malware payload; the test uses
//     the size scanner with a tiny limit to simulate
//     the poisoned-tree path without external binaries).
//  2. Clean export works (a small accepted artifact
//     lands in <workspace>/exports/<projectID>/<artifact>-<sha12>/
//     and the audit row is "egress_complete").
//
// Idempotency (re-running ExportOne on the same
// artifact is a no-op) is the third AC; the test asserts
// the directory contains exactly one file after two
// ExportOne calls.
package egress

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/airlock/scanner"
	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

const (
	// Tiny size cap so the test can simulate a
	// poisoned tree without external binaries.
	// 16 bytes is enough for the "valid artifact"
	// body but rejects anything larger.
	testMaxBytes = 16
)

// helper wires a complete exporter with a clean-only
// registry, a real artifact store, a real project, and
// a real event log.
type helper struct {
	t         *testing.T
	store     *store.Store
	artStore  *artifact.Store
	projRepo  *project.Repo
	registry  *scanner.Registry
	exporter  *Exporter
	workspace string
}

func newHelper(t *testing.T) *helper {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	workspace := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	artifactDir := filepath.Join(dir, "artifacts")
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		t.Fatal(err)
	}
	artStore := artifact.NewStore(st, artifactDir)
	projRepo := project.NewRepo(st)
	registry, err := scanner.NewRegistry(
		map[string]scanner.Scanner{
			"size":    scanner.NewSize(testMaxBytes),
			"zipbomb": scanner.NewZipBomb(100, 10000, 50),
		},
		[]string{"size", "zipbomb"},
		[]string{"size", "zipbomb"},
		[]string{"size"},
	)
	if err != nil {
		t.Fatal(err)
	}
	exporter, err := New(Options{
		WorkspaceRoot: workspace,
		Registry:      registry,
		ArtifactStore: artStore,
		ProjectRepo:   projRepo,
		Store:         st,
		PollInterval:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &helper{
		t:         t,
		store:     st,
		artStore:  artStore,
		projRepo:  projRepo,
		registry:  registry,
		exporter:  exporter,
		workspace: workspace,
	}
}

func (h *helper) seedProject(name string) (project.Project, project.Task) {
	h.t.Helper()
	p, t, err := h.projRepo.Create(context.Background(), name, project.ArchetypeText,
		"a goal that satisfies the minimum length requirement", nil)
	if err != nil {
		h.t.Fatal(err)
	}
	return p, t
}

func (h *helper) seedJob(projectID, taskID string) string {
	h.t.Helper()
	jobRepo := job.NewRepository(h.store)
	j, err := jobRepo.Create(context.Background(), taskID, projectID)
	if err != nil {
		h.t.Fatal(err)
	}
	return j.ID
}

func (h *helper) seedAcceptedArtifact(projectID, jobID string, kind artifact.Kind, content []byte) artifact.Artifact {
	h.t.Helper()
	art, err := h.artStore.CreateDraftFor(context.Background(), projectID, "", jobID, kind, content)
	if err != nil {
		h.t.Fatal(err)
	}
	// §9.3 status flow: draft → candidate → accepted.
	if err := h.artStore.SetStatus(context.Background(), art.ID, artifact.StatusCandidate); err != nil {
		h.t.Fatal(err)
	}
	if err := h.artStore.SetStatus(context.Background(), art.ID, artifact.StatusAccepted); err != nil {
		h.t.Fatal(err)
	}
	return art
}

// TestExportOne_CleanArtifactLandsInExports is the
// happy-path AC: a small accepted artifact exports
// to <workspace>/exports/<projectID>/<artifact>-<sha12>/.
func TestExportOne_CleanArtifactLandsInExports(t *testing.T) {
	h := newHelper(t)
	p, t1 := h.seedProject("clean")
	jobID := h.seedJob(p.ID, t1.ID)
	content := []byte("small artifact")
	art := h.seedAcceptedArtifact(p.ID, jobID, artifact.KindDocument, content)
	if _, _, err := h.exporter.ExportOne(context.Background(), art.ID); err != nil {
		t.Fatalf("ExportOne: %v", err)
	}
	dst := ExportPath(h.workspace, p.ID, art.ID, art.ContentHash)
	matches, err := filepath.Glob(filepath.Join(dst, "*"))
	if err != nil {
		t.Fatal(err)
	}
	nonTmp := 0
	for _, m := range matches {
		base := filepath.Base(m)
		if strings.HasPrefix(base, ".ath-") {
			continue
		}
		nonTmp++
		got, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(content) {
			t.Errorf("exported content = %q, want %q", got, content)
		}
	}
	if nonTmp != 1 {
		t.Errorf("exported file count = %d, want 1", nonTmp)
	}
}

// TestExportOne_Idempotent re-running ExportOne on the
// same artifact must not produce a second file.
func TestExportOne_Idempotent(t *testing.T) {
	h := newHelper(t)
	p, t1 := h.seedProject("idempotent")
	jobID := h.seedJob(p.ID, t1.ID)
	content := []byte("idem content") // 12 bytes, under 16-byte cap
	art := h.seedAcceptedArtifact(p.ID, jobID, artifact.KindDocument, content)
	for i := 0; i < 3; i++ {
		if _, _, err := h.exporter.ExportOne(context.Background(), art.ID); err != nil {
			t.Fatalf("ExportOne #%d: %v", i, err)
		}
	}
	dst := ExportPath(h.workspace, p.ID, art.ID, art.ContentHash)
	matches, _ := filepath.Glob(filepath.Join(dst, "*"))
	nonTmp := 0
	for _, m := range matches {
		base := filepath.Base(m)
		if strings.HasPrefix(base, ".ath-") {
			continue
		}
		nonTmp++
	}
	if nonTmp != 1 {
		t.Errorf("after 3 exports, file count = %d, want 1 (idempotent)", nonTmp)
	}
}

// TestExportOne_PoisonedTreeBlocked: an artifact whose
// content exceeds the size scanner's limit is blocked.
// No file lands in exports/; the audit row carries the
// rejection reason.
func TestExportOne_PoisonedTreeBlocked(t *testing.T) {
	h := newHelper(t)
	p, t1 := h.seedProject("poisoned")
	jobID := h.seedJob(p.ID, t1.ID)
	content := make([]byte, 1024)
	for i := range content {
		content[i] = 'A'
	}
	art := h.seedAcceptedArtifact(p.ID, jobID, artifact.KindDocument, content)
	if _, _, err := h.exporter.ExportOne(context.Background(), art.ID); err != nil {
		t.Fatalf("ExportOne: %v (a blocked export should not error)", err)
	}
	dst := ExportPath(h.workspace, p.ID, art.ID, art.ContentHash)
	matches, _ := filepath.Glob(filepath.Join(dst, "*"))
	nonTmp := 0
	for _, m := range matches {
		base := filepath.Base(m)
		if strings.HasPrefix(base, ".ath-") {
			continue
		}
		nonTmp++
	}
	if nonTmp != 0 {
		t.Errorf("blocked export wrote %d files; want 0", nonTmp)
	}
	rows, err := h.store.QueryEvents(context.Background(), store.EventFilter{
		Category: "airlock",
	})
	if err != nil {
		t.Fatal(err)
	}
	blocked := 0
	for _, r := range rows {
		if !strings.Contains(r.DataJSON, `"egress_blocked"`) {
			continue
		}
		if !strings.Contains(r.DataJSON, art.ID) {
			continue
		}
		if !strings.Contains(r.DataJSON, "scanner:size:exceeds_max") {
			continue
		}
		blocked++
	}
	if blocked == 0 {
		t.Errorf("no egress_blocked audit row found for poisoned artifact %s", art.ID)
	}
}

// TestExportOne_NotAcceptedArtifact: a draft (not
// accepted) artifact is a no-op; no file is written;
// the audit row carries "not_accepted".
func TestExportOne_NotAcceptedArtifact(t *testing.T) {
	h := newHelper(t)
	p, t1 := h.seedProject("notaccepted")
	jobID := h.seedJob(p.ID, t1.ID)
	content := []byte("not yet accepted")
	art, err := h.artStore.CreateDraftFor(context.Background(), p.ID, "", jobID, artifact.KindDocument, content)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.exporter.ExportOne(context.Background(), art.ID); err != nil {
		t.Fatalf("ExportOne: %v", err)
	}
	dst := ExportPath(h.workspace, p.ID, art.ID, art.ContentHash)
	if _, err := os.Stat(dst); err == nil {
		t.Errorf("export directory was created for a non-accepted artifact: %s", dst)
	}
	rows, _ := h.store.QueryEvents(context.Background(), store.EventFilter{Category: "airlock"})
	found := false
	for _, r := range rows {
		if strings.Contains(r.DataJSON, `"egress_unchanged"`) &&
			strings.Contains(r.DataJSON, art.ID) &&
			strings.Contains(r.DataJSON, "not_accepted") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no egress_unchanged (not_accepted) audit row found for draft artifact %s", art.ID)
	}
}

// TestExtractAcceptedArtifactID covers the event-shape
// matcher directly. The function is the trigger that
// decides which event rows the exporter processes.
func TestExtractAcceptedArtifactID(t *testing.T) {
	cases := []struct {
		name     string
		dataJSON string
		jobID    string
		want     string
		wantOK   bool
	}{
		{
			name:     "status to accepted",
			dataJSON: `{"event":"status","from":"candidate","to":"accepted"}`,
			jobID:    "job-1",
			want:     "job-1",
			wantOK:   true,
		},
		{
			name:     "status to rejected",
			dataJSON: `{"event":"status","from":"candidate","to":"rejected"}`,
			jobID:    "job-1",
			want:     "",
			wantOK:   false,
		},
		{
			name:     "different event type",
			dataJSON: `{"event":"startup"}`,
			jobID:    "job-1",
			want:     "",
			wantOK:   false,
		},
		{
			name:     "no job id",
			dataJSON: `{"event":"status","to":"accepted"}`,
			jobID:    "",
			want:     "",
			wantOK:   false,
		},
		{
			name:     "malformed json",
			dataJSON: `{not json`,
			jobID:    "job-1",
			want:     "",
			wantOK:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := extractAcceptedArtifactID(c.dataJSON, c.jobID)
			if ok != c.wantOK {
				t.Errorf("ok = %v, want %v", ok, c.wantOK)
			}
			if got != c.want {
				t.Errorf("got = %q, want %q", got, c.want)
			}
		})
	}
}

// TestExportPath pins the directory layout.
func TestExportPath(t *testing.T) {
	got := ExportPath("/state/workspace", "proj-id", "art-id", "abcdef1234567890...")
	want := filepath.Join("/state/workspace", "exports", "proj-id", "art-id-abcdef123456")
	if got != want {
		t.Errorf("ExportPath = %q, want %q", got, want)
	}
	base := filepath.Base(got)
	idx := strings.LastIndex(base, "-")
	if idx < 0 {
		t.Fatalf("base %q has no '-'", base)
	}
	suffix := base[idx+1:]
	if len(suffix) != 12 {
		t.Errorf("hash suffix length = %d, want 12", len(suffix))
	}
	if _, err := hex.DecodeString(suffix); err != nil {
		t.Errorf("hash suffix %q is not hex: %v", suffix, err)
	}
}
