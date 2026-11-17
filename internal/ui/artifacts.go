package ui

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/tcs76321/athanor/internal/corrections"
	"github.com/tcs76321/athanor/internal/review"
	"github.com/tcs76321/athanor/internal/strategy"
)

// This file owns the artifact reader: presentation by kind, exact-byte
// download, egress export, blinded review, comparison, and the supersede diff.

// handleDiff renders a line diff between an artifact and the version it
// superseded.
// handleArtifact renders one artifact's full content. The project page links
// here; without it the human could see statuses and diffs but not the accepted
// bytes.
func (u *UI) handleArtifact(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	a, err := u.deps.Artifacts.Get(ctx, id)
	if err != nil {
		u.fail(w, err)
		return
	}
	content, err := u.deps.Artifacts.ReadContent(ctx, id)
	if err != nil {
		u.fail(w, err)
		return
	}
	var reviews []review.Review
	if u.deps.Reviews != nil {
		reviews, _ = u.deps.Reviews.ListByArtifact(ctx, id)
	}
	// The presentation surface (first-use F5): what was built, how to run it,
	// what it cost, and what is known-broken. Each source is optional; the
	// page renders what exists rather than failing.
	p, _ := u.deps.Projects.Get(ctx, a.ProjectID)
	var outcome strategy.Outcome
	hasOutcome := false
	if u.deps.Strategy != nil && a.JobID != "" {
		if o, err := u.deps.Strategy.GetOutcomeByJob(ctx, a.JobID); err == nil {
			outcome, hasOutcome = o, true
		}
	}
	var issues []corrections.Record
	if u.deps.Corrections != nil {
		if all, err := u.deps.Corrections.ListByProject(ctx, a.ProjectID); err == nil {
			for _, c := range all {
				if c.ArtifactID == a.ID {
					issues = append(issues, c)
				}
			}
		}
	}
	capability := ""
	if a.TaskID != "" {
		if t, terr := u.deps.Projects.Task(ctx, a.TaskID); terr == nil {
			capability = t.Capability
		}
	}
	mode := artifactModeFor(string(a.Kind), capability)
	files := decorateFiles(artifactFiles(string(a.Kind), capability, string(content)))
	contentText := string(content)
	size := len(content)
	if mode == modeMedia {
		// Never push binary bytes through the template or the page.
		files = nil
		contentText = ""
	}
	u.render(w, "Artifact "+a.ID, artifactBody, map[string]any{
		"Artifact": a, "Content": contentText, "Reviews": reviews,
		"Project": p, "Outcome": outcome, "HasOutcome": hasOutcome, "Issues": issues,
		"Exported": r.URL.Query().Get("exported") != "", "CanExport": u.exporter != nil,
		"Mode": string(mode), "Files": files, "Size": size, "Branch": artifactBranch(a, p),
		"DownloadName": downloadName(a),
	})
}

