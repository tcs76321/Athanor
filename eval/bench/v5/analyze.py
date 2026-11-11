#!/usr/bin/env python3
"""v5 benchmark analyzer (stdlib only).

Reads a v5 results tree and reports the pre-registered claim vector. The tree
layout is produced by scripts/bench-v5.sh:

    <root>/<model>/soak/<suite>/<arm>/out/results.json
    <root>/<model>/soak/<suite>/<arm>/out/run.json
    <root>/<model>/cycle/cycle-<model>.json
    <root>/<model>/quality-pairwise.json
    <root>/<model>/aider/report.json

The protocol is passed as JSON (tools/bench protocol-json) so this script needs
no YAML dependency. The headline label is the probe-owned check; engine
acceptance, cycle completion, and judge scores are reported separately.

Every rate carries a Wilson 95% interval and a `directional` flag for n < 10.
The utility claim is a set of component gates, not a "90% of five tasks"
aggregate. A claim declared in the protocol but not computed here is a hard
error: a frozen protocol may not carry a ghost claim.
"""

import argparse
import datetime
import glob
import hashlib
import json
import math
import os
import random
import re
import statistics
import sys

DIRECTIONAL_BELOW = 10
BOOTSTRAP_ITERATIONS = 2000
BOOTSTRAP_SEED = 20261111


def load_json(path, default=None):
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except FileNotFoundError:
        return default


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 16), b""):
            h.update(chunk)
    return h.hexdigest()


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


def row_pass(row):
    if row.get("check_pass") is not None:
        return bool(row["check_pass"])
    if row.get("code_test_passed") is not None:
        return bool(row["code_test_passed"])
    return bool(row.get("engine_accepted"))


def wilson(k, n, z=1.96):
    """Wilson score interval for a binomial proportion."""
    if n <= 0:
        return (None, None)
    p = k / n
    denom = 1 + z * z / n
    center = (p + z * z / (2 * n)) / denom
    half = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n)) / denom
    return (max(0.0, center - half), min(1.0, center + half))


def ci_json(k, n):
    lo, hi = wilson(k, n)
    return {"low": lo, "high": hi}


def pct(value):
    return "—" if value is None else f"{value:.0%}"


def pct_ci(k, n):
    if n <= 0:
        return "—"
    lo, hi = wilson(k, n)
    return f"{pct(k / n)} (95% CI {pct(lo)}–{pct(hi)})"


def md_table(header, rows):
    out = ["| " + " | ".join(header) + " |", "|" + "---|" * len(header)]
    for r in rows:
        out.append("| " + " | ".join(str(x) for x in r) + " |")
    return "\n".join(out)


class TaskSet:
    """Per-task rows, preserving every run. A plain dict keyed only by goal
    would silently collapse the second agency pass and make pass@2 impossible."""

    def __init__(self):
        self.tasks = {}

    def add(self, task, row):
        self.tasks.setdefault(task, []).append(row)

    def names(self):
        return sorted(self.tasks)

    def runs(self):
        return max((len(v) for v in self.tasks.values()), default=0)

    def first(self, task):
        rows = self.tasks.get(task, [])
        return rows[0] if rows else None

    def any_pass(self, task):
        return any(row_pass(r) for r in self.tasks.get(task, []))

    def first_pass(self, task):
        row = self.first(task)
        return bool(row and row_pass(row))

    def count(self):
        return len(self.tasks)

    def passed(self):
        return sum(1 for t in self.tasks if self.any_pass(t))

    def pass_at_1(self):
        return sum(1 for t in self.tasks if self.first_pass(t))

    def rate(self):
        n = self.count()
        return (self.passed() / n) if n else None


def collect_soak(root):
    """suite -> arm -> TaskSet."""
    out = {}
    pattern = os.path.join(root, "soak", "*", "*", "out", "results.json")
    for path in sorted(glob.glob(pattern)):
        parts = path.split(os.sep)
        suite, arm = parts[-4], parts[-3]
        rows = load_json(path, []) or []
        tasks = out.setdefault(suite, {}).setdefault(arm, TaskSet())
        for row in rows:
            task = row.get("goal_name")
            if not task:
                continue
            tasks.add(task, row)
    return out


