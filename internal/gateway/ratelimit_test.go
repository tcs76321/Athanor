package gateway

import (
	"testing"
	"time"
)

// TestTokenBucket_FirstTakeSucceeds pins the "fresh bucket
// starts full" property: a host that has never been seen is
// not penalized.
func TestTokenBucket_FirstTakeSucceeds(t *testing.T) {
	now := time.Unix(0, 0)
	b := newTokenBucket(now, 5, 0.1)
	if !b.take(now) {
		t.Fatal("first take on a full bucket should succeed")
	}
}

// TestTokenBucket_DrainThenRefuse is the §21.5
// rate-limit-bucket property: once the bucket is empty,
// subsequent takes return false until time passes.
func TestTokenBucket_DrainThenRefuse(t *testing.T) {
	now := time.Unix(0, 0)
	b := newTokenBucket(now, 2, 0.1)
	// Two takes drain the bucket.
	if !b.take(now) {
		t.Fatal("first take should succeed")
	}
	if !b.take(now) {
		t.Fatal("second take should succeed")
	}
	// Third immediate take is refused.
	if b.take(now) {
		t.Fatal("third take on empty bucket should fail")
	}
}

// TestTokenBucket_ContinuousRefill pins the
// "refill-continuously" shape: a partial token accumulates
// over multiple short intervals, not in a single chunk
// at the next Take. This is the §21.5 quality bar (the
// alternative is "all-at-once at second boundaries" which
// is fine for a coarse rate limiter but wrong for a
// per-host bucket where two requests 0.6s apart at
// rate=1/s should be honored).
func TestTokenBucket_ContinuousRefill(t *testing.T) {
	now := time.Unix(0, 0)
	b := newTokenBucket(now, 1, 1) // 1 token/sec
	// Drain.
	if !b.take(now) {
		t.Fatal("first take should succeed")
	}
	// 0.5s later: 0.5 tokens accumulated. Not enough.
	if b.take(now.Add(500 * time.Millisecond)) {
		t.Fatal("take at 0.5s should fail (bucket at 0.5 tokens)")
	}
	// 1s later: 1.0 tokens accumulated. Should succeed.
	if !b.take(now.Add(1 * time.Second)) {
		t.Fatal("take at 1.0s should succeed (bucket back to 1)")
	}
}

// TestTokenBucket_CapacityCap pins the "tokens cannot
// exceed capacity" property: a bucket idle for a long
// time does not accumulate infinite tokens.
func TestTokenBucket_CapacityCap(t *testing.T) {
	now := time.Unix(0, 0)
	b := newTokenBucket(now, 3, 1)
	// 1 hour later: would be 3600 tokens, capped at 3.
	if !b.take(now.Add(time.Hour)) {
		t.Fatal("first take after long idle should succeed")
	}
	if !b.take(now.Add(time.Hour)) {
		t.Fatal("second take should succeed")
	}
	if !b.take(now.Add(time.Hour)) {
		t.Fatal("third take should succeed")
	}
	if b.take(now.Add(time.Hour)) {
		t.Fatal("fourth take should fail (capped at capacity)")
	}
}

// TestTokenBucket_ZeroConfigRefuses pins the "fail-closed
// on capacity=0" property: a bucket with no capacity
// refuses every take. A `refill=0` configuration is *not*
// a misconfiguration — it just means "no replenishment,"
// and the cold-start token is still available. A
// permissive misconfiguration (capacity=0) would be a
// bug, hence the asymmetry.
func TestTokenBucket_ZeroConfigRefuses(t *testing.T) {
	now := time.Unix(0, 0)
	cases := []struct {
		name     string
		capacity float64
		refill   float64
		want     bool
	}{
		{"zero_capacity", 0, 1, false},
		{"both_zero", 0, 0, false},
		{"refill_zero_still_cold_starts", 1, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTokenBucket(now, c.capacity, c.refill)
			if got := b.take(now); got != c.want {
				t.Errorf("take = %v, want %v", got, c.want)
			}
		})
	}
}

// TestHostRateLimiter_PerHostIsolation pins the §21.5
// "per-host" property: a host that exhausts its bucket
// does not affect a different host's bucket.
func TestHostRateLimiter_PerHostIsolation(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	r := newHostRateLimiter(clock, 1, 0)
	// Drain host A.
	if !r.take("a.example.org:443") {
		t.Fatal("first take on a.example.org should succeed")
	}
	if r.take("a.example.org:443") {
		t.Fatal("second take on a.example.org should fail")
	}
	// host B has its own bucket and is unaffected.
	if !r.take("b.example.org:443") {
		t.Fatal("first take on b.example.org should succeed (per-host isolation)")
	}
}

// TestHostRateLimiter_ColdStart pins the "first request
// to a fresh host is not penalized" property. Operators
// whose allowlist grows over time should not see cold-host
// rate-limit denials.
func TestHostRateLimiter_ColdStart(t *testing.T) {
	clock := func() time.Time { return time.Unix(0, 0) }
	r := newHostRateLimiter(clock, 1, 0) // refill=0 so the only token is the cold-start one
	if !r.take("fresh.example.org:443") {
		t.Fatal("cold-start take should succeed (bucket starts full)")
	}
}