// handleArtifactDownload serves the artifact's exact stored bytes as an
// attachment. The content hash is verified by ReadContent first, so a corrupt
// or bit-rotted file fails loudly instead of being downloaded silently.
func (u *UI) handleArtifactDownload(w http.ResponseWriter, r *http.Request) {
	a, err := u.deps.Artifacts.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		u.fail(w, err)
		return
	}
	content, err := u.deps.Artifacts.ReadContent(r.Context(), a.ID)
	if err != nil {
		u.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", artifactContentType(a.Kind))
	w.Header().Set("Content-Disposition", `attachment; filename="`+downloadName(a)+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

// handleArtifactExport runs the egress pipeline synchronously for one
// artifact and returns to the reader with a result notice.
func (u *UI) handleArtifactExport(w http.ResponseWriter, r *http.Request) {
	if u.exporter == nil {
		u.fail(w, fmt.Errorf("the egress pipeline is not configured"))
		return
	}
	id := r.PathValue("id")
	if _, exported, err := u.exporter.ExportOne(r.Context(), id); err != nil {
		u.fail(w, err)
		return
	} else if !exported {
		u.fail(w, fmt.Errorf("artifact is not accepted; export only runs on accepted artifacts"))
		return
	}
	http.Redirect(w, r, "/ui/artifacts/"+id+"?exported=1", http.StatusSeeOther)
}

func (u *UI) handleReviewCreate(w http.ResponseWriter, r *http.Request) {
	if u.deps.Reviews == nil {
		u.fail(w, fmt.Errorf("review queue is not configured"))
		return
	}
	_ = r.ParseForm()
	score, err := strconv.ParseFloat(r.FormValue("score"), 64)
	if err != nil {
		u.fail(w, fmt.Errorf("score must be a number between 0 and 1"))
		return
	}
	art, err := u.deps.Artifacts.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		u.fail(w, err)
		return
	}
	if _, err := u.deps.Reviews.Create(r.Context(), review.CreateInput{
		ArtifactID: art.ID, JobID: art.JobID, ProjectID: art.ProjectID,
		Score: score, Accepted: r.FormValue("accepted") != "",
		Blinded: true, Note: r.FormValue("note"),
	}); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/artifacts/"+art.ID, http.StatusSeeOther)
}

// handleCompare renders a line/file diff between any two artifacts. The watch
// page uses it to compare divergence candidates; the artifact page can compare
// an accepted result with an earlier version or a sibling candidate.
func (u *UI) handleCompare(w http.ResponseWriter, r *http.Request) {
	aID, bID := r.URL.Query().Get("a"), r.URL.Query().Get("b")
	if aID == "" || bID == "" {
		u.fail(w, fmt.Errorf("compare needs two artifact ids"))
		return
	}
	left, err := u.deps.Artifacts.Get(r.Context(), aID)
	if err != nil {
		u.fail(w, err)
		return
	}
	right, err := u.deps.Artifacts.Get(r.Context(), bID)
	if err != nil {
		u.fail(w, err)
		return
	}
	leftText, err := u.deps.Artifacts.ReadContent(r.Context(), left.ID)
	if err != nil {
		u.fail(w, err)
		return
	}
	rightText, err := u.deps.Artifacts.ReadContent(r.Context(), right.ID)
	if err != nil {
		u.fail(w, err)
		return
	}
	capability := ""
	if left.TaskID != "" {
		if t, terr := u.deps.Projects.Task(r.Context(), left.TaskID); terr == nil {
			capability = t.Capability
		}
	}
	u.render(w, "Compare artifacts", compareBody, map[string]any{
		"Left": left, "Right": right,
		"FileDiffs": fileDiffs(string(left.Kind), capability, string(leftText), string(rightText)),
	})
}

func (u *UI) handleDiff(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a, err := u.deps.Artifacts.Get(ctx, r.PathValue("id"))
	if err != nil {
		u.fail(w, err)
		return
	}
	newContent, err := u.deps.Artifacts.ReadContent(ctx, a.ID)
	if err != nil {
		u.fail(w, err)
		return
	}
	var oldContent []byte
	if a.SupersedesID != "" {
		old, err := u.deps.Artifacts.Get(ctx, a.SupersedesID)
		if err != nil {
			u.fail(w, err)
			return
		}
		oldContent, err = u.deps.Artifacts.ReadContent(ctx, old.ID)
		if err != nil {
			u.fail(w, err)
			return
		}
	}
	capability := ""
	if a.TaskID != "" {
		if t, terr := u.deps.Projects.Task(ctx, a.TaskID); terr == nil {
			capability = t.Capability
		}
	}
	u.render(w, "Diff "+a.ID, diffBody, map[string]any{
		"Artifact":  a,
		"FileDiffs": fileDiffs(string(a.Kind), capability, string(oldContent), string(newContent)),
	})
}

// listProjects reads every project. project.Repo has no List; the UI queries
// the projects table directly (read-only), which is ordinary for a view.
//
// The store holds a single connection (ADR-0027), so the id rows must be
// drained and closed before the per-id Get queries, or the Get waits forever
// for the connection the open rows hold.
