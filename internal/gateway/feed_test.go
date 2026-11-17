package gateway

import (
	"strings"
	"testing"
)

func TestParseFeedRSS(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
<rss version="2.0"><channel>
  <title>Example Wire</title>
  <item>
    <title><![CDATA[Launch & update]]></title>
    <link>https://wire.test/story-001</link>
    <guid>story-001</guid>
    <pubDate>Tue, 17 Nov 2026 07:00:00 GMT</pubDate>
    <description><![CDATA[<p>A <b>new</b> thing.</p><script>alert(1)</script>]]></description>
  </item>
  <item>
    <title>Second</title>
    <link>https://wire.test/story-002</link>
    <guid>story-002</guid>
    <description>Another item.</description>
  </item>
</channel></rss>`)
	doc, err := parseFeed(body)
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	if doc.Title != "Example Wire" {
		t.Errorf("title = %q", doc.Title)
	}
	if len(doc.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(doc.Items))
	}
	first := doc.Items[0]
	if first.ID != "story-001" || first.Link != "https://wire.test/story-001" {
		t.Errorf("first = %+v", first)
	}
	if !strings.Contains(first.Summary, "A new thing.") || strings.Contains(first.Summary, "<") {
		t.Errorf("summary not sanitized: %q", first.Summary)
	}
	md := renderFeed(doc)
	for _, want := range []string{"# Feed: Example Wire", "- id: story-001", "title: Launch & update", "date: Tue, 17 Nov 2026", "link: https://wire.test/story-001"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestParseFeedAtom(t *testing.T) {
	body := []byte(`<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Atom Wire</title>
  <entry>
    <id>tag:wire.test,2026:1</id>
    <title>Atom story</title>
    <updated>2026-11-17T07:00:00Z</updated>
    <summary>Summary text.</summary>
    <link rel="alternate" href="https://wire.test/a1"/>
  </entry>
</feed>`)
	doc, err := parseFeed(body)
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	if doc.Title != "Atom Wire" || len(doc.Items) != 1 {
		t.Fatalf("doc = %+v", doc)
	}
	if doc.Items[0].Link != "https://wire.test/a1" || doc.Items[0].ID != "tag:wire.test,2026:1" {
		t.Errorf("item = %+v", doc.Items[0])
	}
}

func TestParseFeedRDF(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns="http://purl.org/rss/1.0/">
  <channel><title>RDF Wire</title></channel>
  <item rdf:about="https://wire.test/r1">
    <title>RDF story</title>
    <link>https://wire.test/r1</link>
    <description>RDF item.</description>
  </item>
</rdf:RDF>`)
	doc, err := parseFeed(body)
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	if doc.Title != "RDF Wire" || len(doc.Items) != 1 || doc.Items[0].ID != "https://wire.test/r1" {
		t.Fatalf("doc = %+v", doc)
	}
}

func TestParseFeedEmptyAndUnsupported(t *testing.T) {
	doc, err := parseFeed([]byte(`<rss><channel><title>Empty</title></channel></rss>`))
	if err != nil {
		t.Fatalf("empty feed: %v", err)
	}
	if !strings.Contains(renderFeed(doc), "no items") {
		t.Errorf("empty feed markdown = %q", renderFeed(doc))
	}
	if _, err := parseFeed([]byte(`<html><body>x</body></html>`)); err == nil {
		t.Fatal("HTML accepted as a feed")
	}
}

func TestParseFeedCapsItemsAndFields(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<rss><channel><title>Big</title>`)
	for i := 0; i < maxFeedItems+10; i++ {
		b.WriteString(`<item><title>`)
		b.WriteString(strings.Repeat("t", maxFeedTitleBytes+20))
		b.WriteString(`</title><guid>id</guid></item>`)
	}
	b.WriteString(`</channel></rss>`)
	doc, err := parseFeed([]byte(b.String()))
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	if len(doc.Items) != maxFeedItems {
		t.Errorf("items = %d, want %d", len(doc.Items), maxFeedItems)
	}
	if len(doc.Items[0].Title) > maxFeedTitleBytes+len("…") {
		t.Errorf("title not capped: %d bytes", len(doc.Items[0].Title))
	}
}
