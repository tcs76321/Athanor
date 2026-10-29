package analysis_test

import (
	"testing"

	"github.com/tcs76321/athanor/internal/analysis"
)

func TestParseFencedBlock(t *testing.T) {
	content := "A plan in prose.\n\n```analysis\n" +
		`{"units":[{"goal":"parse the grammar","mode":"direct","falsifier":"parse the sample"},` +
		`{"goal":"precedence","mode":"deliberate","reason":"hidden constraints"}]}` +
		"\n```\nDIFFICULTY: hard"
	a := analysis.Parse(content)
	if !a.Parsed {
		t.Fatal("fenced analysis block was not parsed")
	}
	if len(a.Units) != 2 {
		t.Fatalf("units = %d, want 2", len(a.Units))
	}
	if a.Units[0].ID != "u1" || a.Units[0].Mode != analysis.Direct {
		t.Errorf("unit 1 = %+v", a.Units[0])
	}
	if a.Units[1].Mode != analysis.Deliberate {
		t.Errorf("unit 2 mode = %q, want deliberate", a.Units[1].Mode)
	}
	if got := a.Deliberate(); len(got) != 1 {
		t.Errorf("deliberate units = %d, want 1", len(got))
	}
}

func TestParseBareJSON(t *testing.T) {
	a := analysis.Parse(`{"units":[{"goal":"one","mode":"bogus"}]}`)
	if !a.Parsed || len(a.Units) != 1 {
		t.Fatalf("bare JSON = %+v", a)
	}
	if a.Units[0].Mode != analysis.Direct {
		t.Errorf("unknown mode = %q, want the direct default", a.Units[0].Mode)
	}
}

func TestParseFailsSafe(t *testing.T) {
	for _, content := range []string{
		"",
		"just prose, no block",
		"```analysis\n{not json}\n```",
		`{"units":[]}`,
		`{"units":[{"goal":"","mode":"deliberate"}]}`,
	} {
		a := analysis.Parse(content)
		if a.Parsed {
			t.Errorf("content %q parsed; want the fail-safe", content)
		}
		if len(a.Units) != 1 || a.Units[0].Mode != analysis.Direct {
			t.Errorf("fallback = %+v, want one direct unit", a)
		}
	}
}

func TestParseCapsUnits(t *testing.T) {
	units := ""
	for i := 0; i < 20; i++ {
		if i > 0 {
			units += ","
		}
		units += `{"goal":"part","mode":"direct"}`
	}
	a := analysis.Parse(`{"units":[` + units + `]}`)
	if len(a.Units) != 8 {
		t.Errorf("units = %d, want the cap 8", len(a.Units))
	}
}
