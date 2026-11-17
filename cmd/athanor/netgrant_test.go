package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/netgrant"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func TestNetworkRequesterAndGrantApprover(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	p, _, err := project.NewRepo(st).CreateInferred(context.Background(), "p", "research", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	hitlRepo := hitl.NewRepo(st)
	req := networkRequester{repo: hitlRepo}

	ctx := context.Background()
	if err := req.RequestNetworkAccess(ctx, p.ID, "", "https://docs.test/v1/a", "denied"); err != nil {
		t.Fatalf("request: %v", err)
	}
	// A second request for the same project+host is deduped.
	if err := req.RequestNetworkAccess(ctx, p.ID, "", "https://docs.test/v1/b", "denied"); err != nil {
		t.Fatal(err)
	}
	pending, err := hitlRepo.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1 (deduped)", len(pending))
	}
	if pending[0].Type != hitl.TypeNetworkAccess {
		t.Errorf("type = %q", pending[0].Type)
	}

	grants := netgrant.NewRepo(st)
	approver := networkGrantApprover{grants: grants, ttl: 30 * 24 * time.Hour}
	if err := approver.Approve(ctx, pending[0]); err != nil {
		t.Fatalf("approve: %v", err)
	}
	active, err := grants.ActiveForProject(ctx, p.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].Host != "docs.test" || active[0].PathPrefix != "/v1/a" {
		t.Fatalf("grant = %+v", active)
	}
}
