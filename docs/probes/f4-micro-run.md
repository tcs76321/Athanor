# F4 — micro-run results

The F4 close-out re-runs the M3-T7 micro set (`make probe-micro`: 3 goals ×
both arms) after the measurement repair and the verification changes. Three
runs were captured: run 1 (pre-correction), run 2 (first correction), run 3
(final correction).

Environment: Apple M2 Max, 32 GB; Ollama 0.35.1; generator `ornith-1.5:9b`
(`e5df7dcdd8a2`); judges `gemma4:12b-mlx`, `granite4.2:3b`. The micro config
maps every persona to the generator, so `generator_family == judge_family`
(`ornith-1.5`) — see finding 4.

## Outcomes across runs

| run | code change | essay (dialectical) | md2html (dialectical) | todo-list (dialectical) | single |
|---|---|---|---|---|---|
| 1 | pre-correction | fail 0/3 | 1.00 3/3 | fail 0/3 | essay fail; md2html 1.00; todo fail |
| 2 | advisory split + raw-module prompt | fail 0/3 | 1.00 3/3 | fail 0/3 | unchanged |
| 3 | **fence unwrap + advisory prompt** | fail 0/3 (run 3 scored 1.00 but declined) | 1.00 3/3 | **2/3 completed 1.00** | essay fail; md2html 1.00; **todo 1.00** |

Diversity (dialectical mean pairwise Jaccard) stayed **0.65–0.89** across all
runs — far above the 0.30 floor. Zero orphan pods after every arm.

## Findings

1. **The instrument is repaired for code.** In runs 1–2, `todo-list` failed
   every real test with `ModuleNotFoundError`: the model emitted the module
   inside a ` ```python ` fence, which the pod imported as invalid Python. Run
   3 unwraps the fence (`normalizeCode`) and the code arm completes (2/3
   dialectical, 1/1 single, score 1.00). Before F4 the same goal scored 1.00
   against `test_command: "true"` — the signal was entirely masked. This is
   the measurement repair working.
2. **A prompt instruction is not enough; the harness must normalize.** The
   `codeOnlyInstruction` (run 2) was ignored by the 9B model. The durable fix
   is to unwrap a fenced block before the pod runs it.
3. **The LLM judge remains the weak link.** `md2html-readme` saturates at
   1.00 (the judge cannot rank READMEs), and the essay's run-3 failure was
   legitimate — the artifact had five paragraphs against an explicit
   "exactly three paragraphs" criterion, and the judge declined with clear
   reasons. So text results are now correctly attributed, but the judge still
   cannot *discriminate*, consistent with the [anchor
   calibration](f4-anchor-calibration.md) (Spearman 0.00).
4. **The micro config is single-family.** Every persona is `ornith-1.5`, so
   `cross_family_ok: false`; under `judge_mode: verifier` the cross-family
   gate would decline comparisons. A verifier-mode probe needs a judge from a
   different family (e.g. `gemma4`) configured on the `security` persona.
5. **The probe runs the LLM judge path by default** (`judge_mode: llm`). The
   deterministic path is implemented and unit-tested, but this run did not
   exercise it end-to-end; enabling it is the next measurement.

## Interpretation

F4 does not make the broken LLM-judge signal good. It makes the system **not
depend** on it: code acceptance now has a real, objective test signal, and
the verification-first path ([ADR-0045](../adr/0045-verification-first-selection.md))
plus the reward-hacking guard ([ADR-0046](../adr/0046-judge-protocol.md))
bound the damage a saturated judge can do. The headline experiment (does
N-candidate divergence beat single-shot?) remains **unresolved**: on this
micro set the arms tie (both complete the code and document goals; both fail
the essay), for ~1.6–2.4× the tokens on the dialectical arm.

## Gate G-F4

| Arm | Status |
|---|---|
| Instrument trustworthy — real code tests | ✅ (todo-list discriminates; fence-normalized) |
| Instrument trustworthy — judge agreement with the anchor | ❌ **0.00** (saturation) |
| Compute scales with difficulty | ✅ unit-tested (`Adaptive`; easy → N=1) |
| Selection verifiable (deterministic verifiers, cross-family) | ✅ implemented/unit-tested; ⏳ not exercised end-to-end in this run |
| Judgment bounded | ✅ (T8) |
| Diversity measured and useful | ✅ 0.65–0.89, above the 0.30 floor |
| The loop learns (insight → persona plan) | ✅ unit-tested + audited |

**Verdict: Gate G-F4 is NOT closed.** The judge-discrimination arm fails on
the human anchor. F4 is code-complete and materially improves the instrument;
closing the gate requires a verifier-mode run with a cross-family judge and a
judge protocol that actually ranks (harder tasks / pairwise comparison).

## Follow-ups

- Run the probe with `execution.policy.judge_mode: verifier` and a
  cross-family `security` model; measure the deterministic-accept fraction.
- Pairwise judging or harder tasks to break the LLM-judge saturation.
- Consider cleaning fenced content at artifact-persistence time, not only at
  pod execution, so accepted code artifacts are fence-free.
