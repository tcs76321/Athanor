package main

import (
	"os"
	"regexp"
	"testing"
)

const (
	benchV3Dir      = "../../eval/bench/v3"
	benchV3Protocol = benchV3Dir + "/protocol.yaml"
	benchV3Core     = benchV3Dir + "/core.yaml"
	benchV3Agentic  = benchV3Dir + "/agentic.yaml"
	benchV3Quality  = benchV3Dir + "/quality.yaml"
	benchV3OneShot  = benchV3Dir + "/one-shot.yaml"
	benchV3Anchor   = benchV3Dir + "/anchor.yaml"
	benchV3Memory   = benchV3Dir + "/memory-scripts.yaml"
	benchV3Rubric   = benchV3Dir + "/quality-rubric.yaml"
)

var benchV3IDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// TestBenchV3Corpus validates the seed corpus and the schema that the full
// 64-task freeze will keep using. It deliberately allows the seed counts to be
// below the protocol target; the upper bounds are the freeze targets.
func TestBenchV3Corpus(t *testing.T) {
	p, err := loadProtocol(benchV3Protocol)
	if err != nil {
		t.Fatalf("loadProtocol v3: %v", err)
	}
	suitePaths := map[string]string{
		"core":     benchV3Core,
		"agentic":  benchV3Agentic,
		"quality":  benchV3Quality,
		"one_shot": benchV3OneShot,
	}
	counts := map[string]int{}
	held := map[string]int{}
	minCounts := map[string]int{"core": 8, "agentic": 3, "quality": 2, "one_shot": 3}
	maxCounts := map[string]int{"core": 28, "agentic": 14, "quality": 14, "one_shot": 8}
	ids := map[string]string{}
	archetypes := map[string]bool{}

	for name, path := range suitePaths {
		goals, err := loadCorpus(path)
		if err != nil {
			t.Fatalf("loadCorpus %s: %v", name, err)
		}
		for _, g := range goals {
			counts[name]++
			ids[g.Name] = name
			if g.Suite != name {
				t.Errorf("%s: suite = %q, want %q", g.Name, g.Suite, name)
			}
			if !benchV3IDPattern.MatchString(g.Name) {
				t.Errorf("%q is not a lowercase kebab id", g.Name)
			}
			if g.HeldOut {
				held[name]++
			}
			if g.Fixture != "" {
				if _, err := os.Stat(repoPath(g.Fixture)); err != nil {
					t.Errorf("%s: fixture %q: %v", g.Name, g.Fixture, err)
				}
			}
			if g.Repo != "" {
				if _, err := os.Stat(repoPath(g.Repo)); err != nil {
					t.Errorf("%s: repo %q: %v", g.Name, g.Repo, err)
				}
			}
			for _, k := range unknownCheckKeys(g.Checks) {
				t.Errorf("%s: unknown check key %q (would be a silent pass)", g.Name, k)
			}
			switch name {
			case "agentic":
				if len(g.Requires) == 0 {
					t.Errorf("%s: agentic task has no requires", g.Name)
				}
				for _, req := range g.Requires {
					if !knownMechanisms[req] {
						t.Errorf("%s: unknown mechanism %q", g.Name, req)
					}
				}
				if g.OneShotExpected != "fail" && g.OneShotExpected != "pass" {
					t.Errorf("%s: one_shot_expected = %q", g.Name, g.OneShotExpected)
				}
				if g.Fixture == "" && g.Repo == "" {
					t.Errorf("%s: agentic task has no fixture/repo", g.Name)
				}
			case "quality":
				if g.Rubric != "quality-v1" {
					t.Errorf("%s: rubric = %q, want quality-v1", g.Name, g.Rubric)
				}
				if g.TestCommand == "" && len(g.Checks) == 0 && len(g.Steps) == 0 {
					t.Errorf("%s: quality task has no deterministic anchor", g.Name)
				}
			case "one_shot":
				if g.OneShotExpected != "pass" {
					t.Errorf("%s: one_shot_expected = %q, want pass", g.Name, g.OneShotExpected)
				}
				if len(g.Requires) != 0 {
					t.Errorf("%s: one-shot task requires mechanisms %v", g.Name, g.Requires)
				}
			}
			if name == "core" {
				archetypes[g.Archetype] = true
			}
		}
	}
	for name, n := range counts {
		if n < minCounts[name] {
			t.Errorf("%s: %d tasks, want at least %d", name, n, minCounts[name])
		}
		if n > maxCounts[name] {
			t.Errorf("%s: %d tasks, over the freeze target %d", name, n, maxCounts[name])
		}
		if held[name] > p.Suites[name].HeldOut {
			t.Errorf("%s: %d held out, protocol allows %d", name, held[name], p.Suites[name].HeldOut)
		}
	}
	for _, want := range []string{"code", "data", "document", "text"} {
		if !archetypes[want] {
			t.Errorf("core suite has no %q task", want)
		}
	}

	// Anchor: every id exists in core, and no held-out task is used.
	core, err := loadCorpus(benchV3Core)
	if err != nil {
		t.Fatal(err)
	}
	anchored, err := filterByAnchor(core, benchV3Anchor)
	if err != nil {
		t.Fatalf("filterByAnchor: %v", err)
	}
	if len(anchored) == 0 {
		t.Fatal("anchor is empty")
	}
	for _, g := range anchored {
		if g.Suite != "core" {
			t.Errorf("anchor task %s is in suite %q", g.Name, g.Suite)
		}
		if g.HeldOut {
			t.Errorf("anchor uses held-out task %s", g.Name)
		}
	}

	if _, err := loadMemoryScripts(benchV3Memory); err != nil {
		t.Fatalf("loadMemoryScripts: %v", err)
	}
	if _, err := os.Stat(benchV3Rubric); err != nil {
		t.Fatalf("quality rubric: %v", err)
	}
}
