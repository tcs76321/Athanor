# Agent rating of the human-anchor set — frontier cloud model

**Rater:** **DeepSeek V4.1 Flash in OpenCode** (frontier cloud model, via the
OpenCode agent harness) · **Date:** 2026-10-06 · **Tier:** machine / agent
(provisional), **not** a human rating.

This is an *agent version of the human evaluation*: the same procedure the
[anchor protocol](README.md) defines for a human — read a case's goal and
acceptance criteria, judge the artifact blind to provenance, rate 1–5 with a
reason — performed by a frontier cloud model instead of a person. Its purpose
is an upper-bound, high-quality *machine* rater to compare against the local
models in the G-F4 probe ([closeout](../../docs/probes/gf4-closeout.md)); it does **not**
replace the human anchor.

Machine-readable form:
[`eval/anchor/ratings-deepseek-v4.1-flash-opencode.csv`](ratings-deepseek-v4.1-flash-opencode.csv).
The canonical anchor (`ratings.csv`) is **unchanged** — this rating is recorded
separately so the human-anchor semantics are preserved.

## Ratings (blind, 1–5)

| case | rating | criteria met | reason |
|---|---|---|---|
| `md-d1` | 3 | Y | three sections in order and a one-line description; H1 is the raw run id |
| `md-d2` | 2 | Y | sections present but License first; absurd package name; trailing LIMITATION |
| `md-d3` | 5 | Y | clean title and description; correct order; sane install |
| `md-s1` | 3 | Y | sections correct and no LIMITATION; H1 and install name are the raw run id |
| `sa-d1` | 4 | Y | concrete modules; two real, distinct risks |
| `sa-d2` | 3 | Y | inventive metaphor but off-brief; sections and two risks present; LIMITATION |
| `sa-d3` | 2 | Y | self-referential (the brief document is a "part"); meta LIMITATION |
| `sa-s1` | 5 | Y | professional module names; concrete risks; fully meets |
| `essay-e1` | 5 | Y | exactly three paragraphs; distinct reasons; one-sentence conclusion |
| `essay-e2` | 1 | N | one paragraph; not three; reasons are a list rather than paragraphs |
| `brief-d1` | 5 | Y | concrete Parts/Steps/Risks; two named risks |
| `brief-d2` | 2 | N | sections present but content vague; no risks actually named |
| `code-fib-pass` | 5 | Y | test passes; docstring present; usage example absent (minor) |
| `code-fib-fail` | 1 | N | wrong result (`n-1`); no docstring; test fails |
| `code-todo-pass` | 5 | Y | correct; docstrings on every public method |
| `code-todo-fail` | 1 | N | `complete` is a no-op; no docstrings; test fails |

## Agreement with the existing anchor

Compared against the current `eval/anchor/ratings.csv` (the agent-provisional
text/document ratings and the objective code labels):

| subset | n | Spearman | Kendall τ-b |
|---|---|---|---|
| all cases | 16 | **0.984** | 0.970 |
| code (objective) | 4 | **1.000** | 1.000 |
| text/document | 12 | 0.982 | 0.964 |

**15 of 16 ratings are identical.** The single divergence is `md-d3`: this
rater gives **5** where the existing machine row gives **4** — on review, the
artifact is clean and fully meets the criteria (correct title, one-line
description, ordered sections, sane install), so the existing row reads as
slightly harsh. One `criteria_met` disagreement: `brief-d2` is marked **N**
here (no risks are actually named) where the existing row marks Y; the numeric
rating (2) agrees either way.

## Comparison to the local probe judges

In the 10-model probe, the local judges scored the same `eval/anchor` set at
Spearman **0.57–0.81** (best: `granite4.2:3b` 0.81; `ornith-1.5:35b` 0.80).
This frontier rater reaches **0.984** — far above the local tier.

That gap is informative but must be read correctly: it is **machine vs
machine**, so it says the frontier model reproduces the existing
machine-provisional anchor almost exactly, whereas the local 3–35 B models do
not. It does **not** establish agreement with a human.

## Interpretation and limits

- The frontier rating **largely confirms the existing machine tier** — the
  anchor's provisional ratings are self-consistent and reproducible by a strong
  model, with one debatable `md-d3` point.
- It is **not independent ground truth**. A machine agreement of 0.98 to a
  machine-authored anchor is expected and does not close the Gate G-F4
  "human-anchor agreement" arm.
- The durable teacher remains the **human** rating (P4). This document is a
  reference point for *how far a strong cloud model is from the local models*,
  not a replacement for the person.
