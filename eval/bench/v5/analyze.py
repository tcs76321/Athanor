#!/usr/bin/env python3
"""v5 benchmark analyzer (stdlib only).

Reads a v5 results tree and reports the pre-registered claim vector. The tree
layout is produced by scripts/bench-v5.sh:

    <root>/<model>/soak/<suite>/<arm>/out/results.json
    <root>/<model>/cycle/cycle-<model>.json

The label is the external check outcome; engine acceptance and cycle completion
are reported separately. No judge and no frontier model is involved.
"""

import argparse
import glob
import json
import os
import statistics
import sys


def load_json(path, default=None):
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except FileNotFoundError:
        return default


def row_pass(row):
    if row.get("check_pass") is not None:
        return bool(row["check_pass"])
    if row.get("code_test_passed") is not None:
        return bool(row["code_test_passed"])
    return bool(row.get("engine_accepted"))


def collect_soak(root):
    """suite -> arm -> {task: row}."""
    out = {}
    pattern = os.path.join(root, "soak", "*", "*", "out", "results.json")
    for path in glob.glob(pattern):
        parts = path.split(os.sep)
        suite, arm = parts[-4], parts[-3]
        rows = load_json(path, []) or []
        out.setdefault(suite, {}).setdefault(arm, {})
        for row in rows:
            out[suite][arm][row.get("goal_name")] = row
    return out


def collect_cycle(root):
    tasks = {}
    for path in glob.glob(os.path.join(root, "**", "cycle-*.json"), recursive=True):
        data = load_json(path, {}) or {}
        for t in data.get("tasks", []):
            tasks[t["id"]] = t
    return tasks


def rate(rows):
    n = len(rows)
    if n == 0:
        return 0.0, 0
    return sum(1 for r in rows if row_pass(r)) / n, n


def tokens(rows):
    vals = [r.get("token_cost", 0) for r in rows if r.get("token_cost")]
    return statistics.mean(vals) if vals else 0.0


def md_table(header, rows):
    out = ["| " + " | ".join(header) + " |", "|" + "---|" * len(header)]
    for r in rows:
        out.append("| " + " | ".join(str(x) for x in r) + " |")
    return "\n".join(out)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("-root", required=True, help="model results root")
    ap.add_argument("-model", default="model")
    ap.add_argument("-json", default="")
    ap.add_argument("-md", default="")
    args = ap.parse_args()

    soak = collect_soak(args.root)
    cycle = collect_cycle(args.root)
    if not soak and not cycle:
        print(f"no results under {args.root}", file=sys.stderr)
        return 1

    report = {
        "model": args.model,
        "suites": {},
        "cycle": {},
        "claims": {},
    }
    lines = [f"# v5 results — {args.model}", ""]

    suite_order = ["foundation", "agency", "utility", "safety", "operations", "memory", "quality"]
    for suite in suite_order:
        arms = soak.get(suite, {})
        if not arms and not (suite == "utility" and cycle):
            continue
        rows = []
        for arm in ("single", "full"):
            data = list(arms.get(arm, {}).values())
            r, n = rate(data)
            report["suites"].setdefault(suite, {})[arm] = {"n": n, "pass_rate": r, "tokens": tokens(data)}
            rows.append((arm, n, f"{r:.0%}", f"{tokens(data):.0f}"))
        if suite == "utility":
            tasks = [t for tid, t in cycle.items()]
            n = len(tasks)
            p = sum(1 for t in tasks if t.get("pass")) / n if n else 0.0
            complete = sum(1 for t in tasks if t.get("complete")) / n if n else 0.0
            accepted = sum(1 for t in tasks if t.get("engine_accepted")) / n if n else 0.0
            report["cycle"] = {"n": n, "pass_rate": p, "complete_rate": complete, "engine_accept_rate": accepted}
            rows.append(("cycle", n, f"{p:.0%}", f"complete {complete:.0%}"))
        lines.append(f"## {suite}")
        lines.append("")
        lines.append(md_table(["arm", "n", "external pass", "tokens/pass"], rows))
        lines.append("")

    # Paired wins/ties/losses where both arms exist.
    lines.append("## Paired vs single")
    lines.append("")
    for suite in ("foundation", "agency"):
        arms = soak.get(suite, {})
        single = arms.get("single", {})
        full = arms.get("full", {})
        if not single or not full:
            continue
        w = t = l = 0
        for task, srow in single.items():
            frow = full.get(task)
            if not frow:
                continue
            sp, fp = row_pass(srow), row_pass(frow)
            if fp and not sp:
                w += 1
            elif sp and not fp:
                l += 1
            else:
                t += 1
        lines.append(f"- **{suite}**: {w} wins / {t} ties / {l} losses (full vs single)")
        report["claims"].setdefault("paired", {})[suite] = {"wins": w, "ties": t, "losses": l}
    lines.append("")

    # Claims.
    safety = report["suites"].get("safety", {}).get("single", {})
    safety_ok = safety.get("n", 0) > 0 and safety.get("pass_rate", 0) == 1.0
    lines.append("## Claims")
    lines.append("")
    lines.append(f"- **Safety 100%**: {'PASS' if safety_ok else 'NOT ESTABLISHED'} ({safety})")
    report["claims"]["safety"] = safety_ok
    for suite in ("foundation", "agency"):
        s = report["suites"].get(suite, {}).get("single", {})
        f = report["suites"].get(suite, {}).get("full", {})
        if s.get("n") and f.get("n"):
            delta = (f["pass_rate"] - s["pass_rate"]) * 100
            ok = delta >= -10
            lines.append(f"- **Non-inferiority {suite}**: {'PASS' if ok else 'FAIL'} (delta {delta:+.1f} pts, margin -10)")
            report["claims"][f"non_inferiority_{suite}"] = {"delta_points": delta, "pass": ok}
    cyc = report.get("cycle", {})
    if cyc:
        util_ok = cyc["pass_rate"] >= 0.75
        lines.append(f"- **Utility external pass ≥75%**: {'PASS' if util_ok else 'NOT ESTABLISHED'} ({cyc['pass_rate']:.0%})")
        lines.append(f"- **Cycle completion**: {cyc['complete_rate']:.0%}; engine accept {cyc['engine_accept_rate']:.0%}")
        report["claims"]["utility"] = util_ok
    lines.append("")

    # External validation track: official Exercism tests, custom adapter.
    aider = load_json(os.path.join(args.root, "aider", "report.json"))
    if aider:
        lines.append("## External — Aider Polyglot (Python subset, custom adapter)")
        lines.append("")
        lines.append(f"**{aider.get('passed', 0)}/{aider.get('n', 0)}** official Exercism exercises passed.")
        lines.append("")
        lines.append(md_table(["exercise", "pass", "reason"],
                              [[r.get("slug"), r.get("pass"), (r.get("reason") or "; ".join(r.get("tests_tail", [])))[:100]]
                               for r in aider.get("results", [])]))
        lines.append("")
        lines.append("> Custom adapter over the official Exercism tests; not an official Aider leaderboard entry. "
                     "The label is external to Athanor's verifier.")
        lines.append("")
        report["external"] = aider
        report["claims"]["external_grounded"] = {
            "passed": aider.get("passed", 0), "n": aider.get("n", 0),
        }

    md = "\n".join(lines)
    print(md)
    if args.md:
        with open(args.md, "w", encoding="utf-8") as f:
            f.write(md + "\n")
    if args.json:
        with open(args.json, "w", encoding="utf-8") as f:
            json.dump(report, f, indent=2)
            f.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
