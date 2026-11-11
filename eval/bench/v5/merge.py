#!/usr/bin/env python3
"""Merge canonical and post-fix v5 runs into one evidence tree.

The run tree (tools/bench/results/) is gitignored; this tool copies only the
small, durable pieces (results.json, run.json, per-task cycle JSON, quality and
Aider reports) into a committed evidence directory. The catalog makes the
supersede decisions explicit instead of relying on directory mtimes.

Usage:
    python3 eval/bench/v5/merge.py -catalog <catalog.json> -model bonsai27b \
        -out docs/probes/runs/bench-v5-...-merged/bonsai27b
"""

import argparse
import glob
import json
import os
import shutil
import sys


def load_json(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def write_json(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f, indent=2)
        f.write("\n")


def copy_file(src, dst):
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    shutil.copy2(src, dst)


def merge_soak(model_cfg, out, manifest):
    for entry in model_cfg.get("soak", []):
        suite, arm = entry["suite"], entry["arm"]
        src = entry["source"]
        dst_dir = os.path.join(out, "soak", suite, arm, "out")
        results = os.path.join(src, "out", "results.json")
        run = os.path.join(src, "out", "run.json")
        item = {
            "suite": suite,
            "arm": arm,
            "source": src,
            "supersedes": entry.get("supersedes", ""),
            "reason": entry.get("reason", ""),
        }
        if os.path.exists(results):
            copy_file(results, os.path.join(dst_dir, "results.json"))
            item["results"] = True
        else:
            item["results"] = False
            manifest["missing"].append(f"{suite}/{arm}: results.json")
        if os.path.exists(run):
            copy_file(run, os.path.join(dst_dir, "run.json"))
            item["run"] = True
        else:
            item["run"] = False
            manifest["missing"].append(f"{suite}/{arm}: run.json")
        manifest["soak"].append(item)


def merge_cycle(model_cfg, out, manifest):
    cycle_dir = os.path.join(out, "cycle")
    # A merge replaces the whole cycle set: a stale per-task file must not
    # compete with the new canonical file in the analyzer's glob.
    if os.path.isdir(cycle_dir):
        for old in glob.glob(os.path.join(cycle_dir, "cycle-*.json")):
            os.remove(old)
    for entry in model_cfg.get("cycle", []):
        src = entry["source"]
        task_id = entry.get("task", "")
        item = {
            "task": task_id,
            "source": src,
            "supersedes": entry.get("supersedes", ""),
            "reason": entry.get("reason", ""),
        }
        if not os.path.exists(src):
            item["copied"] = False
            manifest["missing"].append(f"cycle {task_id}: {src}")
            manifest["cycle"].append(item)
            continue
        data = load_json(src)
        tasks = data.get("tasks", [])
        if task_id:
            tasks = [t for t in tasks if (t.get("id") or t.get("goal_name")) == task_id]
            if not tasks:
                item["copied"] = False
                manifest["missing"].append(f"cycle {task_id}: not in {src}")
                manifest["cycle"].append(item)
                continue
        name = f"cycle-{task_id or 'all'}.json"
        write_json(os.path.join(out, "cycle", name), {"tasks": tasks})
        item["copied"] = True
        item["tasks"] = [t.get("id") or t.get("goal_name") for t in tasks]
        manifest["cycle"].append(item)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("-catalog", required=True)
    ap.add_argument("-model", required=True)
    ap.add_argument("-out", required=True)
    args = ap.parse_args()

    catalog = load_json(args.catalog)
    models = catalog.get("models", {})
    if args.model not in models:
        print(f"merge: model {args.model} is not in the catalog", file=sys.stderr)
        return 2
    model_cfg = models[args.model]
    os.makedirs(args.out, exist_ok=True)
    manifest = {
        "model": args.model,
        "mixed_ok": bool(catalog.get("mixed_ok", False)),
        "soak": [],
        "cycle": [],
        "quality": {},
        "aider": {},
        "missing": [],
    }
    merge_soak(model_cfg, args.out, manifest)
    merge_cycle(model_cfg, args.out, manifest)

    quality = model_cfg.get("quality", "")
    if quality and os.path.exists(quality):
        copy_file(quality, os.path.join(args.out, "quality-pairwise.json"))
        manifest["quality"] = {"source": quality, "copied": True}
    else:
        manifest["quality"] = {"source": quality, "copied": False}
        if quality:
            manifest["missing"].append(f"quality: {quality}")

    aider = model_cfg.get("aider", "")
    if aider and os.path.exists(aider):
        copy_file(aider, os.path.join(args.out, "aider", "report.json"))
        manifest["aider"] = {"source": aider, "copied": True}
    else:
        manifest["aider"] = {"source": aider, "copied": False}
        if aider:
            manifest["missing"].append(f"aider: {aider}")

    write_json(os.path.join(args.out, "manifest.json"), manifest)
    print(f"merge: wrote {args.out} ({len(manifest['soak'])} soak, "
          f"{len(manifest['cycle'])} cycle, {len(manifest['missing'])} missing)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
