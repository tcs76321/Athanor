# Human-anchor eval set (F4-T0c)

A small, curated corpus of artifacts with **human-quality ratings**, used to
calibrate the judgment layer and to check whether the engine's own ordering is
even right.

## Why

The M3-T7 probe found the engine rubric ranking artifacts *against* the grain
and the third-party judges saturating at 1.0 — i.e. no trustworthy quality
signal. A learned verifier (F4-T3/T4) and any RLVR loop need an **anchor**: a
set of examples where a *human* has said what "good" means. This is that anchor.

The durable asset is the ratings, not any model. When base models churn, this
corpus stays and re-grounds the verifier.

## Files

- `cases.md` — the artifacts, one section each, with the goal + acceptance
  criteria. Deliberately **blind**: it does not say which arm or model produced
  the artifact, so a rating is not biased by provenance.
- `ratings.csv` — the ratings. Columns:
  `case_id,rater,rating,criteria_met,notes`.
  - `rating`: 1–5 (1 = fails the criteria, 5 = fully meets with no defect).
  - `rater`: `agent` (provisional, machine) or a human name.
  - **Agent rows are provisional** — one reader with reasons, to be corrected by
    a human. They are a starting point, not ground truth.

## Protocol

1. Read a case's goal + criteria, then the artifact.
2. Decide `criteria_met` (Y/N) and a 1–5 rating with a one-line reason.
3. Add a row to `ratings.csv`. Do not look at provenance before rating.

## How it is used

- F4-T4 calibrates judges against these ratings and rejects a judge whose
  verdicts contradict the anchor.
- The probe's head-to-head is only meaningful once the engine's ordering agrees
  with the anchor above a threshold (Gate G-F4).
