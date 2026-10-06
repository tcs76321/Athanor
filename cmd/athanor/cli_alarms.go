package main

import (
	"flag"
	"fmt"
)

// runAlarms implements `athanor alarms` (list active) and
// `athanor alarms -resolve <id>` (M7-T3; §22.3).
func runAlarms(args []string) error {
	fs := flag.NewFlagSet("alarms", flag.ContinueOnError)
	addr := fs.String("addr", defaultAddr, "daemon address")
	resolve := fs.String("resolve", "", "resolve an active alarm by id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *resolve != "" {
		if err := apiCall("POST", *addr+"/alarms/"+*resolve+"/resolve", nil, nil); err != nil {
			return err
		}
		fmt.Printf("resolved alarm %s\n", *resolve)
		return nil
	}

	var out struct {
		Alarms []struct {
			ID        string `json:"id"`
			Category  string `json:"category"`
			Level     string `json:"level"`
			Message   string `json:"message"`
			JobID     string `json:"job_id"`
			ProjectID string `json:"project_id"`
		} `json:"alarms"`
	}
	if err := apiCall("GET", *addr+"/alarms", nil, &out); err != nil {
		return err
	}
	if len(out.Alarms) == 0 {
		fmt.Println("no active alarms")
		return nil
	}
	for _, a := range out.Alarms {
		scope := a.JobID
		if scope == "" {
			scope = a.ProjectID
		}
		fmt.Printf("[%-8s] %-16s %s %s\n", a.Level, a.Category, a.Message, scope)
	}
	return nil
}
