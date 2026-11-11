# v5 benchmark — known issues and fix register

**Status:** candidate instrument, not frozen · **Branch:** `bench/v5-0x` ·
**Date:** 2026-11-10 · **Evidence:** see "Evidence retention" below.

This is the working issue register from the v5 build and the first 2×2 + external
runs. It is intentionally blunt: the benchmark is doing its job, and several
product claims fail. Fix these before treating v5 as a frozen instrument.

**Freeze readiness: not ready.** The P0 engine fixes validate (external/engine
convergence, safety, cycle completion, memory), but the reconciliation bundle
still has real reds: the utility evidence is 3/5 (day1 and brief-wal are not
committed), mechanism coverage is 0% on the four `requires` rows, cost exceeds
the envelope on the saturated slice, and quality is `not_measured`. The
authoritative report is
[`docs/probes/runs/bench-v5-20261111-merged/`](probes/runs/bench-v5-20261111-merged/README.md);
the older postfix reports are explicitly superseded. v5 stays
`status: candidate` and `cut: pending` until a single-revision freeze run clears
the release-blocking gates.

---

## P0 — blocks a credible v5 claim or freeze

### 1. Engine repo-test/acceptance is mis-wired for external repo tasks — FIXED
- **Cause:** the engine staged a single-file candidate as `solution.py` unless
  it could infer the module name from the test command, while the Exercism
  tests import `two_fer`, `raindrops`, etc. The task's declared solution file
  was not on the project execution wire shape, so it was never persisted.
- **Evidence:** after fixing `solution_file` staging + API persistence, the
  one-shot 8-exercise rerun was **4/8 external and exactly 4/8 engine
  accepted** for bonsai, and **1/8 external and exactly 1/8 engine accepted**
  for gemma — row-for-row convergence. The earlier 8/8 exploratory run used
  scheduler retries and is a different measurement shape.
- **Also fixed:** a passing hard verifier no longer loses to the judge's
  advisory `missing_criteria`, and the pod exports `REPO=/tmp/work` for test
  commands.
- **Files:** `internal/project/project.go`, `internal/api/api.go`,
  `internal/engine/phases/pod_wiring.go`, `internal/engine/phases/evaluate.go`,
  `cmd/athanor/pod_executor.go`, `tools/bench/aider_polyglot.py`.

### 2. Document cycles do not complete — FIXED
- **Root causes:** cycle blocks had no deterministic per-task checks; a judge
  `missing_criteria` could veto a passing check; `bestArtifact` fell back to
  the newest document when the named synthesize artifact was rejected; and a
  blocked leaf left the cycle `running` forever.
- **Fix:** compile block acceptance into per-task `checks_json`, make hard
  checks authoritative, prefer the named synthesize artifact even when it is a
  candidate, and make a cycle with a blocked-only graph enter
  `awaiting_review` immediately instead of stalling.
- **Follow-up bug found in validation:** the `extract` block is a `data`
  capability, and the declared-checks verifier only ran for text/document, so
  the extract block fell back to the LLM judge. The verifier now applies to
  data candidates too when the declared checks are structural.
- **Validated:** the full utility suite now completes **5/5 cycles at 7/7
  blocks** (`brief-local-first`, `brief-wal`, `news-day1`, `news-day2`,
  `news-day3`). External pass is 3/5: both briefs and day1 pass; day2 and day3
  fail their news semantics checks. Completion is fixed; the utility external
  floor is not yet met.

### 3. Safety path-escape failure (bonsai) — FIXED
- **Evidence:** the external code test passed 4/4, but `v5-safety-path` ended
  `failed` because the LLM judge added a false `security_issues` claim
  (`uses os.path.realpath`) after the deterministic test passed.
- **Fix:** a hard-verifier pass now treats LLM security opinions as advisory and
  audits them (`judge_security_advisory`); the task's deterministic test/check
  is the safety gate.
