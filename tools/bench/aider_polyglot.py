#!/usr/bin/env python3
"""Aider Polyglot (Exercism) adapter — prepare and score a Python subset.

This is a *custom Athanor adapter* over the official Exercism exercise corpus,
not the official Aider leaderboard harness. The score is grounded: the original
exercise tests are re-run in a fresh, network-none container after Athanor exits.

Subcommands:
  prepare  copy exercises and write the soak corpus
  score    write each produced code artifact into the exercise, run the tests
"""

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys

SLUGS = [
    "two-fer",
    "leap",
    "pangram",
    "isogram",
    "raindrops",
    "rna-transcription",
    "acronym",
    "hamming",
]


def solution_name(slug: str) -> str:
    return slug.replace("-", "_") + ".py"


def prepare(args):
    os.makedirs(os.path.join(args.work, "exercises"), exist_ok=True)
    meta = {}
    for slug in args.slugs.split(","):
        slug = slug.strip()
        if not slug:
            continue
        src = os.path.join(args.source, slug)
        if not os.path.isdir(src):
            print(f"skip missing exercise {slug}", file=sys.stderr)
            continue
        dst = os.path.join(args.work, "exercises", slug)
        if os.path.exists(dst):
            shutil.rmtree(dst)
        shutil.copytree(src, dst)
        meta[slug] = {
            "host": dst,
            "container_repo": f"/bench/repo/.bench-aider/{args.run}/exercises/{slug}",
            "solution": solution_name(slug),
        }

    tasks = []
    for slug, m in meta.items():
        tasks.append(
            f"""  - id: aider-{slug}
    title: Exercism {slug}
    suite: aider
    archetype: code
    language: python
    tier: H
    held_out: false
    goal: |
      Implement {m['solution']} so every provided test passes. Do not modify
      the tests or any other file. Return the full Python module.
    criteria: [all provided tests pass, tests unchanged]
    repo: {m['container_repo']}
    test_command: |
      cd "$REPO" && python -m pytest -q
"""
        )
    corpus = "version: 5\nsuite: aider\ntasks:\n" + "".join(tasks)
    with open(os.path.join(args.work, "corpus.yaml"), "w", encoding="utf-8") as f:
        f.write(corpus)
    with open(os.path.join(args.work, "meta.json"), "w", encoding="utf-8") as f:
        json.dump(meta, f, indent=2)
    print(f"prepared {len(meta)} exercises under {args.work}")


def file_hash(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()


def score(args):
    with open(os.path.join(args.work, "meta.json"), encoding="utf-8") as f:
        meta = json.load(f)
    rows = []
    if os.path.exists(args.artifacts):
        with open(args.artifacts, encoding="utf-8") as f:
            rows = json.load(f)
    by_goal = {r.get("goal_name"): r for r in rows}

    results = []
    passed = 0
    for slug, m in meta.items():
        row = by_goal.get(f"aider-{slug}")
        res = {"slug": slug, "pass": False}
        if not row or not row.get("text"):
            res["reason"] = "no code artifact produced"
            results.append(res)
            continue
        sol = os.path.join(m["host"], m["solution"])
        with open(sol, "w", encoding="utf-8") as f:
            f.write(row["text"])
        # The official tests are the label. They run in a fresh container.
        cmd = [
            "podman", "run", "--rm", "--network=none",
            "-v", m["host"] + ":/work", "-w", "/work",
            args.image, "sh", "-c", "python -m pytest -q",
        ]
        proc = subprocess.run(cmd, capture_output=True, text=True, timeout=args.timeout)
        res["exit"] = proc.returncode
        res["artifact_id"] = row.get("artifact_id", "")
        res["tests_tail"] = (proc.stdout + proc.stderr).strip().splitlines()[-3:]
        res["pass"] = proc.returncode == 0
        if res["pass"]:
            passed += 1
        results.append(res)

    report = {"adapter": "athanor-aider-polyglot-python-subset", "n": len(results),
              "passed": passed, "results": results}
    with open(args.out + ".json", "w", encoding="utf-8") as f:
        json.dump(report, f, indent=2)
    lines = ["# Aider Polyglot — Python subset (custom Athanor adapter)", "",
             f"Passed **{passed}/{len(results)}**", "",
             "| exercise | pass | exit | reason |", "|---|---|---|---|"]
    for r in results:
        reason = r.get("reason", "; ".join(r.get("tests_tail", []))[:120])
        lines.append(f"| {r['slug']} | {r['pass']} | {r.get('exit','')} | {reason} |")
    lines += ["", "> Custom adapter over the official Exercism exercises. "
                  "The test outcome is external to Athanor. Not an official Aider leaderboard entry."]
    with open(args.out + ".md", "w", encoding="utf-8") as f:
        f.write("\n".join(lines) + "\n")
    print("\n".join(lines))


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("prepare")
    p.add_argument("--source", required=True)
    p.add_argument("--work", required=True)
    p.add_argument("--run", required=True)
    p.add_argument("--slugs", default=",".join(SLUGS))
    p.set_defaults(fn=prepare)
    s = sub.add_parser("score")
    s.add_argument("--work", required=True)
    s.add_argument("--artifacts", required=True)
    s.add_argument("--out", required=True)
    s.add_argument("--image", default="athanor-jobpod:latest")
    s.add_argument("--timeout", type=int, default=120)
    s.set_defaults(fn=score)
    args = ap.parse_args()
    args.fn(args)


if __name__ == "__main__":
    main()
