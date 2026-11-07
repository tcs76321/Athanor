package main

import (
	"database/sql"
	"fmt"
)

// runSequence executes a multi-goal task in one project (ADR-0116). Each step
// is a real goal; the project execution config is patched per step so a code
// step runs its test command and a document step runs its declared checks.
// The sequence stops at the first failed checkpoint; Progress records how far
// it got, and the last step's outcome is the final pass.
func (r *runnerConfig) runSequence(g sampleGoal, projectID, stateDir string, mx *jobMetrics) {
	steps := g.Steps
	mx.Sequence = make([]sequenceStep, 0, len(steps))
	db, err := openResultsDB(stateDir)
	if err != nil {
		mx.Error = "open results db: " + err.Error()
		return
	}
	defer func() { _ = db.Close() }()

	passed := 0
	var aggregate *jobTelemetry
	for i, step := range steps {
		exec := map[string]any{
			"test_command": step.TestCommand,
			"checks":       checksToExecution(step.Checks),
			"data_schema":  "",
			"fixture_path": "",
			"fixture_root": "",
		}
		if err := apiCall("PATCH", r.baseURL()+"/projects/"+projectID+"/execution", exec, nil); err != nil {
			mx.Error = fmt.Sprintf("step %d patch execution: %v", i+1, err)
			break
		}
		var gr struct {
			JobID  string   `json:"job_id"`
			JobIDs []string `json:"job_ids"`
		}
		if err := apiCall("POST", r.baseURL()+"/projects/"+projectID+"/goals", map[string]any{
			"goal": step.Goal, "acceptance_criteria": step.Criteria,
		}, &gr); err != nil {
			mx.Error = fmt.Sprintf("step %d submit: %v", i+1, err)
			break
		}
		jobIDs := gr.JobIDs
		if len(jobIDs) == 0 && gr.JobID != "" {
			jobIDs = []string{gr.JobID}
		}
		if len(jobIDs) == 0 {
			mx.Error = fmt.Sprintf("step %d submit: no jobs returned", i+1)
			break
		}
		state, artifactID, werr := waitJobs(r.baseURL(), jobIDs, r.timeout)
		if werr != nil {
			mx.Error = fmt.Sprintf("step %d wait: %v", i+1, werr)
			break
		}

		sr := sequenceStep{
			Index: i + 1, Goal: step.Goal, JobID: jobIDs[0],
			State: state, ArtifactID: artifactID,
		}
		stepPass := state == "completed"
		if g.Archetype == "code" {
			tm := jobMetrics{JobID: jobIDs[0], Archetype: "code"}
			tm.fillCodeTest(db)
			if tm.CodeTestPassed != nil {
				stepPass = *tm.CodeTestPassed
				mx.CodeTestPassed = tm.CodeTestPassed
				mx.CodeTestRan = tm.CodeTestRan
				mx.CodeTestTotal = tm.CodeTestTotal
				mx.CodeTestPassedCount = tm.CodeTestPassedCount
				mx.CodeTestCommand = tm.CodeTestCommand
			}
		}
		text := ""
		if artifactID != "" {
			text, _ = readArtifact(stateDir, artifactID)
		}
		if len(step.Checks) > 0 && text != "" {
			ok, results := runChecks(step.Checks, text)
			sr.CheckPass = &ok
			for _, cr := range results {
				if !cr.Advisory && !cr.Pass {
					sr.CheckFailed = append(sr.CheckFailed, cr.Kind)
				}
			}
			stepPass = state == "completed" && ok
			mx.CheckPass = &ok
		}
		sr.Pass = stepPass
		mx.Sequence = append(mx.Sequence, sr)
		mx.State, mx.ArtifactID, mx.ArtifactText = state, artifactID, text
		mx.Winner = "new"
		if stepPass {
			passed++
		}
		mx.TokenCost += stepTokens(db, jobIDs[0])
		if tm := stepMetrics(db, jobIDs[0], g.Archetype); tm != nil {
			aggregate = mergeTelemetry(aggregate, tm)
		}
		if !stepPass {
			break
		}
	}
	mx.Telemetry = aggregate
	mx.Progress = 0
	if len(steps) > 0 {
		mx.Progress = float64(passed) / float64(len(steps))
	}
	mx.EngineAccepted = mx.State == "completed" && mx.Winner == "new"
}

func stepTokens(db *sql.DB, jobID string) int {
	var tokens int
	_ = db.QueryRow(`SELECT token_cost FROM strategy_outcomes WHERE job_id = ?`, jobID).Scan(&tokens)
	return tokens
}

func stepMetrics(db *sql.DB, jobID, archetype string) *jobTelemetry {
	m := jobMetrics{JobID: jobID, Archetype: archetype, State: "completed"}
	m.fillTelemetry(db)
	return m.Telemetry
}

func mergeTelemetry(dst, src *jobTelemetry) *jobTelemetry {
	if src == nil {
		return dst
	}
	if dst == nil {
		return src
	}
	dst.Calls += src.Calls
	dst.PromptTokens += src.PromptTokens
	dst.CompletionTokens += src.CompletionTokens
	dst.RepairLoops += src.RepairLoops
	dst.ReflectLoops += src.ReflectLoops
	dst.Rerolls += src.Rerolls
	dst.CandidateRaise += src.CandidateRaise
	dst.KVPressure += src.KVPressure
	dst.KVWarn += src.KVWarn
	dst.KVCritical += src.KVCritical
	dst.ContextFloors += src.ContextFloors
	dst.Notes += src.Notes
	dst.Corrections += src.Corrections
	dst.GitCommitted += src.GitCommitted
	dst.GitCommitFailed += src.GitCommitFailed
	dst.DormantRows += src.DormantRows
	dst.WorkingSetChunks += src.WorkingSetChunks
	dst.WorkingSetBytes += src.WorkingSetBytes
	dst.MemoryHits += src.MemoryHits
	dst.MemoryBytes += src.MemoryBytes
	dst.Compactions += src.Compactions
	dst.CompactionSource += src.CompactionSource
	dst.CompactionBytes += src.CompactionBytes
	dst.LearnedSelections += src.LearnedSelections
	dst.Replans += src.Replans
	dst.Observations += src.Observations
	dst.Clarifications += src.Clarifications
	dst.MemoryRemembered += src.MemoryRemembered
	dst.ByPhaseCalls = mergeIntMap(dst.ByPhaseCalls, src.ByPhaseCalls)
	dst.ByPhaseTokens = mergeIntMap(dst.ByPhaseTokens, src.ByPhaseTokens)
	dst.ByPersonaTokens = mergeIntMap(dst.ByPersonaTokens, src.ByPersonaTokens)
	for _, model := range src.Models {
		found := false
		for _, existing := range dst.Models {
			if existing == model {
				found = true
				break
			}
		}
		if !found {
			dst.Models = append(dst.Models, model)
		}
	}
	return dst
}

func mergeIntMap(dst, src map[string]int) map[string]int {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = map[string]int{}
	}
	for k, v := range src {
		dst[k] += v
	}
	return dst
}
