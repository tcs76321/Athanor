package main

import "testing"

func TestRunChecksDocument(t *testing.T) {
	checks := map[string]any{
		"required_sections": []any{"Overview", "Install", "API", "License"},
		"max_words":         40,
	}
	good := "# Overview\n\nA short module.\n\n# Install\n\npip install x\n\n# API\n\napi()\n\n# License\n\nMIT\n"
	if pass, results := runChecks(checks, good); !pass {
		t.Errorf("good doc failed: %+v", results)
	}
	bad := "# Overview\n\nonly one section\n"
	if pass, results := runChecks(checks, bad); pass {
		t.Errorf("bad doc passed: %+v", results)
	}
}

func TestRunChecksText(t *testing.T) {
	checks := map[string]any{
		"max_words":         10,
		"forbidden_phrases": []any{"click here", "act now"},
	}
	if pass, results := runChecks(checks, "Hello there friend welcome aboard today now"); !pass {
		t.Errorf("clean text failed: %+v", results)
	}
	if pass, _ := runChecks(checks, "Please click here to continue"); pass {
		t.Error("forbidden phrase not caught")
	}
	if pass, _ := runChecks(checks, "one two three four five six seven eight nine ten eleven"); pass {
		t.Error("over-length text passed max_words")
	}
}

func TestRunChecksExactParagraphsAndHeadings(t *testing.T) {
	checks := map[string]any{"exact_paragraphs": 2, "no_headings": true}
	if pass, results := runChecks(checks, "First para.\n\nSecond para.\n"); !pass {
		t.Errorf("two paras failed: %+v", results)
	}
	if pass, _ := runChecks(checks, "# Title\n\nOnly one para.\n"); pass {
		t.Error("heading should fail no_headings")
	}
}

func TestRunChecksAdvisoryDoesNotGate(t *testing.T) {
	checks := map[string]any{"must_flag_contradiction": true}
	pass, results := runChecks(checks, "anything")
	if !pass {
		t.Errorf("advisory-only checks gated: %+v", results)
	}
	if len(results) != 1 || !results[0].Advisory {
		t.Errorf("advisory result missing: %+v", results)
	}
}
