# F4 — Adaptive Judgment & Verification

**Foundations track** (like F2/F3): cross-cutting hardening of the execution
spine, not a new user-facing capability. Sits between M6 and M7; does not
renumber M7.

**Status:** code complete; **Gate G-F4 partially met** — the deterministic
verifier arm is met, the judge-agreement arm is weak/ambiguous. · **Gate:**
G-F4 · **Seeded by:** the M3-T7 quality probe
(`docs/probes/m3-t7-quality-probe.md`) and its
[results](probes/m3-t7-quality-probe-results.md).

**Measured 2026-10-05** (M2 Max, Ollama 0.35.1, `ornith-1.5:9b`):
- **Verifier-mode runs** (cross-family gemma4 judge; single-model and
  multi-model generation) decided **7/7 code accepts deterministically**
  (`judge_called=false`) — the gate's verifier arm. [verifier run](probes/f4-verifier-run.md).
- **Expanded 16-case anchor:** judge agreement `gemma4` **0.61** (passes the
  0.60 floor), `granite4.2` 0.53, and the **generator itself 0.77**;
  `pairwise` fails on position bias (27/40 order-inconsistent), and
  pointwise-100 does not beat pointwise-5. [anchor](probes/f4-anchor-calibration.md).
- **Micro re-run:** code instrument repaired (real tests decide; fences
  normalized), diversity 0.6–0.88. [micro](probes/f4-micro-run.md).

The judge-agreement arm is **weak and confounded**: on the anchor, capability
appears to dominate family (the generator scored best), which is the opposite
of a naive cross-family claim, though the anchor measures absolute scoring,
not the correlated-error property F4-T3 guards against. Deterministic
verification remains the load-bearing acceptance signal; the LLM judge stays
an advisory tie/fallback. A paired same-vs-cross-family ranking experiment is
the follow-up.

**Landed:** T0 (`/tmp/solution.py` real tests; judge retry/reliability gate;
`eval/anchor`), T1 (`internal/policy`; [ADR-0044](../docs/adr/0044-compute-policy-seam.md)),
T2 (difficulty hint + `RecentStats` history + `compute_policy` selector),
T8 (per-phase `think`/`max_output_tokens` bounds),
T3 (`internal/verify` + verification-first selection + cross-family judge;
[ADR-0045](../docs/adr/0045-verification-first-selection.md)),
T4 (quorum + reward-hacking guard + `internal/judge` anchor math;
[ADR-0046](../docs/adr/0046-judge-protocol.md)),
T5/T6 (heterogeneous roles + Jaccard re-roll; cost-aware ties;
[ADR-0047](../docs/adr/0047-diversity-and-cost.md)),
T7 (insight→plan bias, reflection gating, MCE scope;
[ADR-0048](../docs/adr/0048-reduce-and-complete.md)). The gate is not claimed
until the anchor agreement and probe re-run are recorded.

## Why

The M3-T7 probe tests the project's central hypothesis: does N-candidate
divergence + deterministic judgment beat a single candidate? The Best-of-N /
test-time-scaling literature says the answer is conditional — gains concentrate
on hard tasks, are bounded by judge quality, and cost grows ~linearly in N
while benefit saturates.

The probe then answered it for Athanor, and the answer moved the bottleneck:

- **Generation works.** Mean per-cycle candidate diversity was **0.795** — the
  divergence phase produces genuinely different candidates.
- **Judgment fails.** The engine rubric scored *against* the grain on its largest
  quality gaps (the one artifact violating an explicit criterion scored highest;
  the best artifact scored lowest), the third-party judges errored on **22%/51%**
  of calls and saturated at **1.0** across the board, and the code goals were
  unmeasurable because the Job Pod runs a no-op test command.
- **The verifier is the generator.** The engine judges its own candidates with
  the *same model*, so errors are correlated and best-of-3 collapses toward
  best-of-1.

So the constraint is **measurement and verification — not compute volume and not
generation**. F4 therefore does three things, in this order: it **repairs the
instrument**, makes the system's compute a **decision**, and makes selection a
**verifiable** act.

## Principle

Security constraints are mountains; **N, judging, strategy, budgets, and model
routing are rivers**. F4 changes the rivers. It must not weaken any invariant
(§4), Gate G0–G6, or the containment guarantees. A learned artifact may *advise*
a river; it never replaces a mountain — above all the frozen judge.

## Waves and tasks

Waves are the *dependency / execution order*. Wave 0 unblocks everything else:
F4's own gate is unfalsifiable until the instrument measures something.

