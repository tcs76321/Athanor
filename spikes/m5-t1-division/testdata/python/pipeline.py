"""Pipeline utilities for a synthetic data-processing job.

This module is a hand-written fixture for the M5-T1 division spike. It
is deliberately shaped like real code: module docstring, imports,
constants, classes with docstrings, decorated functions, nested
functions, and multi-line calls.

The docstring exists to punish naive regex splitters: it contains
lines that LOOK like top-level declarations but live inside a string.
Real docstrings carry flush-left text too, so here are column-zero
declaration-shaped lines:

def fake_one():
    pass

class FakeClass:
    pass

@fake_decorator
def fake_decorated():
    pass

None of those lines may become a chunk boundary in a string-aware
strategy.
"""

import functools
import json
import os
import sys


DEFAULT_RETRIES = 3
DEFAULT_TIMEOUT_S = 30.0
STATUS_PENDING = "pending"
STATUS_DONE = "done"
STATUS_FAILED = "failed"


class Stage:
    """A single stage of the pipeline.

    The class docstring also contains declaration-shaped text:

        def not_real():
            return None

    Attributes:
        name: stage name
        retries: maximum retry count
    """

    def __init__(self, name, retries=DEFAULT_RETRIES, timeout=DEFAULT_TIMEOUT_S):
        self.name = name
        self.retries = retries
        self.timeout = timeout
        self.status = STATUS_PENDING
        self._attempts = 0

    def describe(self):
        return f"Stage({self.name!r}, retries={self.retries}, status={self.status})"

    def mark_done(self):
        self.status = STATUS_DONE

    def mark_failed(self):
        self.status = STATUS_FAILED


class Pipeline:
    """An ordered collection of stages with dependency awareness."""

    def __init__(self, name):
        self.name = name
        self.stages = []
        self._by_name = {}

    def add_stage(self, stage):
        if stage.name in self._by_name:
            raise ValueError(f"duplicate stage name: {stage.name}")
        self.stages.append(stage)
        self._by_name[stage.name] = stage
        return self

    def stage(self, name):
        return self._by_name.get(name)

    def pending(self):
        return [s for s in self.stages if s.status == STATUS_PENDING]

    def summary(self):
        counts = {STATUS_PENDING: 0, STATUS_DONE: 0, STATUS_FAILED: 0}
        for s in self.stages:
            counts[s.status] = counts.get(s.status, 0) + 1
        return {"pipeline": self.name, "stages": len(self.stages), **counts}


def _attempt(stage, payload):
    """Run one attempt of a stage. Nested defs follow.

    The nested function below sits INSIDE a function body; a
    top-level-boundary strategy must NOT split here.
    """
    def run_once():
        if payload is None:
            raise ValueError("payload required")
        return {"stage": stage.name, "ok": True}

    try:
        result = run_once()
        stage.mark_done()
        return result
    except Exception as exc:  # noqa: BLE001 - spike fixture
        stage.mark_failed()
        return {"stage": stage.name, "ok": False, "error": str(exc)}


@functools.lru_cache(maxsize=128)
def cached_hash(text):
    """Deterministic hash for demo purposes (not cryptographic)."""
    total = 0
    for ch in text:
        total = (total * 31 + ord(ch)) % (2**61 - 1)
    return total


def load_config(path=None):
    """Load a JSON config, with defaults for missing keys.

    The multi-line call below is a bracket continuation whose argument
    lines start at column 0 with declaration-shaped strings — an
    indentation-aware splitter must not cut here; a naive regex will.
    """
    defaults = {
        "retries": DEFAULT_RETRIES,
        "timeout": DEFAULT_TIMEOUT_S,
    }
    if path is None:
        return defaults
    with open(path, "r", encoding="utf-8") as fh:
        raw = json.load(fh)
    merged = dict(defaults)
    merged.update(raw)
    return merged


def run_pipeline(pipeline, payload=None, retries=DEFAULT_RETRIES):
    """Execute pending stages in order, honoring per-stage retries."""
    results = []
    for stage in pipeline.pending():
        last_error = None
        for attempt in range(max(1, stage.retries or retries)):
            stage._attempts = attempt + 1
            outcome = _attempt(stage, payload)
            if outcome.get("ok"):
                last_error = None
                break
            last_error = outcome.get("error")
        if last_error is not None:
            results.append({"stage": stage.name, "status": STATUS_FAILED, "error": last_error})
        else:
            results.append({"stage": stage.name, "status": STATUS_DONE})
    return results


def write_summary(pipeline, directory):
    """Write the pipeline summary as JSON; returns the output path."""
    os.makedirs(directory, exist_ok=True)
    out_path = os.path.join(directory, f"{pipeline.name}.summary.json")
    with open(out_path, "w", encoding="utf-8") as fh:
        json.dump(pipeline.summary(), fh, indent=2, sort_keys=True)
    return out_path


TRIPLE_SHAPED = """
The constant below is a plain string. Everything inside it is text,
including declarations that sit at column zero, exactly like real code:

def fake_async():
    pass

class FakeAlone:
    pass

@decorated_too
def fake_decorated():
    pass

A line that begins with three quotes is how the scanner escapes this
state, and the line after this paragraph is how it returns.
"""



def uses_triple_constant(n):
    """Reference the triple-quoted constant so linters stay quiet."""
    lines = TRIPLE_SHAPED.strip().splitlines()
    return len(lines) + n


def main(argv=None):
    argv = argv or sys.argv[1:]
    config = load_config(argv[0] if argv else None)
    pipeline = Pipeline("demo")
    pipeline.add_stage(Stage("extract", timeout=config["timeout"]))
    pipeline.add_stage(Stage("transform"))
    pipeline.add_stage(Stage("load"))
    outcomes = run_pipeline(pipeline)
    write_summary(pipeline, ".")
    return 0 if all(o["status"] == STATUS_DONE for o in outcomes) else 1


if __name__ == "__main__":
    sys.exit(main())
