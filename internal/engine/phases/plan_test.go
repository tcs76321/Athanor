package phases

import "testing"

// TestParseDifficultyHint pins the parser: the last valid marker wins, case is
// ignored, and an absent or unrecognized marker yields "".
func TestParseDifficultyHint(t *testing.T) {
	cases := []struct{ in, want string }{
		{"blah\nDIFFICULTY: easy", "easy"},
		{"blah\ndifficulty: HARD\n", "hard"},
		{"DIFFICULTY: easy\nrevised\nDIFFICULTY: hard", "hard"},
		{"DIFFICULTY: maybe", ""},
		{"no marker here", ""},
	}
	for _, c := range cases {
		if got := parseDifficultyHint(c.in); got != c.want {
			t.Errorf("parseDifficultyHint(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
