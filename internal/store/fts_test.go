package store

import (
	"context"
	"testing"
)

// TestCheckFTS5 verifies the preflight accepts a build that has FTS5
// compiled in. The whole suite is built with `-tags sqlite_fts5` (the
// Makefile's GO_TAGS; ADR-0026 §1), so a pass here proves the tag is
// actually in effect. The failure arm is a build-time property (a
// tag-less binary), which the boot preflight surfaces rather than a unit
// test: the test binary cannot un-compile FTS5 at run time.
func TestCheckFTS5(t *testing.T) {
	s, _ := openTemp(t)
	if err := CheckFTS5(context.Background(), s.DB()); err != nil {
		t.Fatalf("CheckFTS5: %v", err)
	}
}
