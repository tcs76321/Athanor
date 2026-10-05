# M3-T7 Quality Probe — Experiment Protocol

This document captures the experiment protocol for
the M3-T7-b (calibration) and M3-T7-c (verdict
stability) measurement experiments. The code path
that produces the data is already in place
(M3-T1's `comparison` event with `confidence`,
M3-T3's `comparison_unknown_winner_downgraded`,
the §19.3 deterministic guard); what's missing is
the *measurement* — running each verdict twice
under controlled conditions and reporting the
calibration + stability table.

The M3-T7-a (Jaccard diversity) measurement is a
code change in `internal/engine/diverge.go` that
emits the `divergence_jaccard` event. See the
`spikes/m3-t7-probe/` scaffold for the runner.

## Headline experiment: dialectical vs single-shot

The primary M3-T7 question is **does N-candidate divergence + deterministic
evaluation/comparison beat a single candidate?** This experiment is the
reason the probe exists; the T-a/b/c sub-measurements below are supporting
diagnostics.

- **Factor:** `execution.divergence_candidates` ∈ {1 = single-shot, 3 =
  dialectical}, everything else held constant.
- **Single-shot arm (N=1):** the full engine, same prompt assembly, same
  evaluation/comparison — one candidate. This isolates the value of the
  *multiple candidates + comparison* from the rest of the pipeline. (The
  engine still runs `phaseEvaluate`/`phaseCompare`; with one candidate the
  comparison degrades to `winner=new`.)
- **Dialectical arm (N=3):** the default.
- **Metrics:** final-artifact quality (five channels, below), comparison
  `winner`/`confidence`, token cost, wall time, call count, retries/
  reflection loops, and — for N=3 — average pairwise Jaccard diversity.
- **Cost-normalized comparison is mandatory:** the N=3 arm costs ~2–3× the
  calls, so quality-per-token and quality-per-minute are reported alongside
  raw quality.

## Locked design (2026-10-05)

These decisions are fixed for the run; changing them re-opens the protocol.

### Sample set — 10 goals (2 text / 6 code / 2 document)

One project per goal. Goals 1–5 began as the M1-T8 set and goals 6–10 as the
M3-T2 rubric-covering code set; the objectives were tightened (explicit
paragraph/section counts, one-line functions) so a 9B generator produces a
bounded artifact and the N=3-vs-N=1 difference is visible rather than buried
in long prose. Goal 9 is deliberately failure-sensitive.

| # | Src | Archetype | Goal | Criteria |
|---|---|---|---|---|
| 1 | M1-T8 | text | "Write exactly three short paragraphs arguing why local-first software matters, one reason per paragraph." | exactly three paragraphs; each names a distinct reason; a one-sentence conclusion |
| 2 | M1-T8 | text | "Draft a short onboarding email (under 120 words) for a new member of a local software club." | under 120 words; one clear call to action |
| 3 | M1-T8 | code | "Write a Python module defining a Book dataclass with title and author fields, and a function summarize(book) that returns 'title by author'." | pure stdlib; docstrings on every public function; a usage example |
| 4 | M1-T8 | document | "Write a short README with exactly three sections named Install, Usage, and License." | sections named Install, Usage, License; a one-line project description at the top |
| 5 | M1-T8 | document | "Write a short design brief with exactly three sections: Parts, Steps, and Risks." | sections named Parts, Steps, Risks; at least two risks named |
| 6 | M3-T2 | code | "Write a Python function fib(n) that returns the n-th Fibonacci number using recursion, with a docstring." | pure stdlib; a docstring on the function; a usage example |
| 7 | M3-T2 | code | "Write a Python module with three one-line functions: trim(s), pad(s, width), and reverse(s)." | pure stdlib; docstrings on every public function |
| 8 | M3-T2 | code | "Write a Python class Cache with get(key), set(key, value), and evict(key) methods." | pure stdlib; no TODO or FIXME placeholders |
| 9 | M3-T2 | code | "Write a Python function reverse(s) that returns its input reversed and must always succeed." | pure stdlib; tests pass |
| 10 | M3-T2 | code | "Write a Python class TodoList with add(task), complete(index), and pending() methods." | pure stdlib; docstrings on every public function |

