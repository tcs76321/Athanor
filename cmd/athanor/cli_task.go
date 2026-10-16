package main

import (
	"flag"
	"fmt"
	"net/http"
)

// runTask is `athanor task reset -id <task>` (known-issues H8): the operator
// recovery surface over a DAG stalled by a blocked task. It talks to a running
// daemon. A task that is not blocked is refused by the daemon (409).
func runTask(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: athanor task reset -id ID [-addr]")
	}
	verb := args[0]
	fs := flag.NewFlagSet("task "+verb, flag.ContinueOnError)
	addr := fs.String("addr", defaultAddr, "daemon address")
	id := fs.String("id", "", "task id (required)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	switch verb {
	case "reset":
		if *id == "" {
			return fmt.Errorf("-id is required")
		}
		var out struct {
			JobIDs []string `json:"job_ids"`
		}
		if err := apiCall(http.MethodPost, *addr+"/tasks/"+*id+"/reset", nil, &out); err != nil {
			return err
		}
		if len(out.JobIDs) == 0 {
			fmt.Printf("task %s reset\n", *id)
			return nil
		}
		fmt.Printf("task %s reset; started jobs: %v\n", *id, out.JobIDs)
		return nil
	default:
		return fmt.Errorf("unknown task command %q", verb)
	}
}
