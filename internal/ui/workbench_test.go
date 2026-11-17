package ui

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/coverage"
	"github.com/tcs76321/athanor/internal/netgrant"
)

func TestWorkbenchShowsGrantsCoverageAndSources(t *testing.T) {
	fx := setup(t)
	ctx := context.Background()
	grants := netgrant.NewRepo(fx.ui.deps.Store)
	g, err := grants.Create(ctx, netgrant.CreateInput{
		ProjectID: fx.project.ID, Host: "docs.test", PathPrefix: "/v1",
		Reason: "research", ExpiresAt: ptrTime(time.Now().Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ledger := coverage.NewRepo(fx.ui.deps.Store)
	if err := ledger.Record(ctx, fx.project.ID, "news", []coverage.Item{{Key: "story-001", Title: "First"}}); err != nil {
		t.Fatal(err)
	}

	_, body := get(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/workbench")
	for _, want := range []string{"Workbench", "docs.test", "story-001", "Coverage", "Schedule", "Sources"} {
		if !strings.Contains(body, want) {
			t.Errorf("workbench missing %q", want)
		}
	}

	resp := postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/grants/"+g.ID+"/revoke", url.Values{})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke status = %d, want 303", resp.StatusCode)
	}
	active, err := grants.ActiveForProject(ctx, fx.project.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("grant still active: %+v", active)
	}
}

func TestRevokeRejectsForeignGrant(t *testing.T) {
	fx := setup(t)
	ctx := context.Background()
	other, _, err := fx.ui.deps.Projects.Create(ctx, "other", "text", "another project", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	g, err := netgrant.NewRepo(fx.ui.deps.Store).Create(ctx, netgrant.CreateInput{
		ProjectID: other.ID, Host: "other.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/grants/"+g.ID+"/revoke", url.Values{})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign revoke status = %d, want 404", resp.StatusCode)
	}
}
