package job

import (
	"context"
	"testing"
)

func TestByTaskOrdersOldestFirst(t *testing.T) {
	r, s := openRepo(t)
	projectID, taskID := seedProjectTask(t, s)

	first, err := r.Create(context.Background(), taskID, projectID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Create(context.Background(), taskID, projectID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := r.Create(context.Background(), taskID, projectID) // same task, still grouped
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == first.ID || other.ID == second.ID {
		t.Fatalf("ids collided")
	}

	jobs, err := r.ByTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("ByTask: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("jobs = %d, want 3", len(jobs))
	}
	for _, j := range jobs {
		if j.TaskID != taskID {
			t.Errorf("job %s task = %q, want %q", j.ID, j.TaskID, taskID)
		}
	}
	if jobs[0].ID != first.ID {
		t.Errorf("first job = %s, want %s (oldest first)", jobs[0].ID, first.ID)
	}

	none, err := r.ByTask(context.Background(), "missing")
	if err != nil {
		t.Fatalf("ByTask(missing): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("missing task jobs = %v, want none", none)
	}
}
