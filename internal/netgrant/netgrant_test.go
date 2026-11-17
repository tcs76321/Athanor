package netgrant

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func fixture(t *testing.T) (*Repo, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	p, _, err := project.NewRepo(st).CreateInferred(context.Background(), "p", "research a topic", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return NewRepo(st), p.ID
}

func TestCreateAndListActive(t *testing.T) {
	repo, projectID := fixture(t)
	ctx := context.Background()
	expires := time.Now().Add(24 * time.Hour)
	g, err := repo.Create(ctx, CreateInput{
		ProjectID: projectID, Scheme: "HTTPS", Host: "Example.COM",
		PathPrefix: "v1/../v1/", Reason: "research", ExpiresAt: &expires,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if g.Scheme != "https" || g.Host != "example.com" || g.PathPrefix != "/v1" {
		t.Errorf("normalized grant = %+v", g)
	}
	active, err := repo.ActiveForProject(ctx, projectID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != g.ID {
		t.Fatalf("active = %+v", active)
	}
	all, err := repo.ListByProject(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("list = %+v", all)
	}
}

func TestExpiredGrantIsNotActive(t *testing.T) {
	repo, projectID := fixture(t)
	past := time.Now().Add(-time.Hour)
	if _, err := repo.Create(context.Background(), CreateInput{
		ProjectID: projectID, Host: "expired.test", ExpiresAt: &past,
	}); err != nil {
		t.Fatal(err)
	}
	active, err := repo.ActiveForProject(context.Background(), projectID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("expired grant active: %+v", active)
	}
}

func TestRevoke(t *testing.T) {
	repo, projectID := fixture(t)
	ctx := context.Background()
	g, err := repo.Create(ctx, CreateInput{ProjectID: projectID, Host: "revoke.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Revoke(ctx, g.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	active, _ := repo.ActiveForProject(ctx, projectID, time.Now())
	if len(active) != 0 {
		t.Fatalf("revoked grant active: %+v", active)
	}
	if err := repo.Revoke(ctx, g.ID); err != nil {
		t.Fatalf("idempotent Revoke: %v", err)
	}
	if err := repo.Revoke(ctx, "missing"); err == nil {
		t.Fatal("revoking a missing grant did not error")
	}
}

func TestCreateValidation(t *testing.T) {
	repo, projectID := fixture(t)
	ctx := context.Background()
	for name, in := range map[string]CreateInput{
		"no project": {Host: "example.com"},
		"no host":    {ProjectID: projectID},
		"bad scheme": {ProjectID: projectID, Scheme: "ftp", Host: "example.com"},
		"idn":        {ProjectID: projectID, Host: "例え.jp"},
	} {
		if _, err := repo.Create(ctx, in); err == nil {
			t.Errorf("%s: Create accepted %+v", name, in)
		}
	}
}
