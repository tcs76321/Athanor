package main

import "testing"

func TestValidateJSONSchema(t *testing.T) {
	schema := []byte(`{"type":"array","items":{"type":"object","required":["a"],"additionalProperties":false,"properties":{"a":{"type":"string"},"n":{"type":"integer"}}}}`)
	cases := []struct {
		name string
		doc  string
		ok   bool
	}{
		{"valid", `[{"a":"x","n":1}]`, true},
		{"bad type", `[{"a":1}]`, false},
		{"missing required", `[{"n":1}]`, false},
		{"extra property", `[{"a":"x","z":1}]`, false},
		{"not an array", `{"a":"x"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateJSONSchema(schema, []byte(tc.doc))
			if tc.ok && err != nil {
				t.Errorf("valid doc rejected: %v", err)
			}
			if !tc.ok && err == nil {
				t.Errorf("invalid doc accepted: %s", tc.doc)
			}
		})
	}
	if err := validateJSONSchema(schema, []byte("not json")); err == nil {
		t.Error("non-JSON document accepted")
	}
}

// TestCheckJSONSchemaDataFixture proves the M8-T14 path end-to-end against the
// authored data-task schema.
func TestCheckJSONSchemaDataFixture(t *testing.T) {
	schema := "../../eval/bench/fixtures/data-json-normalize/schema.json"
	good := `[{"timestamp":"2026-01-02T14:03:11Z","severity":"warning","message":"disk almost full"}]`
	if err := checkJSONSchema(schema, good); err != nil {
		t.Errorf("valid data rejected: %v", err)
	}
	bad := `[{"timestamp":"2026-01-02","severity":"warn","message":"x"}]`
	if err := checkJSONSchema(schema, bad); err == nil {
		t.Error("bad severity accepted")
	}
}
