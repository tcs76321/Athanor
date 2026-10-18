# Rate limiter fixture

Implement `Limiter` in `ratelimit/bucket.py`. The public API and the state shape
are documented in `ratelimit/__init__.py`. `MemoryStore` is complete.

Requirements:

- Tokens refill at `rate` per second, capped at `capacity`.
- `allow(key, n)` consumes `n` tokens or returns `False` without consuming.
- `n > capacity` is always `False`.
- Keys are independent.
- State is persisted through the store, so a new `Limiter` with the same store
  and clock sees the previous tokens/last time.
- Use the injected clock; never call `time.time()` directly.
