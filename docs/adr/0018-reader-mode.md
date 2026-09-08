# ADR 0018 — Reader Mode extraction (M4-T6)

**Status:** Accepted · **Date:** 2026-09-07 · **Refs:** ARCHITECTURE §21.5,
§16, §2; ROADMAP M4-T6; ADR-0017 (gateway), ADR-0015 (scanner trust
boundaries)

## Context

M4-T5 (ADR-0017) built the §21.5 gateway: the `Policy` allowlist, the
`Client.Fetch` transport (rate limit, header sanitization, response size
cap, DNS-rebinding guard), and the `network` audit log. Responsibility 6 of
§21.5 — *Reader Mode extraction* (HTTP fetch → readability → sanitize →
markdown) — is M4-T6. The gateway Client returns a raw
`Response{StatusCode, Header, Body, Truncated, URL, Decision}`; T6 adds the
extraction pass on top.

ARCHITECTURE §2 names two packages for the extraction pass:
`github.com/go-shiori/go-readability` (readability) and
`github.com/microcosm-cc/bluemonday` (HTML sanitizer). The project is
deliberately lean — two dependencies before M4 (`mattn/go-sqlite3`,
`gopkg.in/yaml.v3`); adding a dependency is a project decision, not an agent
decision (AGENTS.md §Dependencies). This ADR records that decision, the one
wrinkle it surfaced, and the closed extraction pipeline that replaced the
unstructured "readability → sanitize → markdown" sketch in §21.5.

## Decision

### 1. Depend on the maintained go-readability fork, not the deprecated original

`github.com/go-shiori/go-readability` is deprecated upstream — pkg.go.dev
carries the maintainer's banner: *"Deprecated: use
codeberg.org/readeck/go-readability/v2 instead."* The maintained
continuation is `codeberg.org/readeck/go-readability/v2` (v2.1.2, MIT,
tracks Readability.js v0.6, API-identical `FromReader` / `Article` /
`Parser`, pure Go). We depend on the maintained module. This ADR records the
divergence from ARCHITECTURE §2's literal package name so a future reader
knows it is intentional, not a typo; the §2 table is updated in the close-out
commit. `github.com/microcosm-cc/bluemonday` v1.0.27 (BSD-3-Clause, stable)
is taken as named.

Both are pure Go stdlib packages — no CGO involvement, so the sqlite3 build
and Gates G0–G3 are unaffected. The solver did upgrade two transitive
`golang.org/x/*` modules (`golang.org/x/net` and `golang.org/x/sys`); the
M4-T1 `O_NOFOLLOW` wrappers (the only sanctioned `syscall` importers in
`internal/`) must re-prove Gate G1 — a demonstrated part of this task's
close-out, not an unexamined side effect.

### 2. Fetch stays in the gateway; readability never fetches

go-readability exposes `FromURL`, which performs its own `net/http` fetch.
The gateway is the single outbound door (ADR-0017 §1; Gate G1 rule 6
enforces it structurally). Reader Mode therefore uses **only**
`readability.FromReader(input io.Reader, pageURL)`: `Client.Fetch` produces
the bytes, the reader parses them. `FromURL` is deliberately never called
from this codebase — calling it would open a second egress path that bypasses
the allowlist, the rate limiter, header sanitization, the size cap, and the
`network` audit. The structural test in Gate G1 rule 6 already makes a
regression here a build break for *our* files; this ADR extends the same
### 3. Pipeline: fetch → readability → bluemonday → markdown → injection scan

The §21.5 sketch is expanded into a closed, testable pipeline:

1. **Content-Type gate.** `text/html` or `application/xhtml+xml` → the
   readability pass. `text/plain` → verbatim passthrough (there is no
   markup to extract or sanitize; the injection scan in step 5 still runs).
   Everything else — images, PDFs, `application/*`, and missing/unknown
   Content-Type — is refused with the typed `ErrNotReadable` and audited as
   `reader_mode_rejected`. This mirrors the upstream `FromURL` behavior
   ("URL is not a HTML document") while being explicit and fail-closed.
2. **`readability.FromReader`.** Extracts the main content into an
   `Article`. A parse failure, or an `Article` whose `Node` is nil (no main
   content, e.g. a JS-rendered page) → `ErrNoReadableContent`. Reader Mode
   *never* falls back to returning the raw HTML — the fallback would hand
   untrusted markup to the LLM and defeat the sanitizer.
3. **bluemonday `UGCPolicy().Sanitize(...)`** on the rendered extraction.
   UGCPolicy is the maintained "HTML from untrusted sources that will be
   re-rendered" policy: it strips `script`/`style`/`noscript`/`iframe`/
   `object`/`embed`/`form` elements, all event-handler attributes, and
   non-http(s) URL schemes, and adds `rel="nofollow"` to links. The M4-T6
   acceptance criterion ("zero script content survives sanitization") is
   this allowlist's behavior, pinned by tests — not a bespoke list we
   maintain. "Run bluemonday after any other processing" (its own docs) is
   exactly this pipeline's order.
