package division

import (
	"sort"
	"testing"
	"time"
)

// TestMetricsReport prints the spike's decision-matrix numbers. It is a
// test (not a main) so `go test -v` captures the tables for the findings
// doc; it asserts nothing beyond the property tests.
func TestMetricsReport(t *testing.T) {
	corpus, err := LoadCorpus()
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}

	strats := spikeStrategies()
	chunkSets := map[string]map[string][]Chunk{}          // strat -> file -> chunks
	boundaries := map[string]map[string]map[int]bool{}    // strat -> file -> boundary set
	durations := map[string]map[string]time.Duration{}    // strat -> file -> divide time

	for _, strat := range strats {
		chunkSets[strat.Name()] = map[string][]Chunk{}
		boundaries[strat.Name()] = map[string]map[int]bool{}
		durations[strat.Name()] = map[string]time.Duration{}
	}

	for _, strat := range strats {
		var total time.Duration
		for _, src := range corpus {
			t0 := time.Now()
			chunks := strat.Divide(src.FilePath, src.Lang, src.Src)
			d := time.Since(t0)
			total += d
			chunkSets[strat.Name()][src.FilePath] = chunks
			boundaries[strat.Name()][src.FilePath] = boundarySet(chunks)
			durations[strat.Name()][src.FilePath] = d
		}
		t.Logf("wall: %-13s divided %d files (%d bytes) in %v",
			strat.Name(), len(corpus), totalBytes(corpus), total)
	}

	for _, lang := range []string{"go", "python", "javascript", "markdown"} {
		files := corpusByLang(corpus, lang)
		if len(files) == 0 {
			continue
		}
		t.Logf("=== language: %s (%d corpus files) ===", lang, len(files))
		for _, strat := range strats {
			m := langMetrics(chunkSets[strat.Name()], durations[strat.Name()], files)
			t.Logf("%-13s chunks=%-6d mean=%-6.0fB p95=%-6.0fB max=%-6dB throughput=%.2fMB/s",
				strat.Name(), m.chunks, m.mean, m.p95, m.max, m.mbps)
		}
		// Boundary agreement against the AST ground truth for the language.
		var gtName string
		switch lang {
		case "go":
			gtName = "pure-go" // go/parser is ground truth for Go
		case "python", "javascript":
			gtName = "tree-sitter" // the real grammar is ground truth otherwise
		default:
			continue // markdown has no AST ground truth in this spike
		}
		for _, strat := range strats {
			if strat.Name() == gtName {
				continue
			}
			p, r := agreement(boundaries[gtName], boundaries[strat.Name()], files)
			t.Logf("%-13s boundary agreement vs %s: precision=%.2f recall=%.2f",
				strat.Name(), gtName, p, r)
		}
	}
}

type langStats struct {
	chunks, max  int
	mean, p95    float64
	mbps         float64
}

func langMetrics(perFile map[string][]Chunk, dur map[string]time.Duration, files []Source) langStats {
	var st langStats
	var sizes []int
	var elapsed time.Duration
	var bytesDivided int64
	for _, f := range files {
		for _, c := range perFile[f.FilePath] {
			sizes = append(sizes, len(c.Content))
		}
		st.chunks += len(perFile[f.FilePath])
		bytesDivided += int64(len(f.Src))
		elapsed += dur[f.FilePath]
	}
	sort.Ints(sizes)
	if len(sizes) > 0 {
		st.max = sizes[len(sizes)-1]
		var sum int
		for _, s := range sizes {
			sum += s
		}
		st.mean = float64(sum) / float64(len(sizes))
		idx := len(sizes) * 95 / 100
		if idx >= len(sizes) {
			idx = len(sizes) - 1
		}
		st.p95 = float64(sizes[idx])
	}
	if elapsed > 0 {
		st.mbps = float64(bytesDivided) / 1e6 / elapsed.Seconds()
	}
	return st
}

func totalBytes(corpus []Source) int {
	n := 0
	for _, s := range corpus {
		n += len(s.Src)
	}
	return n
}

func corpusByLang(corpus []Source, lang string) []Source {
	var out []Source
	for _, s := range corpus {
		if s.Lang == lang {
			out = append(out, s)
		}
	}
	return out
}

func boundarySet(chunks []Chunk) map[int]bool {
	set := map[int]bool{}
	for _, c := range chunks {
		if c.ByteStart > 0 {
			set[c.ByteStart] = true
		}
	}
	return set
}

// agreement computes precision/recall of boundaries: fraction of the
// candidate's boundaries that appear in ground truth, and vice versa.
func agreement(gt, cand map[string]map[int]bool, files []Source) (precision, recall float64) {
	var hit, total, gtTotal int
	for _, f := range files {
		g := gt[f.FilePath]
		c := cand[f.FilePath]
		for b := range c {
			total++
			if g[b] {
				hit++
			}
		}
		gtTotal += len(g)
	}
	if total > 0 {
		precision = float64(hit) / float64(total)
	}
	if gtTotal > 0 {
		recall = float64(hit) / float64(gtTotal)
	}
	return precision, recall
}