**Code-goal test execution (probe limitation).** The Job Pod runs a candidate
via `python -` on stdin and never writes it to a file, so the project's test
command has nothing to test (`pytest -q` exits 5, failing *every* candidate).
The probe therefore creates code projects with `execution.test_command: "true"`
— a no-op pass — and the code signal comes from `execute_code` (does it run?)
plus the LLM rubric. Absolute code scores are inflated by this; the N=3-vs-N=1
comparison is unaffected because both arms share it. A production fix (persist
the candidate to a file, then run the real test command) is out of scope here.

**Code-goal outcome capture (probe limitation, fixed).** The engine sets a job's
`finished_at` at the terminal transition but writes its `strategy_outcomes` row
only after the Job Pod tears down — about 10s later for a code goal, versus the
same millisecond for a text/document goal. An early collector waited only 4s and
so recorded `score=0.00, winner=` for every code goal even though the DB held the
correct `accepted_new` rows (the earlier full run's `book-collection`/`fibonacci`
rows). The collector now polls up to 30s and reconciles at arm end
(`reconcileMetrics`). `make probe-reconcile` repairs an already-written
`results.json` from its state DB; run it before `make probe-report`. The cleaner
production fix — capture the outcome before `stopPod` in `engine.Run` so it lands
atomically with the terminal state — is deferred (F4).

### Models and runs

| Role | Model | Notes |
|---|---|---|
| Generator | `ornith-1.5:9b` | the fast local model; the first full run uses it only |
| Judge (third-party) | `gemma4:12b-mlx` | independent family; scores every artifact offline |
| Judge (third-party) | `granite4.2:3b` | independent family; cheap second opinion |

The generator runs both arms (N=3 ×3 runs + N=1 ×1 run); the two judges
score every artifact offline via the probe's `judge` subcommand. The 27 B
model is an optional later run — omitting it is what keeps the full matrix
to a few hours instead of a day. All models must already be pulled locally
(`ollama list`).

### Repetition

- Dialectical arm (N=3): each goal run **3×**. The headline uses all three
  (median); T-c stability reads the three `winner` values directly.
- Single arm (N=1): each goal run **1×**.
- Generator: 10 × (3+1) = **40 jobs** (a few hours on the fast model).

### Memory / residency rule

The generator (`ornith-1.5:9b`, ~7 GB) fits comfortably alone. The judges
(`gemma4:12b-mlx` ~14 GB, `granite4.2:3b` ~2 GB) run offline in a separate
pass, so they never co-reside with the generator's daemon. Before any run:

1. Read `ollama ps`, `vm_stat`, `memory_pressure`.
2. Load the model; require `ollama ps` to report **`100% GPU`** and swap
   I/O to stay **0**. A partial-CPU reading invalidates the run.
3. Set `OLLAMA_MAX_LOADED_MODELS=1` and `OLLAMA_NUM_PARALLEL=1` so
   co-residency cannot happen by accident.
4. The judge pass runs with the generator daemon stopped, so each judge
   gets the machine; the two judges may co-reside if headroom allows.

### Scoring — four channels

1. **Deterministic** criteria checks (stdlib-only, docstrings present, no
   TODO/FIXME, tests exit 0) — machine-checkable per artifact.
2. **Third-party judges:** `gemma4:12b-mlx` and `granite4.2:3b`, blind
   (goal + criteria + artifact only; no arm/model label), invoked by the
   `judge` subcommand at temperature 0 with a fixed seed. Both differ in
   family from the generator, so their errors are not correlated with it.
3. **Human rating:** the operator scores every artifact against the
   criteria. *Relief valve:* rating may be limited to the single run plus
   the first dialectical run per goal; the remaining dialectical runs feed
   T-c and the automated judges only.
4. **Online agent (optional):** the probe emits a paste-ready full-artifact
   judge packet per artifact; the operator transcribes the returned score.
   **Privacy:** this sends artifact text off-machine — opt in per packet.

### Determinism and seeding

Ollama supports `options.seed`, but does not echo it back, and
`temperature: 0` is greedy yet **not bit-reproducible** (batched eval,
`prompt_eval_cached_count` prefix-cache reuse, quantization, GPU backend
noise). The design:

- **No global seed.** Divergence stays **unseeded** — a shared seed would
  collapse the N candidates and destroy T-a/diversity.
- **Temp-0 judgment calls** (evaluating, comparing) may carry a seed
  derived from `f(jobID, phase, candidateArtifactID)`, controlled by
  `inference.judgment_seed` (`off` default). The resolved seed is recorded
  in the `llm_call` audit row.
- **Structured judgment** is grammar-constrained with `format: "json"`
  (ADR-0012), removing parse-level variance.
