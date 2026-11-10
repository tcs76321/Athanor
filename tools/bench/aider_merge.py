#!/usr/bin/env python3
"""Merge Aider/Exercism subset reports into one external-track report."""

import argparse
import json
import sys

ORDER = [
    "two-fer", "leap", "pangram", "isogram",
    "raindrops", "rna-transcription", "acronym", "hamming",
]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--inputs", required=True, help="comma-separated report JSON paths")
    ap.add_argument("--out", required=True, help="output prefix (writes .json and .md)")
    args = ap.parse_args()

    by_slug = {}
    for path in [p.strip() for p in args.inputs.split(",") if p.strip()]:
        with open(path, encoding="utf-8") as f:
            data = json.load(f)
        for row in data.get("results", []):
            by_slug[row["slug"]] = row

    rows = [by_slug[s] for s in ORDER if s in by_slug]
    passed = sum(1 for r in rows if r.get("pass"))
    report = {
        "adapter": "athanor-aider-polyglot-python-subset",
        "track": "aider-polyglot",
        "arm": "single",
        "n": len(rows),
        "passed": passed,
        "results": rows,
        "note": "Custom adapter over official Exercism tests; not an Aider leaderboard entry.",
    }
    with open(args.out + ".json", "w", encoding="utf-8") as f:
        json.dump(report, f, indent=2)
        f.write("\n")

    lines = ["# Aider Polyglot — Python subset (custom Athanor adapter)", "",
             f"Passed **{passed}/{len(rows)}**", "",
             "| exercise | pass | exit | reason |", "|---|---|---|---|"]
    for r in rows:
        reason = r.get("reason") or "; ".join(r.get("tests_tail", []))[:120]
        lines.append(f"| {r['slug']} | {r['pass']} | {r.get('exit','')} | {reason} |")
    lines += ["", "> Custom adapter over the official Exercism exercises. "
                  "The test outcome is external to Athanor. Not an official Aider leaderboard entry."]
    with open(args.out + ".md", "w", encoding="utf-8") as f:
        f.write("\n".join(lines) + "\n")

    print(f"merged {len(rows)} exercises: {passed}/{len(rows)} -> {args.out}.md")
    return 0


if __name__ == "__main__":
    sys.exit(main())
