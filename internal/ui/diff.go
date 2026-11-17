package ui

import (
	"fmt"
	"strings"
)

// This file owns the linear-space line diff used by the artifact comparison
// and supersede views.

// lineDiff is an LCS line diff. Equal lines are context; removed and added
// lines are tagged with "-"/"+". It uses Hirschberg's algorithm (known-issues
// M10), so memory is linear in the smaller input rather than the O(n*m)
// matrix the earlier implementation allocated (a ~20k-line pair allocated
// gigabytes). Inputs above maxDiffLines are summarized as a full replace to
// bound the O(n*m) time as well.
type diffLine struct {
	Kind string // " ", "-", "+", "@"
	Text string
}

// maxDiffLines caps the line diff (known-issues M10). Above it the change is
// rendered coarsely rather than burning minutes of CPU on a UI request.
const maxDiffLines = 4000

func lineDiff(oldText, newText string) []diffLine {
	a := splitLines(oldText)
	b := splitLines(newText)
	if len(a) > maxDiffLines || len(b) > maxDiffLines {
		out := make([]diffLine, 0, len(a)+len(b)+1)
		for _, l := range a {
			out = append(out, diffLine{"-", l})
		}
		out = append(out, diffLine{"@", fmt.Sprintf("… large change: %d lines → %d lines (diff summarized) …", len(a), len(b))})
		for _, l := range b {
			out = append(out, diffLine{"+", l})
		}
		return out
	}
	return mergeDiff(a, b, lcsSeq(a, b))
}

// lcsSeq returns a longest common subsequence of a and b using Hirschberg's
// algorithm: O(len(a)*len(b)) time, O(min(len(a),len(b))) space.
func lcsSeq(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	if len(a) == 1 {
		for _, x := range b {
			if x == a[0] {
				return []string{a[0]}
			}
		}
		return nil
	}
	mid := len(a) / 2
	left := lcsRow(a[:mid], b)
	right := lcsRow(reverseStrings(a[mid:]), reverseStrings(b))
	best, bestK := -1, 0
	for k := 0; k <= len(b); k++ {
		if v := left[k] + right[len(b)-k]; v > best {
			best, bestK = v, k
		}
	}
	out := lcsSeq(a[:mid], b[:bestK])
	return append(out, lcsSeq(a[mid:], b[bestK:])...)
}

// lcsRow returns the final DP row: row[j] = LCS length of a and b[:j].
func lcsRow(a, b []string) []int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for i := range a {
		cur[0] = 0
		for j := range b {
			switch {
			case a[i] == b[j]:
				cur[j+1] = prev[j] + 1
			case prev[j+1] >= cur[j]:
				cur[j+1] = prev[j+1]
			default:
				cur[j+1] = cur[j]
			}
		}
		prev, cur = cur, prev
	}
	return prev
}

func reverseStrings(s []string) []string {
	out := make([]string, len(s))
	for i := range s {
		out[i] = s[len(s)-1-i]
	}
	return out
}

// mergeDiff walks a, b, and their common subsequence to produce the diff.
// lcs must be a subsequence of both a and b (which lcsSeq guarantees).
func mergeDiff(a, b, lcs []string) []diffLine {
	var out []diffLine
	i, j, k := 0, 0, 0
	for k < len(lcs) {
		for a[i] != lcs[k] {
			out = append(out, diffLine{"-", a[i]})
			i++
		}
		for b[j] != lcs[k] {
			out = append(out, diffLine{"+", b[j]})
			j++
		}
		out = append(out, diffLine{" ", a[i]})
		i++
		j++
		k++
	}
	for ; i < len(a); i++ {
		out = append(out, diffLine{"-", a[i]})
	}
	for ; j < len(b); j++ {
		out = append(out, diffLine{"+", b[j]})
	}
	return out
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}
