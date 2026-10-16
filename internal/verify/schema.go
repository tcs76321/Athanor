package verify

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/tcs76321/athanor/internal/project"
)

// dataVerifier is the deterministic edge for the `data` archetype (M9-T3). It
// validates a candidate against a JSON Schema supplied on the input, so a
// `data` artifact can be accepted or rejected by a parser rather than the
// advisory LLM judge — the verifier-less archetype the F4 closeout flagged
// (ADR-0063). It applies only when a schema is present and a violation is a
// hard fail; a missing schema degrades to the judge rather than a free pass.
//
// The schema source is the operator's, not the model's: it comes from the
// project's execution config, never from the candidate.
type dataVerifier struct{}

func (dataVerifier) Archetypes() []string { return []string{project.ArchetypeData} }
func (dataVerifier) Name() string         { return "json-schema" }

func (dataVerifier) Verify(in Input) Verdict {
	schema := strings.TrimSpace(in.DataSchema)
	if schema == "" {
		return Verdict{Verifier: "json-schema", Applied: false, Hard: true}
	}
	if err := validateJSONSchema([]byte(schema), []byte(in.Content)); err != nil {
		return Verdict{
			Verifier: "json-schema", Applied: true, Hard: true, Pass: false, Score: 0,
			Reasons: []string{"json-schema violation: " + err.Error()},
		}
	}
	return Verdict{
		Verifier: "json-schema", Applied: true, Hard: true, Pass: true, Score: 1,
		Reasons: []string{"output validates against the declared JSON schema"},
	}
}

// validateJSONSchema checks a document against a schema. A schema that fails to
// parse, or a document that is not valid JSON, is an error. The supported
// subset is deliberately small — type, required, properties, items, enum,
// additionalProperties, and allOf/anyOf — which is enough for structured data
// and avoids a dependency.
func validateJSONSchema(schemaJSON, doc []byte) error {
	var schema map[string]any
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		return fmt.Errorf("schema is not valid JSON: %w", err)
	}
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return fmt.Errorf("output is not valid JSON: %w", err)
	}
	return validateNode(schema, v, "$")
}

func validateNode(schema map[string]any, v any, path string) error {
	if all, ok := schema["allOf"].([]any); ok {
		for _, sub := range all {
			if sm, ok := sub.(map[string]any); ok {
				if err := validateNode(sm, v, path); err != nil {
					return err
				}
			}
		}
	}
	if anyList, ok := schema["anyOf"].([]any); ok {
		matched := false
		for _, sub := range anyList {
			if sm, ok := sub.(map[string]any); ok {
				if validateNode(sm, v, path) == nil {
					matched = true
					break
				}
			}
		}
		if !matched {
			return fmt.Errorf("%s: no anyOf branch matched", path)
		}
	}
	if t, ok := schema["type"].(string); ok && !jsonTypeMatches(t, v) {
		return fmt.Errorf("%s: expected type %s, got %s", path, t, jsonTypeName(v))
	}
	if e, ok := schema["enum"].([]any); ok && !containsJSON(e, v) {
		return fmt.Errorf("%s: value %v is not in the enum", path, v)
	}

	switch val := v.(type) {
	case map[string]any:
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				name, _ := r.(string)
				if _, ok := val[name]; !ok {
					return fmt.Errorf("%s: missing required property %q", path, name)
				}
			}
		}
		props, _ := schema["properties"].(map[string]any)
		if props != nil {
			for k, sub := range props {
				cv, ok := val[k]
				if !ok {
					continue
				}
				if sm, ok := sub.(map[string]any); ok {
					if err := validateNode(sm, cv, path+"."+k); err != nil {
						return err
					}
				}
			}
			if ap, ok := schema["additionalProperties"].(bool); ok && !ap {
				for k := range val {
					if _, ok := props[k]; !ok {
						return fmt.Errorf("%s: unexpected property %q", path, k)
					}
				}
			}
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, e := range val {
				if err := validateNode(items, e, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func jsonTypeMatches(t string, v any) bool {
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == float64(int64(f))
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	default:
		return true
	}
}

func jsonTypeName(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		return "unknown"
	}
}

func containsJSON(list []any, v any) bool {
	for _, e := range list {
		if reflect.DeepEqual(e, v) {
			return true
		}
	}
	return false
}
