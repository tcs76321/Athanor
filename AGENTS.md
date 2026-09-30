# AGENTS.md

Working agreements for AI coding agents and human contributors. Short on
purpose; details live in `ARCHITECTURE.md`, `ROADMAP.md`, and `DEVELOPMENT.md`.

## Commands

- One tool call per command. No multi-command strings, no compound shell
  expressions, no nested quoting. If two things need checking, make two calls.
- **Never batch commands that depend on each other.** The tool runner runs
  every command in a single `run_commands` call **concurrently**, so anything
  that reads, stages, or builds a file can race a command in the same batch
  that writes it. A write issued alongside a `git add` or a `go test` may land
  *after* the read, silently producing a commit of the pre-write bytes and a
  dirty tree. This happened in M5-T5.6: an `echo >> file` trailing-newline fix
  and the `git add file` meant to capture it were sent together, the commit
  recorded the old bytes, and the tidy had to be committed separately
  (`fdd975e`).
  - Rule: **at most one state-changing command per call.** A file write, an
    `echo >>`, a `git add`, and a `git commit` are all state-changing.
  - Sequence across separate calls: write → verify (`gofmt -l`, `git diff`) →
    stage → `make check` → `git commit`.
  - Independent *reads* (greps, `git status`, `sed -n`, file reads) may be
    batched freely — that is what the parallelism is for.
  - If two state changes must both happen, do them in two calls and confirm
    the second one's result before relying on it.
- Never run bare `go build` or `go test` — use the `make` targets so
  `CGO_ENABLED=1` is set. See `DEVELOPMENT.md`.
- Keep command output short. Pipe through `head`, `tail`, or `grep` when
  inspecting long output; the full result is rarely needed.
- **No multiline input of any kind.** The tool runner's shell cannot
  reliably accept input that spans more than one line. This applies to:
  - Heredocs (`<<EOF`, `<<'EOF'`, `<<-`).
  - `printf` line-stacking.
  - `git commit -m 'a' -m 'b'` (two `-m` flags) — use a single `-m`.
  - `git commit -F -` followed by stdin input.
  - `python3 << 'PYEOF'` and any other `<<` redirect.
  - `cat | tee`, `printf | sh`, or any pipe that supplies more than
    one line of stdin.
  - For multi-line file content, **write the file with the editor
    tool**, not by piping to `cat`, `tee`, or `printf`. If a single
    command genuinely needs more than one line of input, break it
    into separate calls.
- The same ban extends to **interactive programs that read from
  stdin after launch** (`q`, `Ctrl-C`, `cat`, `less`). The agent
  must not send keystrokes to a process the tool runner started;
  if a previous command appears hung, the agent stops, reports the
  state, and waits for the human.

## Git and pagers

- `git log`, `git diff`, `git show`, and similar open a pager that waits
  for `q` and hangs the terminal. Always use `git --no-pager <command>`,
  or set `GIT_PAGER=cat` in the environment, for every history or diff
  inspection.
- Use `git --no-pager log --oneline -n` for compact history.

## Commits

- One commit per logical change. Match the project's existing style:
  `M#-T#: <title>` for roadmap tasks, `chore:`, `docs:`, `ci:`,
  `fix:` for everything else. Lowercase, terse, no trailing period.
- Commit bodies are short — one or two lines, only if they add
  information not in the title. No multi-paragraph essays.
- Update `ROADMAP.md` status table when a milestone-level task lands.

## Work incrementally

- Run `make check` (lint + vet + test-race) after every commit, not at the
  end. If something breaks, find the breaking commit, don't bisect.
- Run `make hooks` once after cloning to install the pre-push gate so CI
  lint/vet failures surface locally before the push lands. Bypass with
  `git push --no-verify` only when you know why the hook is unhappy.
- Re-prove Gate G1 (`CGO_ENABLED=1 go test ./internal/gate/`) after any
  change to `internal/` that touches imports or the engine surface.

## Commits — agent ↔ human handoff

