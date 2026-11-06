package ui

import (
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/project"
)

func TestArtifactModeFor(t *testing.T) {
	cases := []struct {
		kind, capability string
		want             artifactMode
	}{
		{"code", "", modeCode},
		{"dataset", "", modeDataset},
		{"configuration", "", modeConfig},
		{"media", "", modeMedia},
		{"document", "", modeProse},
		{"proposal", project.CapabilityCode, modeCode},
		{"proposal", project.CapabilityData, modeDataset},
		{"proposal", project.CapabilityDocument, modeProse},
	}
	for _, tc := range cases {
		if got := artifactModeFor(tc.kind, tc.capability); got != tc.want {
			t.Errorf("artifactModeFor(%q,%q) = %q, want %q", tc.kind, tc.capability, got, tc.want)
		}
	}
}

func TestArtifactFilesParsesMarkers(t *testing.T) {
	body := "=== FILE: pkg/a.py ===\nprint(1)\n=== FILE: pkg/b.py ===\nprint(2)\n"
	files := artifactFiles("code", "", body)
	if len(files) != 2 {
		t.Fatalf("len(files) = %d, want 2", len(files))
	}
	if files[0].Path != "pkg/a.py" || !strings.Contains(files[0].Content, "print(1)") {
		t.Errorf("first file = %#v", files[0])
	}
	if files[1].Path != "pkg/b.py" {
		t.Errorf("second path = %q", files[1].Path)
	}
}

func TestArtifactFilesSingleFallback(t *testing.T) {
	files := artifactFiles("code", "", "print(1)\n")
	if len(files) != 1 || files[0].Path == "" {
		t.Fatalf("files = %#v, want one named file", files)
	}
	if !strings.Contains(files[0].Content, "print(1)") {
		t.Errorf("content = %q", files[0].Content)
	}
}

func TestDownloadNameIsSafe(t *testing.T) {
	a := artifact.Artifact{ID: "abcdef1234567890", Kind: artifact.KindDataset, Version: 3}
	got := downloadName(a)
	if got != "athanor-dataset-v3-abcdef12.json" {
		t.Errorf("downloadName = %q", got)
	}
	for _, bad := range []string{"/", "\\", "\x00"} {
		if strings.Contains(got, bad) {
			t.Errorf("downloadName %q contains %q", got, bad)
		}
	}
}

func TestFileDiffsSplitsTrees(t *testing.T) {
	oldText := "=== FILE: a.py ===\nold\n=== FILE: b.py ===\nsame\n"
	newText := "=== FILE: a.py ===\nnew\n=== FILE: b.py ===\nsame\n=== FILE: c.py ===\nadded\n"
	diffs := fileDiffs("code", "", oldText, newText)
	if len(diffs) != 3 {
		t.Fatalf("len(diffs) = %d, want 3", len(diffs))
	}
	if diffs[0].Path != "a.py" || diffs[1].Path != "b.py" || diffs[2].Path != "c.py" {
		t.Errorf("paths = %q,%q,%q", diffs[0].Path, diffs[1].Path, diffs[2].Path)
	}
	if !hasDiffKind(diffs[0].Lines, "-") || !hasDiffKind(diffs[0].Lines, "+") {
		t.Errorf("a.py diff did not record the change: %#v", diffs[0].Lines)
	}
	if hasDiffKind(diffs[1].Lines, "-") || hasDiffKind(diffs[1].Lines, "+") {
		t.Errorf("b.py should have no changes: %#v", diffs[1].Lines)
	}
}

func TestFileDiffsSingleStream(t *testing.T) {
	diffs := fileDiffs("document", "", "one\ntwo\n", "one\nTWO\n")
	if len(diffs) != 1 || diffs[0].Path != "" {
		t.Fatalf("diffs = %#v, want one unnamed stream", diffs)
	}
	if !hasDiffKind(diffs[0].Lines, "+") {
		t.Error("single-stream diff lost the addition")
	}
}

func hasDiffKind(lines []diffLine, kind string) bool {
	for _, l := range lines {
		if l.Kind == kind {
			return true
		}
	}
	return false
}
