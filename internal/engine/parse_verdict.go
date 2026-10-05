// ADR-0012 follow-up: a single generic helper
// `parseVerdictJSON[T]` consolidates the two
// near-clone brace scanners in `evaluate.go` and
// `compare.go` (M3-T1 added them separately, when
// the two parsers' structure was still evolving).
//
// Both parsers share the same shape:
//
//  1. Find the first '{' in the LLM's content
//     (lenient about wrapping prose / code fences).
//  2. Track depth (with quote + escape handling)
//     to find the matching '}'.
//  3. Unmarshal the substring into the verdict
//     type.
//
// M3-T7.5 adds a tolerant decode. The M3-T7 smoke run
// showed that `format: "json"` guarantees *parseable*
// JSON, not *typed* JSON: the 9B judge emitted
// `"confidence": "0.95"` (string) and
// `"style_issues": "..."` (string, not array), and the
// strict decode hard-failed the whole job. The helper
// now retries once with a reflection-based coercion of
// the common drift (string↔number, string→[string]) and
// reports every coercion so the caller can audit it
// without hiding it. The strict fast path runs first, so
// a conforming model never pays the reflection cost.
package engine

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// parseVerdictJSON extracts the first JSON object in
// `content` and unmarshals it into a value of type T,
// discarding the coercion report. Retained for the
// fuzz/test surface; the engine calls
// parseVerdictJSONCoerced so it can audit drift.
func parseVerdictJSON[T any](content string) (T, error) {
	v, _, err := parseVerdictJSONCoerced[T](content)
	return v, err
}

// parseVerdictJSONCoerced is parseVerdictJSON plus a
// tolerant second pass. It returns the decoded verdict
// and the list of coercions applied — empty when the
// model conformed. Errors are returned as a wrapped
// *VerdictParseError so the caller can branch with
// errors.As.
func parseVerdictJSONCoerced[T any](content string) (T, []string, error) {
	var v T
	obj, err := extractVerdictObject(content)
	if err != nil {
		return v, nil, err
	}
	// Strict fast path: a conforming model never pays for reflection.
	if err := json.Unmarshal(obj, &v); err == nil {
		return v, nil, nil
	}
	// Tolerant path: decode the object generically, coerce each field to
	// the destination type, then decode again.
	var raw map[string]any
	if err := json.Unmarshal(obj, &raw); err != nil {
		return v, nil, &VerdictParseError{Msg: fmt.Sprintf("decoding verdict JSON: %v", err)}
	}
	notes, cerr := coerceMapInto(raw, &v)
	if cerr != nil {
		return v, nil, &VerdictParseError{Msg: fmt.Sprintf("decoding verdict JSON: %v", cerr)}
	}
	return v, notes, nil
}

// extractVerdictObject returns the first balanced JSON
// object in content. The matching '}' is found by
// tracking brace depth with quote + escape handling (an
// embedded '}' inside a string does not count).
func extractVerdictObject(content string) ([]byte, error) {
	start := strings.IndexByte(content, '{')
	if start < 0 {
		return nil, &VerdictParseError{Msg: fmt.Sprintf("no JSON object in verdict: %q", content)}
	}
	depth, end := 0, -1
	inStr, escape := false, false
	for i := start; i < len(content); i++ {
		c := content[i]
		if escape {
			escape = false
			continue
		}
		if c == '\\' && inStr {
			escape = true
			continue
		}
		if c == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return nil, &VerdictParseError{Msg: fmt.Sprintf("unterminated JSON object in verdict: %q", content)}
	}
	return []byte(content[start : end+1]), nil
}

// coerceMapInto coerces raw's values to the field types
// of *dst (a pointer to struct with json tags) and
// unmarshals. It returns the coercion notes, one per
// field that needed fixing.
func coerceMapInto(raw map[string]any, dst any) ([]string, error) {
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("coercion target must be a pointer to struct")
	}
	rt := rv.Elem().Type()
	var notes []string
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		name := jsonFieldName(f)
		if name == "" {
			continue
		}
		val, ok := raw[name]
		if !ok {
			continue
		}
		coerced, note := coerceValue(val, f.Type)
		if note != "" {
			notes = append(notes, name+" ("+note+")")
		}
		raw[name] = coerced
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return notes, err
	}
	return notes, json.Unmarshal(b, dst)
}

// jsonFieldName returns the JSON name of a struct field,
// or "" when the field is unexported, untagged, or "-".
func jsonFieldName(f reflect.StructField) string {
	tag, ok := f.Tag.Lookup("json")
	if !ok || tag == "" {
		return ""
	}
	name := strings.Split(tag, ",")[0]
	if name == "" || name == "-" {
		return ""
	}
	return name
}

// coerceValue coerces v toward the target type t and
// returns a human-readable note when a change was made.
// It only handles the drift shapes seen in practice;
// anything else is returned unchanged for json.Unmarshal
// to accept or reject.
func coerceValue(v any, t reflect.Type) (any, string) {
	if v == nil {
		return nil, ""
	}
	switch t.Kind() {
	case reflect.Float32, reflect.Float64:
		switch x := v.(type) {
		case float64:
			return x, ""
		case string:
			if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
				return f, "string→number"
			}
		case bool:
			if x {
				return float64(1), "bool→number"
			}
			return float64(0), "bool→number"
		}
	case reflect.Int, reflect.Int64:
		switch x := v.(type) {
		case float64:
			return x, ""
		case string:
			if n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil {
				return n, "string→int"
			}
		}
	case reflect.Bool:
		switch x := v.(type) {
		case bool:
			return x, ""
		case string:
			if b, err := strconv.ParseBool(strings.TrimSpace(x)); err == nil {
				return b, "string→bool"
			}
		case float64:
			return x != 0, "number→bool"
		}
	case reflect.String:
		switch x := v.(type) {
		case string:
			return x, ""
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64), "number→string"
		case bool:
			return strconv.FormatBool(x), "bool→string"
		}
	case reflect.Slice:
		if t.Elem().Kind() != reflect.String {
			return v, ""
		}
		switch x := v.(type) {
		case string:
			return []string{x}, "string→[]string"
		case []any:
			out := make([]string, 0, len(x))
			changed := false
			for _, e := range x {
				if s, ok := e.(string); ok {
					out = append(out, s)
				} else {
					out = append(out, fmt.Sprint(e))
					changed = true
				}
			}
			if changed {
				return out, "elements→string"
			}
			return out, ""
		}
	}
	return v, ""
}

// VerdictParseError is the typed error
// parseVerdictJSON returns. Callers can use
// errors.As to branch on the specific failure
// mode (e.g. compare.go's "unknown winner" path
// uses the same package-level error type, but
// can be matched separately from this one).
type VerdictParseError struct {
	Msg string
}

func (e *VerdictParseError) Error() string { return e.Msg }
