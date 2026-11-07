package verify

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"

	"github.com/tcs76321/athanor/internal/project"
)

// dataChecksVerifier applies the array-level constraints a JSON Schema
// cannot express in the engine's supported subset: exact length, uniqueness,
// ordering, and per-field regex matches. It mirrors eval/bench's
// array_length / array_unique_by / array_sorted_by / json_field_matches, so
// the engine's acceptance edge and the external scorer agree about the same
// candidate. It is HARD: a declared data check is authoritative.
type dataChecksVerifier struct{}

func (dataChecksVerifier) Capabilities() []string { return []string{project.ArchetypeData} }
func (dataChecksVerifier) Name() string           { return "data-checks" }

func (dataChecksVerifier) Verify(in Input) Verdict {
	c := in.Checks
	if c == nil || (c.ArrayLength <= 0 && c.ArrayUniqueBy == "" &&
		c.ArraySortedBy == "" && len(c.JSONFieldMatches) == 0) {
		return Verdict{Verifier: "data-checks", Applied: false, Hard: true}
	}

	value, ok := parseJSONValue(in.Content)
	if !ok {
		return Verdict{
			Verifier: "data-checks", Applied: true, Hard: true, Pass: false, Score: 0,
			Reasons: []string{"data checks: output is not valid JSON"},
		}
	}
	// A length check applies to any array; the other checks require objects.
	arr, isArray := value.([]any)

	v := Verdict{Verifier: "data-checks", Applied: true, Hard: true}
	passed, total := 0, 0
	check := func(ok bool, reason string) {
		total++
		if ok {
			passed++
			return
		}
		v.Reasons = append(v.Reasons, reason)
	}

	if c.ArrayLength > 0 {
		check(isArray && len(arr) == c.ArrayLength,
			fmt.Sprintf("array_length: got %d, want %d", arrayLen(isArray, arr), c.ArrayLength))
	}

	if c.ArrayUniqueBy != "" {
		objs, objects := jsonObjectArray(value)
		unique := objects
		seen := make(map[string]bool, len(objs))
		for _, obj := range objs {
			key := fmt.Sprint(obj[c.ArrayUniqueBy])
			if seen[key] {
				unique = false
				break
			}
			seen[key] = true
		}
		check(objects && unique,
			"array_unique_by: duplicate "+c.ArrayUniqueBy+" value")
	}

	if c.ArraySortedBy != "" {
		objs, objects := jsonObjectArray(value)
		sorted := objects
		for i := 1; objects && i < len(objs); i++ {
			if !jsonLessOrEqual(objs[i-1][c.ArraySortedBy], objs[i][c.ArraySortedBy]) {
				sorted = false
				break
			}
		}
		check(objects && sorted,
			"array_sorted_by: values are not ascending by "+c.ArraySortedBy)
	}

	if len(c.JSONFieldMatches) > 0 {
		objs, objects := jsonObjectArray(value)
		keys := make([]string, 0, len(c.JSONFieldMatches))
		for key := range c.JSONFieldMatches {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		patterns := make(map[string]*regexp.Regexp, len(keys))
		patternsOK := true
		for _, key := range keys {
			re, err := regexp.Compile(c.JSONFieldMatches[key])
			if err != nil {
				patternsOK = false
				v.Reasons = append(v.Reasons, "json_field_matches: invalid pattern for "+key)
			} else {
				patterns[key] = re
			}
		}
		matched := objects && patternsOK
		if matched {
			for i, obj := range objs {
				for _, key := range keys {
					text, isString := obj[key].(string)
					if !isString || !patterns[key].MatchString(text) {
						matched = false
						v.Reasons = append(v.Reasons,
							fmt.Sprintf("json_field_matches: %s[%d]=%v does not match %s",
								key, i, obj[key], c.JSONFieldMatches[key]))
						break
					}
				}
				if !matched {
					break
				}
			}
		}
		check(matched, "json_field_matches: field values did not match")
	}

	if total == 0 {
		return Verdict{Verifier: "data-checks", Applied: false, Hard: true}
	}
	v.Pass = passed == total
	v.Score = float64(passed) / float64(total)
	if v.Pass {
		v.Reasons = []string{"all declared data checks passed"}
	}
	return v
}

// parseJSONValue decodes a candidate's JSON value, accepting the same
// fenced/prose wrapper the schema verifier accepts (ADR-0070 amendment).
func parseJSONValue(content string) (any, bool) {
	var value any
	if err := json.Unmarshal([]byte(content), &value); err == nil {
		return value, true
	}
	extracted, ok := extractJSONValue(content)
	if !ok {
		return nil, false
	}
	if err := json.Unmarshal([]byte(extracted), &value); err != nil {
		return nil, false
	}
	return value, true
}

// jsonObjectArray returns value as a list of objects. A non-array, or an
// array containing a non-object, is not usable by the keyed checks.
func jsonObjectArray(value any) ([]map[string]any, bool) {
	arr, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]map[string]any, 0, len(arr))
	for _, elem := range arr {
		obj, ok := elem.(map[string]any)
		if !ok {
			return nil, false
		}
		out = append(out, obj)
	}
	return out, true
}

func arrayLen(isArray bool, arr []any) int {
	if !isArray {
		return -1
	}
	return len(arr)
}

// jsonLessOrEqual compares two JSON scalar values (number or string) for the
// array_sorted_by check. Unknown types are not comparable and fail closed.
func jsonLessOrEqual(a, b any) bool {
	switch x := a.(type) {
	case float64:
		y, ok := b.(float64)
		return ok && x <= y
	case string:
		y, ok := b.(string)
		return ok && x <= y
	}
	return false
}
