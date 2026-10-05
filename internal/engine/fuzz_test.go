package engine

import "testing"

// FuzzParseVerdictJSON asserts the shared brace scanner (ADR-0012) never
// panics on adversarial model output: deep nesting, unbalanced braces,
// embedded quotes/escapes, and arbitrary bytes. The parsed value is not
// inspected — the property is memory safety on untrusted input, which is
// what the scanner faces because the judge's text is not trusted.
func FuzzParseVerdictJSON(f *testing.F) {
	f.Add(`{"passed":true,"score":0.9}`)
	f.Add("prose before {\"winner\":\"new\"} prose after")
	f.Add("```json\n{\"winner\":\"none\"}\n```")
	f.Add("{")
	f.Add("}")
	f.Add(`{"a":"}"}`)
	f.Add(`{"a":"\\"}`)
	f.Add("")
	f.Add(string([]byte{0xff, 0xfe, '{', 0x00}))
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = parseVerdictJSON[evalVerdict](s)
	})
}
