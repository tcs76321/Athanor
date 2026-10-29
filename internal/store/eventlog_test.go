package store

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/tcs76321/athanor/migrations"
)

func requireMigrated(t *testing.T, s *Store) {
	t.Helper()
	if err := Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
}

func TestAppendEventRoundTrip(t *testing.T) {
	s, _ := openTemp(t)
	requireMigrated(t, s)
	ctx := context.Background()

	id, err := s.AppendEvent(ctx, Event{
		Category:  "jobs",
		Level:     EventWarn,
		ProjectID: "p1",
		Data:      map[string]any{"job": "j1", "seq": 2},
	})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	recs, err := s.QueryEvents(ctx, EventFilter{Category: "jobs"})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	r := recs[0]
	if r.ID != id {
		t.Errorf("id = %d, want %d", r.ID, id)
	}
	if r.Category != "jobs" || r.Level != "warn" || r.ProjectID != "p1" || r.JobID != "" {
		t.Errorf("unexpected record: %+v", r)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(r.DataJSON), &data); err != nil {
		t.Fatalf("DataJSON not valid JSON: %v (%q)", err, r.DataJSON)
	}
	if data["job"] != "j1" || data["seq"] != float64(2) {
		t.Errorf("data round-trip mismatch: %v", data)
	}
	if r.TS.IsZero() || time.Since(r.TS) > time.Minute {
		t.Errorf("timestamp not recent: %v", r.TS)
	}
}

func TestAppendEventDefaultsAndValidation(t *testing.T) {
	s, _ := openTemp(t)
	requireMigrated(t, s)
	ctx := context.Background()

	// Empty level defaults to info.
	if _, err := s.AppendEvent(ctx, Event{Category: "power"}); err != nil {
		t.Fatalf("default level append: %v", err)
	}
	recs, _ := s.QueryEvents(ctx, EventFilter{Category: "power"})
	if len(recs) != 1 || recs[0].Level != "info" {
		t.Fatalf("level = %v, want default info", recs[0].Level)
	}

	// Empty category rejected.
	if _, err := s.AppendEvent(ctx, Event{}); err == nil {
		t.Error("empty category accepted")
	}
	// Unknown level rejected.
	if _, err := s.AppendEvent(ctx, Event{Category: "jobs", Level: "loud"}); err == nil {
		t.Error("invalid level accepted")
	}
}

// TestAppendEventReferencesAreLoose documents a deliberate design choice:
// events carry optional project/job references WITHOUT foreign keys
// (migration 0001), so audit rows survive even if the referenced entity
// rows are later pruned. Unknown ids are therefore accepted here.
func TestAppendEventReferencesAreLoose(t *testing.T) {
	s, _ := openTemp(t)
	requireMigrated(t, s)
	if _, err := s.AppendEvent(context.Background(), Event{
		Category:  "jobs",
		JobID:     "no-such-job",
		ProjectID: "no-such-project",
	}); err != nil {
		t.Errorf("loose event reference rejected: %v", err)
	}
}