- **Offline judges** are called directly against Ollama at `temperature: 0`
  with a fixed seed per (goal, judge).
- **T-c runs twice:** *unseeded* (natural total nondeterminism,
  production-faithful) **and** *seeded* (irreducible backend/numeric
  residual). The delta is the sampling contribution.
- Record model **digests** (not just tags) and the Ollama version per run.
- If T-c shows residual instability, the recommended fix is **quorum**
  (2-of-3 agreement before accepting `winner: new`), not more seeding.

### Orchestration

The probe runner manages the daemon lifecycle itself: it writes each arm's
config, starts/stops the daemon with the matching config + state dir,
creates projects, submits goals, polls to terminal, and tears down. One
command per arm; maximally reproducible.

## Smoke findings (2026-10-05)

The harness was validated with a bounded smoke (goal #1, `ornith-1.5:9b`,
both arms) before the full run. It did its job: it surfaced **five real-model
defects** — all fixed — and then completed **4/4 jobs** (all `completed`,
`winner=new`, score 0.97; zero orphan pods).

| # | Defect | Fix |
|---|---|---|
| 1 | `format:"json"` guarantees parseable, not *typed*, JSON: a string `confidence` / `style_issues` hard-failed the job | tolerant, audited verdict parser (`verdict_coerced`) — [ADR-0042](../adr/0042-judgment-json-robustness.md) |
| 2 | Schema-constrained decoding made `ornith` run away (>7 min) on an unbounded string field | schema mode made opt-in, default off — ADR-0042 |
| 3 | A fresh project's first artifact was downgraded to `none` unless the judge set `better_than_previous=true` — meaningless with no previous, so ~half of fresh-project jobs failed | `DecideWinner` accepts a passing, confident record with no previous — [ADR-0043](../adr/0043-first-artifact-acceptance.md) |
| 4 | A degenerate generation streamed until the 10-minute HTTP client timeout | `inference.max_output_tokens` (default 4096) — runaway guard |
| 5 | A thinking-capable model spent the whole token budget on its reasoning phase, leaving visible content empty | `inference.think` (default false) — thinking disabled by default |

The smoke confirmed defect #3 is decisive: without the fix, roughly half of
fresh-project jobs fail regardless of artifact quality, which would have
swamped the measurement. The full run (all 10 goals, including the code goals
that create Job Pods) remains pending; its findings seed the F4 track
([`docs/f4-plan.md`](../f4-plan.md)).

## T-b: Judge-confidence calibration

**Question:** Does the LLM's reported confidence
in the winning verdict match the observed
outcome (the rubric-graded score)?

**Setup:**

1. Sample N=10–20 goals from the M3-T2 probe's
   curated set (the 5 code-archetype goals plus
   a matching set of text-archetype goals).
