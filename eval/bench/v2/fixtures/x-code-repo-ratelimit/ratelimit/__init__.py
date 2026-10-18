"""Per-key token-bucket rate limiting.

Public API:
    Limiter(rate, capacity, store, clock)
        rate: tokens per second (float)
        capacity: maximum tokens per key
        store: object with get(key) and set(key, value)
        clock: zero-argument callable returning seconds

    Limiter.allow(key, n=1) -> bool
        Refill the key's bucket for elapsed time, then consume n tokens if
        available. Tokens never exceed capacity. Each key is independent.
        The store persists the per-key state across Limiter instances.

    MemoryStore(): an in-memory store with get/set.

State stored per key is a tuple (tokens, last_time). Do not change the public
API or the MemoryStore contract.
"""
from .bucket import Limiter
from .store import MemoryStore

__all__ = ["Limiter", "MemoryStore"]
