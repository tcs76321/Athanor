class Limiter:
    """Per-key token bucket; state is (tokens, last_time) in the store."""

    def __init__(self, rate, capacity, store, clock):
        self.rate = float(rate)
        self.capacity = float(capacity)
        self.store = store
        self.clock = clock

    def allow(self, key, n=1):
        now = self.clock()
        state = self.store.get(key)
        if state is None:
            tokens, last = self.capacity, now
        else:
            tokens, last = state
            tokens = min(self.capacity, tokens + max(0.0, now - last) * self.rate)
            last = now
        if n > self.capacity or tokens < n:
            self.store.set(key, (tokens, last))
            return False
        self.store.set(key, (tokens - n, last))
        return True