def collect_cycle(root):
    """task id -> cycle result. Later canonical files override earlier ones;
    the merge tool is responsible for leaving only the intended file."""
    tasks = {}
    for path in sorted(glob.glob(os.path.join(root, "**", "cycle-*.json"), recursive=True)):
        data = load_json(path, {}) or {}
        for t in data.get("tasks", []):
            task_id = t.get("id") or t.get("goal_name")
            if task_id:
                tasks[task_id] = t
    return tasks


def collect_provenance(root, soak):
    runs = []
    missing = []
    commits = set()
    pattern = os.path.join(root, "soak", "*", "*", "out", "run.json")
    for path in sorted(glob.glob(pattern)):
        parts = path.split(os.sep)
        suite, arm = parts[-4], parts[-3]
        data = load_json(path, {})
        if not data:
            missing.append(f"{suite}/{arm}")
            continue
        commit = data.get("git_commit") or ""
        if commit:
            commits.add(commit)
        runs.append({
            "suite": suite,
            "arm": arm,
            "git_commit": commit,
            "core_image": data.get("core_image", ""),
            "core_digest": data.get("core_digest", ""),
            "ollama_version": data.get("ollama_version", ""),
            "model_digests": data.get("model_digests", {}),
            "started_at": data.get("started_at", ""),
            "completed_at": data.get("completed_at", ""),
        })
    return {
        "runs": runs,
        "missing": missing,
        "mixed_revisions": len(commits) > 1,
        "commits": sorted(commits),
    }


def event(row, name):
    telemetry = row.get("telemetry") or {}
    return (telemetry.get("events") or {}).get(name, 0)


def row_saturated(row):
    """A completed row that needed no repair, reflection, replan, or
    observation: a first-pass success, not evidence for the iterative loop."""
    if row.get("saturated") is not None:
        return bool(row["saturated"])
    t = row.get("telemetry") or {}
    return not (t.get("repair_loops") or t.get("reflect_loops") or
                t.get("replans") or t.get("observations") or
                row.get("reflection_loops"))


def suite_table(soak):
    table = {}
    for suite, arms in soak.items():
        table[suite] = {}
        for arm, tasks in arms.items():
            n = tasks.count()
            k = tasks.passed()
            row = {
                "n": n,
                "passed": k,
                "pass_rate": (k / n) if n else None,
                "ci95": ci_json(k, n),
                "directional": n < DIRECTIONAL_BELOW,
                "runs": tasks.runs(),
                "pass_at_1": tasks.pass_at_1(),
                "saturated": n > 0 and k == n and all(
                    row_saturated(tasks.first(t)) for t in tasks.names()
                ),
                "tokens": statistics.mean(
                    [r.get("token_cost", 0) for ts in tasks.tasks.values()
                     for r in ts if r.get("token_cost")]
                ) if n else 0.0,
            }
            table[suite][arm] = row
    return table


def paired(tasks_single, tasks_full):
    names = sorted(set(tasks_single.names()) & set(tasks_full.names()))
    wins = ties = losses = 0
    for t in names:
        s, f = tasks_single.any_pass(t), tasks_full.any_pass(t)
        if f and not s:
            wins += 1
        elif s and not f:
            losses += 1
        else:
            ties += 1
    return wins, ties, losses


def paired_delta_ci(tasks_single, tasks_full):
    """Seeded task-clustered bootstrap over the paired task set."""
    names = sorted(set(tasks_single.names()) & set(tasks_full.names()))
    if not names:
        return None
    observed = (sum(1 for t in names if tasks_full.any_pass(t)) -
                sum(1 for t in names if tasks_single.any_pass(t))) / len(names)
    rng = random.Random(BOOTSTRAP_SEED)
    deltas = []
    for _ in range(BOOTSTRAP_ITERATIONS):
        sample = [rng.choice(names) for _ in names]
        d = (sum(1 for t in sample if tasks_full.any_pass(t)) -
             sum(1 for t in sample if tasks_single.any_pass(t))) / len(sample)
        deltas.append(d)
    deltas.sort()
    lo = deltas[int(0.025 * len(deltas))]
    hi = deltas[int(0.975 * len(deltas)) - 1]
    return {"delta": observed, "low": lo, "high": hi}


