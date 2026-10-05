package main

import (
	"flag"
	"fmt"
	"net/http"
	"time"
)

type projectCreateResult struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	TaskID string `json:"task_id"`
}

func runProject(args []string) error {
	if len(args) == 0 || args[0] != "create" {
		return fmt.Errorf("usage: athanor project create -name NAME -archetype text -goal \"...\"")
	}
	fs := flag.NewFlagSet("project create", flag.ContinueOnError)
	name := fs.String("name", "", "project name (required)")
	archetype := fs.String("archetype", "text", "project archetype: text|code|document|data|media")
	goal := fs.String("goal", "", "project goal, 20-500 characters (required)")
	repo := fs.String("repo", "", "repository root the indexer walks (optional)")
	var criteria criteriaFlag
	fs.Var(&criteria, "criteria", "acceptance criteria, separated by ';'")
	addr := fs.String("addr", defaultAddr, "daemon address")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *name == "" || *goal == "" {
		return fmt.Errorf("-name and -goal are required")
	}
	var out projectCreateResult
	if err := apiCall(http.MethodPost, *addr+"/projects", map[string]any{
		"name": *name, "archetype": *archetype, "goal": *goal,
		"acceptance_criteria": criteria, "repository_path": *repo,
	}, &out); err != nil {
		return err
	}
	fmt.Printf("project %s created (task %s)\nSubmit a goal with:\n  athanor goal submit -project %s -goal \"...\"\n", out.ID, out.TaskID, out.ID)
	return nil
}

type goalSubmitResult struct {
	TaskID string `json:"task_id"`
	JobID  string `json:"job_id"`
}

func runGoal(args []string) error {
	if len(args) == 0 || args[0] != "submit" {
		return fmt.Errorf("usage: athanor goal submit -project ID -goal \"...\"")
	}
	fs := flag.NewFlagSet("goal submit", flag.ContinueOnError)
	projectID := fs.String("project", "", "project id (required)")
	goal := fs.String("goal", "", "goal text, 20-500 characters (required)")
	var criteria criteriaFlag
	fs.Var(&criteria, "criteria", "acceptance criteria, separated by ';'")
	addr := fs.String("addr", defaultAddr, "daemon address")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *projectID == "" || *goal == "" {
		return fmt.Errorf("-project and -goal are required")
	}
	var out goalSubmitResult
	if err := apiCall(http.MethodPost, *addr+"/projects/"+*projectID+"/goals", map[string]any{
		"goal": *goal, "acceptance_criteria": criteria,
	}, &out); err != nil {
		return err
	}
	fmt.Printf("goal submitted: job %s (task %s)\nWatch with:\n  athanor job watch -job %s\n", out.JobID, out.TaskID, out.JobID)
	return nil
}

type jobStatus struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	PausedFrom string `json:"paused_from"`
	ArtifactID string `json:"artifact_id"`
}

// runJob polls a job until it reaches a terminal state (or the watch
// timeout), printing each state transition as it happens, then the final
// artifact.
func runJob(args []string) error {
	if len(args) == 0 || args[0] != "watch" {
		return fmt.Errorf("usage: athanor job watch -job ID")
	}
	fs := flag.NewFlagSet("job watch", flag.ContinueOnError)
	jobID := fs.String("job", "", "job id (required)")
	timeout := fs.Duration("timeout", 10*time.Minute, "give up after this long")
	addr := fs.String("addr", defaultAddr, "daemon address")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *jobID == "" {
		return fmt.Errorf("-job is required")
	}

	deadline := time.Now().Add(*timeout)
	lastState := ""
	for {
		var status jobStatus
		if err := apiCall(http.MethodGet, *addr+"/jobs/"+*jobID, nil, &status); err != nil {
			return err
		}
		if status.State != lastState {
			fmt.Printf("%s\n", status.State)
			lastState = status.State
		}
		switch status.State {
		case "completed":
			if status.ArtifactID != "" {
				fmt.Printf("artifact: %s\n", status.ArtifactID)
			}
			return nil
		case "failed":
			fmt.Println("see the event log: GET /jobs/" + *jobID + "/events")
			return fmt.Errorf("job failed")
		case "cancelled":
			return nil
		case "paused":
			fmt.Printf("job paused (from %s) — see the daemon log for the reason\n", status.PausedFrom)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("watch timed out after %v (job is still %s)", timeout, status.State)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
