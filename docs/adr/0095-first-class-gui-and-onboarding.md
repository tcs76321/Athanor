# ADR 0095 — First-class GUI, two-level onboarding, and the UI asset boundary

**Status:** Accepted — first implementation slice · **Date:** 2026-11-04 ·
**Refs:** ARCHITECTURE §20.3, §21.8, §27; [ADR-0039](0039-local-web-ui.md),
[ADR-0057](0057-cross-site-request-defense.md), [ADR-0078](0078-core-pod-only-runtime.md),
[ADR-0094](0094-project-discovery-portfolio.md).

## Context

The local UI was a functional operator console: a top navigation of internal
surfaces (Corrections, Setup, Inspect), a statistics-first dashboard, raw
Markdown in `<pre>`, a raw event log as the watch view, and inline CSS/JS with
no CSP. The first-run experience was `/ui/setup` (a model table) and the CLI;
the project interview was optional, static, and deterministic. Real use showed
the product surface lagging the engine: a "make money online" goal became one
document job, not a discovery portfolio.

## Decision

1. **The GUI is a first-class product surface.** The information architecture
   is project-centric: a persistent rail with Home, Inbox, Projects, Library,
   Insights, and System. Power surfaces remain reachable — through the rail
   groups, the sitemap, and global search — but are not in the primary path.
2. **Two-level onboarding.**
   - **Machine onboarding** runs once per computer: `/ui/welcome` runs the
     doctor checks in-process (Podman, Git, Ollama, models, memory, disk,
     state, Job Pod image, context floors, network, power), detects host
     RAM/CPU/disk and proposes editable Job Pod limits, lists installed
     models, self-selects a model per persona, pulls missing ones only with
     explicit consent, offers the bounded qualification, records preferences,
     and hands off to the first project. Completion is durable in
     `system_state`; skip and re-run are explicit.
   - **Project onboarding** runs per project: a resumable brief (goal, limits,
     sources, success), discovery for exploratory goals, selection, a 30-day
     plan, and approval. It complements — does not delete — the deterministic
     interview.
3. **The GUI performs first-run diagnostics and resource planning.** The
   environment step calls `internal/doctor` through a `cmd/` probe adapter; it
   does not ask the operator to run a CLI command. A dedicated resources step
   shows detected RAM/CPU/disk and prefills Job Pod memory/CPU/tmpfs/PID limits
   from host capacity, preserving values already in config. Applying the
   assignment or the limits writes the config atomically with a timestamped
   backup and a validation pass; the running registry keeps the old values
   until restart, which the UI states explicitly. The restart flag is persisted
   in `system_state` and shown on every page until boot clears it. In the Core
   Pod the config is mounted read-write so the write lands on the host; host
   `serve` writes the host file directly.
4. **Athanor self-selects persona models.** The Models step explains the five
   roles, lists installed models, and shows a deterministic assignment
   (largest for hard reasoning, smallest for ingestion, a different family for
   the judge and alternative). The operator reviews it and can apply it; the
   current persona→model table is no longer the primary content of the step.
5. **Assets are embedded and the CSP is strict.** CSS, JS, and the vendored
   HTMX runtime live under `internal/ui/assets/` and are served from
   `/ui/assets/*`. Inline scripts and styles are removed; pages carry
   `default-src 'self'`, `frame-ancestors 'none'`, and no remote fonts/CDNs.
   HTMX is a static asset, not a Go module, and is the only front-end
   dependency approved for this pass.
6. **Monokai is the base theme, with a warm editorial light alternative.** The
   theme is a process-wide preference stored in `system_state`, applied
   server-side (`data-theme`) so first paint is correct without an inline
   script. Settings offers Monokai, Ember Paper, Monokai Light, Nord,
   Solarized Light, and High Contrast.
7. **Artifacts and plans are readable.** A small, safe Markdown renderer in
   `internal/ui/markdown.go` supports the closed subset Athanor emits and
   escapes all source HTML. Raw code artifacts remain `<pre>`.
8. **Search is the universal escape hatch.** Projects, artifacts, jobs, events,
   and UI surfaces are searchable from `/ui/search` and the command palette;
   the sitemap lists every surface. Nothing is hidden.
9. **The UI is responsive for large displays, not mobile-first.** The layout
   targets 1280–3840 px with a 240–300 px rail, a 1680–1900 px content ceiling,
   fluid type, and URL-addressable state so multiple windows are independent.

## Consequences

- The first run is a guided experience, not a settings page.
- Project creation has a real beginning (brief → discovery → choice → plan),
  and "explore" is a first-class mode without a new archetype.
- The UI has a testable security boundary: no inline script/style, a strict
  CSP, and an embedded asset filesystem.
- HTMX is vendored; its version and license must be kept with the asset.
- The current markdown renderer is deliberately small; extending it is a
  feature, not a dependency decision.
- Visual polish remains iterative; this ADR records the structure and the
  theme system, not a frozen pixel design.

## Alternatives rejected

- **A SPA/framework.** It violates the no-new-runtime-dependency posture and
  the no-JS fallback; server-rendered forms plus HTMX cover the interactions.
- **Keep inline CSS/JS with `unsafe-inline`.** It blocks a useful CSP and makes
  the design system impossible to theme consistently.
- **Keep the top-nav operator console.** It exposes internals before the
  operator has done any work.
- **Force every project through discovery.** A concrete code or document goal
  should go straight to the interview/plan; discovery is for exploratory goals.
