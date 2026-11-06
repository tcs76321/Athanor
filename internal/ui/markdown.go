package ui

// A small, safe Markdown renderer for artifact and plan reading. It is
// deliberately not a full CommonMark implementation: it covers the constructs
// Athanor produces (headings, paragraphs, lists, fenced code, inline code,
// emphasis, links, blockquotes, tables, horizontal rules) and escapes every
// byte of source HTML. A strict allowlist (no raw HTML, http/https/mailto
// links only, sanitized code-language classes) keeps model output from
// becoming an injection vector in the UI.
//
// The package already depends on html/template; returning template.HTML is the
// explicit "this string is trusted" assertion at the one place that builds it.

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"

	"html/template"
)

var (
	mdHeading  = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	mdFence    = regexp.MustCompile("^```([A-Za-z0-9_+.#-]*)\\s*$")
	mdUL       = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
	mdOL       = regexp.MustCompile(`^(\s*)\d+\.\s+(.*)$`)
	mdHR       = regexp.MustCompile(`^\s*(?:-{3,}|\*{3,}|_{3,})\s*$`)
	mdTableSep = regexp.MustCompile(`^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)+\|?\s*$`)
	mdLink     = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	mdBold     = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__`)
	mdItalic   = regexp.MustCompile(`\*([^*\n]+)\*|_([^_\n]+)_`)
	mdCode     = regexp.MustCompile("`([^`]+)`")
	mdSlug     = regexp.MustCompile(`[^a-z0-9]+`)
)

// renderMarkdown renders Markdown source to safe HTML.
func renderMarkdown(src string) template.HTML {
	var b strings.Builder
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	ids := map[string]int{}
	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			i++
			continue
		}
		// fenced code
		if m := mdFence.FindStringSubmatch(trimmed); m != nil {
			lang := strings.ToLower(m[1])
			i++
			var code []string
			for i < len(lines) && strings.TrimSpace(lines[i]) != "```" {
				code = append(code, lines[i])
				i++
			}
			if i < len(lines) {
				i++
			}
			b.WriteString("<pre>")
			if lang != "" {
				fmt.Fprintf(&b, `<code class="language-%s">`, html.EscapeString(lang))
			} else {
				b.WriteString("<code>")
			}
			b.WriteString(html.EscapeString(strings.Join(code, "\n")))
			b.WriteString("</code></pre>\n")
			continue
		}
		// heading
		if m := mdHeading.FindStringSubmatch(trimmed); m != nil {
			level := len(m[1])
			text := renderInline(m[2])
			id := slugify(stripTags(string(renderInline(m[2]))))
			if id == "" {
				id = "section"
			}
			if n := ids[id]; n > 0 {
				ids[id] = n + 1
				id = fmt.Sprintf("%s-%d", id, n+1)
			} else {
				ids[id] = 1
			}
			fmt.Fprintf(&b, `<h%d id="%s">%s</h%d>`+"\n", level, id, text, level)
			i++
			continue
		}
		// horizontal rule
		if mdHR.MatchString(line) {
			b.WriteString("<hr>\n")
			i++
			continue
		}
		// blockquote
		if strings.HasPrefix(trimmed, ">") {
			var quote []string
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">") {
				quote = append(quote, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), ">")))
				i++
			}
			b.WriteString("<blockquote>\n")
			b.WriteString(string(renderMarkdown(strings.Join(quote, "\n"))))
			b.WriteString("</blockquote>\n")
			continue
		}
		// table: header row followed by a separator row
		if i+1 < len(lines) && strings.Contains(line, "|") && mdTableSep.MatchString(lines[i+1]) {
			header := splitTableRow(line)
			i += 2
			var rows [][]string
			for i < len(lines) && strings.Contains(lines[i], "|") && strings.TrimSpace(lines[i]) != "" {
				rows = append(rows, splitTableRow(lines[i]))
				i++
			}
			b.WriteString("<table>\n<thead><tr>")
			for _, h := range header {
				fmt.Fprintf(&b, "<th>%s</th>", renderInline(h))
			}
			b.WriteString("</tr></thead>\n<tbody>\n")
			for _, row := range rows {
				b.WriteString("<tr>")
				for _, c := range row {
					fmt.Fprintf(&b, "<td>%s</td>", renderInline(c))
				}
				b.WriteString("</tr>\n")
			}
			b.WriteString("</tbody></table>\n")
			continue
		}
		// unordered / ordered list
		if mdUL.MatchString(line) || mdOL.MatchString(line) {
			ordered := mdOL.MatchString(line)
			b.WriteString("<")
			if ordered {
				b.WriteString("ol")
			} else {
				b.WriteString("ul")
			}
			b.WriteString(">\n")
			for i < len(lines) {
				if ordered {
					m := mdOL.FindStringSubmatch(lines[i])
					if m == nil {
						break
					}
					fmt.Fprintf(&b, "<li>%s</li>\n", renderInline(strings.TrimSpace(m[2])))
				} else {
					m := mdUL.FindStringSubmatch(lines[i])
					if m == nil {
						break
					}
					fmt.Fprintf(&b, "<li>%s</li>\n", renderInline(strings.TrimSpace(m[2])))
				}
				i++
			}
			if ordered {
				b.WriteString("</ol>\n")
			} else {
				b.WriteString("</ul>\n")
			}
			continue
		}
		// paragraph
		var para []string
		for i < len(lines) {
			t := strings.TrimSpace(lines[i])
			if t == "" || mdHeading.MatchString(t) || mdFence.MatchString(t) ||
				mdUL.MatchString(t) || mdOL.MatchString(t) || mdHR.MatchString(t) ||
				strings.HasPrefix(t, ">") || mdTableSep.MatchString(t) {
				break
			}
			para = append(para, t)
			i++
		}
		b.WriteString("<p>")
		b.WriteString(renderInline(strings.Join(para, " ")))
		b.WriteString("</p>\n")
	}
	return template.HTML(b.String())
}

