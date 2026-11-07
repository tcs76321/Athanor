package ui

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/project"
)

// indexRun is the in-memory status of one bounded repository index pass. The
// indexer owns the durable manifest; this is only the operator-visible
// progress/result for the page that started it.
type indexRun struct {
	Status    string // running | done | failed
	Error     string
	StartedAt time.Time
	Done      bool
}

// indexView returns a copy of a project's current index run.
func (u *UI) indexView(projectID string) *indexRun {
	u.indexMu.Lock()
	defer u.indexMu.Unlock()
	if run := u.indexRuns[projectID]; run != nil {
		cp := *run
		return &cp
	}
	return nil
}

// startIndex runs one bounded index pass in the background. A repo-backed
// project should have a populated Dormant Index before its first job; doing
// this on the request path made project creation wait up to five minutes, so
// the UI starts it and reports progress instead.
func (u *UI) startIndex(p project.Project) {
	if u.deps.Indexer == nil || strings.TrimSpace(p.RepositoryPath) == "" {
		return
	}
	u.indexMu.Lock()
	if run := u.indexRuns[p.ID]; run != nil && !run.Done {
		u.indexMu.Unlock()
		return
	}
	pruneRuns(u.indexRuns, maxRunHistory)
	u.indexRuns[p.ID] = &indexRun{Status: "running", StartedAt: time.Now()}
	u.indexMu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		err := u.deps.Indexer.Preindex(ctx, p.ID, p.RepositoryPath)
		u.indexMu.Lock()
		run := u.indexRuns[p.ID]
		if run != nil {
			run.Done = true
			run.Status = "done"
			if err != nil {
				run.Status = "failed"
				run.Error = err.Error()
			}
		}
		u.indexMu.Unlock()
	}()
}

// waitForIndex blocks plan approval until the project's bounded pre-index has
// finished. The create path starts indexing asynchronously so the project form
// stays responsive; starting the first job before it completes produces an
// empty Dormant Index and a worse first result. A failure is reported to the
// caller; the operator can still retry the index from the project page.
func (u *UI) waitForIndex(ctx context.Context, p project.Project) error {
	if u.deps.Indexer == nil || strings.TrimSpace(p.RepositoryPath) == "" {
		return nil
	}
	if u.indexView(p.ID) == nil {
		u.startIndex(p)
	}
	deadline := time.NewTimer(5 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		run := u.indexView(p.ID)
		if run == nil {
			return nil
		}
		if run.Done {
			if run.Status == "failed" {
				return fmt.Errorf("repository pre-index failed: %s", run.Error)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("repository pre-index is still running; wait for it on the project page and try again")
		case <-ticker.C:
		}
	}
}

// handleProjectIndex starts (or restarts) the bounded index pass.
func (u *UI) handleProjectIndex(w http.ResponseWriter, r *http.Request) {
	p, err := u.deps.Projects.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		u.fail(w, err)
		return
	}
	if strings.TrimSpace(p.RepositoryPath) == "" {
		u.fail(w, project.ErrNoRepository)
		return
	}
	if u.deps.Indexer == nil {
		u.fail(w, fmt.Errorf("repository indexing is not configured"))
		return
	}
	u.startIndex(p)
	http.Redirect(w, r, "/ui/projects/"+p.ID, http.StatusSeeOther)
}
