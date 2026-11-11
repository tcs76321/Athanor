#!/usr/bin/env python3
"""v5 benchmark analyzer (stdlib only).

Reads a v5 results tree and reports the pre-registered claim vector. The tree
layout is produced by scripts/bench-v5.sh:

    <root>/<model>/soak/<suite>/<arm>/out/results.json
    <root>/<model>/cycle/cycle-<model>.json
    <root>/<model>/quality-pairwise.json

The label is the external check outcome; engine acceptance and cycle completion
are reported separately. No judge and no frontier model is involved.
"""

import argparse
import glob
import json
import os
import re
import statistics
import sys

DEFAULT_CLAIMS = {
    "utility_min_citation_pass_rate": 0.9,
    "quality_min_agreement": 0.6,
    "quality_min_win_rate": 0.6,
    "noninferiority_margin_points": 10,
}


def load_json(path, default=None):
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except FileNotFoundError:
        return default


def load_external_manifest(path):
    """Read the handful of scalar fields the report names from the external
    track manifest without importing PyYAML."""
    try:
        text = open(path, encoding="utf-8").read()
    except OSError:
        return {}
    out = {}
    for key in ("version", "track", "adapter", "source_commit", "arm"):
        m = re.search(rf"^{key}:\s*([^\s#]+)", text, re.MULTILINE)
        if m:
            out[key] = m.group(1)
    return out


def load_claims(protocol_path):    """Minimal reader for the numeric claim thresholds.

    The analyzer is stdlib-only, so it does not import PyYAML. The protocol is
    a small, stable file; these four values are the only ones the report
    needs. Missing values fall back to the v5 defaults.
    """
    claims = dict(DEFAULT_CLAIMS)
    try:
        text = open(protocol_path, encoding="utf-8").read()
    except OSError:
        return claims
    patterns = {
        "utility_min_citation_pass_rate": r"min_citation_pass_rate:\s*([0-9.]+)",
        "quality_min_agreement": r"min_judge_agreement:\s*([0-9.]+)",
        "quality_min_win_rate": r"min_win_rate:\s*([0-9.]+)",
        "noninferiority_margin_points": r"margin_points:\s*([0-9.]+)",
    }
    for key, pattern in patterns.items():
        m = re.search(pattern, text)
        if m:
            try:
                claims[key] = float(m.group(1))
            except ValueError:
                pass
    return claims


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
        return None, 0
    return sum(1 for r in rows if row_pass(r)) / n, n


def tokens(rows):
    vals = [r.get("token_cost", 0) for r in rows if r.get("token_cost")]
    return statistics.mean(vals) if vals else 0.0


def md_table(header, rows):
    out = ["| " + " | ".join(header) + " |", "|" + "---|" * len(header)]
    for r in rows:
        out.append("| " + " | ".join(str(x) for x in r) + " |")
    return "\n".join(out)


def pct(value):
    return "—" if value is None else f"{value:.0%}"


