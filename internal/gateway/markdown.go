package gateway

import (
	"bytes"
	"io"
	"strings"

	"golang.org/x/net/html"
)

// renderMarkdown walks a sanitized `*html.Node` tree and produces
// markdown (M4-T6, ADR-0018 §3 step 4). The input must already have
// passed through bluemonday's UGCPolicy: script/style/iframe/object/
// form/embed elements and event-handler attributes are gone, and URL
// attributes are restricted to http(s). This renderer therefore does
// NOT defend against malicious HTML — it maps a trusted, sanitized
// tree to a deliberately small markdown subset:
//
//   - h1–h6    → "# …" … "###### …"
//   - p        → paragraph text, blank-line separated
//   - ul / ol  → "- " / "1. " items, nested lists at two-space indent
//   - a        → "[text](href)" for http(s) hrefs; plain text otherwise
//   - pre,code → fenced ``` blocks
//   - blockquote → "> " lines
//   - hr       → "---"
//   - br       → newline
//   - table    → one row per line, cells joined by " | "
//   - img      → alt text, or nothing when no alt
//   - others   → their child content, recursed
//
// The subset is closed and table-tested (`markdown_test.go`). A page
// whose structure exceeds this subset is a Browser Mode case (M6,
// ADR-0018 §"Not in M4-T6"), not a reason to grow the renderer.
//
// Why render at all instead of returning bluemonday's sanitized HTML?
// The consumer is an LLM prompt (§16 research workflow): markdown is
// the canonical serialization a model already knows, and HTML in the
// prompt is an injection surface that would survive sanitization
// only as text the model must be instructed to ignore. By returning
// markdown we remove the angle-bracket surface entirely.
//
// renderMarkdown is a pure function of its Node tree; it performs no
// I/O and allocates only the output builder. It is safe to call
// concurrently.
func renderMarkdown(root *html.Node) string {
	var b bytes.Buffer
	walkMarkdown(&b, root, 0)
	return b.String()
}

// writeMarkdown appends `s` to the in-memory markdown builder. The
// `io.WriteString` error is deliberately discarded: bytes.Buffer's
// Write cannot fail, and the error return is a generic io.Writer
// contract artifact, not a recoverable condition here.
func writeMarkdown(b *bytes.Buffer, s string) {
	_, _ = io.WriteString(b, s)
}

// walkMarkdown writes the markdown for `n` (and its subtree) into b.
// `depth` is the block-level list/blockquote nesting, used only to
// pick the list bullet indentation; all other block break decisions
// in this subset are context-free.
func walkMarkdown(b *bytes.Buffer, n *html.Node, depth int) {
	if n == nil {
		return
	}
	switch n.Type {
	case html.ElementNode:
		renderElement(b, n, depth)
	case html.TextNode:
		writeMarkdown(b, n.Data)
	case html.DocumentNode:
		// `html.Parse` returns a DocumentNode root; recurse the
		// document's children (html → head/body → content).
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walkMarkdown(b, c, depth)
		}
	}
}

// renderElement dispatches each supported element tag to its
// markdown mapping and recurses into children. Unknown elements fall
// back to rendering their children inline (a `span`, `em`, or `cite`
// that bluemonday preserved still carries meaningful text).
func renderElement(b *bytes.Buffer, n *html.Node, depth int) bool {
	switch n.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(n.Data[1]) - int('0')
		writeMarkdown(b, strings.Repeat("#", level))
		writeMarkdown(b, " ")
		renderChildrenInline(b, n)
		writeMarkdown(b, "\n\n")
	case "p":
		renderChildrenInline(b, n)
		writeMarkdown(b, "\n\n")
	case "ul", "ol":
		renderList(b, n, depth)
	case "li":
		renderListItem(b, n, depth)
	case "a":
		renderLink(b, n)
	case "pre":
		renderCodeBlock(b, n)
	case "code":
		// A bare <code> not inside <pre> (bluemonday keeps
		// <code> as an element). Render as inline code when the
		// parent is not a pre; the standalone case is fenced in
		// renderCodeBlock.
		if n.Parent != nil && n.Parent.Data == "pre" {
			renderCodeBlock(b, n)
		} else {
			writeMarkdown(b, "`")
			renderChildrenInline(b, n)
			writeMarkdown(b, "`")
		}
	case "blockquote":
		renderBlockquote(b, n, depth)
	case "hr":
		writeMarkdown(b, "---\n\n")
	case "br":
		writeMarkdown(b, "\n")
	case "table":
		renderTable(b, n)
	case "img":
		renderImage(b, n)
	case "div", "section", "article", "header", "footer", "main", "nav",
		 "aside", "figure", "figcaption":
		renderChildrenInline(b, n)
		writeMarkdown(b, "\n")
	case "span", "em", "strong", "i", "b", "u", "s", "sub", "sup", "small",
		 "mark", "abbr", "cite", "time", "q":
		// Inline elements render their content with no block
		// separator — the surrounding text context supplies any
		// spacing.
		renderChildrenInline(b, n)
	default:
		// Unknown / non-content element: still render children so
		// a sanitizer-preserved `nav`/`aside` text is not lost.
		renderChildrenInline(b, n)
	}
	return true
}

