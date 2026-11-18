# Demo — research and daily news

**Status:** runnable on the supported Core Pod. **Scope:** the first-use
research and recurring-news flows. This is a product demo, not a benchmark run;
the frozen corpus is untouched.

## What this proves

- A research block finds sources through the configured search engine, fetches
  them through the allowlisted gateway, and writes a report whose claims cite
  what it read.
- A recurring news cycle reads a local wire or an RSS/Atom feed, separates
  `New` from `Previously reported`, records what it covered, and does not
  present the same item as new on the next run.
- A denied source is a visible failure with an approval request, not a silent
  omission.
- The workbench shows the report, sources, grants, coverage, and the next run
  in one place.

> **State storage.** Use the Core Pod's Podman-managed state volume (the
> installer does). Do **not** bind-mount the state directory on macOS: the
> 2026-11-17 live demo did, and `PRAGMA integrity_check` reported a malformed
> database after two jobs. SQLite locking over a host bind mount is the known
> risk (`ARCHITECTURE.md` §4 persistence rules); a named volume is the
> supported shape.

## 1. Local research (no network)

Put a source in a repository under the mounted repos directory:

```text
~/athanor/repos/local-first-note/
└── sources/
    └── note.md
```

`note.md` is any text with a few facts. Then:

1. **Create the project.** Goal: "Write a brief on local-first software."
   Repository: `~/athanor/repos/local-first-note`. Sources: `note.md`.
2. **Add a Daily Brief cycle** from the project page → New cycle → Daily Brief.
   Leave the schedule off for a manual run.
3. **Start the cycle** from the canvas. The research/fetch blocks receive the
   authoritative file text; no tool call or network is needed.
4. **Read the artifact.** The report should name `note.md` in each substantive
   paragraph and list it under Sources.
5. **Open the workbench.** The latest accepted artifact is there; the coverage
   section is empty until the retain block runs.

## 2. Web research with discovery

Configure the global profile and a search engine in `config.yaml`:

```yaml
network:
  profile: research
  search_engine_url_template: "https://html.duckduckgo.com/html/?q={{.Query}}"
  allow_list: []          # the research profile supplies common sources
```

The search engine host must be on the profile/list; result hosts still need a
grant or a global allow entry.

> **Public engines may block automated requests.** The live 2026-11-17 run
> reached DuckDuckGo's HTML endpoint and got a page with no extractable result
> links (`research_search` completed with `results=0`). The plumbing is fine;
> the engine chose not to serve results. A local **SearXNG** instance (add its
> host to `allow_list`) is the reliable choice. The research block still
> produces an honest "no sources" result, and the trace names the zero-result
> query.

1. Create a research project with a goal such as "What changed in SQLite WAL
   mode this year?" and no repository.
2. Add a Daily Brief cycle. In the canvas inspector, select the `research`
   block and add one query per line under **Search queries**, e.g.
   `SQLite WAL mode changes 2026`.
3. Start the cycle. The engine runs the query, takes the top hits, dedupes them
   with any explicit URLs, and fetches each through the gateway.
4. If a result host is not allowed, the run still completes; the workbench and
   the Inbox show a `network_access` request. Approve it; the next run can fetch
   that host for 30 days. The grant is listed on the workbench and can be
   revoked there.
5. Open the report. Every fetched source is a labeled untrusted block in the
   trace: the model saw it as data, not instructions. The Sources section names
   what was actually read.

### Local wire instead of a feed

For a deterministic, offline news run:

```text
~/athanor/repos/daily-wire/
└── wire/
    ├── day1/
    │   ├── story-001.md
    │   └── story-002.md
    └── day2/
        └── story-005.md   # an update to story-001
```

Create the project with repository `~/athanor/repos/daily-wire`, then add a
**News Report** cycle. The template reads `wire/`; it does not need the network.

1. Run day 1. The report has `# New`, `# Previously reported`, and `# Sources`;
   the synthesize block is checked for the required headings.
2. Replace `wire/` with day 2 and run again. `story-005` is new; `story-001`
   appears only under Previously reported. A repeat under New fails the
   deterministic check and the repair loop can fix it.
3. Run day 3 with no new items. The report says so under New and does not
   repeat old ids there.
4. The workbench coverage section lists the retained ids; the next cycle's
   synthesize block receives them as forbidden under New.

## 3. An RSS/Atom feed

Use the feed URL as a source:

```yaml
network:
  profile: research
  allow_list:
    - example.com
```

Add `https://example.com/feed.xml` to the project sources. The Reader accepts
`application/rss+xml`, `application/atom+xml`, `application/xml`, and
`text/xml`, normalizes the feed to a bounded item list, sanitizes each summary,
and returns `mode=feed`. The news report cites the item ids.

## 4. Scheduling in local time

On the project page → New cycle → News Report, set:

- Schedule: `0 7 * * *`
- Timezone: `America/Los_Angeles` (the form defaults to the server's zone)

The trigger stores UTC and shows the local timezone. The next run is visible on
the project page and the workbench. A DST change moves the local hour, not the
stored instant logic.

## 5. What to inspect in the trace

- `research_search` — one row per query: start, completed, results, added.
- `research_source` — one row per discovered URL.
- `research_fetch` — one row per source: fetched, denied, empty, error.
- `network_access_request` — filed when a denial is actionable.
- `coverage_recorded` — items written by retain.
- The prompt trace shows the `UNTRUSTED DATA` block around each source.

## Live evidence — 2026-11-17

Run on macOS / Podman 6.0.2 / Ollama, Core Pod image built from this tree,
`bonsai-2:9b` + `granite4.2:8b`, `divergence_candidates: 1`.

| Check | Result |
|---|---|
| Core Pod boot + health | pass |
| Local source research (`sources/note.md`) | pass — the accepted brief cited `note.md` in every paragraph, `Sources Used` listed it |
| Research search | query executed; DuckDuckGo HTML returned 0 extractable links; `research_search completed results=0` |
| Hostile results page | `ExtractLinks` returns structure; `Scan` rejects the hostile hit; `Extract` remains fail-closed (`TestExtractLinksSkipsWholePageGate`) |
| State bind mount | **failed integrity** after the live run — use a Podman volume, as the production installer does |

The local-source run is the end-to-end proof that the research path works with a
local model. The search run is an honest engine limitation, not a code failure.

## 6. Honest limitations

- `search_web` requires `network.search_engine_url_template`; without it the
  tool is inert and the research block uses explicit URLs only.
- A search-engine grant does not grant result hosts; each result host needs the
  global allowlist or a project grant.
- Feed summaries are text-only after sanitization; the article body is not
  fetched automatically.
- The retention ledger suppresses ids; it does not judge whether a genuinely
  new story is important.
- Quality is model-dependent; the deterministic checks (headings, ids,
  citations, word caps) are the enforceable contract.
