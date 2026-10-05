package main

import (
	"flag"
	"fmt"
	"net/http"
	"time"
)

type hitlRequestJSON struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	Severity  string     `json:"severity"`
	Status    string     `json:"status"`
	JobID     string     `json:"job_id"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type hitlListJSON struct {
	Requests []hitlRequestJSON `json:"requests"`
}

// runHITL is `athanor hitl list|approve|reject|defer` (M6-T4): the operator
// surface over the §20 queue. It talks to a running daemon.
func runHITL(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: athanor hitl list|approve|reject|defer [-id ID] [-note TEXT] [-for 1h]")
	}
	verb := args[0]
	fs := flag.NewFlagSet("hitl "+verb, flag.ContinueOnError)
	addr := fs.String("addr", defaultAddr, "daemon address")
	id := fs.String("id", "", "request id (required for a decision)")
	note := fs.String("note", "", "decision note")
	deferFor := fs.String("for", "", "defer duration, e.g. 1h (required for defer)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	switch verb {
	case "list":
		var out hitlListJSON
		if err := apiCall(http.MethodGet, *addr+"/hitl", nil, &out); err != nil {
			return err
		}
		if len(out.Requests) == 0 {
			fmt.Println("no pending HITL requests")
			return nil
		}
		for _, r := range out.Requests {
			fmt.Printf("%s  %s/%s  %s  job=%s  waiting=%s\n",
				r.ID, r.Type, r.Severity, r.Status, r.JobID,
				time.Since(r.CreatedAt).Round(time.Second))
		}
		return nil
	case "approve", "reject", "defer":
		if *id == "" {
			return fmt.Errorf("-id is required")
		}
		body := map[string]any{"action": verb, "note": *note}
		if verb == "defer" {
			if *deferFor == "" {
				return fmt.Errorf("-for is required for defer")
			}
			body["defer_for"] = *deferFor
		}
		var out hitlRequestJSON
		if err := apiCall(http.MethodPost, *addr+"/hitl/"+*id+"/decision", body, &out); err != nil {
			return err
		}
		fmt.Printf("request %s: %s\n", out.ID, out.Status)
		return nil
	default:
		return fmt.Errorf("unknown hitl command %q", verb)
	}
}