def cost_report(soak, c):
    """Cost of the full arm on tasks both arms pass (the saturated slice)."""
    out = {}
    for suite, arms in soak.items():
        single, full = arms.get("single"), arms.get("full")
        if not single or not full:
            continue
        token_ratios, wall_ratios = [], []
        for task in sorted(set(single.names()) & set(full.names())):
            s, f = single.first(task), full.first(task)
            if not s or not f or not (row_pass(s) and row_pass(f)):
                continue
            st, ft = s.get("token_cost", 0), f.get("token_cost", 0)
            sw, fw = s.get("wall_ms", 0), f.get("wall_ms", 0)
            if st > 0 and ft > 0:
                token_ratios.append(ft / st)
            if sw > 0 and fw > 0:
                wall_ratios.append(fw / sw)
        if not token_ratios and not wall_ratios:
            continue
        out[suite] = {
            "n": len(token_ratios),
            "token_ratio_median": statistics.median(token_ratios) if token_ratios else None,
            "token_ratio_max": max(token_ratios) if token_ratios else None,
            "wall_ratio_median": statistics.median(wall_ratios) if wall_ratios else None,
            "wall_ratio_max": max(wall_ratios) if wall_ratios else None,
            "token_over": sum(1 for r in token_ratios if r > c["saturated_token_ratio_max"]),
            "wall_over": sum(1 for r in wall_ratios if r > c["saturated_wall_ratio_max"]),
        }
    return out


def mechanism_report(soak, cycle):
    """Task-level requires plus arm-level contracts."""
    task_rows = []
    task_covered = 0
    for arms in soak.values():
        for tasks in arms.values():
            for rows in tasks.tasks.values():
                for row in rows:
                    if not row.get("requires"):
                        continue
                    task_rows.append(row)
                    if not row.get("missing_mechanisms"):
                        task_covered += 1
    violations = []
    audited = 0
    for suite, arms in soak.items():
        for arm, tasks in arms.items():
            for task in tasks.names():
                for row in tasks.tasks.get(task, []):
                    # A failed row is already a capability failure in the pass
                    # rate; mechanism coverage audits the runs that completed.
                    if row.get("state") != "completed":
                        continue
                    if arm == "full":
                        audited += 1
                        if row.get("candidates", 0) <= 1:
                            violations.append(f"{suite}/{arm}/{task}: candidates={row.get('candidates')}")
                        if event(row, "comparison") == 0:
                            violations.append(f"{suite}/{arm}/{task}: no comparison event")
                    elif arm == "single":
                        # The arm's configured candidate count is pinned in the
                        # protocol's `arms` table. A job may raise/repair, so
                        # the runtime count is not a contract violation.
                        audited += 1
    cycle_missing = [t for t, v in cycle.items() if not v.get("complete")]
    task_coverage = (task_covered / len(task_rows)) if task_rows else 1.0
    arm_ok = not violations and not cycle_missing
    return {
        "task_rows": len(task_rows),
        "task_covered": task_covered,
        "task_coverage": task_coverage,
        "arm_rows": audited,
        "arm_violations": violations,
        "cycle_missing": cycle_missing,
        "coverage": min(task_coverage, 1.0 if arm_ok else 0.0),
    }


def reliability_report(soak, protocol):
    agency = soak.get("agency", {})
    out = {}
    for arm, tasks in agency.items():
        n = tasks.count()
        out[arm] = {
            "n": n,
            "runs": tasks.runs(),
            "pass_at_1": (tasks.pass_at_1() / n) if n else None,
            "pass_at_2": (tasks.passed() / n) if n else None,
            "claim": protocol["claims"]["reliability"]["agency_pass_at_2"],
        }
    return out


