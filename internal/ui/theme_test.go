package ui

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestThemesAreUniqueAndDefined guards the theme picker: every advertised theme
// must have its own palette block (so it cannot silently look like another),
// and IDs must be unique.
// TestThemeBackgroundsAreUnique prevents the light themes from collapsing into
// near-identical palettes: every advertised theme must use a different
// background token.
func TestThemeBackgroundsAreUnique(t *testing.T) {
	css, err := fs.ReadFile(assetsFS, "assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, th := range themeNames {
		block := `:root`
		if th.ID != "monokai" {
			block = `[data-theme="` + th.ID + `"]`
		}
		start := strings.Index(string(css), block)
		if start < 0 {
			t.Errorf("theme %q has no block", th.ID)
			continue
		}
		end := strings.Index(string(css)[start:], "}")
		if end < 0 {
			t.Errorf("theme %q block is unterminated", th.ID)
			continue
		}
		body := string(css)[start : start+end]
		m := regexp.MustCompile(`--bg:\s*([^;]+);`).FindStringSubmatch(body)
		if m == nil {
			t.Errorf("theme %q has no --bg token", th.ID)
			continue
		}
		bg := strings.TrimSpace(m[1])
		if other, ok := seen[bg]; ok {
			t.Errorf("themes %q and %q share background %q", other, th.ID, bg)
		}
		seen[bg] = th.ID
	}
}

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
