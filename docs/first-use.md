# First use — 0.0.0 UX, workflow, and first-real-use runbook

**Status:** specification (living) · **Date:** 2026-10-17 · **Refs:**
[ADR-0075](adr/0075-frozen-corpus-compass.md) (frozen-corpus soak),
[ADR-0076](adr/0076-continuous-evidence.md) (continuous evidence),
[ADR-0077](adr/0077-two-day-cycle.md) (two-day cycle),
[ADR-0069](adr/0069-core-pod-packaging.md) (Core Pod); `docs/deploy-core-pod.md`.

Legend: ✅ exists today · 🔨 to build · ⚠️ decision/gap · `[UNKNOWN]` not verified.

**GUI:** the 0.0.0 bar is a **functional** first-use flow; **first-class GUI is
the main 1.0.0 focus** ([1.0.0-direction.md](1.0.0-direction.md)).

---

## Part A — The user journey (abstract, user-facing)

### Day 1 — discover, install, set up, start work

1. **Discover** the project; the README convinces the user to try it.
2. **Prerequisites** are clear and self-serve (Ollama + models, rootless Podman /
   podman machine, Job Pod image, AC power, single-model residency env).
3. **`curl` install** does everything it can:
   - checks prerequisites, using `athanor doctor` as the check engine;
   - installs the binary and (target) the Core Pod image;
   - offers to pull the persona models (with sizes; never silently downloads
     tens of GB);
   - writes a default config;
   - prints a summary and **opens the browser** to the UI (or prints the URL on
     a headless/SSH host).
   - When something fails, it prints the exact remediation (doctor's job).
4. **UI onboarding** (short, skippable):
   - optional display name (⚠️ cosmetic; likely unnecessary);
   - working-hours / "don't interrupt me" window — **inferred** from AC/idle and
     proposed once, not interrogated (⚠️);
   - any settings that cannot be defaulted.
   - model inventory + **qualification ladder** (🔨 shipped as an onboarding
     step; today `athanor models` is inventory + advisory only).
5. **First project** (still tutorial-mode: tooltips, stateful, dismissible):
   - **Project → goals → tasks.**
   - **Project initialisation** (🔨): a resumable clarifying interview, then a
     **plan/brief artifact the user approves**. It can take ~10 minutes and is
     skippable. It is the first human eval.
6. **Work begins** under the approved plan (single-slot: one job at a time).
7. The user watches, steps away, comes back, **asks for a presentation**, and
   reviews (human eval). The presentation is a distinct surface (🔨): what was
   built, how to run it, known-broken, and what it cost.

### Night 1 — dream on the human evals, test hypotheses

8. The dreaming pass analyses the day's human evals and results, forms
   **hypotheses**, and runs **bounded shadow experiments** to test them
   ([ADR-0077](adr/0077-two-day-cycle.md)).
   - Shadow/draft only; never accept, commit, push, or mutate accepted state.
   - Budgeted and preemptible; never on the frozen corpus's held-out slice.
   - Anything external/irreversible (push, new-site access, applying routing)
     is **queued for the morning**, never attempted overnight (HITL-free night).
9. It prepares the day-2 package: builds/takes, a must-fix bug list, "I saw X —
   what do you think of Y?", and conclusions with samples + effect.

### Day 2 — review, confirm, prioritise

10. The **morning meeting** shows the package and asks for decisions.
11. The user reviews/responds, **confirms tested project-scoped conclusions**,
    and **approves major adjustments** that were tested. Global-policy and
    untested changes are proposed, not approved (two-axis rule, ADR-0077).
12. The **digest becomes the approval ledger**: hypothesis → experiment →
    samples → result → decision → provenance.
13. **The first full cycle ends on day 2.**

### Day 2+ and nights 2+ — the repeatable loop

14. Work continues under the confirmed adjustments; new human evals accumulate.
15. **Night 2 digests the day-2 human eval in depth**, forms the next
    hypotheses, and tests them; the morning confirms. The loop repeats and the
    system improves.
16. **Multi-project prioritisation** (🔨): how much machine time and review time
    each project gets. Machine time is sequential (single-slot); the question is
    allocation, not parallelism.

---

## Part B — Current state vs. to build

| Step | Today | To build |
|---|---|---|
| Runtime | host binary **and** Core Pod image (two runtimes) | **Core Pod only** ([ADR-0078](adr/0078-core-pod-only-runtime.md)); host binary = client/dev |
| Discover/install | `make build`, `scripts/install.sh [--service]` | `curl` installer with checksums/signing; Core Pod pulled; browser open |
| Prereq checks | `athanor doctor` (⚠️ macOS `podman machine` reports `Rootless=false` → false FAIL) | fix the macOS check; installer uses doctor |
| Onboarding | `athanor init`, `athanor models` (inventory + advisory routing) | UI wizard; **qualification ladder run**; minimal prefs; optional name |
| First project | `project create` (starts the first goal), `goal submit` (adds another) | project interview → plan artifact → approve; tutorial mode |
| Work/watch | `job watch`, `/ui`, job watch page, SSE | presentation surface; notifications |
| Human eval | accept/reject, `reject` corrections, `/ui/statistics` | review queue (sampled, blinded) + ratings store |
| Overnight | daydream (5 fixed actions), backups, alarms, strategy mining | hypothesis-driven shadow experiments; budgets; HITL-free queue; research via allowlist; new-site requests |
| Morning | `athanor digest` (computed summary) | persistent, interactive digest = approval ledger |
| Improvement | strategy outcomes, technique guidance, qualification | experiment records; two-axis approval; confirmed policy |
| Soak | probe over `bin/athanor` (host) | run against the **Core Pod**; frozen corpus per major version |
| Multi-project | `task.priority`; no project budget | project weight/budget (machine + review time) |
| Issues | failed checks / evaluation records | first-class "must-fix bugs" list, operator-confirmed |
| Web | gateway default-deny + allowlist; research tools | "request access to a new site" HITL type; research pipeline |

---

## Part C — First real use runbook (Core Pod only)

This is the operational path for the operator's beta use. **The Core Pod is the
only supported runtime** ([ADR-0078](adr/0078-core-pod-only-runtime.md)); the
host binary is a client and a development/testing tool. The host `serve`/`go
run` path is an unsupported development escape hatch and is never measured.

### C1. Preflight (host)

- Ollama running; models pulled (persona models).
- Rootless Podman / `podman machine` running; `podman run --rm alpine:3.20 true` works.
- `OLLAMA_MAX_LOADED_MODELS=1`, `OLLAMA_NUM_PARALLEL=1` set on the **server**.
- AC power; a few GB free.
- The installer resolves the macOS `podman machine` socket through
  `podman machine ssh`; no host state/workspace path is bind-mounted (named
  volumes only, so SQLite/WAL never crosses virtiofs).

### C2. Build and install

```bash
make build                 # host CLI (client + development commands)
sh scripts/install.sh      # builds the Job Pod + Core images if missing,
                           # writes ~/.config/athanor/config.yaml, starts the
                           # Core Pod, and waits for /healthz
```

`install.sh --service` also writes a systemd/launchd unit that supervises the
container. `make jobpod-image` / `make core-image` remain available for a
manual build; `--no-build` makes the installer require the images.

### C3. Config for the Core Pod

The installer writes the first config with `athanor init -core`:

```yaml
inference:
  ollama_url: "http://host.containers.internal:11434"   # Core Pod → host Ollama
job_pod:
  image: "athanor-jobpod:latest"
  token_host_dir: "/Users/you/.config/athanor/tokens"    # host-visible token bind
```

Edit personas, `limits` (single-slot defaults), `power`, `backup`, `hitl`,
`execution`, `network` as needed. `athanor doctor` reports the runtime shape and
warns on a mismatched `ollama_url` (M9-T7).

### C4. Run the Core Pod

`sh scripts/install.sh` starts it. Re-running `athanor start -config
~/.config/athanor/config.yaml` is idempotent (it starts the existing container
and waits for health). Verify:

```bash
curl -s http://127.0.0.1:7420/healthz
podman exec athanor-core athanor doctor
podman ps -a | grep athanor-job-          # zero after work settles
```

### C5. Model onboarding (today)

```bash
athanor models                             # inventory + advisory routing
# qualification data (manual today):
make probe-measure PROBE_OUT=<results tree>   # writes measurements.json
athanor models -qualification <...>/measurements.json
# apply the proposal by editing config.yaml (the human is the gate)
```

⚠️ The shipped qualification ladder (run on *this* machine's models, bounded,
resumable) is a to-build item; today this is manual.

### C6. First project

```bash
athanor project create -name first -archetype code \
  -goal "..." -test-command "pytest -q"   # starts the first job; prints its id
athanor job watch -job <JOB_ID>
athanor goal submit -project <ID> -goal "..." -criteria "..."   # optional follow-up
```

`project create`'s `-goal` is the first goal and it is enqueued immediately; the
command prints the job id to watch. The UI project interview (to build) replaces
this flow.

> **Code projects need tools.** The shipped daemon default is
> `job_pod.default_tools: []`. `project create` and `goal submit` now give a
> `code` task the three execution tools (`execute_code`, `run_tests`, `lint`)
> as a per-task default (M9-T4); other archetypes stay tool-free and explicit.
> Without those tools the real test command cannot run and the code acceptance
> gate rejects every candidate.

### C7. Daily review (today)

- `athanor digest -hours 12` — jobs/goals/artifacts, pending approvals, alarms,
  tokens, daydream stats, failures.
- UI: `/ui` dashboard, `/ui/projects/{id}` (artifacts, diff, reject/correct),
  `/ui/jobs/{id}` (watch, pause/resume/cancel/retry, notes).
- `athanor hitl list` + `approve|reject|defer`.
- `athanor strategy insights|mine|promote|mute`.
- Record your review as a `reject` correction when something is wrong; there is
  no review queue/rating UI yet (to build).

### C8. Overnight (today vs. target)

- Today: keep the daemon running on AC; the power supervisor enables
  daydreaming on idle; scheduled backups fire; alarms are monitored.
- Target: hypothesis-driven shadow experiments, bounded, preemptible, HITL-free;
  the day-2 package is prepared automatically.

### C9. Backup / stop

```bash
athanor backup -keep 5                     # snapshots under <state-dir>/backups
athanor freeze                             # optional
# stop the daemon (SIGINT/SIGTERM) or `podman stop <core>`
# restore (daemon stopped): athanor restore -from <snap>.db -force
```

---

## Part D — Implementation plan (dependency-ordered)

Two tracks; the first makes beta usable, the second makes it learn.

### Track 1 — Use it (beta, now)

1. **Core Pod is the only runtime** — installer/`start` build/pull and run the
   container; split client vs. host-inspection commands; make the soak and probe
   target the Core Pod (state-dir read from a mounted volume); keep host
   `go test`/`go run` as the dev escape hatch only ([ADR-0078](adr/0078-core-pod-only-runtime.md)).
2. **Doctor macOS fix** — a running podman machine must not be a false FAIL.
3. **Onboarding (minimal)** — inferred quiet hours, optional name; a shipped
   **qualification ladder** command that enumerates installed models, runs a
   bounded grounded qualification, and proposes routing (apply = config edit).
4. **Project interview + plan artifact** — resumable clarifying questions, a
   plan the operator approves; a `clarification` HITL type.
5. **Presentation surface** — "what was built / how to run / known-broken /
   cost", with the signal to present.
6. **Notifications** — pending HITL, completion, critical alarm.
7. ~~**`project create` semantics** — decide: enqueue the first goal, or stop
   taking one (remove the unrun-task quirk).~~ **Done (M9-T5):** the initial
   goal is enqueued immediately; `start: false` creates metadata only.
8. **Task/DAG inspection CLI** — a read-only `athanor tasks` over `GET /projects/{id}/tasks`.

### Track 2 — Learn from it

9. **Evidence reducer over a state dir** (keystone) — same report/leaderboard
   path for real use and soaks (`docs/evidence-pipeline.md`).
10. **Derived labels** — tests, downstream use (commit/export/edit/re-run/
    abandon), implicit actions; the judge never a label.
11. **Review queue + ratings store** — sampled, blinded; the human anchor.
12. **Experiment object + shadow runner** — hypothesis → spec → samples →
    result → proposal; budgets; preemption; never held-out.
13. **Persistent digest = approval ledger** — items, decisions, provenance.
14. **Two-axis approval** — tested × scope; UI/CLI for confirm/defer/reject.
15. **Project budgets/priority** — machine time + review time, with defaults.
16. **Issues surface** — operator-confirmed must-fix bugs.
17. **Web access requests** — a HITL type for adding a site; research through
    the gateway allowlist + airlock/scanners.
18. **Frozen benchmark v2** — 41 hard H/X deterministic tasks (no E/M) + 4
    memory + 6 human, 10 det held-out + 3+3, 12 H anchor, protocol/digest
    pinned; v1 pilot retained.

### Track 3 — Package the first use

19. `curl` installer with checksum/signature verification, doctor-driven checks,
    optional model pulls, browser open.
20. Fresh-install demo (<15 min) on Linux and macOS; the same runbook as Part C.

**Ordering:** 1 → 2 → 9 → 10 → 12 → 13 → 11/14 → 3 → 4 → 5/6 → 15/16/17 →
18 → 19/20. The evidence reducer (9) and experiment object (12) gate the loop;
the onboarding/project/presentation work gates first use.

---

## Part E — Open decisions

- Display name: keep, optional, or drop?
- Quiet hours: infer from idle (recommended), ask once, or both?
- Project interview length: fixed question count vs. "until the plan is
  confident"; hard cap?
- Presentation trigger: explicit button, job completion, or a schedule?
- Experiment budget: fixed nightly envelope vs. adaptive?
- Global-policy graduation: how many projects / what held-out evidence?
- Core Pod on macOS: socket path and volume strategy `[UNKNOWN]` — verify.