The human signs every commit (GPG). The agent stages the files,
runs `make check`, shows the staged diff, and stops. The human
runs `git commit` (or the agent runs it once on the human's
signal) and types the GPG passphrase in their own terminal. One
commit per logical change; do not batch unrelated work.

**Commit message format — strict:**

- **Exactly one `-m`.** The agent MUST use a single
  `git commit -m '<full message>'` invocation. The second `-m`
  flag, heredoc bodies, `printf | git commit -F -`, and any
  other multiline input are **forbidden** for the same reason
  heredocs are forbidden in §Commands: long or multi-arg
  invocations hang the tool runner's shell (`quote>` prompt,
  swallowed EOF, garbled output) and the agent has no way to
  recover the prompt.
- **The full message fits on one line.** Title + body, in one
  line, in one set of single quotes. If the body is needed it
  goes inside the same single-quoted string, separated from the
  title by ` — ` (em-dash, two spaces) or by a single space;
  no embedded newlines.
  - Good: `git commit -m 'M4-T1: path containment library + adversarial-corpus tests — paths under internal/airlock/paths, O_NOFOLLOW via gated syscall files (Gate G1 rule 5).'`
  - Bad: `git commit -m 'title' -m 'body line 1\nline 2'` (two `-m`s, multiline).
  - Bad: `git commit -F -` followed by a heredoc.
- **Bodies are short.** One line, ≤120 characters total. If the
  rationale is longer than that, it belongs in the ADR or the
  commit's CHANGELOG entry, not in the commit message.
- **No trailing period.** Lowercase, terse, matches existing
  style (`M#-T#:`, `chore:`, `docs:`, `ci:`, `fix:`).

**GPG signing — strict:**

- The agent must never use `--no-gpg-sign`, `git -c
  commit.gpgsign=false`, `GIT_GPG_PROGRAM=true`, or any other
  mechanism that bypasses the human's GPG signing. The
  signature is the audit trail.
- The agent runs `git commit` at most **once per staged
  change.** If the invocation times out, errors, or appears
  hung (no output, no prompt, no return), the agent does **not**
  retry, loop, or `q`/Ctrl-C/EOF the shell. The agent stops,
  reports the state, and waits for the human. The human can
  recover by running `git commit` themselves, or by inspecting
  the shell that the tool runner left open.

**Sequence per commit (the agent follows this, no deviations):**

0. The last file write must be in its own call and must have finished before
   staging. Never send a write and the `git add` that stages it in the same
   batch — the runner executes a batch concurrently and the commit can
   capture the pre-write bytes (§Commands, M5-T5.6).
1. Stage the files (`git add <path>`).
2. Run `make check`. Report pass/fail.
3. Show the staged diff (`git --no-pager diff --cached`).
4. Print the proposed one-line `git commit -m '...'` command
   verbatim, do not run it yet.
5. Wait for the human to say "commit" (or equivalent).
6. Run **exactly one** `git commit -m '<one-line message>'`.
7. Stop. Do not push, do not stage the next commit, do not run
   any further git commands. The human confirms the signature
   in their own terminal before the next turn.

**If a commit is malformed** (wrong message, missing file,
wrong files staged): reset with `git reset --soft HEAD~1` and
re-stage. Do not force-amend a signed commit.

**For multi-commit work** (e.g. a roadmap task broken into 5
commits per the plan), do one commit at a time per the
sequence above. Do not pre-stage the next commit's changes
while waiting.

## Plan mode

- In plan mode: only read, search, and inspect. Do not edit files, run
  state-changing commands, or create directories.
- When reality disagrees with the plan, stop and surface it. Update the
  plan, write an ADR if needed, then continue. No silent drift.

## Dependencies

- The project is deliberately lean: two deps today (`mattn/go-sqlite3`,
  `gopkg.in/yaml.v3`). Adding a dependency is a project decision, not
  an agent decision. Surface the request in the plan, don't act on it.

## Containment

- Gate G1 (`internal/gate/gate_test.go`) forbids `os/exec`, container
  clients, and `syscall` in `internal/`. Do not add them.
- Spikes (`spikes/`) may use whatever they need. Spike code never gets
  imported by `internal/`.