// renderChildrenInline writes the text content of n's subtree with no
// extra block markers. The terminal `\n` separator is handled by the
// caller so a `span` inside a heading does not double-append.
func renderChildrenInline(b *bytes.Buffer, n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.ElementNode:
			renderElement(b, c, 0)
		case html.TextNode:
			writeMarkdown(b, c.Data)
		}
	}
}

// renderList renders a ul/ol with its item children. The list is
// block-level; `depth` tracks nesting so nested lists indent by two
// spaces per level. The list is terminated with a blank line.
func renderList(b *bytes.Buffer, n *html.Node, depth int) {
	renderListItems(b, n, depth)
	writeMarkdown(b, "\n")
}

// renderListItems renders the <li> children of a list element with no
// terminal blank line. `renderList` supplies the separator; a nested
// list rendered inside an item must not add its own blank line.
func renderListItems(b *bytes.Buffer, n *html.Node, depth int) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "li" {
			renderListItem(b, c, depth)
		}
	}
}

// renderListItem writes one list item: the bullet (ordered or
// unordered), the non-list child content inline, and nested lists on
// their own lines two spaces deeper.
func renderListItem(b *bytes.Buffer, n *html.Node, depth int) {
	ordered := n.Parent != nil && n.Parent.Data == "ol"
	bullet := "- "
	if ordered {
		bullet = "1. "
	}
	indent := strings.Repeat("  ", depth)
	writeMarkdown(b, indent)
	writeMarkdown(b, bullet)
	// First pass: inline content (text and non-list elements).
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || (c.Data != "ul" && c.Data != "ol") {
			writeNodeInline(b, c)
		}
	}
	writeMarkdown(b, "\n")
	// Second pass: nested lists at depth+1 on their own lines.
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (c.Data == "ul" || c.Data == "ol") {
			renderListItems(b, c, depth+1)
		}
	}
}

// writeNodeInline writes a single node as inline content: text as-is,
// an element via its renderElement mapping (which is itself inline for
// inline elements; a block element inside a list item is rendered with
// whatever separators its own case adds — a documented property of the
// closed subset).
func writeNodeInline(b *bytes.Buffer, n *html.Node) {
	if n == nil {
		return
	}
	switch n.Type {
	case html.ElementNode:
		renderElement(b, n, 0)
	case html.TextNode:
		writeMarkdown(b, n.Data)
	}
}

// renderLink writes "[text](href)" when the href is an http(s) URL,
// and plain text otherwise. bluemonday already removed non-http(s)
// schemes (and added rel="nofollow" to fully-qualified links), so the
// scheme check here is a second line of defense and a behavior we pin
// with a test, not the primary gate.
func renderLink(b *bytes.Buffer, n *html.Node) {
	text := gatherText(n)
	href := attr(n, "href")
	if href != "" && (strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://")) {
		writeMarkdown(b, "[")
		writeMarkdown(b, markdownEscape(text))
		writeMarkdown(b, "](")
		writeMarkdown(b, href)
		writeMarkdown(b, ")")
	} else {
		writeMarkdown(b, text)
	}
}

