package ui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/architect"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/cycle"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func conversationSetup(t *testing.T) (*httptest.Server, *UI) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	projects := project.NewRepo(s)
	u := New(Deps{
		Store: s, Projects: projects, Jobs: job.NewRepository(s), Config: cfg,
		Cycles: cycle.NewRepo(s), Starter: &canvasStarter{jobs: []string{"job-1"}},
		Architect: architect.New(nil), Freezer: fakeFreezer{}, Engine: &fakeEngine{},
	})
	mux := http.NewServeMux()
	u.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, u
}

func TestConversationToDraftCanvas(t *testing.T) {
	ts, _ := conversationSetup(t)
	code, body := get(t, ts.URL+"/ui/new")
	if code != http.StatusOK || !strings.Contains(body, "/ui/new/message") {
		t.Fatalf("new page = %d: %s", code, body)
	}
	// Extract the session id from the initial form.
	idx := strings.Index(body, `name="session" value="`)
	if idx < 0 {
		t.Fatal("session hidden field missing")
	}
	rest := body[idx+len(`name="session" value="`):]
	session := rest[:strings.Index(rest, `"`)]
	if session == "" {
		t.Fatal("empty session id")
	}

	resp, err := noRedirectClient.PostForm(ts.URL+"/ui/new/message", url.Values{
		"session": {session}, "message": {"Build a small parser with tests."},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("message status = %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "session=") {
		t.Fatalf("message location = %q", loc)
	}
	code, body = get(t, ts.URL+loc)
	if code != http.StatusOK || !strings.Contains(body, "Proposed frame") || !strings.Contains(body, "Approve and open canvas") {
		t.Fatalf("proposal page = %d: %s", code, body)
	}

	resp, err = noRedirectClient.PostForm(ts.URL+"/ui/new/approve", url.Values{"session": {session}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Logf("approve body: %s", b)
		t.Fatalf("approve status = %d", resp.StatusCode)
	}
	loc = resp.Header.Get("Location")
	if !strings.Contains(loc, "/canvas?cycle=") {
		t.Fatalf("approve location = %q", loc)
	}
}

func TestConversationApproveWithoutProposalIsRefused(t *testing.T) {
	ts, _ := conversationSetup(t)
	resp, err := noRedirectClient.PostForm(ts.URL+"/ui/new/approve", url.Values{"session": {"missing"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), "no proposal") {
		t.Fatalf("approve = %d: %s", resp.StatusCode, b)
	}
}