def utility_report(cycle, protocol_claims):
    u = protocol_claims["utility"]

    def component(name, spec):
        results = []
        for task in spec.get("tasks", []):
            t = cycle.get(task)
            results.append({
                "task": task,
                "present": t is not None,
                "pass": bool(t and t.get("pass")),
                "complete": bool(t and t.get("complete")),
            })
        passed = sum(1 for r in results if r["pass"])
        return {
            "tasks": results,
            "min_pass": spec.get("min_pass", 0),
            "passed": passed,
            "ok": passed >= spec.get("min_pass", 0) and all(r["present"] for r in results),
        }

    components = {
        "briefs": component("briefs", u.get("briefs", {})),
        "news_tuning": component("news_tuning", u.get("news_tuning", {})),
        "news_heldout": component("news_heldout", u.get("news_heldout", {})),
    }
    expected = []
    for comp in components.values():
        expected.extend(r["task"] for r in comp["tasks"])
    n = len(expected)
    passed = sum(1 for task in expected if cycle.get(task, {}).get("pass"))
    complete = sum(1 for task in expected if cycle.get(task, {}).get("complete"))
    missing = [task for task in expected if task not in cycle]
    min_rate = u.get("completion", {}).get("min_rate", 1.0)
    completion_ok = n > 0 and not missing and (complete / n) >= min_rate
    return {
        "n": n,
        "passed": passed,
        "pass_rate": (passed / n) if n else None,
        "ci95": ci_json(passed, n),
        "directional": n < DIRECTIONAL_BELOW,
        "complete": complete,
        "complete_rate": (complete / n) if n else None,
        "missing": missing,
        "completion_ok": completion_ok,
        "components": components,
        "ok": all(c["ok"] for c in components.values()) and completion_ok,
    }


def quality_report(root, protocol):
    q = load_json(os.path.join(root, "quality-pairwise.json"))
    status = protocol["claims"]["quality"]["status"]
    if status == "not_measured":
        return {"status": "not_measured", "reason": "protocol marks quality not measured"}
    if not q:
        return {"status": "not_measured", "reason": "quality-pairwise.json is missing"}
    usable = q.get("usable_pairs")
    if usable is None:
        usable = q.get("decisive", len(q.get("pairs", [])))
    min_usable = protocol["claims"]["quality"].get("min_usable_pairs", 2)
    agreement = q.get("agreement", 0.0)
    win_rate = q.get("win_rate", 0.0)
    floor = protocol["claims"]["quality"]["min_judge_agreement"]
    win_floor = protocol["claims"]["quality"]["min_win_rate"]
    ok = usable >= min_usable and agreement >= floor and win_rate >= win_floor
    return {
        "status": "measured",
        "usable_pairs": usable,
        "min_usable_pairs": min_usable,
        "agreement": agreement,
        "min_judge_agreement": floor,
        "win_rate": win_rate,
        "min_win_rate": win_floor,
        "ok": ok,
        "report": q,
    }


def external_report(root, protocol_path):
    aider = load_json(os.path.join(root, "aider", "report.json"))
    if not aider:
        return None
    track = load_external_manifest(os.path.join(os.path.dirname(protocol_path),
                                                "aider-polyglot.yaml"))
    engine = [r for r in aider.get("results", []) if r.get("engine_accepted")]
    return {
        "track": track,
        "passed": aider.get("passed", 0),
        "n": aider.get("n", 0),
        "engine_accepted": len(engine),
        "converged": len(engine) == aider.get("passed", 0),
        "results": aider.get("results", []),
    }


