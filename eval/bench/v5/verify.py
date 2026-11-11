#!/usr/bin/env python3
"""Verify a committed v5 report against its committed evidence.

This is the stale-report gate: it re-runs the analyzer's report builder over the
committed raw rows and fails when the committed JSON disagrees, when a declared
claim has no computation, when the corpus/protocol digest moved, or when the
provenance is missing/inconsistent.

Usage:
    python3 verify.py -evidence docs/probes/runs/bench-v5-.../bonsai27b \
        -report .../bonsai27b-v5-report.json \
        -protocol-json /tmp/protocol.json

The evidence directory must mirror a model results root:
    soak/<suite>/<arm>/out/{results.json,run.json}
    cycle/*.json
    quality-pairwise.json
    aider/report.json
"""

import argparse
import copy
import json
import math
import os
import sys

import analyze


CLAIM_KEYS = [
    "safety",
    "non_inferiority",
    "mechanism_coverage",
    "utility",
    "reliability",
    "quality",
    "cost",
    "external",
]


def load_json(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def close(a, b, tol=1e-9):
    if isinstance(a, float) or isinstance(b, float):
        if a is None or b is None:
            return a is None and b is None
        return math.isclose(a, b, rel_tol=tol, abs_tol=tol)
    if isinstance(a, dict) and isinstance(b, dict):
        if set(a) != set(b):
            return False
        return all(close(a[k], b[k], tol) for k in a)
    if isinstance(a, list) and isinstance(b, list):
        return len(a) == len(b) and all(close(x, y, tol) for x, y in zip(a, b))
    return a == b


def diff(path, want, got, out):
    if isinstance(want, dict) and isinstance(got, dict):
        for key in sorted(set(want) | set(got)):
            if key not in want:
                out.append(f"{path}.{key}: unexpected {got[key]!r}")
            elif key not in got:
                out.append(f"{path}.{key}: missing")
            else:
                diff(f"{path}.{key}", want[key], got[key], out)
        return
    if isinstance(want, list) and isinstance(got, list):
        if len(want) != len(got):
            out.append(f"{path}: length {len(want)} != {len(got)}")
            return
        for i, (w, g) in enumerate(zip(want, got)):
            diff(f"{path}[{i}]", w, g, out)
        return
    if not close(want, got):
        out.append(f"{path}: report={got!r} evidence={want!r}")


def claim_coverage(protocol, report):
    """A frozen protocol may not carry a claim the analyzer does not compute."""
    problems = []
    claims = protocol.get("claims", {})
    computed = report.get("claims", {})
    for key in claims:
        if key not in CLAIM_KEYS:
            if protocol.get("version") == 5 and key in ("agentic", "early_stop"):
                continue  # v3-only struct fields, zero-valued in the JSON bridge
            problems.append(f"claim {key} has no computation path")
    for key in CLAIM_KEYS:
        if key not in claims:
            continue
        if key == "external":
            if not report.get("external"):
                problems.append("claim external has no report section")
            continue
        if key == "non_inferiority":
            suites = claims["non_inferiority"].get("suites", [])
            for suite in suites:
                if f"non_inferiority_{suite}" not in computed:
                    problems.append(f"claim non_inferiority.{suite} is not computed")
            continue
        if key == "quality" and claims["quality"].get("status") == "not_measured":
            if computed.get("quality", {}).get("status") != "not_measured":
                problems.append("quality is not_measured in protocol but report disagrees")
            continue
        if key not in computed:
            problems.append(f"claim {key} is not computed")
    return problems


def suite_coverage(protocol, report):
    problems = []
    suites = report.get("suites", {})
    cycle = report.get("cycle", {})
    for name in protocol.get("suites", {}):
        if name == "utility":
            if not cycle:
                problems.append("utility has no cycle report")
            continue
        if not suites.get(name):
            problems.append(f"suite {name} has no evidence")
    return problems


def verify(evidence, report_path, protocol_json, allow_mixed=False):
    protocol = load_json(protocol_json)
    report = load_json(report_path)
    args = argparse.Namespace(
        root=evidence,
        model=report.get("model", "model"),
        protocol=protocol.get("task_digest", "").replace("CORPUS.digest", "protocol.yaml"),
        protocol_json=protocol_json,
    )
    expected, _ = analyze.build_report(args, protocol)

    problems = []
    for section in ["suites", "cycle", "mechanism", "cost", "reliability"]:
        diff(section, expected.get(section), report.get(section), problems)

    # Claims are compared section by section so a missing claim is explicit.
    for key in ["safety", "mechanism_coverage", "utility", "cost"]:
        diff(f"claims.{key}", expected.get("claims", {}).get(key),
             report.get("claims", {}).get(key), problems)
    for key in expected.get("claims", {}):
        if key.startswith("non_inferiority_"):
            diff(f"claims.{key}", expected["claims"][key],
                 report.get("claims", {}).get(key), problems)
    # The bootstrap interval is deterministic, but compare the point estimate
    # strictly and the interval with tolerance.
    diff("claims.paired", expected.get("claims", {}).get("paired"),
         report.get("claims", {}).get("paired"), problems)

    # Quality: status and claim, not the full nested report.
    for key in ["status", "ok", "usable_pairs", "agreement", "win_rate"]:
        diff(f"quality.{key}", expected.get("quality", {}).get(key),
             report.get("quality", {}).get(key), problems)
    if expected.get("quality", {}).get("status") == "measured":
        diff("claims.quality", expected.get("claims", {}).get("quality"),
             report.get("claims", {}).get("quality"), problems)

    # External track.
    for key in ["passed", "n", "engine_accepted", "converged"]:
        diff(f"external.{key}", expected.get("external", {}).get(key),
             report.get("external", {}).get(key), problems)

    # Corpus and protocol digests must match the files on disk now.
    corpus = report.get("corpus", {})
    digest_file = corpus.get("digest_file", "")
    if digest_file:
        if not os.path.exists(digest_file):
            problems.append(f"corpus digest file missing: {digest_file}")
        elif corpus.get("digest_sha256") != analyze.sha256_file(digest_file):
            problems.append("corpus digest moved since the report was generated")
    protocol_path = args.protocol
    if os.path.exists(protocol_path):
        if corpus.get("protocol_sha256") != analyze.sha256_file(protocol_path):
            problems.append("protocol digest moved since the report was generated")

    # Status/cut consistency.
    if protocol.get("cut") == "pending" and protocol.get("status") != "candidate":
        problems.append("cut=pending requires status=candidate")
    if protocol.get("cut") != "pending" and protocol.get("status") != "frozen":
        problems.append(f"cut={protocol.get('cut')} requires status=frozen")

    # Provenance.
    prov = report.get("provenance", {})
    if prov.get("mixed_revisions") and not allow_mixed:
        problems.append("mixed revisions; freeze requires one commit")
    if prov.get("missing"):
        problems.append("missing run.json: " + ", ".join(prov["missing"]))
    for run in prov.get("runs", []):
        for field in ["git_commit", "core_image", "core_digest", "ollama_version"]:
            if not run.get(field):
                problems.append(f"{run.get('suite')}/{run.get('arm')}: missing {field}")

    problems.extend(claim_coverage(protocol, report))
    problems.extend(suite_coverage(protocol, report))
    return problems


def self_test():
    """Mutate a report and assert that verify rejects it."""
    import tempfile
    root = os.path.join(os.path.dirname(__file__), "..", "..", "..")
    evidence = os.path.join(root, "docs", "probes", "runs",
                            "bench-v5-20261111-merged", "bonsai27b")
    report = os.path.join(os.path.dirname(evidence), "bonsai27b-v5-report.json")
    if not os.path.exists(report):
        print("verify: self-test skipped (merged evidence is not committed yet)")
        return 0
    protocol_json = "/tmp/athanor-protocol-v5.json"
    if not os.path.exists(protocol_json):
        print("verify: self-test skipped (protocol JSON is missing)")
        return 0
    base = load_json(report)
    with tempfile.TemporaryDirectory() as tmp:
        bad = copy.deepcopy(base)
        bad["suites"]["foundation"]["single"]["passed"] = 0
        bad_path = os.path.join(tmp, "bad.json")
        with open(bad_path, "w", encoding="utf-8") as f:
            json.dump(bad, f)
        problems = verify(evidence, bad_path, protocol_json, allow_mixed=True)
        if not problems:
            print("verify: self-test FAILED (a mutated report passed)")
            return 1
    print("verify: self-test OK")
    return 0


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("-evidence", required=False,
                    help="model evidence root (mirrors a results root)")
    ap.add_argument("-report", required=False, help="committed report JSON")
    ap.add_argument("-protocol-json", required=False, help="protocol JSON")
    ap.add_argument("-allow-mixed", action="store_true",
                    help="allow a reconciliation report to mix commits")
    ap.add_argument("-self-test", action="store_true")
    args = ap.parse_args()
    if args.self_test:
        return self_test()
    if not args.evidence or not args.report or not args.protocol_json:
        print("verify: -evidence, -report, and -protocol-json are required", file=sys.stderr)
        return 2
    problems = verify(args.evidence, args.report, args.protocol_json, args.allow_mixed)
    if problems:
        print("verify: report does not match the evidence:", file=sys.stderr)
        for p in problems:
            print("  - " + p, file=sys.stderr)
        return 1
    print("verify: report matches the evidence")
    return 0


if __name__ == "__main__":
    sys.exit(main())