4. **`renderMarkdown`** (`internal/gateway/markdown.go`). Maps the
   sanitized tree to a deliberately small markdown subset: headings,
   paragraphs, `ul`/`ol` (nested lists at two-space indent), links
   (http(s) only — other schemes render as plain text), fenced code,
   blockquote, `hr`, `br`, tables (one line per row, cells joined by
   ` | `). An `img` renders its `alt` text or nothing; everything else
   renders its children. A third markdown-library dependency is
   deliberately *not* added; the subset is closed and table-tested, and a
   page that needs richer structure than this subset is a Browser Mode
   case (M6), not a reason to grow the dependency budget.
5. **Prompt-injection scan, fail-closed.** The in-tree
   `scanner.PromptInjectionHeuristic` (M4-T3) runs over the **final
   markdown** — the exact byte surface handed to the caller — with the
   ingress pipeline's convention of `MinLength = 1` (scan everything,
   `cmd/athanor/ingress.go`). A non-clean verdict → typed
   `ErrPromptInjection`; no markdown leaves the reader; `reader_mode_rejected`
   is audited. This is ARCHITECTURE §16: *"All retrieved documents pass
   through the Scanner (prompt-injection heuristics…)"*.

   The heuristic is invoked directly rather than through
   `scanner.NewRegistry`, because a Reader Mode scan is a single-scanner
   annotation of untrusted text, not a file-pipeline aggregation. Inventing
   a fourth `PipelineKind` (and the `quarantined_files.pipeline`
   CHECK-constraint migration that ADR-0015 requires per kind) for a path
## Consequences

- The gateway is now the full §21.5 door: fetch (T5) + extract (T6). T7
  wires the tools behind it; T8 assembles the adversarial suite.
- `renderMarkdown` must stay inside `internal/gateway/`. It adds no new
  import pattern, but moving HTML→markdown rendering anywhere else would
  expand the surface the containment story (and Gate G1 rule 6) has to
  reason about. The package doc says so.
- A page served as `text/html` whose readable content is less than the
  readability `CharThresholds` (500) yields `ErrNoReadableContent` — correct:
  Reader Mode is not a file-type sniffer, and it does not guess at a server
  that lies about Content-Type.
- Readability *quality* on real pages remains an empirical unknown (the same
  reason M1-T8 and M3-T7 probes exist). The M4-T6 acceptance corpus is a
  test corpus and the close-out demo walks it; a future quality probe would
  run the §16 research workflow against real allowlisted pages.

## Not in M4-T6

- Tools behind the gateway (`fetch_url`, `search_web`) — M4-T7.
- Relative-URL resolution in markdown links (rendered as plain text today).
- Re-fetch policy when the response was truncated (`Response.Truncated` is
  surfaced on `ReaderResult`; the re-fetch decision is T7's caller).
- Malware byte-scanning of fetched content (ClamAV/YARA): fetched content
  becomes markdown, never executable on the host (M2), and archival scanning
  of a *fetched-documents* store is a future task.
- Browser Mode (M6, ADR-0017 §10).
   that never quarantines a file would be ceremony without a pay-off. The
   registry stays the dispatcher for the file pipelines that aggregate.

### 4. `Reader` is an interface behind the same inversion seam as `Client`

`gateway.Reader` is an interface with
`Extract(ctx, *Response) (*ReaderResult, error)`; `NewReader` returns the
interface. T7's `fetch_url` tool and M6's consumers inject a fake exactly
the way ADR-0017 §1 does for `Client`. `ReaderResult` carries
`{Title, SiteName, Excerpt, Markdown, SourceURL, Mode, Truncated}`.

### 5. `network.reader_mode_default` becomes effective

T5 declared + defaulted the flag `true` with a deferral comment (the
dormant-`Execution`-flags pattern from `f309500`). T6 activates it:
`cmd/athanor/gateway.go` constructs the Reader with
`Enabled: netCfg.ReaderMode()`, and the new `config.Network.ReaderMode()`
resolver applies the `true` default only when the field is unset (the same
resolver pattern as `Execution.MinJudge()`). An explicit `false` disables
extraction: `Extract` returns `ErrReaderDisabled` and the caller (T7)
receives the raw fetched `Response` instead of a markdown draft.

### 6. Audit: two new `network` event names

`reader_mode_applied` (info) and `reader_mode_rejected` (warn) join the
closed `network` category's open event-name set (ADR-0017 §8 forward
references). The applied payload carries `url`, `host`, `decision`,
`status`, `bytes_read`, `truncated`, `mode` (`readability`|`plain`),
`markdown_bytes`, `request_id`; the rejected payload carries `url` and
`reason`. No schema change, no migration.
discipline to the library call we choose to make.