package main

import (
	"flag"
	"fmt"
	"net/http"
)

type correctionJSON struct {
	ID           string `json:"id"`
	Category     string `json:"category"`
	Severity     string `json:"severity"`
	Scope        string `json:"scope"`
	DerivedRule  string `json:"derived_rule"`
	Status       string `json:"status"`
	AppliedCount int    `json:"applied_count"`
}

type correctionListJSON struct {
	Corrections []correctionJSON `json:"corrections"`
}

// runCorrections is `athanor corrections -project <id>` (M6-T6): it lists a
// project's CorrectionRecords.
func runCorrections(args []string) error {
	if len(args) > 0 && args[0] == "list" {
		args = args[1:]
	}
	fs := flag.NewFlagSet("corrections", flag.ContinueOnError)
	projectID := fs.String("project", "", "project id (required)")
	addr := fs.String("addr", defaultAddr, "daemon address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectID == "" {
		return fmt.Errorf("-project is required")
	}
	var out correctionListJSON
	if err := apiCall(http.MethodGet, *addr+"/projects/"+*projectID+"/corrections", nil, &out); err != nil {
		return err
	}
	if len(out.Corrections) == 0 {
		fmt.Println("no corrections")
		return nil
	}
	for _, c := range out.Corrections {
		fmt.Printf("%s  %s/%s  %s  %s  applied=%d\n",
			c.ID, c.Category, c.Severity, c.Scope, c.Status, c.AppliedCount)
	}
	return nil
}

// runReject is `athanor reject ...` (M6-T6): the §18.4 mandatory structured
// rejection form. It records a CorrectionRecord; every rejection must carry
// a category, severity, reason, desired behavior, and scope.
func runReject(args []string) error {
	fs := flag.NewFlagSet("reject", flag.ContinueOnError)
	projectID := fs.String("project", "", "project id (required)")
	artifactID := fs.String("artifact", "", "artifact the rejection is about")
	category := fs.String("category", "", "architecture|style|testing|security|performance|tooling|documentation|other")
	severity := fs.String("severity", "", "low|medium|high|critical")
	reason := fs.String("reason", "", "what was wrong (required)")
	desired := fs.String("desired", "", "desired behavior / derived rule (required)")
	scope := fs.String("scope", "project", "project|global")
	addr := fs.String("addr", defaultAddr, "daemon address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectID == "" || *category == "" || *severity == "" || *reason == "" || *desired == "" {
		return fmt.Errorf("-project, -category, -severity, -reason, and -desired are required")
	}
	body := map[string]any{
		"source": "user_rejection", "category": *category, "severity": *severity,
		"reason": *reason, "desired_behavior": *desired, "scope": *scope,
	}
	if *artifactID != "" {
		body["artifact_id"] = *artifactID
	}
	var out correctionJSON
	if err := apiCall(http.MethodPost, *addr+"/projects/"+*projectID+"/corrections", body, &out); err != nil {
		return err
	}
	fmt.Printf("correction %s recorded (%s/%s, %s)\n", out.ID, out.Category, out.Severity, out.Scope)
	return nil
}
