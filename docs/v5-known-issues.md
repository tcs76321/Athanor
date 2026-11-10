# v5 benchmark — known issues and fix register

**Status:** candidate instrument, not frozen · **Branch:** `bench/v5-0x` ·
**Date:** 2026-11-10 · **Evidence:** see "Evidence retention" below.

This is the working issue register from the v5 build and the first 2×2 + external
runs. It is intentionally blunt: the benchmark is doing its job, and several
product claims fail. Fix these before treating v5 as a frozen instrument.

---

## P0 — blocks a credible v5 claim or freeze

### 1. Engine repo-test/acceptance is mis-wired for external repo tasks
- **Evidence:** Aider/Exercism single arm: **8/8 external passes** (80 official
  assertions) while the engine accepted only **~2/8**. First batch: 4/4
  external, 1/4 engine accepted. Second batch: 3/4 external, 1/4 engine
  accepted; `acronym` produced no artifact after an index timeout and passed on
  retry. The engine's own state was `failed` on exercises whose best candidate
  passed the official tests.
- **Meaning:** Athanor generated the correct code; its own test/acceptance path
  did not test the artifact that the external scorer tested, or rejected it.
  Until fixed, the external track is a model + best-candidate signal, not a
  harness-verification signal.
- **Fix:** apply the synthesized candidate tree to the exercise repo snapshot
  before the frozen test command (or make the corpus test command copy the
  artifact to the target module), then re-run the same 8 exercises. The engine's
  label and the external label should converge.
- **Files:** `internal/engine/phases/*` (code materialization/test path),
  `tools/bench/` repo task collection, `tools/bench/aider_polyglot.py`.

### 2. Document cycles do not complete
- **Evidence:** utility cycle completion **0/5 (bonsai)** and **2/5 (gemma)**.
  Brief cycles stop at 5–6/7 blocks; news day1/day3 stop at 0/7 or 5/7. The
  produced artifacts pass the external citation checks (cycle external pass
  60% bonsai / 40% gemma) but the DAG never finishes.
- **Meaning:** the cycle is not a completed operating run. Engine
  acceptance/retry on document tasks is the bottleneck.
- **Fix:** deterministic document verifier or a more tolerant acceptance path;
  stop retrying when the artifact passes the frozen checks; ensure verify/retain
  blocks terminate. Re-run the utility suite and require both external pass and
  cycle completion for the claim.

### 3. Safety path-escape failure (bonsai)
- **Evidence:** `v5-safety-path` failed for `bonsai27b`; gemma passed 4/4. Safety
  is a hard gate, so the 2×2 safety claim fails even though one generator passes.
- **Fix:** inspect the artifact/test; determine whether the model's
  `read_note` accepted `../secret` or the test path failed. Add a regression
  task; do not waive the gate.

### 4. Missing ADR and README for v5
- The protocol and comments reference **ADR-0136**, but
  `docs/adr/0136-*` does not exist. `eval/bench/v5/README.md` is also missing.
- **Fix:** write ADR-0136 (v5 instrument: local-only 2×2, claim vector,
  external track, freeze rules) and the v5 README (how to run, what is frozen,
  what is a subset). Correct any references.

### 5. Evidence is not durable
- `tools/bench/results/` is gitignored, so the 2×2 reports and quality scores
  are only on this machine. Aider reports live under `.bench-aider/` (also
  ignored).
- **Fix:** copy the small reports/scores to a committed evidence path (e.g.
  `docs/probes/runs/bench-v5-20261110/`) or a non-ignored `eval/bench/v5/runs/`
  with a manifest. Do not commit large artifacts.

### 6. External track is now merged (verify after re-runs)
- The 4/4, 3/4, and acronym 1/1 reports are merged into an **8/8** report under
  `tools/bench/results/v5/bonsai27b/aider/` and into the committed evidence dir;
  `analyze.py` now includes the external section.
- **Remaining:** do the same for gemma; regenerate the report whenever a track
  run changes.

---

## P1 — validity and measurement

### 7. Quality judges disagree; quality is not established
- **Evidence:** bonsai agreement **−0.667 (single) / −0.490 (full)**; gemma
  **0.544 / −0.500**. The gemma score command exits 1 below the 0.60 floor; the
  runner continues with `|| true`.
- **Meaning:** the pointwise rubric is not a measuring instrument at n=6.
- **Fix:** move to **blind pairwise A/B (both orders)** with the two judges and a
  human/frontier anchor; report win/tie/loss and agreement. Do not claim quality
  from the absolute rubric. Update `analyze.py` to read
  `quality-*-scores.json` and report agreement explicitly.

### 8. Agency suite is saturated
- **Evidence:** 100% on both arms and both generators.
- **Meaning:** these tasks do not discriminate; they do not support the agency
  claim. They are currently anchors, not evidence.
- **Fix:** harder agentic tasks (multi-file, repair, observation, long-horizon,
  interruption), or mark the suite saturated and exclude it from the claim.

### 9. Memory correction sequence fails
- **Evidence:** memory suite 50% on both (insight passes, correction fails).
- **Fix:** inspect the second-step output; ensure the correction is injected and
  that the check tests the applied term. If the engine cannot carry the
  correction, fix the memory path before claiming memory.

### 10. News day2/day3 semantics
- **Evidence:** day1 external pass for both; day2/day3 fail their new/old/no-op
  checks; cycle completion low.
- **Fix:** tune the news template/prompt and the `new_items_section` semantics;
  verify the wire fixture is the only source. This is both a product-quality and
  task-design issue.

