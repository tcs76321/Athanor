# Deterministic media verifier — design

**Status:** proposed · **Date:** 2026-10-17 · **Refs:**
[1.0.0-direction.md](1.0.0-direction.md) (pillar 7),
[ADR-0070](adr/0070-declared-checks.md) (declared checks),
[ADR-0076](adr/0076-continuous-evidence.md) (no self-grading);
ARCHITECTURE §6.2 (archetypes), §21.3 (airlock), §25 (tools); Gate G1.

## Scope

`media` is the last archetype whose acceptance is judge-advisory. The 1.0.0 goal
is a **deterministic structural verifier**, not a quality judge: it proves the
artifact is the *shape* the task asked for. Semantic quality (composition,
correctness of the depicted content, "is this a good trailer") stays
judge-advisory and human-anchored.

## Task contract

A media task declares `media_checks` in its execution config, for example:

```yaml
media_checks:
  kind: image            # image | audio | video
  format: png
  max_bytes: 2000000
  exact_width: 1024
  exact_height: 1024
  no_alpha: true
  required_metadata: {title: "..."}   # optional
```

The verifier accepts only when every declared property matches. A media task
with no `media_checks` is **not** verifier-gated (documented, judge-advisory).

## Deterministic properties

| Kind | Deterministic checks |
|---|---|
| **Image** | magic bytes / container format; decodes; width/height (exact or max); color model (RGB/RGBA/gray); alpha presence; DPI; animation frame count; byte size; exact hash or perceptual-hash distance vs a reference |
| **Audio** | container/codec; sample rate; channels; duration (± tolerance); bitrate; integrated loudness (LUFS) range; silence/clipping thresholds |
| **Video** | container/codec; resolution; frame rate; duration; audio-track presence; keyframe interval; byte size |
| **Metadata** | required tags/captions present; no external references; no embedded scripts/executables |
| **Safety** | the artifact passes the airlock/scanner path; no executable payloads |

**Not deterministic** (advisory only): aesthetics, semantic correctness,
"matches the brief," perceptual quality scores.

## Tooling and trust boundary

- The **Core** must verify the artifact bytes; a **Job Pod is untrusted**, so a
  pod-reported "pass" cannot be the ground truth.
- Gate G1 forbids `os/exec` in `internal/`. Options:
  1. **Pure Go** — stdlib `image` (PNG/JPEG/GIF) covers images with no new
     dependency; WebP needs `golang.org/x/image` (a project dependency
     decision); audio/video metadata needs a pure-Go parser (another dependency).
  2. **Named `cmd/` adapter** — shell out to `ffprobe` in a Gate-G1-allowlisted
     file (like the git/podman adapters). Requires `ffprobe` in the Core Pod
     image (a packaging change).
- **Recommendation:** ship **image** support first (stdlib, no dependency) and
  make audio/video return an explicit **unsupported** result — never a silent
  pass — until the ffprobe/pure-Go decision is made. This keeps the verifier
  honest and unblocks the common media task (generated images).

## Acceptance

- A media artifact with a wrong format, dimension, duration, or missing required
  metadata is **rejected without the judge**.
- A conforming artifact is accepted; the semantic layer is advisory and
  documented.
- An unsupported media kind is reported as unsupported, not passed.
- The artifact goes through the airlock/scanner path.
- Tests: fixture pass/fail per property; no network; deterministic repeat.

## Open questions

- ffprobe adapter (Core Pod image change) vs a pure-Go parser dependency?
- Which media tasks are in the benchmark's media stratum, and what do they
  declare?
- Is perceptual-hash-to-reference a useful check, or a false comfort?
- Does media generation need a dedicated Job Pod image (ffmpeg, codecs)?