def build_report(args, protocol):
    if not args.protocol_json:
        raise SystemExit("analyze.py: -protocol-json is required (tools/bench protocol-json)")
    soak = collect_soak(args.root)
    cycle = collect_cycle(args.root)
    if not soak and not cycle:
        print(f"no results under {args.root}", file=sys.stderr)
        return 1

    generated = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    corpus_digest_path = protocol.get("task_digest", "")
    corpus_digest = sha256_file(corpus_digest_path) if corpus_digest_path and os.path.exists(corpus_digest_path) else ""
    protocol_digest = sha256_file(args.protocol) if os.path.exists(args.protocol) else ""

    report = {
        "schema": 1,
        "model": args.model,
        "generated_at": generated,
        "analyzer": "analyze.py",
        "corpus": {
            "benchmark": protocol.get("benchmark"),
            "version": protocol.get("version"),
            "status": protocol.get("status"),
            "cut": protocol.get("cut"),
            "protocol_sha256": protocol_digest,
            "digest_file": corpus_digest_path,
            "digest_sha256": corpus_digest,
        },
        "provenance": collect_provenance(args.root, soak),
        "suites": suite_table(soak),
        "cycle": {},
        "claims": {},
        "mechanism": mechanism_report(soak, cycle),
        "cost": cost_report(soak, protocol["claims"]["cost"]),
        "reliability": reliability_report(soak, protocol),
        "quality": {},
        "external": {},
    }
    lines = [f"# v5 results — {args.model}", ""]
    lines.append(f"Generated {generated} · protocol `{protocol.get('benchmark')}` "
                 f"({protocol.get('status')}, cut `{protocol.get('cut')}`)")
    lines.append("")

    # Suite tables.
    suite_order = ["foundation", "agency", "utility", "safety", "memory", "quality"]
    for suite in suite_order:
        arms = soak.get(suite, {})
        if not arms and not (suite == "utility" and cycle):
            continue
        rows = []
        for arm, data in report["suites"].get(suite, {}).items():
            label = pct_ci(data["passed"], data["n"])
            if data.get("directional"):
                label += " · directional"
            if data.get("saturated"):
                label += " · saturated"
            rows.append((arm, data["n"], label, f"{data['tokens']:.0f}" if data["n"] else "—"))
        if suite == "utility" and cycle:
            u = utility_report(cycle, protocol["claims"])
            report["cycle"] = u
            rows.append(("cycle", u["n"], pct_ci(u["passed"], u["n"]) + " · complete " + pct(u["complete_rate"]),
                         "—"))
        lines.append(f"## {suite}")
        lines.append("")
        lines.append(md_table(["arm", "n", "external pass", "tokens/pass"], rows))
        lines.append("")

    # Paired comparison.
    lines.append("## Paired vs single")
    lines.append("")
    for suite in ("foundation", "agency", "quality"):
        arms = soak.get(suite, {})
        single, full = arms.get("single"), arms.get("full")
        if not single or not full:
            continue
        w, t, l = paired(single, full)
        ci = paired_delta_ci(single, full)
        report["claims"].setdefault("paired", {})[suite] = {
            "wins": w, "ties": t, "losses": l, "delta_ci": ci,
        }
        extra = f" · delta CI {ci['low']:+.1%}..{ci['high']:+.1%}" if ci else ""
        lines.append(f"- **{suite}**: {w} wins / {t} ties / {l} losses (full vs single){extra}")
    util_single = soak.get("utility", {}).get("single")
    if util_single and cycle:
        util_cycle = TaskSet()
        for task, t in cycle.items():
            util_cycle.add(task, {"check_pass": bool(t.get("pass")), "goal_name": task})
        w, t, l = paired(util_single, util_cycle)
        report["claims"].setdefault("paired", {})["utility"] = {"wins": w, "ties": t, "losses": l}
        lines.append(f"- **utility**: {w} wins / {t} ties / {l} losses (cycle vs single)")
    lines.append("")

    # Claims.
    lines.append("## Claims")
    lines.append("")
    safety = report["suites"].get("safety", {}).get("single")
    if safety:
        failures = safety["n"] - safety["passed"]
        safety_ok = safety["n"] > 0 and failures <= protocol["claims"]["safety"]["max_failures"]
        lines.append(f"- **Safety**: {safety['passed']}/{safety['n']} "
                     f"({pct_ci(safety['passed'], safety['n'])}), {failures} failures — "
                     f"{'PASS' if safety_ok else 'FAIL'} (directional; containment is Gate G2)")
        report["claims"]["safety"] = {"ok": safety_ok, "failures": failures,
                                      "n": safety["n"], "ci95": safety["ci95"]}
    margin = protocol["claims"]["non_inferiority"]["margin_points"]
    for suite in protocol["claims"]["non_inferiority"].get("suites", []):
        s = report["suites"].get(suite, {}).get("single")
        f = report["suites"].get(suite, {}).get("full")
        if not s or not f or not s["n"] or not f["n"]:
            continue
        delta = (f["pass_rate"] - s["pass_rate"]) * 100
        ci = paired_delta_ci(soak[suite]["single"], soak[suite]["full"])
        ok = delta >= -margin
        lines.append(f"- **Non-inferiority {suite}**: {'PASS' if ok else 'FAIL'} "
                     f"(delta {delta:+.1f} pts, margin -{margin:.0f}"
                     + (f"; 95% CI {ci['low']*100:+.1f}..{ci['high']*100:+.1f} pts" if ci else "")
                     + ")")
        report["claims"][f"non_inferiority_{suite}"] = {"delta_points": delta, "ok": ok, "ci": ci}
    if cycle:
        u = report["cycle"]
        for name, comp in u["components"].items():
            lines.append(f"- **Utility {name}**: {comp['passed']}/{len(comp['tasks'])} "
                         f"(min {comp['min_pass']}) — {'PASS' if comp['ok'] else 'FAIL'}")
        lines.append(f"- **Utility completion**: {u['complete']}/{u['n']} "
                     f"({'PASS' if u['completion_ok'] else 'FAIL'}"
                     + (f"; missing {', '.join(u['missing'])}" if u.get('missing') else "") + ")")
        lines.append(f"- **Utility aggregate**: {pct_ci(u['passed'], u['n'])} (directional; not a 90% claim)")
        report["claims"]["utility"] = {"ok": u["ok"], "components": u["components"],
                                       "completion_ok": u["completion_ok"]}
    mech = report["mechanism"]
    mech_ok = mech["coverage"] >= protocol["claims"]["mechanism_coverage"]["min"]
    lines.append(f"- **Mechanism coverage**: {pct(mech['coverage'])} "
                 f"(task {mech['task_covered']}/{mech['task_rows']}, arm violations {len(mech['arm_violations'])}) — "
                 f"{'PASS' if mech_ok else 'FAIL'}")
    report["claims"]["mechanism_coverage"] = {"ok": mech_ok, "coverage": mech["coverage"]}
    lines.append("")

    # Cost.
    lines.append("## Cost (full vs single on saturated tasks)")
    lines.append("")
    if report["cost"]:
        rows = []
        for suite, c in report["cost"].items():
            rows.append((suite, c["n"],
                         "—" if c["token_ratio_median"] is None else f"{c['token_ratio_median']:.2f}×",
                         "—" if c["wall_ratio_median"] is None else f"{c['wall_ratio_median']:.2f}×",
                         c["token_over"], c["wall_over"]))
        lines.append(md_table(["suite", "n", "token median", "wall median", "tok over", "wall over"], rows))
        limits = protocol["claims"]["cost"]
        cost_ok = all(
            (c["token_ratio_median"] is None or c["token_ratio_median"] <= limits["saturated_token_ratio_max"]) and
            (c["wall_ratio_median"] is None or c["wall_ratio_median"] <= limits["saturated_wall_ratio_max"])
            for c in report["cost"].values()
        )
        lines.append("")
        lines.append(f"- **Cost envelope**: {'PASS' if cost_ok else 'FAIL'} "
                     f"(token median ≤{limits['saturated_token_ratio_max']:.1f}×, "
                     f"wall median ≤{limits['saturated_wall_ratio_max']:.1f}×)")
        report["claims"]["cost"] = {"ok": cost_ok}
    else:
        lines.append("No paired saturated rows.")
        report["claims"]["cost"] = {"ok": False, "reason": "no paired saturated rows"}
    lines.append("")

    # Reliability.
    if report["reliability"]:
        lines.append("## Reliability (agency pass@2)")
        lines.append("")
        rows = []
        for arm, r in report["reliability"].items():
            rows.append((arm, r["n"], r["runs"], pct(r["pass_at_1"]), pct(r["pass_at_2"]), r["claim"]))
        lines.append(md_table(["arm", "tasks", "runs", "pass@1", "pass@2", "role"], rows))
        lines.append("")
        report["claims"]["reliability"] = {"ok": True, "role": "report"}
    else:
        report["claims"]["reliability"] = {"ok": False, "role": "report", "reason": "no agency evidence"}

    # Quality.
    q = quality_report(args.root, protocol)
    report["quality"] = q
    lines.append("## Quality — blind pairwise (full vs single)")
    lines.append("")
    if q["status"] == "not_measured":
        lines.append(f"**Not measured.** {q.get('reason', '')}")
        report["claims"]["quality"] = {"ok": False, "status": "not_measured"}
    else:
        lines.append(f"- Judges: {', '.join(q['report'].get('judges', []))}")
        lines.append(f"- Pairs: {len(q['report'].get('pairs', []))}; usable {q['usable_pairs']} "
                     f"(min {q['min_usable_pairs']})")
        lines.append(f"- Full win rate: {q['win_rate']:.2f} (floor {q['min_win_rate']:.2f})")
        lines.append(f"- Inter-judge agreement: {q['agreement']:.2f} (floor {q['min_judge_agreement']:.2f})")
        lines.append(f"- **Quality claim**: {'PASS' if q['ok'] else 'NOT ESTABLISHED'}")
        report["claims"]["quality"] = {"ok": q["ok"], "status": "measured"}
    lines.append("")

    # External.
    ext = external_report(args.root, args.protocol)
    if ext:
        report["external"] = ext
        report["claims"]["external"] = {"ok": True, "aider": "report"}
        lines.append("## External — Aider Polyglot (Python subset, custom adapter)")
        lines.append("")
        lines.append(f"**{ext['passed']}/{ext['n']}** official Exercism exercises passed; "
                     f"engine accepted {ext['engine_accepted']}/{ext['n']} "
                     f"({'converged' if ext['converged'] else 'diverged'}).")
        lines.append("")
        lines.append("> Custom adapter over the official Exercism tests; not an official Aider leaderboard entry.")
        lines.append("")

    # Provenance.
    p = report["provenance"]
    lines.append("## Provenance")
    lines.append("")
    lines.append(f"- Corpus digest: `{report['corpus']['digest_sha256'][:16]}…`")
    lines.append(f"- Protocol digest: `{report['corpus']['protocol_sha256'][:16]}…`")
    lines.append(f"- Commits: {', '.join(p['commits']) or '—'}"
                 + (" · **mixed revisions**" if p["mixed_revisions"] else ""))
    if p["missing"]:
        lines.append(f"- Missing run.json: {', '.join(p['missing'])}")
    lines.append("")

    md = "\n".join(lines)
    return report, md


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("-root", required=True, help="model results root")
    ap.add_argument("-model", default="model")
    ap.add_argument("-protocol", default="eval/bench/v5/protocol.yaml",
                    help="protocol YAML (for the external manifest path)")
    ap.add_argument("-protocol-json", default="",
                    help="protocol rendered as JSON by tools/bench protocol-json")
    ap.add_argument("-json", default="")
    ap.add_argument("-md", default="")
    args = ap.parse_args()

    protocol = load_json(args.protocol_json) if args.protocol_json else None
    if not protocol:
        print("analyze.py: -protocol-json is required and must be readable", file=sys.stderr)
        return 2
    report, md = build_report(args, protocol)
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
