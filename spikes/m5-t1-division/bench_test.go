package division

import "testing"

// BenchmarkDivide measures end-to-end Divide() on the largest corpus
// file per language, per strategy. For tree-sitter this includes
// per-call parser construction — the honest cost of the current API
// shape; M5-T2 can amortize it with a parser pool. SetBytes makes
// go test report MB/s directly.
func BenchmarkDivide(b *testing.B) {
	corpus, err := LoadCorpus()
	if err != nil {
		b.Fatalf("load corpus: %v", err)
	}
	largest := map[string]Source{}
	for _, src := range corpus {
		if cur, ok := largest[src.Lang]; !ok || len(src.Src) > len(cur.Src) {
			largest[src.Lang] = src
		}
	}
	for _, strat := range spikeStrategies() {
		for _, lang := range []string{"go", "python", "javascript", "markdown"} {
			src, ok := largest[lang]
			if !ok {
				continue
			}
			b.Run(strat.Name()+"/"+lang, func(b *testing.B) {
				b.SetBytes(int64(len(src.Src)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					chunks := strat.Divide(src.FilePath, src.Lang, src.Src)
					if len(chunks) == 0 {
						b.Fatal("no chunks")
					}
				}
			})
		}
	}
}
