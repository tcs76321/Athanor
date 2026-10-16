package policy

import "hash/fnv"

// ExplorationEpsilon is the floor probability that the learned selection bias
// is dropped for a familiar task class, so evidence keeps accruing (M8-T29;
// ADR-0067 §5). On a novel class the probability rises to 1 with the novelty
// score.
const ExplorationEpsilon = 0.10

// Explore reports whether the learned selection bias should be dropped for this
// job to gather fresh evidence away from the learned default. It is
// deterministic in key (a job id), so a re-run of the same job explores the
// same way, and monotonic in novelty: a fully novel class always explores; a
// fully familiar one explores at the ExplorationEpsilon floor. Pure and total.
func Explore(key string, novelty float64) bool {
	if novelty < 0 {
		novelty = 0
	}
	if novelty > 1 {
		novelty = 1
	}
	p := ExplorationEpsilon + (1-ExplorationEpsilon)*novelty
	if p >= 1 {
		return true
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	// 10_000 buckets give 0.01% resolution and avoid a float comparison on the
	// boundary; the same key always lands in the same bucket.
	return int64(h.Sum64()%10000) < int64(p*10000)
}
