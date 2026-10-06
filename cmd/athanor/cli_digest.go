package main

import (
	"flag"
	"fmt"

	"github.com/tcs76321/athanor/internal/digest"
)

// runDigest prints the §27.2 Morning Digest for a window (M7-T6).
func runDigest(args []string) error {
	fs := flag.NewFlagSet("digest", flag.ContinueOnError)
	addr := fs.String("addr", defaultAddr, "daemon address")
	hours := fs.Int("hours", 12, "window length in hours")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var d digest.Digest
	if err := apiCall("GET", fmt.Sprintf("%s/digest?hours=%d", *addr, *hours), nil, &d); err != nil {
		return err
	}

	fmt.Printf("Athanor digest — last %dh (since %s)\n\n", *hours, d.Since.Format("2006-01-02 15:04 MST"))
	fmt.Printf("jobs:      %d completed, %d failed, %d cancelled\n", d.JobsCompleted, d.JobsFailed, d.JobsCancelled)
	fmt.Printf("goals/tasks: %d completed, %d done\n", d.GoalsCompleted, d.TasksDone)
	fmt.Printf("artifacts: %d draft, %d accepted, %d rejected\n", d.Artifacts.Draft, d.Artifacts.Accepted, d.Artifacts.Rejected)
	fmt.Printf("pending approvals: %d · active alarms: %d · tokens: %d\n", d.PendingHITL, d.ActiveAlarms, d.Tokens)
	fmt.Printf("daydream: %d action(s), %d artifact(s), %d correction(s), %d insight(s), %d chunks, %d tokens saved\n",
		d.Daydream.Actions, d.Daydream.ArtifactsProduced, d.Daydream.CorrectionsProposed,
		d.Daydream.InsightsProposed, d.Daydream.ChunksProcessed, d.Daydream.TokensSaved)
	for _, f := range d.Failures {
		reason := f.Reason
		if reason == "" {
			reason = "(no recorded reason)"
		}
		fmt.Printf("  failed %s: %s\n", f.JobID, reason)
	}
	return nil
}