- **Validated:** the safety suite is **4/4 completed and engine accepted**,
  including `v5-safety-path`.

### 4. Missing ADR and README for v5 — FIXED
- `docs/adr/0136-v5-grounded-agency-benchmark.md` and `eval/bench/v5/README.md`
  now exist and are committed. The ADR records the claim vector, deterministic
  acceptance authority, external track, freeze rules, and the separate
  frontier tier.

### 5. Evidence is not durable — FIXED
- Small reports, quality scores, and Aider reports are committed under
  `docs/probes/runs/bench-v5-20261110/` with a manifest. Large artifacts and
  per-trial state remain in the gitignored run trees.

### 6. External track is now merged (verify after re-runs)
- The 4/4, 3/4, and acronym 1/1 reports are merged into an **8/8** report under
  `tools/bench/results/v5/bonsai27b/aider/` and into the committed evidence dir;
  `analyze.py` now includes the external section.
- **Remaining:** do the same for gemma; regenerate the report whenever a track
  run changes.

---

## P1 — validity and measurement

### 7. Quality judges disagree; quality is not established — NOT ESTABLISHED
- **Evidence:** the pre-fix pointwise rubric gave bonsai −0.667/−0.490 and
  gemma 0.544/−0.500.
- **Fix:** `tools/bench quality-pairwise` now runs blind A/B in both orders
  with granite and mimo, reports inter-judge agreement, self-consistency, and a
  full-vs-single win rate, and the analyzer reads those fields and the protocol
  floors. The old pointwise scores are reference only.
- **Remaining:** pairwise was run on the existing quality artifacts: full win
  rate 0.75, but inter-judge agreement **0.00** and self-consistency 0.30. The
  quality claim is **not established**. The old pointwise scores are reference
  only.

### 8. Agency suite is saturated — HARDENED (not re-run)
- **Evidence:** the pre-fix suite was 100% on both arms/generators with two
  tasks.
- **Fix:** added three harder tasks: observation-driven multi-file repair, a
  multi-module wire-format contract, and a two-step long-horizon sequence. The
  suite is only evidence if it now discriminates; a 100% rerun is reported as
  saturated. A fresh agency run is still pending.

### 9. Memory correction sequence fails — FIXED
- **Evidence:** the pre-fix two-step sequence failed at the first step.
- **Fix:** the sequence runner now supports a `correction` step, posts it
  through the project corrections API, and requires the next job's
  `corrections_injected` event. The v5 correction task no longer restates the
  corrected term in step 2, so the later artifact must use the injected
  guidance.
- **Validated:** both memory sequences pass; the correction sequence records
  `correction_injected: true` on the second task.

### 10. News day2/day3 semantics — DAY3 FIXED, DAY2 STILL RED
- **Evidence:** day1 and day3 pass after the post-fix utility rerun; day2 still
  puts the updated story's old id (`story-001`/`story-004`) in New.
- **Fix:** the day3 check was too strict: it forbade every story id anywhere,
  contradicting the task's "Previously reported" heading. It is now a
  `new_items_section` check that allows repeats under Previously reported and
  still fails a repeat in New. The news template states the general update
  contract (new id in New; the earlier id only under Previously reported).
- **Held out:** day2 is not tuned on. It remains a red held-out claim. A general
  product fix (for example, a structural New-section check that forces a repair)
  is the next step, not a task-specific prompt edit.

### 11. External track is single-arm only; full arm times out
- **Evidence:** the `full` arm reached 6/8 at the 2h cap (isogram running,
  acronym/hamming not reached). The external track is now single by default.
- **Fix:** `AIDER_HOURS` now extends the cap; run a **4-exercise full subset**
  under 4h. Keep the 8-exercise single as the cheap floor. Label both. Pending
  validation.

### 12. `acronym` index timeout / hidden retries — MITIGATED
- **Evidence:** the first attempt's repository index call exceeded the
  client timeout; the retry passed 9/9. A later external run showed the probe
  returning on the first failed job while the scheduler retried the task in
  the background, so two tasks ran at once and the next job timed out.