// TestConcurrentAppendsSerialize proves concurrent writers serialize
// correctly on the single-connection store (ADR 0004): every append lands,
// ids are unique, and the race detector has nothing to flag.
func TestConcurrentAppendsSerialize(t *testing.T) {
	s, _ := openTemp(t)
	requireMigrated(t, s)

	const workers, perWorker = 16, 20
	ids := make(chan int64, workers*perWorker)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				id, err := s.AppendEvent(context.Background(), Event{
					Category: "daydream",
					Data:     map[string]any{"i": i},
				})
				if err != nil {
					t.Errorf("append: %v", err)
					return
				}
				ids <- id
			}
		}()
	}
	wg.Wait()
	close(ids)

	seen := map[int64]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate event id %d", id)
		}
		seen[id] = true
	}
	recs, err := s.QueryEvents(context.Background(), EventFilter{Category: "daydream"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != workers*perWorker {
		t.Errorf("persisted %d events, want %d", len(recs), workers*perWorker)
	}
}

func TestQueryEventsFilters(t *testing.T) {
	s, _ := openTemp(t)
	requireMigrated(t, s)
	ctx := context.Background()

	mustAppend := func(e Event) {
		t.Helper()
		if _, err := s.AppendEvent(ctx, e); err != nil {
			t.Fatalf("AppendEvent(%+v): %v", e, err)
		}
	}
	mustAppend(Event{Category: "jobs", ProjectID: "p1", JobID: "j1"})
	mustAppend(Event{Category: "jobs", ProjectID: "p1", JobID: "j2"})
	mustAppend(Event{Category: "network", ProjectID: "p1"})
	mustAppend(Event{Category: "network", ProjectID: "p2"})

	cases := []struct {
		name   string
		filter EventFilter
		want   int
	}{
		{"all", EventFilter{}, 4},
		{"category", EventFilter{Category: "jobs"}, 2},
		{"project", EventFilter{ProjectID: "p2"}, 1},
		{"job", EventFilter{JobID: "j1"}, 1},
		{"category+project", EventFilter{Category: "network", ProjectID: "p1"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recs, err := s.QueryEvents(ctx, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			if len(recs) != tc.want {
				t.Errorf("got %d records, want %d", len(recs), tc.want)
			}
		})
	}

	limited, err := s.QueryEvents(ctx, EventFilter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 || limited[0].ID >= limited[1].ID {
		t.Errorf("limit/order wrong: %+v", limited)
	}
}

// TestQueryEventsAfterIDAndLatest pins the streaming delta cursor
// (known-issues M9): AfterID selects strictly-newer events, and LatestEventID
// returns the tail without loading the table.
func TestQueryEventsDescBeforeLevelQuery(t *testing.T) {
	s, _ := openTemp(t)
	requireMigrated(t, s)
	ctx := context.Background()

	id1, _ := s.AppendEvent(ctx, Event{Category: "jobs", Data: map[string]any{"event": "one", "note": "needle"}})
	id2, _ := s.AppendEvent(ctx, Event{Category: "jobs", Level: EventWarn, Data: map[string]any{"event": "two", "note": "ordinary"}})
	id3, _ := s.AppendEvent(ctx, Event{Category: "context", Data: map[string]any{"event": "three", "note": "100%"}})

	desc, err := s.QueryEvents(ctx, EventFilter{Desc: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(desc) != 3 || desc[0].ID != id3 || desc[2].ID != id1 {
		t.Fatalf("desc ids = %v", []int64{desc[0].ID, desc[1].ID, desc[2].ID})
	}

	older, err := s.QueryEvents(ctx, EventFilter{Desc: true, BeforeID: id2, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(older) != 1 || older[0].ID != id1 {
		t.Errorf("before id2 = %+v, want only id1", older)
	}

	warn, err := s.QueryEvents(ctx, EventFilter{Level: "warn"})
	if err != nil {
		t.Fatal(err)
	}
	if len(warn) != 1 || warn[0].ID != id2 {
		t.Errorf("level filter = %+v, want the warn row", warn)
	}

	found, err := s.QueryEvents(ctx, EventFilter{Query: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != id1 {
		t.Errorf("query filter = %+v, want the needle row", found)
	}

	literal, err := s.QueryEvents(ctx, EventFilter{Query: "100%"})
	if err != nil {
		t.Fatal(err)
	}
	if len(literal) != 1 || literal[0].ID != id3 {
		t.Errorf("literal %% filter = %+v, want the 100%% row only", literal)
	}
}

func TestQueryEventsAfterIDAndLatest(t *testing.T) {
	s, _ := openTemp(t)
	requireMigrated(t, s)
	ctx := context.Background()

	var ids []int64
	for i := 0; i < 5; i++ {
		id, err := s.AppendEvent(ctx, Event{Category: "jobs", Data: map[string]any{"i": i}})
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		ids = append(ids, id)
	}

	delta, err := s.QueryEvents(ctx, EventFilter{Category: "jobs", AfterID: ids[1]})
	if err != nil {
		t.Fatal(err)
	}
	if len(delta) != 3 || delta[0].ID != ids[2] {
		t.Fatalf("delta = %d events, want 3 starting at %d", len(delta), ids[2])
	}

	got, err := s.LatestEventID(ctx, EventFilter{Category: "jobs"})
	if err != nil || got != ids[4] {
		t.Fatalf("LatestEventID = %d (err %v), want %d", got, err, ids[4])
	}
	if got, _ := s.LatestEventID(ctx, EventFilter{Category: "nope"}); got != 0 {
		t.Fatalf("LatestEventID(no match) = %d, want 0", got)
	}
}

// TestQueryEventsAfterIDDrainsInBatches proves a bounded scan can catch up:
// reading AfterID+Limit repeatedly terminates and yields every row exactly
// once (the egress poll and UI stream rely on this, known-issues M9).
func TestQueryEventsAfterIDDrainsInBatches(t *testing.T) {
	s, _ := openTemp(t)
	requireMigrated(t, s)
	ctx := context.Background()
	const total, batch = 7, 3
	for i := 0; i < total; i++ {
		if _, err := s.AppendEvent(ctx, Event{Category: "jobs"}); err != nil {
			t.Fatal(err)
		}
	}
	seen := 0
	var cursor int64
	for {
		rows, err := s.QueryEvents(ctx, EventFilter{Category: "jobs", AfterID: cursor, Limit: batch})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		seen += len(rows)
		cursor = rows[len(rows)-1].ID
		if len(rows) < batch {
			break
		}
	}
	if seen != total {
		t.Fatalf("drained %d events, want %d", seen, total)
	}
}
