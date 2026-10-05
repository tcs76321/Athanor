package main

import (
	"flag"
	"fmt"
	"net/http"
)

type insightJSON struct {
	ID        string  `json:"id"`
	Polarity  string  `json:"polarity"`
	Feature   string  `json:"feature"`
	Value     string  `json:"value"`
	Context   string  `json:"context"`
	Statement string  `json:"statement"`
	Status    string  `json:"status"`
	Cohort    int     `json:"cohort_jobs"`
	Accept    float64 `json:"accept_rate"`
	Baseline  float64 `json:"baseline_accept_rate"`
}

type insightListJSON struct {
	Insights []insightJSON `json:"insights"`
}

type mineJSON struct {
	Proposed []insightJSON `json:"proposed"`
}

type promoteJSON struct {
	Activated bool   `json:"activated"`
	RequestID string `json:"request_id"`
}

// runStrategy is `athanor strategy mine|insights|promote|mute` (M6-T11): the
// operator surface over §13.4 strategy analysis. Promotion is HITL-gated
// unless strategy_analysis.auto_promote is set.
func runStrategy(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: athanor strategy mine|insights|promote|mute")
	}
	verb := args[0]
	fs := flag.NewFlagSet("strategy "+verb, flag.ContinueOnError)
	addr := fs.String("addr", defaultAddr, "daemon address")
	id := fs.String("id", "", "insight id (promote/mute)")
	status := fs.String("status", "", "filter insights by status")
	cohort := fs.Int("cohort", 20, "min cohort size (mine)")
	delta := fs.Float64("delta", 0.15, "min accept-rate delta (mine)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	switch verb {
	case "mine":
		var out mineJSON
		if err := apiCall(http.MethodPost, *addr+"/strategy/mine", map[string]any{
			"min_cohort_size": *cohort, "min_accept_rate_delta": *delta,
		}, &out); err != nil {
			return err
		}
		fmt.Printf("%d proposed insight(s)\n", len(out.Proposed))
		for _, ins := range out.Proposed {
			fmt.Printf("  %s  %s  %s\n", ins.Polarity, ins.ID, ins.Statement)
		}
		return nil
	case "insights":
		url := *addr + "/strategy/insights"
		if *status != "" {
			url += "?status=" + *status
		}
		var out insightListJSON
		if err := apiCall(http.MethodGet, url, nil, &out); err != nil {
			return err
		}
		if len(out.Insights) == 0 {
			fmt.Println("no insights")
			return nil
		}
		for _, ins := range out.Insights {
			fmt.Printf("%s  %s/%s  %s  %s\n", ins.ID, ins.Polarity, ins.Status, ins.Feature+"="+ins.Value, ins.Statement)
		}
		return nil
	case "promote":
		if *id == "" {
			return fmt.Errorf("-id is required")
		}
		var out promoteJSON
		if err := apiCall(http.MethodPost, *addr+"/strategy/insights/"+*id+"/promote", map[string]any{}, &out); err != nil {
			return err
		}
		if out.Activated {
			fmt.Printf("insight %s activated\n", *id)
		} else {
			fmt.Printf("insight %s promotion requires approval: athanor hitl approve -id %s\n", *id, out.RequestID)
		}
		return nil
	case "mute":
		if *id == "" {
			return fmt.Errorf("-id is required")
		}
		var out insightJSON
		if err := apiCall(http.MethodPost, *addr+"/strategy/insights/"+*id+"/mute", map[string]any{}, &out); err != nil {
			return err
		}
		fmt.Printf("insight %s: %s\n", out.ID, out.Status)
		return nil
	default:
		return fmt.Errorf("unknown strategy command %q", verb)
	}
}