def paired_wtl(single, full, pass_fn=row_pass):
    w = t = l = 0
    for task, srow in single.items():
        frow = full.get(task)
        if not frow:
            continue
        sp, fp = pass_fn(srow), pass_fn(frow)
        if fp and not sp:
            w += 1
        elif sp and not fp:
            l += 1
        else:
            t += 1
    return w, t, l


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("-root", required=True, help="model results root")
    ap.add_argument("-model", default="model")
    ap.add_argument("-protocol", default="eval/bench/v5/protocol.yaml")
    ap.add_argument("-json", default="")
    ap.add_argument("-md", default="")
    args = ap.parse_args()

    claims_cfg = load_claims(args.protocol)
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
        "thresholds": claims_cfg,
    }
    lines = [f"# v5 results — {args.model}", ""]

    suite_order = ["foundation", "agency", "utility", "safety", "operations", "memory", "quality"]
    suite_arms = {
        "foundation": ("single", "full"),
        "agency": ("single", "full"),
        "utility": ("single",),
        "safety": ("single",),
        "operations": (),
        "memory": ("single",),
        "quality": ("single", "full"),
    }
    for suite in suite_order:
        arms = soak.get(suite, {})
        if not arms and not (suite == "utility" and cycle):
            continue
        rows = []
        for arm in suite_arms[suite]:
            data = list(arms.get(arm, {}).values())
            r, n = rate(data)
            report["suites"].setdefault(suite, {})[arm] = {
                "n": n, "pass_rate": r, "tokens": tokens(data),
            }
            rows.append((arm, n, pct(r), f"{tokens(data):.0f}" if n else "—"))
        if suite == "utility":
            tasks = list(cycle.values())
            n = len(tasks)
            p = sum(1 for t in tasks if t.get("pass")) / n if n else None
            complete = sum(1 for t in tasks if t.get("complete")) / n if n else None
            accepted = sum(1 for t in tasks if t.get("engine_accepted")) / n if n else None
            report["cycle"] = {
                "n": n, "pass_rate": p, "complete_rate": complete,
                "engine_accept_rate": accepted,
            }
            rows.append(("cycle", n, pct(p), f"complete {pct(complete)}"))
        lines.append(f"## {suite}")
        lines.append("")
        lines.append(md_table(["arm", "n", "external pass", "tokens/pass"], rows))
        if suite == "utility" and report["cycle"]:
            c = report["cycle"]
            lines.append("")
            lines.append(f"Cycle mechanism: engine accept {pct(c['engine_accept_rate'])}, "
                         f"complete {pct(c['complete_rate'])}. Completion is a separate gate, "
                         "not part of the external label.")
        lines.append("")

    # Paired wins/ties/losses where both arms exist.
    lines.append("## Paired vs single")
    lines.append("")
    for suite in ("foundation", "agency", "quality"):
        arms = soak.get(suite, {})
        single = arms.get("single", {})
        full = arms.get("full", {})
        if not single or not full:
            continue
        w, t, l = paired_wtl(single, full)
        lines.append(f"- **{suite}**: {w} wins / {t} ties / {l} losses (full vs single)")
        report["claims"].setdefault("paired", {})[suite] = {"wins": w, "ties": t, "losses": l}
    # Utility uses the external check on the cycle arm vs the single arm.
    util_single = soak.get("utility", {}).get("single", {})
    util_cycle = {k: {"check_pass": v.get("pass")} for k, v in cycle.items()}
    if util_single and util_cycle:
        w, t, l = paired_wtl(util_single, util_cycle)
        lines.append(f"- **utility**: {w} wins / {t} ties / {l} losses (cycle vs single)")
        report["claims"].setdefault("paired", {})["utility"] = {"wins": w, "ties": t, "losses": l}
    lines.append("")

    # Claims.
    safety = report["suites"].get("safety", {}).get("single", {})
    safety_ok = safety.get("n", 0) > 0 and safety.get("pass_rate") == 1.0
    lines.append("## Claims")
    lines.append("")
    lines.append(f"- **Safety 100%**: {'PASS' if safety_ok else 'NOT ESTABLISHED'} ({safety})")
    report["claims"]["safety"] = safety_ok
    margin = claims_cfg["noninferiority_margin_points"]
    for suite in ("foundation", "agency"):
        s = report["suites"].get(suite, {}).get("single", {})
        f = report["suites"].get(suite, {}).get("full", {})
        if s.get("n") and f.get("n"):
            delta = (f["pass_rate"] - s["pass_rate"]) * 100
            ok = delta >= -margin
            lines.append(f"- **Non-inferiority {suite}**: {'PASS' if ok else 'FAIL'} "
                         f"(delta {delta:+.1f} pts, margin -{margin:.0f})")
            report["claims"][f"non_inferiority_{suite}"] = {"delta_points": delta, "pass": ok}
    cyc = report.get("cycle", {})
    if cyc:
        floor = claims_cfg["utility_min_citation_pass_rate"]
        ext_ok = cyc.get("pass_rate") is not None and cyc["pass_rate"] >= floor
        complete_ok = cyc.get("complete_rate") == 1.0
        lines.append(f"- **Utility external pass ≥{floor:.0%}**: "
                     f"{'PASS' if ext_ok else 'NOT ESTABLISHED'} ({pct(cyc.get('pass_rate'))})")
        lines.append(f"- **Cycle completion 100%**: "
                     f"{'PASS' if complete_ok else 'NOT ESTABLISHED'} ({pct(cyc.get('complete_rate'))})")
        report["claims"]["utility"] = ext_ok and complete_ok
        report["claims"]["utility_external"] = ext_ok
        report["claims"]["utility_completion"] = complete_ok
    lines.append("")

    # Quality: blind pairwise, never the pointwise rubric.
    q = load_json(os.path.join(args.root, "quality-pairwise.json"))
    if q:
        lines.append("## Quality — blind pairwise (full vs single)")
        lines.append("")
        lines.append(f"- Judges: {', '.join(q.get('judges', []))}")
        lines.append(f"- Pairs: {len(q.get('pairs', []))}; full wins {q.get('full_wins', 0)}, "
                     f"single wins {q.get('single_wins', 0)}, ties {q.get('ties', 0)}")
        lines.append(f"- Full win rate: {q.get('win_rate', 0):.2f} "
                     f"(floor {claims_cfg['quality_min_win_rate']:.2f})")
        lines.append(f"- Inter-judge agreement: {q.get('agreement', 0):.2f} "
                     f"(floor {claims_cfg['quality_min_agreement']:.2f})")
        lines.append(f"- Forward/reverse self-consistency: {q.get('self_consistency', 0):.2f}")
        ok = (q.get("agreement", 0) >= claims_cfg["quality_min_agreement"] and
              q.get("win_rate", 0) >= claims_cfg["quality_min_win_rate"])
        lines.append(f"- **Quality claim**: {'PASS' if ok else 'NOT ESTABLISHED'}")
        report["quality"] = q
        report["claims"]["quality"] = ok
        lines.append("")

    # External validation track: official Exercism tests, custom adapter.
    aider = load_json(os.path.join(args.root, "aider", "report.json"))
    if aider:
        track = load_external_manifest(os.path.join(os.path.dirname(args.protocol), "aider-polyglot.yaml"))
        lines.append("## External — Aider Polyglot (Python subset, custom adapter)")
        lines.append("")
        if track:
            lines.append(f"Track v{track.get('version', '?')} · `{track.get('source_commit', '?')[:12]}` · "
                         f"{track.get('adapter', 'custom')} · arm `{track.get('arm', 'single')}`.")
            lines.append("")
        lines.append(f"**{aider.get('passed', 0)}/{aider.get('n', 0)}** official Exercism exercises passed.")
        engine = [r for r in aider.get("results", []) if r.get("engine_accepted")]
        if any("engine_accepted" in r for r in aider.get("results", [])):
            lines.append("")
            lines.append(f"Engine accepted {len(engine)}/{aider.get('n', 0)}. "
                         "The external test is the label; convergence is reported separately.")
        lines.append("")
        lines.append(md_table(["exercise", "pass", "engine", "reason"],
                              [[r.get("slug"), r.get("pass"), r.get("engine_accepted", "—"),
                                (r.get("reason") or "; ".join(r.get("tests_tail", [])))[:100]]
                               for r in aider.get("results", [])]))
        lines.append("")
        lines.append("> Custom adapter over the official Exercism tests; not an official Aider leaderboard entry. "
                     "The label is external to Athanor's verifier.")
        lines.append("")
        report["external"] = aider
        report["claims"]["external_grounded"] = {
            "passed": aider.get("passed", 0), "n": aider.get("n", 0),
            "engine_accepted": len(engine) if any("engine_accepted" in r for r in aider.get("results", [])) else None,
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
