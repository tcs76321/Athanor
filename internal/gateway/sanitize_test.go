package gateway

import (
	"net/http"
	"reflect"
	"testing"
)

// TestSanitizeRequestHeaders_StripsDenyListed is the
// §21.5 §6 happy-path test: every name in the deny-list is
// removed, every other name survives.
func TestSanitizeRequestHeaders_StripsDenyListed(t *testing.T) {
	h := http.Header{}
	h.Set("Cookie", "session=secret")
	h.Set("Authorization", "Bearer secret")
	h.Set("Proxy-Authorization", "Basic secret")
	h.Set("X-Api-Key", "secret")
	h.Set("X-Auth-Token", "secret")
	h.Set("X-Secret", "secret")
	h.Set("User-Agent", "old-ua")
	h.Set("Accept", "text/html")

	removed := sanitizeRequestHeaders(h)
	wantRemoved := []string{
		"Authorization",
		"Cookie",
		"Proxy-Authorization",
		"X-Api-Key",
		"X-Auth-Token",
		"X-Secret",
	}
	if !reflect.DeepEqual(removed, wantRemoved) {
		t.Errorf("removed = %v, want %v", removed, wantRemoved)
	}
	for _, name := range wantRemoved {
		if got := h.Get(name); got != "" {
			t.Errorf("header %q survived sanitization: %q", name, got)
		}
	}
	if got := h.Get("User-Agent"); got != "old-ua" {
		t.Errorf("User-Agent stripped: %q", got)
	}
	if got := h.Get("Accept"); got != "text/html" {
		t.Errorf("Accept stripped: %q", got)
	}
}

// TestSanitizeRequestHeaders_CaseInsensitive pins the
// §21.5 case-insensitive matching: a header added with
// any case form is matched.
func TestSanitizeRequestHeaders_CaseInsensitive(t *testing.T) {
	for _, name := range []string{"cookie", "Cookie", "COOKIE", "cOoKiE"} {
		t.Run(name, func(t *testing.T) {
			h := http.Header{}
			h[name] = []string{"value"}
			removed := sanitizeRequestHeaders(h)
			if len(removed) != 1 {
				t.Fatalf("removed = %v, want 1 entry", removed)
			}
		})
	}
}

// TestSanitizeRequestHeaders_EmptyInput pins the
// "empty header is a no-op" property: a request with no
// outgoing headers is returned untouched.
func TestSanitizeRequestHeaders_EmptyInput(t *testing.T) {
	h := http.Header{}
	removed := sanitizeRequestHeaders(h)
	if len(removed) != 0 {
		t.Errorf("removed = %v, want nil", removed)
	}
	if len(h) != 0 {
		t.Errorf("h is not empty: %v", h)
	}
}

// TestSanitizeRequestHeaders_AllowList pins the allow-list contract
// (known-issues L12): a non-allowlisted header is stripped, an allowlisted one
// passes through verbatim.
func TestSanitizeRequestHeaders_AllowList(t *testing.T) {
	h := http.Header{}
	h.Set("X-Safe-Custom", "Bearer look-at-me-im-a-secret")
	h.Set("Accept", "text/html")
	removed := sanitizeRequestHeaders(h)
	if len(removed) != 1 || removed[0] != "X-Safe-Custom" {
		t.Errorf("removed = %v, want [X-Safe-Custom]", removed)
	}
	if got := h.Get("X-Safe-Custom"); got != "" {
		t.Errorf("non-allowlisted header survived: %q", got)
	}
	if got := h.Get("Accept"); got != "text/html" {
		t.Errorf("allowlisted Accept altered/stripped: %q", got)
	}
}

// TestSortStrings is a small unit test for the
// micro-optimization. The function is short and the
// test exists to pin the sort order across changes.
func TestSortStrings(t *testing.T) {
	s := []string{"Cookie", "Authorization", "Proxy-Authorization", "X-Api-Key"}
	sortStrings(s)
	want := []string{"Authorization", "Cookie", "Proxy-Authorization", "X-Api-Key"}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("sortStrings = %v, want %v", s, want)
	}
}
