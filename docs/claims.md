# Load-bearing claims → evidence

**Status:** living · **Reviewed at:** every tag
**Purpose:** keep the claims a reader relies on traceable to a test, gate, or
demo. A claim without a proof is either removed or marked as a target.

| Claim | Where stated | Proof | Status |
|---|---|---|---|
| No tool execution in `internal/`; no raw `syscall` outside the path library | README, ARCHITECTURE §25 | `internal/gate/gate_test.go` (Gate G1) | proven |
| Every internal route is auth-wrapped; every tool handler consults the envelope | ARCHITECTURE §3.2, §25 | `internal/gate/gate_g2_test.go` | proven |
| Job Pods are rootless, read-only-rootfs, `--network=none`, token-only mount | ARCHITECTURE §21.2 | `internal/jobpod/args_common.go`, `args_linux.go`, argv gate, behavioral probes | proven (pod hardening); token-only mount is the current truth |
| Gateway is default-deny, allowlist-only, pinned-IP, redirect-bounded | ARCHITECTURE §21.5 | `internal/gateway` corpus + integration probes | proven |
| Path containment rejects traversal/absolute/NULL and opens with `O_NOFOLLOW` (final component) | README, ARCHITECTURE §21.3 | `internal/airlock/paths` tests + fuzz | proven |
| Event log is append-only | ARCHITECTURE §28 | `events_no_update`/`events_no_delete` triggers; `internal/store` | proven |
| State is persisted at every job transition; crash recovery resumes | ARCHITECTURE §8.2, §23.6 | `internal/engine` transition/recovery tests; demo-m1 | proven |
| Evaluation is deterministic-first; the LLM judge is advisory | ARCHITECTURE §19.3 | `internal/verify`, `internal/engine/decide.go`, F4 closeout | proven for code; text/document depend on declared checks |
| Compaction is Temperature 0.0 and content-addressed | ARCHITECTURE §10.3 | `internal/mce/compact.go` + determinism tests | proven |
| Context floors are never silently reduced | ARCHITECTURE §12.3 | `internal/llm/feasibility.go`, KV pressure tests | proven |
| The model cannot invoke tools; the engine executes them | ARCHITECTURE §25, ADR-0080 | `internal/prompt/render_test.go`; no tool-call parser | proven |
| A code project's tasks carry the execution tools | ARCHITECTURE §25, ADR-0081 | `internal/project/project_test.go` | proven |
| The Core Pod is the only supported runtime | README, ADR-0078 | `install.sh` / `athanor start` target the container; `backup`/`restore -container` and host `doctor`/`models` are wired | **proven for install/start/backup/restore**; fresh-install demo and a Core Pod job remain |
| A repository working set reaches Job Pods | README coding workflow | staged snapshot at `/workspace/repo`; `/tmp/work` overlay; verified by a Core Pod `pytest` asserting the mount (ADR-0082) | **proven** |
| The frozen benchmark is the controlled study | AGENTS, ADR-0075 | v2 corpus exists; full run pending | **partial — smoke only** |

## Rule

When a code or behavior change falsifies a row, update the row in the same
commit (or remove the claim). Do not let a "target" row silently read as
"proven".
