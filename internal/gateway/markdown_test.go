package gateway

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// renderFragment parses `frag` as an HTML fragment and renders it to
// markdown. The input contract is "already-sanitized HTML" (the
// bluemonday output the reader passes in), so these tests drive the
// renderer directly without re-running the sanitizer.
func renderFragment(frag string) string {
	doc, err := html.Parse(strings.NewReader(frag))
	if err != nil {
		panic("markdown_test: html.Parse failed: " + err.Error())
	}
	return renderMarkdown(doc)
}

// TestRenderMarkdown_Headings pins the heading subset.
func TestRenderMarkdown_Headings(t *testing.T) {
	if got := renderFragment(`<h1>Title</h1><h2>Sub</h2>`); got != "# Title\n\n## Sub\n\n" {
		t.Errorf("headings = %q, want %q", got, "# Title\n\n## Sub\n\n")
	}
}

// TestRenderMarkdown_Paragraphs pins paragraph separation.
func TestRenderMarkdown_Paragraphs(t *testing.T) {
	got := renderFragment(`<p>First paragraph.</p><p>Second paragraph.</p>`)
	want := "First paragraph.\n\nSecond paragraph.\n\n"
	if got != want {
		t.Errorf("paragraphs = %q, want %q", got, want)
	}
}

// TestRenderMarkdown_Lists pins the list subset including nesting.
func TestRenderMarkdown_Lists(t *testing.T) {
	ul := renderFragment(`<ul><li>a</li><li>b</li></ul>`)
	if ul != "- a\n- b\n\n" {
		t.Errorf("ul = %q, want %q", ul, "- a\n- b\n\n")
	}
	ol := renderFragment(`<ol><li>one</li><li>two</li></ol>`)
	if ol != "1. one\n1. two\n\n" {
		t.Errorf("ol = %q, want %q", ol, "1. one\n1. two\n\n")
	}
	nested := renderFragment(`<ul><li>a<ul><li>n</li></ul></li><li>b</li></ul>`)
	if nested != "- a\n  - n\n- b\n\n" {
		t.Errorf("nested ul = %q, want %q", nested, "- a\n  - n\n- b\n\n")
	}
}

// TestRenderMarkdown_LinkSubset pins the closed link subset:
// http(s) links render as markdown; javascript: links render as
// plain text (defense-in-depth — bluemonday already strips them).
func TestRenderMarkdown_LinkSubset(t *testing.T) {
	https := renderFragment(`<p>See <a href="https://example.org/doc">docs</a>.</p>`)
	if https != "See [docs](https://example.org/doc).\n\n" {
		t.Errorf("https link = %q", https)
	}
	js := renderFragment(`<p>Click <a href="javascript:alert(1)">here</a>.</p>`)
	if js != "Click here.\n\n" {
		t.Errorf("javascript: link = %q", js)
	}
	// A data: URL is not http(s) → plain text.
	data := renderFragment(`<p><a href="data:text/html,boom">x</a></p>`)
	if data != "x\n\n" {
		t.Errorf("data: link = %q", data)
	}
}

// TestRenderMarkdown_Code pins fenced and inline code.
func TestRenderMarkdown_Code(t *testing.T) {
	fenced := renderFragment(`<pre><code>print("hi")</code></pre>`)
	if fenced != "```\nprint(\"hi\")\n```\n\n" {
		t.Errorf("fenced = %q", fenced)
	}
	inline := renderFragment(`<p>Run <code>make check</code>.</p>`)
	if inline != "Run `make check`.\n\n" {
		t.Errorf("inline = %q", inline)
	}
}

// TestRenderMarkdown_BlockquoteAndHR pins the simple blocks.
func TestRenderMarkdown_BlockquoteAndHR(t *testing.T) {
	q := renderFragment(`<blockquote>A quote.</blockquote>`)
	if q != "> A quote.\n\n" {
		t.Errorf("blockquote = %q", q)
	}
	hr := renderFragment(`<p>before</p><hr><p>after</p>`)
	if hr != "before\n\n---\n\nafter\n\n" {
		t.Errorf("hr = %q", hr)
	}
}

// TestRenderMarkdown_Table pins the table subset through the HTML5
// parser's <tbody> wrapper.
func TestRenderMarkdown_Table(t *testing.T) {
	got := renderFragment(`<table><tr><th>A</th><th>B</th></tr><tr><td>1</td><td>2</td></tr></table>`)
	if got != "A | B\n1 | 2\n\n" {
		t.Errorf("table = %q, want %q", got, "A | B\n1 | 2\n\n")
	}
}

// TestRenderMarkdown_ImageAlt renders alt text, never the src URL.
func TestRenderMarkdown_ImageAlt(t *testing.T) {
	got := renderFragment(`<p>See <img src="https://evil.example/x.png" alt="a diagram">.</p>`)
	if got != "See a diagram.\n\n" {
		t.Errorf("image = %q", got)
	}
}

// TestRenderMarkdown_InlineFormattingSurvives: text inside inline
// emphasis/span elements is preserved.
func TestRenderMarkdown_InlineFormattingSurvives(t *testing.T) {
	got := renderFragment(`<p>a <em>b</em> <strong>c</strong> <span>d</span></p>`)
	if got != "a b c d\n\n" {
		t.Errorf("inline = %q", got)
	}
}

// TestRenderMarkdown_MarkdownEscape pins that link text containing
// markdown metacharacters is escaped rather than interpreted.
func TestRenderMarkdown_MarkdownEscape(t *testing.T) {
	got := renderFragment(`<p><a href="https://e.example/">a [b] (c)</a></p>`)
	if got != "[a \\[b\\] \\(c\\)](https://e.example/)\n\n" {
		t.Errorf("escaped link = %q", got)
	}
}

// TestRenderMarkdown_EmptyRendersEmpty pins the trivial case.
func TestRenderMarkdown_EmptyRendersEmpty(t *testing.T) {
	if got := renderFragment(""); got != "" {
		t.Errorf("empty = %q, want \"\"", got)
	}
}