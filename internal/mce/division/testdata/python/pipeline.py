"""Pipeline utilities for a synthetic data-processing job.

This fixture is shaped like real code: a module docstring, imports,
constants, classes with docstrings, and a decorated function. It exists
so the division property suite exercises Python structural boundaries.
"""

import os
import sys
from dataclasses import dataclass

DEFAULT_BATCH = 256


@dataclass
class Record:
    """One buffered record."""

    key: str
    value: int


class Pipeline:
    """Batches records and flushes them in fixed-size groups."""

    def __init__(self, batch=DEFAULT_BATCH):
        self.batch = batch
        self._buf = []

    def add(self, rec):
        self._buf.append(rec)
        if len(self._buf) >= self.batch:
            self.flush()

    def flush(self):
        """Flush buffered records."""
        self._buf = []


def build(path):
    """Construct a pipeline rooted at path."""
    batch = int(os.environ.get("BATCH", DEFAULT_BATCH))
    return Pipeline(batch=batch)


if __name__ == "__main__":
    target = sys.argv[1] if len(sys.argv) > 1 else "."
    build(target).flush()