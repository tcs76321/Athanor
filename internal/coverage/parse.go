package coverage

import (
	"encoding/json"
	"strings"
)

// fence is the block the retain contract asks for. It is deliberately
// explicit and rare in prose, so a model cannot accidentally trip it.
const fence = "```coverage"

// maxItems bounds one ledger write.
const maxItems = 500

// ParseItems reads the fenced coverage JSON out of a retain artifact. It is
// best-effort: a missing or malformed block returns nil, and the cycle still
// completes (the next run simply has no new keys to suppress).
func ParseItems(text string) []Item {
	start := strings.Index(text, fence)
	if start < 0 {
		return nil
	}
	rest := text[start+len(fence):]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	} else {
		return nil
	}
	end := strings.Index(rest, "```")
	if end < 0 {
		return nil
	}
	raw := strings.TrimSpace(rest[:end])
	if raw == "" {
		return nil
	}
	var doc struct {
		Items []struct {
			ID      string   `json:"id"`
			Key     string   `json:"key"`
			Title   string   `json:"title"`
			Sources []string `json:"sources"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil
	}
	out := make([]Item, 0, len(doc.Items))
	seen := map[string]bool{}
	for _, it := range doc.Items {
		key := strings.TrimSpace(firstNonEmpty(it.ID, it.Key))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Item{Key: key, Title: strings.TrimSpace(it.Title), Sources: it.Sources})
		if len(out) >= maxItems {
			break
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// FormatItems renders the ledger write a retain artifact should carry. It is
// used by templates/tests; the model is asked to emit the same shape.
func FormatItems(items []Item) string {
	payload := struct {
		Items []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"items"`
	}{}
	for _, it := range items {
		payload.Items = append(payload.Items, struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}{ID: it.Key, Title: it.Title})
	}
	raw, _ := json.Marshal(payload)
	return fence + "\n" + string(raw) + "\n```"
}
