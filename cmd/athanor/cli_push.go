package main

import (
	"flag"
	"fmt"
	"net/http"
)

// runPush is `athanor push -project <id> [-remote origin]` (M6-T5): it
// records a HITL request for a git push (ADR-0036). Nothing is pushed until
// an operator approves the request with `athanor hitl approve -id <id>`; a
// rejected or expired request leaves the remote untouched.
func runPush(args []string) error {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	projectID := fs.String("project", "", "project id (required)")
	remote := fs.String("remote", "origin", "git remote to push to")
	addr := fs.String("addr", defaultAddr, "daemon address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectID == "" {
		return fmt.Errorf("-project is required")
	}
	var out hitlRequestJSON
	if err := apiCall(http.MethodPost, *addr+"/projects/"+*projectID+"/push", map[string]any{
		"remote": *remote,
	}, &out); err != nil {
		return err
	}
	fmt.Printf("push request %s is %s\nApprove it with:\n  athanor hitl approve -id %s\n",
		out.ID, out.Status, out.ID)
	return nil
}