2. Run each goal through the dialectical loop.
3. For each job that reaches `StateComparing`:
   - Record the `comparison` event's `confidence`
     field as the LLM's reported value.
   - Record the `EvaluationRecord.score` of the
     winning candidate as the observed quality
     (the rubric's numerical grade, 0.0–1.0).
4. Bin by confidence (e.g. 0.5–0.6, 0.6–0.7,
   0.7–0.8, 0.8–0.9, 0.9–1.0) and compute the
   mean observed score per bin.
5. Plot a reliability diagram: bin confidence
   on the x-axis, mean observed score on the
   y-axis. The diagonal y=x is "perfectly
   calibrated."

**Trigger conditions for follow-up work:**

- >20% of bins more than 0.2 off the diagonal:
  the §19.3 threshold (`min_judge_confidence`,
  default 0.7) is mis-calibrated. The first
  lever, per ROADMAP §7 M3-T7-b, is ADR-0013's
  prompt disclosure (showing the judge the
  truncation), not changing the default.
- The 0.7–0.8 bin is consistently below the
  diagonal (LLM over-claims in the "default
  accept" zone): raise the threshold to 0.8.

**Data inputs:**

- `events.data_json` for the `comparison` event
  (category=`jobs`, level=`info`).
- `evaluation_records.score` joined on
  `comparison.new_artifact_id`.

**Output:**

A `docs/probes/m3-t7-quality-probe.md` results
section (or a new file
`docs/probes/m3-t7-quality-probe-results.md`) with
the reliability diagram and the per-bin table.

## T-c: Verdict stability at T=0

**Question:** How often does the security persona
produce the same verdict when called twice with
the same input at T=0?

**Setup:**

1. Sample N=10–20 goals (same set as T-b).
2. For each goal, run the dialectical loop
   once, then re-run the comparison phase
   alone (3×) with `temperature=0` on the
   security persona.
3. Record the `winner` field for each of the
   3 re-runs.
4. Classify as:
   - 3-same (all three re-runs agree)
   - 2-1 (one outlier)
   - 3-way (no two agree)
5. Report the distribution.

**Trigger conditions:**

- <70% 3-same rate: the verdict is not
  reproducible. Per ROADMAP §7 M3-T7-c, the
  empirical case is then either (a) raise
  `min_judge_confidence`, (b) require two
  `EvaluationRecord` instances to agree
  before accepting `winner: new`, or (c)
  both.
- The 3-way rate is non-negligible: the
  §19.3 guard's `confidence > threshold`
  check is unreliable; the multi-instance
  consensus path is the right fix.

**Data inputs:**

- The `comparison` event's `winner` and
  `confidence` fields.
- The `comparison_unknown_winner_downgraded`
  event (counted as a stability failure in
  the 3-way bucket).

**Output:**

A `docs/probes/m3-t7-quality-probe.md` results
section with the verdict-distribution table
and a per-archetype breakdown.

## How the data is already in events

Both experiments read from the events table. The
M3-T1 engine already emits:

- `comparison` (winner, confidence, reasons)
- `comparison_unknown_winner_downgraded`
  (raw_winner, downgraded_to)
- `evaluation` (per-record score)

The M3-T7-a Jaccard event was added in commit
`a11ab63` and is also in events. None of T-b
or T-c requires a code change to the engine;
they require a *runner* that drives the
comparison phase repeatedly under controlled
conditions. The runner is the next commit in
the M3-T7 sequence (post-spike).

## Runbook (manual collection, F3-T6)

The runner is not implemented; `spikes/m3-t7-probe/` is a scaffold that
prints a banner. Until the runner lands, the measurements can be collected
by hand from a running daemon. This section is the operational recipe.

**Prerequisites**

- Ollama running with the persona models configured in `config.yaml`.
- The daemon running (`make run`) with a real Job Pod image set
  (`job_pod.image`), because code-archetype jobs run tests in a pod.
- A project (`athanor project create`) and N goals (10–20) covering both
  `code` and `text` archetypes.

**T-b — calibration**

1. Submit each goal (`athanor goal submit`); wait for completion.
2. For each job, read the comparison confidence and the winning
   evaluation score. Everything is in the append-only events table:

   ```sql
   -- comparison confidence for a job
   SELECT data_json FROM events
   WHERE job_id = :job AND category = 'jobs'
     AND json_extract(data_json, '$.event') = 'comparison';
   ```

   The winning `EvaluationRecord.score` is the `evaluation` event (or the
   `evaluation_records` row) for `comparison.new_artifact_id`.
3. Bin confidence into 0.5–0.6 … 0.9–1.0 and compute the mean observed
   score per bin; the diagonal `y=x` is perfect calibration.
4. Follow-up triggers are in the T-b section above.

**T-c — stability at T=0**

Ideally the comparison phase is re-run 3× on the same input. With no
"re-run comparison only" CLI, the interim approximation is to run each
goal three times (fresh job) and compare the `comparison.winner` values;
classify each goal as 3-same / 2-1 / 3-way. Note in the findings that this
confounds model nondeterminism with input nondeterminism (fresh
divergence each run); the runner should isolate the comparison phase.

**Output**

Append a results section to this file (or a `-results.md` sibling) with
the per-bin table, the reliability diagram, and the stability
distribution — including the honest caveat above. Do not record findings
that were not measured.

## Status

M3-T7-a (diversity): code landed (`a11ab63`); the probe now reads the
engine's per-cycle `divergence_jaccard` event.
M3-T7-b (calibration): protocol + runbook captured (this document);
**full-run measurement pending** (the smoke is confirmed — see above).
M3-T7-c (stability at T=0): protocol + runbook captured (this document);
**full-run measurement pending**.
Headline (dialectical vs single-shot): design locked 2026-10-05; harness
validated by the smoke (4/4 jobs), full run pending.

The scaffold's T-a/b/c labels were rotated relative to ROADMAP §7 and this
document; corrected in M3-T7.0.

The smoke's five defects are fixed and recorded above. When the full
measurement lands, either the existing `min_judge_confidence` default is
justified in a results section or an ADR changes it; the findings seed the
F4 track ([plan](../f4-plan.md)).
