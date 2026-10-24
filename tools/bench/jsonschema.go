// Minimal JSON Schema validation for the bench corpus (M8-T14 support). The
// data tasks declare a `json_schema:` check; this turns it into a deterministic
// pass/fail. The supported subset is deliberately small — type, required,
// properties, items, enum, additionalProperties, and the composition keywords
// allOf/anyOf — which is enough for the corpus and avoids a dependency (the
// daemon's acceptance stays in internal/verify; this scores the benchmark).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
)

// validateJSONSchema checks a document against a schema. A schema that fails to
// parse, or a document that is not valid JSON, is an error.
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
	// allOf / anyOf (used sparingly by the corpus).
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

// checkJSONSchema loads the schema at schemaPath (relative to the working dir,
// which is the repo root) and validates content against it.
func checkJSONSchema(schemaPath, content string) error {
	if schemaPath == "" {
		return fmt.Errorf("json_schema check has no schema path")
	}
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("schema not readable (%s): %w", schemaPath, err)
	}
	if err := validateJSONSchema(data, []byte(content)); err != nil {
		return fmt.Errorf("schema violation: %w", err)
	}
	return nil
}
