package finetune

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func TestExportAcceptedWorkAsJSONL(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	projects := project.NewRepo(st)
	p, task, err := projects.Create(ctx, "demo", project.ArchetypeCode,
		"Build a tested parser", "", []string{"tests pass", "handles empty input"})
	if err != nil {
		t.Fatal(err)
	}
	arts := artifact.NewStore(st, filepath.Join(dir, "artifacts"))
	a, err := arts.CreateDraftFor(ctx, p.ID, task.ID, "", artifact.KindCode, []byte("def parse(s):\n    return s\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := arts.SetStatus(ctx, a.ID, artifact.StatusCandidate); err != nil {
		t.Fatal(err)
	}
	if err := arts.SetStatus(ctx, a.ID, artifact.StatusAccepted); err != nil {
		t.Fatal(err)
	}
	// A rejected proposal must not enter the set.
	rejected, err := arts.CreateDraftFor(ctx, p.ID, task.ID, "", artifact.KindProposal, []byte("scratch"))
	if err != nil {
		t.Fatal(err)
	}
	_ = rejected

	var buf bytes.Buffer
	n, err := Export(ctx, st, arts, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("exported %d examples, want 1\n%s", n, buf.String())
	}
	line := strings.TrimSpace(buf.String())
	var ex Example
	if err := json.Unmarshal([]byte(line), &ex); err != nil {
		t.Fatal(err)
	}
	if len(ex.Messages) != 3 || ex.Messages[2].Content == "" {
		t.Fatalf("example = %+v", ex)
	}
	if !strings.Contains(ex.Messages[1].Content, "tests pass") {
		t.Errorf("acceptance criteria missing from prompt: %q", ex.Messages[1].Content)
	}
	if ex.Metadata["project_id"] != p.ID {
		t.Errorf("provenance = %+v", ex.Metadata)
	}
}
