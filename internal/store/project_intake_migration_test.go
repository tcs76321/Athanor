package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/migrations"
)

// TestProjectIntakeMigrationBackfillsBrief proves the one-way move from the
// old brief to the project intent: constraints and limitations become the
// boundaries text, sources move to the project, and project_briefs disappears.
func TestProjectIntakeMigrationBackfillsBrief(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	if err := Migrate(s.DB(), migrationsExcept(t, "0045", "0046", "0047"), ""); err != nil {
		t.Fatalf("migrate to 44: %v", err)
	}
	if got := VersionOf(t, s.DB()); got != 44 {
		t.Fatalf("version = %d, want 44", got)
	}
	if _, err := s.DB().ExecContext(ctx,
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','demo','document','Write a daily brief.')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO project_briefs (project_id, status, goal, constraints_json, limitations, sources_json, success)
		VALUES ('p1','briefed','old goal','["no ads","no tracking"]','No outbound messages.','["https://example.com"]','old success')`); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatalf("full migrate: %v", err)
	}
	var boundaries, sources string
	if err := s.DB().QueryRowContext(ctx,
		`SELECT boundaries, sources_json FROM projects WHERE id = 'p1'`).Scan(&boundaries, &sources); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"no ads", "no tracking", "No outbound messages."} {
		if !strings.Contains(boundaries, want) {
			t.Errorf("boundaries %q missing %q", boundaries, want)
		}
	}
	if !strings.Contains(sources, "https://example.com") {
		t.Errorf("sources = %q, want the old source", sources)
	}
	var n int
	if err := s.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='project_briefs'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("project_briefs still exists after 0047")
	}
}
