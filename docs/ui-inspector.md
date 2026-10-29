# The read-only inspector

Athanor's GUI exposes what the Core actually records. The inspector is
**read-only**: it can start, pause, cancel, and review work through the normal
surfaces, but it never edits the event log, artifacts, memory, or configuration.

## Pages

| Page | What it shows |
|---|---|
| `/ui/events` | Every event, newest first, with category/level/job/project/text filters and raw JSON. |
| `/ui/jobs/{id}/trace` | One job end to end: task analysis units, model calls, tool actions, evaluation records, artifacts, context/memory events, strategy profile/outcome, and a timeline. |
| `/ui/artifacts` | Every artifact, any status, newest first. |
| `/ui/memory` | Compacted memory, context chunks, the Dormant Index, and embedding counts, with content detail pages. |
| `/ui/daydream` | Daydream passes with their artifacts, corrections, and insights. |
| `/ui/runtime` | Personas, horizon-budget status, alarms, HITL decisions, and database counts. |

The watch page (`/ui/jobs/{id}`) remains the live view: streamed tokens and new
events. Its **Full trace** button opens the persisted record.

## Tokens

Every model call records token counts: prompt, completion, estimated reasoning,
the think arm, the model, the phase, and the budget. These are always visible.

The **exact** prompt messages and visible response are only recorded when
payload capture is enabled:

```yaml
logging:
  capture_llm_payloads: true
```

Default is `false`. The event log normally carries counts, not repository bytes;
enabling capture stores the assembled prompt and response in each `llm_call`
event, so the log grows with the work. Capture applies to **new** calls only —
old events cannot be reconstructed. When it is off, the trace says so rather
than pretending the payload is unavailable.

## Actions and steps

- **Tool actions** come from the `actions` table (tool, request, response, status).
- **Steps** are the event timeline: phase transitions, divergence candidates,
  evaluations, verification, reflections, regulator decisions, context assembly,
  KV pressure, memory injection/compaction, budget grants, corrections, git
  commits, and errors.
- **Live tokens** are streamed to the watch page but are not stored per chunk;
  the final response is stored only with payload capture on.

## Not exposed

- Secrets: persona model names and paths are shown; API keys are not (Athanor
  has no cloud client in 0.0.0).
- Edits: the inspector cannot mutate any of the above. Configuration is applied
  by editing `config.yaml`; routing is an advisory proposal.
- Retention: the inspector shows what SQLite still holds. Pruned or deleted rows
  cannot be shown.