// renderCodeBlock writes a fenced code block. The code text is
// grabbed as inner text so entities are already decoded by the
// parser; the fence is a fixed ``` marker (the subset does not
// sniff a language class — bluemonday strips unknown attributes, and
// guessing a language in the prompt is not this renderer's job).
func renderCodeBlock(b *bytes.Buffer, n *html.Node) {
	code := strings.TrimSpace(gatherText(n))
	if code == "" {
		return
	}
	writeMarkdown(b, "```\n")
	writeMarkdown(b, code)
	writeMarkdown(b, "\n```\n\n")
}

// renderBlockquote writes a "> " prefixed blockquote at the given
// nesting depth.
func renderBlockquote(b *bytes.Buffer, n *html.Node, depth int) {
	text := strings.TrimSpace(gatherText(n))
	if text == "" {
		return
	}
	prefix := strings.Repeat("> ", depth+1)
	writeMarkdown(b, prefix)
	writeMarkdown(b, text)
	writeMarkdown(b, "\n\n")
}

// renderTable writes one row per line with " | " cell separation.
// The first row is treated as a header (consistent with the common
// markdown dialect). HTML5 parsing wraps rows in <thead>/<tbody>/
// <tfoot>, so rows are collected from any depth.
func renderTable(b *bytes.Buffer, n *html.Node) {
	var rows []*html.Node
	collectRows(n, &rows)
	for _, tr := range rows {
		var cells []string
		for tc := tr.FirstChild; tc != nil; tc = tc.NextSibling {
			if tc.Type == html.ElementNode && (tc.Data == "td" || tc.Data == "th") {
				cells = append(cells, strings.TrimSpace(gatherText(tc)))
			}
		}
		writeMarkdown(b, strings.Join(cells, " | "))
		writeMarkdown(b, "\n")
	}
	writeMarkdown(b, "\n")
}

// collectRows appends every <tr> descendant of `n` to out, skipping
// the <thead>/<tbody>/<tfoot> row-group wrappers the HTML5 parser
// inserts.
func collectRows(n *html.Node, out *[]*html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		if c.Data == "tr" {
			*out = append(*out, c)
		} else {
			collectRows(c, out)
		}
	}
}

// renderImage renders an img's alt text (the semantic content a
// reader can consume), or nothing when the alt is absent. Images are
// never linked into the markdown because the LLM prompt cannot render
// them and the URL would be an untrusted attribution target. Adjacent
// text nodes provide any spacing; the renderer adds none so an img at
// the end of a paragraph does not dangle a trailing space.
func renderImage(b *bytes.Buffer, n *html.Node) {
	if alt := attr(n, "alt"); alt != "" {
		writeMarkdown(b, alt)
	}
}

// attr returns the value of attribute `key` on an element node, or
// "" when absent.
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// gatherText returns the concatenated text content of n's subtree,
// trimming surrounding whitespace. The text is already-decoded
// (the parser unescapes entities), so the result is safe to emit
// inside markdown backticks but must be markdownEscaped when embedded
// in link labels.
func gatherText(n *html.Node) string {
	var b bytes.Buffer
	collectText(&b, n)
	return strings.TrimSpace(b.String())
}

func collectText(b *bytes.Buffer, n *html.Node) {
	if n == nil {
		return
	}
	switch n.Type {
	case html.TextNode:
		writeMarkdown(b, n.Data)
	case html.ElementNode:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collectText(b, c)
		}
	}
}

// markdownEscape escapes the characters that would otherwise break
// out of an inline markdown context: `[` / `]` / `(` / `)` in link
// text, and backticks. The escape is intentionally minimal — the
// renderer's appeal is that untrusted *text* is never interpreted as
// markdown structure.
func markdownEscape(s string) string {
	var b bytes.Buffer
	for i := 0; i < len(s); i++ {
		c := s[i]
		// Go switch cases do not fall through, so the escape char
		// and the original use a plain if rather than a case.
		if c == '[' || c == ']' || c == '(' || c == ')' || c == '`' {
			writeMarkdown(&b, "\\")
		}
		writeMarkdown(&b, string(c))
	}
	return b.String()
}