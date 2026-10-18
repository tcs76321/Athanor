"""A small quota module to document (fixture)."""


class QuotaError(ValueError):
    """Raised when a quota operation is invalid or would exceed the limit."""


class Quota:
    def __init__(self, limit):
        self.limit = limit
        self.used = 0

    def spend(self, n):
        if n < 0:
            raise QuotaError("n must be non-negative")
        if self.used + n > self.limit:
            raise QuotaError("quota exceeded")
        self.used += n
        return self.remaining()

    def refund(self, n):
        if n < 0:
            raise QuotaError("n must be non-negative")
        self.used = max(0, self.used - n)
        return self.remaining()

    def remaining(self):
        return self.limit - self.used
