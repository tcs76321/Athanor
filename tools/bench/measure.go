package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/tcs76321/athanor/internal/judge"
	"github.com/tcs76321/athanor/internal/onboard"
)

// runMeasure is the `measure` subcommand (M9-T2b): it turns a results tree and
// an anchor run into the per-role `onboard.Measurement` list that
// `athanor models -qualification` consumes, and writes measurements.json under
// -out.
//
// Every generator measurement carries Samples = the number of collected jobs,
// and every judge measurement carries Samples = the number of rated anchor
// cases. That is the point of M9-T2b-2: a single-pass pass@k is noisy (the M9
// soak passed 6/6 then 0/2), so onboard.Route's MinSamples guard must see how
// many samples back a score before letting it displace the configured model.
func runMeasure(args []string) {
	fs := flag.NewFlagSet("measure", flag.ExitOnError)
	outDir := fs.String("out", filepath.Join("spikes", "m3-t7-probe", "results"), "results tree (walked for results.json)")
	anchorOut := fs.String("anchor-out", filepath.Join("spikes", "m3-t7-probe", "results", "anchor"), "anchor results dir (anchor.json)")
	ratingsPath := fs.String("ratings", filepath.Join("eval", "anchor", "ratings.csv"), "anchor ratings.csv")
	genRole := fs.String("gen-role", "main", "role the generator measurements qualify")
	_ = fs.Parse(args)

	metrics, err := loadAllMetrics(*outDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "m3-t7 measure:", err)
		os.Exit(1)
	}
	ms := generatorMeasurements(metrics, *genRole)
	// The anchor is optional: a results-only measurement run is still useful.
	if ams, aerr := anchorMeasurements(*anchorOut, *ratingsPath); aerr == nil {
		ms = append(ms, ams...)
	}

	path := filepath.Join(*outDir, "measurements.json")
	if err := writeJSON(path, ms); err != nil {
		fmt.Fprintln(os.Stderr, "m3-t7 measure:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d measurement(s))\n", path, len(ms))
	for _, m := range ms {
		fmt.Printf("  %-9s %-24s score=%.3f samples=%d\n", m.Role, m.Model, m.Score, m.Samples)
	}
}

// generatorMeasurements aggregates the accept rate per generator model.
// "Accepted" mirrors collect.go: a winner of new|previous. Samples is the
// number of collected jobs for that model across every arm and run, so a
// repeated qualification run raises the sample count rather than replacing it.
func generatorMeasurements(metrics []jobMetrics, role string) []onboard.Measurement {
	type agg struct{ accepted, total int }
	byModel := map[string]*agg{}
	for _, m := range metrics {
		if m.Model == "" {
			continue
		}
		a := byModel[m.Model]
		if a == nil {
			a = &agg{}
			byModel[m.Model] = a
		}
		a.total++
		if m.Winner == "new" || m.Winner == "previous" {
			a.accepted++
		}
	}
	var out []onboard.Measurement
	for model, a := range byModel {
		if a.total == 0 {
			continue
		}
		out = append(out, onboard.Measurement{
			Model:   model,
			Role:    role,
			Score:   float64(a.accepted) / float64(a.total),
			Samples: a.total,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out
}

// anchorMeasurements reduces an anchor.json + ratings.csv pair to one
// measurement per judge: the Spearman rank agreement (the score) over the
// rated cases (the samples).
func anchorMeasurements(anchorDir, ratingsPath string) ([]onboard.Measurement, error) {
	raw, err := os.ReadFile(filepath.Join(anchorDir, "anchor.json"))
	if err != nil {
		return nil, err
	}
	var rows []anchorRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	ratings, err := parseAnchorRatings(ratingsPath)
	if err != nil {
		return nil, err
	}
	byJudge := map[string][]anchorRow{}
	for _, r := range rows {
		byJudge[r.Judge] = append(byJudge[r.Judge], r)
	}
	var out []onboard.Measurement
	for j, rs := range byJudge {
		var samples []judge.Sample
		for _, r := range rs {
			if r.Error != "" {
				continue
			}
			if rating, ok := ratings[r.CaseID]; ok {
				samples = append(samples, judge.Sample{AnchorRating: rating, JudgeScore: r.Score})
			}
		}
		if len(samples) == 0 {
			continue
		}
		out = append(out, onboard.Measurement{
			Model:   j,
			Role:    "security",
			Score:   judge.Spearman(samples),
			Samples: len(samples),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out, nil
}
