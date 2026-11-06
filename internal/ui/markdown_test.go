package ui

import (
	"strings"
	"testing"
)

func TestRenderMarkdownBlocks(t *testing.T) {
	src := "# Title\n\nIntro **bold** and *italic* with `code`.\n\n- one\n- two\n\n1. first\n2. second\n\n> quote\n\n```go\nfn()\n```\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n\n---\n"
	got := string(renderMarkdown(src))
	for _, want := range []string{
		`<h1 id="title">Title</h1>`,
		"<strong>bold</strong>",
		"<em>italic</em>",
		"<code>code</code>",
		"<ul>",
		"<ol>",
		"<blockquote>",
		`<code class="language-go">`,
		"<table>",
		"<th>a</th>",
		"<hr>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("renderMarkdown missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderMarkdownEscapesHTML(t *testing.T) {
	cases := []string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`<iframe src="https://evil"></iframe>`,
	}
	for _, src := range cases {
		got := string(renderMarkdown(src))
		if strings.Contains(got, "<script") || strings.Contains(got, "<img") || strings.Contains(got, "<iframe") {
			t.Errorf("raw HTML survived: %q -> %s", src, got)
		}
	}
}

func TestRenderMarkdownRejectsUnsafeLinks(t *testing.T) {
	got := string(renderMarkdown(`[click](javascript:alert(1))`))
	if strings.Contains(got, "javascript:") || strings.Contains(got, "<a ") {
		t.Fatalf("unsafe link rendered: %s", got)
	}
	got = string(renderMarkdown(`[ok](https://example.com/a?b=1)`))
	if !strings.Contains(got, `href="https://example.com/a?b=1"`) {
		t.Fatalf("safe link missing: %s", got)
	}
	// A query string with & must be escaped exactly once.
	got = string(renderMarkdown(`[ok](https://example.com/a?b=1&c=2)`))
	if !strings.Contains(got, `href="https://example.com/a?b=1&amp;c=2"`) {
		t.Fatalf("ampersand was not escaped exactly once: %s", got)
	}
	if strings.Contains(got, "&amp;amp;") {
		t.Fatalf("ampersand was double-escaped: %s", got)
	}
}

func TestRenderMarkdownHeadingIDsUnique(t *testing.T) {
	got := string(renderMarkdown("# Same\n\n# Same\n"))
	if !strings.Contains(got, `id="same"`) || !strings.Contains(got, `id="same-2"`) {
		t.Fatalf("heading ids are not unique: %s", got)
	}
}

func TestRenderMarkdownCodeFenceEscapes(t *testing.T) {
	got := string(renderMarkdown("```\n<script>x</script>\n```\n"))
	if strings.Contains(got, "<script>") {
		t.Fatalf("code fence did not escape: %s", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Fatalf("code fence missing escaped body: %s", got)
	}
}
