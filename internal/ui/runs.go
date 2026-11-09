package ui

import (
	"sort"
	"time"
)

// maxRunHistory bounds each in-memory run map (model pulls, discovery,
// planning, repository indexing). Completed entries are useful for the page
// that started them, but a 24/7 Core serves many projects; keeping every
// historical run — including full discovery/plan sessions — is an unbounded
// retention leak. Older completed entries are dropped first; running entries
// are never pruned.
const maxRunHistory = 64

// runMeta is the small surface pruneRuns needs. Implemented by each UI run
// type; all four are pointer values in their maps.
type runMeta interface {
	runDone() bool
	runAt() time.Time
}

// pruneRuns deletes the oldest completed entries until the map is at most
// `keep` entries. It is a no-op when the map is below the limit or every entry
// is still running.
func pruneRuns[P runMeta](m map[string]P, keep int) {
	if keep < 1 || len(m) <= keep {
		return
	}
	type entry struct {
		key string
		at  time.Time
	}
	done := make([]entry, 0, len(m))
	for key, run := range m {
		if run.runDone() {
			done = append(done, entry{key: key, at: run.runAt()})
		}
	}
	sort.Slice(done, func(i, j int) bool {
		if done[i].at.Equal(done[j].at) {
			return done[i].key < done[j].key
		}
		return done[i].at.Before(done[j].at)
	})
	for i := 0; len(m) > keep && i < len(done); i++ {
		delete(m, done[i].key)
	}
}

func (p *pullProgress) runDone() bool    { return p.Done }
func (p *pullProgress) runAt() time.Time { return p.StartedAt }
func (r *indexRun) runDone() bool        { return r.Done }
func (r *indexRun) runAt() time.Time     { return r.StartedAt }