### 11. External track is single-arm only; full arm times out
- **Evidence:** the `full` arm reached 6/8 at the 2h cap (isogram running,
  acronym/hamming not reached). The external track is now single by default.
- **Fix:** run a **4-exercise full subset** under a longer cap for a full-harness
  external signal; keep the 8-exercise single as the cheap floor. Label both.

### 12. `acronym` index timeout
- **Evidence:** the first attempt's repository index call exceeded the
  10-minute client timeout and the job produced no artifact; the retry passed
  the official tests (9/9).
- **Fix:** investigate sync index latency for a 67-chunk repo, decouple index
  from job start, raise/soften the timeout, and keep retries classified as
  infrastructure, not model failures.

### 13. `analyze.py` correctness gaps
- Unrun arms (`utility/full`, `safety/full`, `memory/full`) display **0%**
  instead of “not run.” Omit unrun arms.
- Quality scores/agreement are not read.
- The external Aider section is absent until the report is merged.
- The cycle table mixes external pass and completion; make both explicit (it
  does) but add the completion gate to the claim text.
- Paired W/T/L only covers foundation/agency; add utility/quality where valid.
- Add the external track version/commit to the report.

### 14. Aider/Exercism is a custom adapter, not the official harness
- One attempt per exercise, 8 Python exercises, custom artifact application.
- **Fix:** keep the label; optionally run the official Aider harness later for a
  directly comparable row. Never present it as a leaderboard rank.

---

## P2 — product/operating improvements

### 15. Full-arm cost
- Foundation full is ~1.8–2.1× single tokens; gemma quality/full ~3.2×. The
  foundation gain is +8.3 points with no losses. Report cost per accepted
  artifact and keep the cost envelope on saturated tasks.

### 16. Local source and template contracts (fixed, verify)
- Local `sources/` and `wire/` ingestion, source-agnostic research/fetch
  contracts, and best-candidate export were fixed during the run. Add
  regression tests for each; the current v5 test covers citation checks and
  protocol/corpus, but not the engine's repo-test path.

### 17. Cycle probe `bestArtifact` only considers document/proposal
- Code cycles would need `kind='code'`; the Aider track uses the soak runner, so
  this is not blocking today. Extend if code cycles are scored.

### 18. Forwarder and cleanup
- `scripts/dev-ollama-forward.py` binds `0.0.0.0:11435` for dev; stop it after
  runs. It is currently required by the Core. Add a note to the README and never
  ship it.
- The 2h wrapper timeout left a Core running; the runner should stop the Core on
  timeout (trap) or use a supervisor.

### 19. Disk and images
- Podman reached "no space left on device" earlier; prune unused images and keep
  `athanor-core`/`athanor-bench` only. Monitor `podman system df`.

### 20. Leaderboard doors (strategy)
- **τ² 2.0 text:** custom agents allowed, but the 2.0 text tab is not live; text
  packages require opening an issue. 1.x text is live but model/scaffold-centric.
- **Terminal-Bench 4.0:** community submissions are currently closed
  (maintainer-run only).
- **TheAgentCompany:** open submissions, agent-scored, best open fit.
- **GAIA:** open submissions, agent systems, high visibility; top ~94% is
  multi-model.
- **Aider Polyglot:** model-centric; use as external evidence, not a harness
  leaderboard.
- **Fix/next:** open the τ² text issue; build TheAgentCompany next; GAIA as the
  visibility play; Terminal-Bench privately until reopened.

---

## Already fixed (keep, add tests)

- Local `sources/` and `wire/` injection into task prompts.
- Source-agnostic research/fetch/extract contracts (URL or file name).
- Cycle probe exports the **best candidate** when the engine rejects.
- Cycle probe detects stalls instead of waiting out the timeout.
- Aider adapter parses multi-file `=== FILE: ... ===` artifacts.
- External track is single-arm by default and versioned separately.
- Aider 8/8 report merged; `analyze.py` renders the external section.
- Acronym index-timeout retry passed 9/9 official tests.
- v5 protocol/corpus/digest, v5 validation test, and `make check` green.
- v5 2×2 completed for both generators.

---

## Immediate order of work

1. Fix the engine repo-test/acceptance path; re-run the 8 Aider exercises and
   require engine accept ≈ external pass.
2. Fix document cycle completion (utility); rerun the utility suite.
3. Triage `v5-safety-path`; do not waive the safety gate.
4. Write ADR-0136 + v5 README; commit the reports to a durable evidence path.
5. External track merged at 8/8; rerun the analyzer and add quality agreement
   reporting. Then do the same external run for gemma.
6. Harden agency/memory/news tasks; move quality to pairwise.
7. Then the next external target: TheAgentCompany (open) or τ² when text opens.

---

## Evidence retention

Current on-disk evidence (before compaction):
- `tools/bench/results/v5/bonsai27b/v5-report.md` and `.json`
- `tools/bench/results/v5/gemma12b/v5-report.md` and `.json`
- `tools/bench/results/v5/{bonsai27b,gemma12b}/quality-{single,full}-scores.json`
- `.bench-aider/aider-20261110-123219-bonsai27b-single/report.md` (4/4 split)
- `.bench-aider/aider-20261110-152742-bonsai27b-single/report.md` (3/4 split)
- `.bench-aider/aider-20261110-165424-bonsai27b-single/report.md` (acronym 1/1)
- Merged: `tools/bench/results/v5/bonsai27b/aider/report.md` (8/8)
- Committed copies: `docs/probes/runs/bench-v5-20261110/`
