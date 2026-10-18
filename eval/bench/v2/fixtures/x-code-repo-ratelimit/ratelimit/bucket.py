class Limiter:
    """Implement the token bucket described in __init__.py."""

    def __init__(self, rate, capacity, store, clock):
        raise NotImplementedError("Limiter.__init__")

    def allow(self, key, n=1):
        raise NotImplementedError("Limiter.allow")
