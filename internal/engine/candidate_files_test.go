package engine

import "testing"

// TestCandidateFiles pins the ADR-0065 tree detection: no markers is a single
// solution.py; `=== FILE: path ===` blocks are a multi-file tree; a hostile
// marker path is an error.
func TestCandidateFiles(t *testing.T) {
	f, err := candidateFiles("print(1)")
	if err != nil || len(f) != 1 || f[0].Path != "solution.py" {
		t.Fatalf("single = %+v, %v; want one solution.py", f, err)
	}
	multi, err := candidateFiles("=== FILE: a.py ===\nx\n=== FILE: b.py ===\ny\n")
	if err != nil || len(multi) != 2 {
		t.Fatalf("multi = %+v, %v; want 2 files", multi, err)
	}
	if multi[0].Path != "a.py" || multi[1].Path != "b.py" {
		t.Errorf("multi paths = %+v, want a.py then b.py", multi)
	}
	if _, err := candidateFiles("=== FILE: ../evil ===\nx\n"); err == nil {
		t.Error("hostile marker path accepted, want an error")
	}
}