// renderInline escapes text and applies the closed inline set. Escaping
// happens first, so no source HTML can survive.
func renderInline(s string) string {
	// Code spans are extracted first so emphasis/link rules cannot touch them.
	var codes []string
	s = mdCode.ReplaceAllStringFunc(s, func(m string) string {
		body := strings.Trim(m, "`")
		codes = append(codes, "<code>"+html.EscapeString(body)+"</code>")
		return fmt.Sprintf("\x00%d\x00", len(codes)-1)
	})
	// Links are extracted before HTML escaping. Parsing them after escaping
	// double-escaped every `&` in a query string (`?a=1&b=2` became
	// `?a=1&amp;amp;b=2`), so a copied URL was wrong.
	var links []string
	s = mdLink.ReplaceAllStringFunc(s, func(m string) string {
		sub := mdLink.FindStringSubmatch(m)
		if sub == nil {
			return m
		}
		href := strings.TrimSpace(sub[2])
		u, err := url.Parse(href)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "mailto") {
			return sub[1]
		}
		links = append(links, fmt.Sprintf(`<a href="%s" rel="noopener noreferrer" target="_blank">%s</a>`,
			html.EscapeString(href), html.EscapeString(sub[1])))
		return fmt.Sprintf("\x01%d\x01", len(links)-1)
	})
	s = html.EscapeString(s)
	for i, l := range links {
		s = strings.ReplaceAll(s, fmt.Sprintf("\x01%d\x01", i), l)
	}
	s = mdBold.ReplaceAllStringFunc(s, func(m string) string {
		sub := mdBold.FindStringSubmatch(m)
		if sub == nil {
			return m
		}
		text := sub[1]
		if text == "" {
			text = sub[2]
		}
		return "<strong>" + text + "</strong>"
	})
	s = mdItalic.ReplaceAllStringFunc(s, func(m string) string {
		sub := mdItalic.FindStringSubmatch(m)
		if sub == nil {
			return m
		}
		text := sub[1]
		if text == "" {
			text = sub[2]
		}
		return "<em>" + text + "</em>"
	})
	for i, c := range codes {
		s = strings.ReplaceAll(s, fmt.Sprintf("\x00%d\x00", i), c)
	}
	return s
}

func splitTableRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = mdSlug.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func stripTags(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch r {
		case '<':
			in = true
		case '>':
			in = false
		default:
			if !in {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