| Wave | ID | Task | Size | Type | Acceptance criteria | Refs |
|---|---|---|---|---|---|---|
| 0 | **F4-T0** | **Measurement repair.** (a) Real code verification: persist the candidate to a file and run the project's real test command (removes the `test_command: "true"` no-op). (b) Judge protocol: a usable score range, no empty responses (granite failed 51%), and a **reliability gate** (success-rate floor). (c) A curated **human-anchor eval set** of rated artifacts. | L | spike+code | Code goals run their real tests (a failing candidate can score < 1.0); judge success rate ≥ the configured floor and scores are non-degenerate; the anchor set exists, is versioned, and is consumed by the calibration task | probe results §judges |
| 1 | **F4-T1** | **Policy seam — model-selection policy.** Extract a pure `Policy` that maps task features + archetype + history + remaining budget + power state → a plan: `{Candidates, MaxReflectionLoops, JudgeMode, JudgeCount, ModelRouting}`. The engine consults the policy instead of hard-coded constants; the default reproduces today's behavior exactly. | L | code | Same inputs → same plan; default plan byte-identical to pre-F4 behavior (regression test); no remaining hard-coded N/reflection/model constants in the engine; plan recorded on `StrategyProfile` and audited (`compute_planned`) | §13, §13.4; ADR-0044 |
| 1 | **F4-T8** | **Bounded inference, enforced.** Every model call carries an explicit wall-time and output-token bound; `think` is a per-phase decision (judgment off; generation per config). A degenerate generation can never stall a job. | M | code | A deliberately runaway call is cut and fails fast; no call path lacks a token bound; bounds recorded per `llm_call` | §13.1 |
| 2 | **F4-T3** | **Verification-first selection.** Per-archetype verifiers decide acceptance; the LLM judge runs only on ties or when no verifier exists. Code: tests/lint/type-check. Text/document: structural checks. Data: schema. The deciding judge must be a **different model family from the generator**. | L | code | ≥ a configured fraction of code accepts are decided by deterministic verifiers, not the LLM; the judge's family differs from the generator's; a judge-only path is audited | §19 |
| 3 | **F4-T4** | **Judge hardening.** Calibrate judges against the **human anchor** (the engine's own reliability curve is degenerate: confidence is a constant ≈0.99); cross-family **quorum** (2-of-3) for high-stakes accepts; a **reward-hacking detector** that distrusts a judge whose score contradicts a deterministic verifier. | L | code | A miscalibrated judge's borderline verdicts are rejected; quorum fires only on high stakes; a judge/verifier contradiction is audited and resolved in the verifier's favor; an ADR records the judge protocol and the `min_judge_confidence` correction | §13.4, §19 |
| 4 | **F4-T2** | **Adaptive compute.** Easy/familiar tasks plan to N=1; hard/novel tasks plan to N=k. Difficulty comes from the planning phase + prior `StrategyOutcome` history for the task class. | M | code | A task the planner marks easy runs N=1; a hard one runs N=k; the choice is audited | §13, §13.4 |
| 4 | **F4-T5** | **Heterogeneous diversity.** Generate candidates from genuinely different sources (`main` + a different-family `alternative`, distinct plans/scaffolds), routed through `Policy.ModelRouting`, and enforce a Jaccard **floor** with a bounded re-roll. | M | code | Measured per-cycle diversity is above the floor on the probe's tasks; candidates come from ≥2 sources on non-trivial tasks; a below-floor set is re-rolled at most k times and audited | §10.1, §13.2 |
| 4 | **F4-T6** | **Cost-aware acceptance.** Quality-per-token and quality-per-minute become first-class: a tie on quality at materially lower cost wins. | M | code | A lower-cost artifact wins a quality tie; the comparison audit row carries cost; the default remains backward-compatible | §28.2 |
| 5 | **F4-T7** | **Reduce & complete.** Audit and simplify low-ROI complexity (the §10.5 KV tier ladder if context is not the bottleneck; reflection gated by failure type), and finish the feedback→policy channels (persona-plan bias, template ranking, ExplorationPath proposals). | L | code | Each simplification is measured (no quality regression on the probe tasks); a mined insight demonstrably biases a subsequent persona plan, audited | §10.5, §13.4 |

Effort target ≤4h per task (S/M); `L` is the hard ceiling and may split.

### Known gap driving F4-T7 (M3-T7 finding A)

Repository indexing attributes chunks to the **project**
(`internal/mce/index.go`), but the engine's context provider queries the
Dormant Index by **job** (`ContextProvider.DormantIndex` → `ChunkStore.IndexForJob`).
No production path ingests repository chunks with a job ID, so the automatic
working set (tiers 3 and 6) is **empty for normal jobs**: repository context is
reachable only through the `query_memory` tool plus `context_swap`, not through
the prompt's Dormant Index. F4-T7 must either wire project-scoped chunks into
the job's working set under a relevance/limit policy — dumping an entire repo
index would bloat the prompt worse than the status quo — or consciously scope
the MCE to the tool path and simplify. The probe's token data informs the
choice.

## Gate G-F4

After F4, the following are enforceable and tested:

- **The instrument is trustworthy.** The probe re-runs with real code tests; the
  judge success rate is at or above the floor and its scores are non-degenerate;
  on the human-anchor set the engine's artifact ordering agrees with the human
  labels above a threshold.
- **Compute scales with difficulty.** An easy task class converges to N=1 in an
  automated test; compute-per-accepted-artifact on the probe's easy class drops
  without an accept-rate regression.
- **Selection is verifiable.** Deterministic verifiers decide the majority of
  code accepts; the deciding judge is a different family from the generator; the
  LLM judge is a tiebreaker, and a judge/verifier contradiction resolves to the
  verifier.
- **Judgment is bounded.** No model call can stall a job; every call has a token
  bound and, where relevant, `think` disabled.
- **Diversity is measured and useful.** Per-cycle candidate diversity is above
  the floor; near-duplicate sets are re-rolled and audited.
- **The loop learns.** A mined `StrategyInsight` demonstrably affects a
  subsequent persona plan, with an audit row.

## Out of scope / deferred

- **Any machine learning.** No adapter fine-tuning, no router bandit, no
  DPO/KTO, no GRPO/RLVR. F4 makes those *pluggable and measurable*; it does not
  train. They belong to M7 (Daydreaming) and a future training milestone, per
  [self-improvement](self-improvement.md) and [model routing](model-routing.md).
- Cross-task population search (FunSearch/AlphaEvolve-style) — Athanor runs a
  bounded per-task loop, not a global evolutionary optimizer.
- Cloud escalation is already designed (§21.7) and HITL-gated; F4 makes the
  policy able to *choose* it, but the mediation path stays a separate task.
- The periodic human check-in / digest (the label-collection surface) lives in
  UI/M7; F4 **consumes** its labels via the human-anchor set (F4-T0/T4).
