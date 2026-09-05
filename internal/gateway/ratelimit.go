package gateway

import (
	"sync"
	"time"
)

// tokenBucket is a §21.5 per-host rate-limit bucket (ADR-0017
// §5). It is a leaky-bucket-with-arithmetic variant: each
// bucket has a capacity and a refill rate; a `Take` call
// returns `true` if a token was available, `false` if the
// bucket is empty. Empty buckets refill continuously at
// `refillPerSecond` tokens per second, not in discrete
// chunks. The implementation is ~30 lines and avoids the
// `golang.org/x/time/rate` dependency (AGENTS.md: adding a
// dependency is a project decision; the §D1 dep list is two
// packages and stays two packages through T5).
//
// tokenBucket is not safe for concurrent use. The
// `hostRateLimiter` wrapper below owns the lock.
type tokenBucket struct {
	// capacity is the maximum number of tokens the
	// bucket can hold. Saturates here; the value
	// doubles as the per-minute rate in the §21.5
	// config (capacity == rate_limit_per_minute).
	capacity float64
	// refillPerSecond is the continuous refill rate.
	// A capacity of 30 with refill 0.5 means the
	// bucket recovers one token every 2 seconds
	// after being drained.
	refillPerSecond float64
	// tokens is the current bucket level. The float
	// type means partial tokens accumulate; an
	// operator who wants whole-token granularity can
	// use a capacity of 60 with refill 1.
	tokens float64
	// lastRefill is the time of the last refill
	// update. Used to compute how many tokens to
	// add on the next Take.
	lastRefill time.Time
}

// newTokenBucket constructs a full bucket at `now`. The
// caller passes `now` so tests are deterministic. A
// misconfigured bucket (capacity=0 or refill=0) is
// represented by a bucket that always refuses takes; the
// constructor's panic-free return is the safe shape for
// the per-host wrapper, which constructs lazily.
func newTokenBucket(now time.Time, capacity, refillPerSecond float64) *tokenBucket {
	return &tokenBucket{
		capacity:        capacity,
		refillPerSecond: refillPerSecond,
		tokens:          capacity,
		lastRefill:      now,
	}
}

// take attempts to consume one token. Returns `true` if a
// token was available, `false` otherwise. The bucket is
// refilled continuously based on the time elapsed since
// the last call. A bucket constructed with capacity=0 or
// refill=0 always returns false (a misconfigured rate
// limiter is the safe direction; a permissive
// misconfiguration is a configuration smell operators
// can debug from the network event log).
func (b *tokenBucket) take(now time.Time) bool {
	if b.capacity <= 0 {
		// A zero-capacity bucket has no tokens to
		// give. The check is on capacity, not on
		// refill, so a `capacity=1, refill=0`
		// configuration still allows the cold-start
		// take — only subsequent takes are
		// refused once the bucket drains.
		return false
	}
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed > 0 {
		if b.refillPerSecond > 0 {
			b.tokens += elapsed * b.refillPerSecond
		}
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		b.lastRefill = now
	}
	if b.tokens >= 1 {
		b.tokens -= 1
		return true
	}
	return false
}

// hostRateLimiter is the §21.5 per-host wrapper. One
// `hostRateLimiter` is owned by a `Client`; the map is
// keyed on the `host:port` string from the URL. The
// `sync.Mutex` is held only during map lookup and
// bucket access; the bucket's `take` is itself short
// (no I/O), so contention is low.
//
// A `hostRateLimiter` is safe for concurrent use.
type hostRateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*tokenBucket
	now      func() time.Time // injected for tests
	capacity float64
	refill   float64
}

// newHostRateLimiter constructs a limiter that gives each
// host `capacity` tokens and refills at `refill` tokens
// per second. The `now` function is the clock; tests pass
// a deterministic function.
func newHostRateLimiter(now func() time.Time, capacity, refillPerSecond float64) *hostRateLimiter {
	if now == nil {
		now = time.Now
	}
	return &hostRateLimiter{
		buckets:  make(map[string]*tokenBucket),
		now:      now,
		capacity: capacity,
		refill:   refillPerSecond,
	}
}

// take returns `true` if a token was available for `host`,
// `false` otherwise. The first request to a given host
// creates a full bucket (so a fresh host is not penalized
// for a cold start).
func (h *hostRateLimiter) take(host string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, ok := h.buckets[host]
	if !ok {
		b = newTokenBucket(h.now(), h.capacity, h.refill)
		h.buckets[host] = b
	}
	return b.take(h.now())
}
