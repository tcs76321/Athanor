package main

import (
	"flag"
	"fmt"
	"net/http"
	"strings"
)

type decomposeTask struct {
	ID        string   `json:"id"`
	ParentID  string   `json:"parent_id"`
	Title     string   `json:"title"`
	Status    string   `json:"status"`
	DependsOn []string `json:"depends_on"`
	Criteria  []string `json:"acceptance_criteria"`
}

type decomposeResult struct {
	GoalID  string          `json:"goal_id"`
	Persona string          `json:"persona"`
	Tasks   []decomposeTask `json:"tasks"`
}

// runGoalDecompose is `athanor goal decompose` (M6-T1): it asks the daemon
// to decompose a goal into a validated DAG and prints the task tree. It
// does not enqueue work; the M6-T2 scheduler will.
func runGoalDecompose(args []string) error {
	fs := flag.NewFlagSet("goal decompose", flag.ContinueOnError)
	projectID := fs.String("project", "", "project id (required)")
	goal := fs.String("goal", "", "goal text, 20-500 characters (required)")
	var criteria criteriaFlag
	fs.Var(&criteria, "criteria", "acceptance criteria, separated by ';'")
	addr := fs.String("addr", defaultAddr, "daemon address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectID == "" || *goal == "" {
		return fmt.Errorf("-project and -goal are required")
	}
	var out decomposeResult
	if err := apiCall(http.MethodPost, *addr+"/projects/"+*projectID+"/decompose", map[string]any{
		"goal": *goal, "acceptance_criteria": criteria,
	}, &out); err != nil {
		return err
	}
	titleByID := make(map[string]string, len(out.Tasks))
	for _, t := range out.Tasks {
		titleByID[t.ID] = t.Title
	}
	fmt.Printf("decomposed into %d task(s) with the %s persona (goal %s)\n", len(out.Tasks), out.Persona, out.GoalID)
	for _, t := range out.Tasks {
		line := "- " + t.Title
		if t.ParentID != "" {
			line += " [" + titleByID[t.ParentID] + "]"
		}
		if len(t.DependsOn) > 0 {
			deps := make([]string, 0, len(t.DependsOn))
			for _, d := range t.DependsOn {
				deps = append(deps, titleByID[d])
			}
			line += " (after: " + strings.Join(deps, ", ") + ")"
		}
		fmt.Println(line)
	}
	return nil
}
