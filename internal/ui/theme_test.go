package ui

import (
	"io/fs"
	"strings"
	"testing"
)

// TestThemesAreUniqueAndDefined guards the theme picker: every advertised theme
// must have its own palette block (so it cannot silently look like another),
// and IDs must be unique.
func TestThemesAreUniqueAndDefined(t *testing.T) {
	css, err := fs.ReadFile(assetsFS, "assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, th := range themeNames {
		if th.ID == "" {
			t.Error("empty theme id")
		}
		if seen[th.ID] {
			t.Errorf("duplicate theme id %q", th.ID)
		}
		seen[th.ID] = true
		if th.ID == "monokai" {
			continue // the :root palette
		}
		if !strings.Contains(string(css), `[data-theme="`+th.ID+`"]`) {
			t.Errorf("theme %q is advertised but has no palette block in app.css", th.ID)
		}
	}
}
