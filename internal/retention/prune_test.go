package retention

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func TestPruneAuxRemovesOnlyOldRows(t *testing.T) {
	st := setup(t)
	old := "2020-01-01T00:00:00.000Z"
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st.DB().Exec(`
		INSERT INTO projects (id,name,archetype,goal) VALUES ('p1','demo','code','do work');
		INSERT INTO tasks (id,project_id,title) VALUES ('t1','p1','task');
		INSERT INTO jobs (id,task_id,project_id,state) VALUES ('j1','t1','p1','completed');
		INSERT INTO daydream_logs (id,action,started_at,finished_at) VALUES
		  ('d-old','consolidate','` + old + `','` + old + `'),
		  ('d-new','consolidate','` + now + `','` + now + `');
		INSERT INTO budget_windows (window_start,window_end,tokens_ceiling,wall_ns_ceiling) VALUES
		  ('2020-01-01T00:00:00Z','2020-01-02T00:00:00Z',100,100),
		  ('2099-01-01T00:00:00Z','2099-01-02T00:00:00Z',100,100);
		INSERT INTO budget_grants (id,job_id,window_start,priority,tokens,wall_ns) VALUES
		  ('g-old','j1','2020-01-01T00:00:00Z','background',10,10),
		  ('g-new','j1','2099-01-01T00:00:00Z','background',10,10);
		INSERT INTO alarms (id,category,level,message,status,created_at,resolved_at) VALUES
		  ('a-old','resource','notice','old','resolved','` + old + `','` + old + `'),
		  ('a-active','resource','notice','active','active','` + old + `',NULL);
	`); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().UTC().AddDate(-1, 0, 0)
	res, err := PruneAux(context.Background(), st, cutoff, cutoff, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if res.Daydream != 1 || res.BudgetGrants != 1 || res.BudgetWindows != 1 || res.Alarms != 1 {
		t.Fatalf("result = %+v, want one of each", res)
	}
	var daydream, windows, grants, alarms int
	if err := st.ReadDB().QueryRow(`SELECT COUNT(*) FROM daydream_logs`).Scan(&daydream); err != nil {
		t.Fatal(err)
	}
	if err := st.ReadDB().QueryRow(`SELECT COUNT(*) FROM budget_windows`).Scan(&windows); err != nil {
		t.Fatal(err)
	}
	if err := st.ReadDB().QueryRow(`SELECT COUNT(*) FROM budget_grants`).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if err := st.ReadDB().QueryRow(`SELECT COUNT(*) FROM alarms`).Scan(&alarms); err != nil {
		t.Fatal(err)
	}
	if daydream != 1 || windows != 1 || grants != 1 || alarms != 1 {
		t.Errorf("remaining = daydream:%d windows:%d grants:%d alarms:%d, want 1 each", daydream, windows, grants, alarms)
	}
}

func TestPruneArchivesByAge(t *testing.T) {
	st := setup(t)
	old := time.Now().UTC().AddDate(-2, 0, 0).Format("2006-01-02T15:04:05.000Z")
	insertEvent(t, st, old)
	dir := t.TempDir()
	if _, err := ArchiveEvents(context.Background(), st, time.Now().UTC().AddDate(-1, 0, 0), Options{ArchiveDir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`UPDATE retention_archives SET created_at = ?`, old); err != nil {
		t.Fatal(err)
	}
	n, err := PruneArchives(context.Background(), st, dir, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("removed = %d, want 1", n)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("archive files = %d, want 0", len(entries))
	}
	var count int
	if err := st.ReadDB().QueryRow(`SELECT COUNT(*) FROM retention_archives`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("manifest rows = %d, want 0", count)
	}
}

func TestMigrateRetentionIsIdempotent(t *testing.T) {
	st := setup(t)
	// A second full migrate is a no-op and leaves the conditional trigger.
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	insertEvent(t, st, time.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
	if _, err := st.DB().Exec(`DELETE FROM events`); err == nil {
		t.Fatal("delete succeeded after re-migrate, want append-only rejection")
	}
}
