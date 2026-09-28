package division

import "testing"

// BenchmarkDivideRepo measures division throughput over this repository's
// own Go sources — the M5-T1 corpus benchmark, ported into the shipped
// package. The corpus is loaded outside the timer; a Divider is constructed
// per iteration to include the grammar-registry/parser-pool path, matching
// the M5-T1 methodology (whose numbers included per-call construction).
func BenchmarkDivideRepo(b *testing.B) {
	corpus, err := loadDir("../../", true)
	if err != nil {
		b.Fatalf("load repo corpus: %v", err)
	}
	if len(corpus) == 0 {
		b.Fatal("empty corpus")
	}
	total := 0
	for _, s := range corpus {
		total += len(s.Src)
	}
	b.SetBytes(int64(total))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d := New(Options{})
		for _, s := range corpus {
			d.Divide(s.RelPath, s.Lang, s.Src)
		}
	}
}