- **Fix:** the benchmark config now sets `max_task_retries: 0` (one job per
  task; the `full` arm's internal candidates/reflection are untouched), and
  the external adapter passes `-skip-index` so a wedged index cannot consume
  the per-task budget. Keep retries classified as infrastructure, not model
  failures.

### 13. `analyze.py` correctness gaps — FIXED
- Unrun arms are omitted/`—` instead of 0%.
- Quality is read from `quality-pairwise.json` with protocol floors.
- The external Aider section includes the engine-accept column.
- Cycle external pass and completion are separate claims.
- Paired W/T/L covers foundation, agency, quality, and utility.
- Thresholds come from `protocol.yaml` (the old hardcoded 0.75 vs 0.9 mismatch
  is gone).

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

- Local `sources/` and `wire/` injection into task prompts; the single-arm
  project create now carries the corpus task's declared `sources` (2026-11-11),
  so one-shot briefs see the same text as cycle tasks.
- Required sections are rendered as `# Heading` in the declared-checks prompt,
  matching the strict markdown-heading parser (2026-11-11).
- The verifier supports a generic `no_repeat_ids_across_sections` rule
  (2026-11-11), but the default news template does not use it: without the
  task's known old-id list it rejects a valid update that references the new id
  under both sections. The probe's `new_items_section` check remains the precise
  label; wiring the known old ids into the cycle is the follow-up.
- The probe treats `awaiting_review` as a terminal blocked cycle rather than a
  running stall (2026-11-11). The scheduler re-reads the task graph after
  `advance` before tracking the cycle, so the blocked leaf is actually visible.
- Source-agnostic research/fetch/extract contracts (URL or file name).
- Cycle probe exports the **best candidate** when the engine rejects.
- Cycle probe detects stalls instead of waiting out the timeout.
- Aider adapter parses multi-file `=== FILE: ... ===` artifacts.
- External track is single-arm by default and versioned separately.
- Aider 8/8 exploratory report merged; `analyze.py` renders the external section.
- Post-fix one-shot Aider run: 4/8 external and 4/8 engine accepted, row-for-row.
- Acronym index-timeout retry passed 9/9 official tests.
- Declared `solution_file` staging + hard-verifier authority.
- Per-task block checks, named-artifact selection, blocked-cycle termination.
- Pairwise quality judging and protocol-aware analyzer.
- One job per benchmark task and a skip-index option for the external track.
- v5 protocol/corpus/digest, v5 validation test, and `make check` green.
- v5 2×2 completed for both generators.

---

## Immediate order of work

1. ~~Fix the engine repo-test/acceptance path~~ — fixed; one-shot 8-exercise
   run converged at 4/8 external and 4/8 engine accepted.
2. ~~Fix document cycle completion~~ — fixed; one brief completed 7/7. Run the
   full utility suite and require external pass + 100% cycle completion.
3. ~~Triage `v5-safety-path`~~ — fixed; 4/4 safety tasks completed and
   engine accepted.
4. ~~Write ADR-0136 + v5 README~~ — done.
5. ~~Merge the 8/8 exploratory external report; add pairwise quality~~ — the
   one-shot external run is 4/8 and engine-convergent. Run gemma’s external
   track and pairwise quality next.
6. ~~Harden agency/memory/news~~ — corpus/runner changes are in; the
   reconciliation bundle shows cycle completion but the utility evidence is
   3/5. The 90% aggregate is replaced by component gates.
7. Rerun the full five-task utility suite for both generators (single + cycle)
   after the day3 check and template contract fix; update the merged evidence.
8. Rerun the affected suites at one commit for the freeze; set `status: frozen`
   and `cut: 0.0.0` only when the release-blocking claims are green.
9. Then the next external target: TheAgentCompany (open) or τ² when text opens.

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
