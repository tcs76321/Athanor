package gateway

import (
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// Feed parsing bounds. A feed is a bounded list of items, not a document: a
// large response can still describe thousands of entries, and the report only
// needs the current window.
const (
	maxFeedItems        = 50
	maxFeedSummaryBytes = 1000
	maxFeedTitleBytes   = 300
)

// feedItem is the normalized shape both RSS and Atom map into.
type feedItem struct {
	ID      string
	Title   string
	Link    string
	Date    string
	Summary string
}

// feedDoc is the parsed feed before rendering.
type feedDoc struct {
	Title string
	Items []feedItem
}

// feedEnvelope accepts the three shapes Athanor reads: RSS 2.0 (`<rss>`),
// Atom (`<feed>`), and RSS 1.0 (`<rdf:RDF>`). encoding/xml matches local names,
// so namespace prefixes do not matter.
type feedEnvelope struct {
	XMLName xml.Name
	Title   string `xml:"title"`
	Channel struct {
		Title string    `xml:"title"`
		Items []rawItem `xml:"item"`
	} `xml:"channel"`
	Items   []rawItem  `xml:"item"`
	Entries []rawEntry `xml:"entry"`
}

type rawItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Date        string `xml:"date"`
	Description string `xml:"description"`
	About       string `xml:"about,attr"`
}

type rawEntry struct {
	ID      string `xml:"id"`
	Title   string `xml:"title"`
	Updated string `xml:"updated"`
	Summary string `xml:"summary"`
	Links   []struct {
		Href string `xml:"href,attr"`
		Rel  string `xml:"rel,attr"`
	} `xml:"link"`
}

// parseFeed recognizes the feed shape and normalizes it. encoding/xml does not
// expand external entities; a malformed document fails closed.
func parseFeed(body []byte) (feedDoc, error) {
	var env feedEnvelope
	if err := xml.Unmarshal(body, &env); err != nil {
		return feedDoc{}, fmt.Errorf("feed: parse: %w", err)
	}
	switch strings.ToLower(env.XMLName.Local) {
	case "rss":
		return fromRSS(env), nil
	case "feed":
		return fromAtom(env), nil
	case "rdf":
		return fromRDF(env), nil
	default:
		return feedDoc{}, fmt.Errorf("feed: unsupported root element %q", env.XMLName.Local)
	}
}

func fromRSS(env feedEnvelope) feedDoc {
	out := feedDoc{Title: cleanText(env.Channel.Title, maxFeedTitleBytes)}
	for _, it := range env.Channel.Items {
		if len(out.Items) >= maxFeedItems {
			break
		}
		out.Items = append(out.Items, feedItem{
			ID:      cleanText(firstNonEmpty(it.GUID, it.Link), maxFeedTitleBytes),
			Title:   cleanText(it.Title, maxFeedTitleBytes),
			Link:    cleanText(it.Link, maxFeedTitleBytes),
			Date:    cleanText(firstNonEmpty(it.PubDate, it.Date), 80),
			Summary: cleanText(it.Description, maxFeedSummaryBytes),
		})
	}
	return out
}

func fromAtom(env feedEnvelope) feedDoc {
	out := feedDoc{Title: cleanText(env.Title, maxFeedTitleBytes)}
	for _, e := range env.Entries {
		if len(out.Items) >= maxFeedItems {
			break
		}
		link := ""
		for _, l := range e.Links {
			if l.Href == "" {
				continue
			}
			if link == "" || l.Rel == "alternate" {
				link = l.Href
			}
			if l.Rel == "alternate" {
				break
			}
		}
		out.Items = append(out.Items, feedItem{
			ID:      cleanText(firstNonEmpty(e.ID, link), maxFeedTitleBytes),
			Title:   cleanText(e.Title, maxFeedTitleBytes),
			Link:    cleanText(link, maxFeedTitleBytes),
			Date:    cleanText(e.Updated, 80),
			Summary: cleanText(e.Summary, maxFeedSummaryBytes),
		})
	}
	return out
}

func fromRDF(env feedEnvelope) feedDoc {
	out := feedDoc{Title: cleanText(env.Channel.Title, maxFeedTitleBytes)}
	items := env.Channel.Items
	if len(items) == 0 {
		items = env.Items
	}
	for _, it := range items {
		if len(out.Items) >= maxFeedItems {
			break
		}
		out.Items = append(out.Items, feedItem{
			ID:      cleanText(firstNonEmpty(it.About, it.Link), maxFeedTitleBytes),
			Title:   cleanText(it.Title, maxFeedTitleBytes),
			Link:    cleanText(it.Link, maxFeedTitleBytes),
			Date:    cleanText(firstNonEmpty(it.Date, it.PubDate), 80),
			Summary: cleanText(it.Description, maxFeedSummaryBytes),
		})
	}
	return out
}

// renderFeed renders a normalized feed as deterministic markdown: one bullet
// per item with id/title/date/link and a sanitized summary. The caller has
// already run the injection scan over the rendered text.
func renderFeed(doc feedDoc) string {
	var b strings.Builder
	if doc.Title != "" {
		b.WriteString("# Feed: ")
		b.WriteString(doc.Title)
		b.WriteString("\n\n")
	}
	if len(doc.Items) == 0 {
		b.WriteString("(no items in this feed)\n")
		return b.String()
	}
	for _, it := range doc.Items {
		b.WriteString("- id: ")
		b.WriteString(orDash(it.ID))
		b.WriteString("\n  title: ")
		b.WriteString(orDash(it.Title))
		if it.Date != "" {
			b.WriteString("\n  date: ")
			b.WriteString(it.Date)
		}
		if it.Link != "" {
			b.WriteString("\n  link: ")
			b.WriteString(it.Link)
		}
		if it.Summary != "" {
			b.WriteString("\n  summary: ")
			b.WriteString(it.Summary)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// cleanText strips markup from a feed field, collapses whitespace, and caps
// its length. Feed summaries are attacker-controlled HTML; bluemonday's
// strict policy leaves text only.
func cleanText(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "<") {
		s = bluemonday.StrictPolicy().Sanitize(s)
	}
	s = strings.Join(strings.Fields(s), " ")
	if max > 0 && len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
