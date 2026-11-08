package phases

import "testing"

// TestNormalizeCode proves a fenced module is unwrapped before the pod runs it
// (F4 micro-run finding: a 9B model wrapped source in a fence).
func TestNormalizeCode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"```python\nx = 1\n```", "x = 1"},
		{"```\nx = 1\n```", "x = 1"},
		{"x = 1", "x = 1"},
		{"```python\nx = 1", "x = 1"},
	}
	for _, c := range cases {
		if got := normalizeCode(c.in); got != c.want {
			t.Errorf("normalizeCode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
