package toolenvelope

import (
	"errors"
	"reflect"
	"testing"
)

// TestParse_KnownTools builds envelopes from the closed set and
// asserts that Allows returns the expected membership. Unknown names
// are covered in TestParse_RejectsUnknown.
func TestParse_KnownTools(t *testing.T) {
	cases := []struct {
		name  string
		input []string
		want  []Tool // expected sorted Tools() result
	}{
		{"empty", nil, nil},
		{"empty slice", []string{}, nil},
		{"execute_code only", []string{"execute_code"}, []Tool{ToolExecuteCode}},
		{"run_tests only", []string{"run_tests"}, []Tool{ToolRunTests}},
		{"lint only", []string{"lint"}, []Tool{ToolLint}},
		{"git_operation only", []string{"git_operation"}, []Tool{ToolGitOperation}},
		{"fetch_url only", []string{"fetch_url"}, []Tool{ToolFetchURL}},
		{"search_web only", []string{"search_web"}, []Tool{ToolSearchWeb}},
		{"all six, order-insensitive", []string{"run_tests", "lint", "execute_code", "git_operation", "search_web", "fetch_url"}, []Tool{ToolExecuteCode, ToolFetchURL, ToolGitOperation, ToolLint, ToolRunTests, ToolSearchWeb}},
		{"both, order-insensitive", []string{"run_tests", "execute_code"}, []Tool{ToolExecuteCode, ToolRunTests}},
		{"duplicates deduped", []string{"execute_code", "execute_code", "run_tests", "lint", "git_operation"}, []Tool{ToolExecuteCode, ToolGitOperation, ToolLint, ToolRunTests}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, err := Parse(tc.input)
			if err != nil {
				t.Fatalf("Parse(%v): %v", tc.input, err)
			}
			got := env.Tools()
			if len(got) == 0 && len(tc.want) == 0 {
				// Both empty: Tools() returns a non-nil empty slice;
				// reflect.DeepEqual on nil vs []Tool{} is false but
				// semantically identical for our purposes.
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Tools() = %v, want %v", got, tc.want)
			}
			// Round-trip membership.
			for _, tool := range tc.want {
				if !env.Allows(tool) {
					t.Errorf("Allows(%q) = false, want true", tool)
				}
			}
			// Tools NOT in the envelope are rejected.
			for _, missing := range []Tool{ToolExecuteCode, ToolRunTests, ToolLint, ToolGitOperation, ToolFetchURL, ToolSearchWeb} {
				isIn := false
				for _, in := range tc.want {
					if in == missing {
						isIn = true
						break
					}
				}
				if !isIn && env.Allows(missing) {
					t.Errorf("Allows(%q) = true, want false (not in envelope)", missing)
				}
			}
		})
	}
}

// TestParse_RejectsUnknown proves the closed set is closed: a
// typo or a future tool added by a caller (but not by the project)
// is rejected with an error that names the offender.
func TestParse_RejectsUnknown(t *testing.T) {
	bad := []string{"execute", "Run_Tests", "", "EXECUTE_CODE", "browse", "search"}
	for _, name := range bad {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]string{name})
			if err == nil {
				t.Fatalf("Parse(%q) returned nil error; want ErrUnknownTool", name)
			}
			if !errors.Is(err, ErrUnknownTool) {
				t.Errorf("Parse(%q) error = %v, want errors.Is ErrUnknownTool", name, err)
			}
		})
	}
}

// TestEnvelope_ZeroValueIsEmpty proves the zero Envelope is the
// "no tools" configuration. This is the structural guarantee that a
// missing config field does not silently grant any tools.
func TestEnvelope_ZeroValueIsEmpty(t *testing.T) {
	var env Envelope
	if !env.IsEmpty() {
		t.Errorf("zero Envelope IsEmpty() = false, want true")
	}
	if env.Allows(ToolExecuteCode) {
		t.Errorf("zero Envelope Allows(execute_code) = true, want false")
	}
	if env.Allows(ToolRunTests) {
		t.Errorf("zero Envelope Allows(run_tests) = true, want false")
	}
	if env.Allows(ToolLint) {
		t.Errorf("zero Envelope Allows(lint) = true, want false")
	}
	if env.Allows(ToolGitOperation) {
		t.Errorf("zero Envelope Allows(git_operation) = true, want false")
	}
	if env.Allows(ToolFetchURL) {
		t.Errorf("zero Envelope Allows(fetch_url) = true, want false")
	}
	if env.Allows(ToolSearchWeb) {
		t.Errorf("zero Envelope Allows(search_web) = true, want false")
	}
	got := env.Tools()
	if len(got) != 0 {
		t.Errorf("zero Envelope Tools() = %v, want empty", got)
	}
}

// TestTools_SortedDeterministic proves Tools() returns a stable
// ordering so EventLog events and audit dumps are diffable.
func TestTools_SortedDeterministic(t *testing.T) {
	env, err := Parse([]string{"run_tests", "execute_code", "lint", "fetch_url", "search_web"})
	if err != nil {
		t.Fatal(err)
	}
	got := env.Tools()
	want := []Tool{ToolExecuteCode, ToolFetchURL, ToolLint, ToolRunTests, ToolSearchWeb}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tools() = %v, want %v (sorted by string value)", got, want)
	}
}
