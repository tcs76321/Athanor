# Job Pod images and language execution

**Status:** living · **Refs:** ADR-0101, ADR-0081, `deploy/jobpod.Containerfile`

The default Job Pod image (`make jobpod-image`, `athanor-jobpod:latest`) is a
Python image. It has `python`, `pytest`, `ruff`, `git`, and a C toolchain. This
matches the default `code` on-ramp (`pytest -q`) and the benchmark corpus.

## What this means

`internal/project.DetectTestCommand` can detect commands for Go, Rust,
JavaScript/TypeScript, Java, Ruby, PHP, and Makefile repositories. Detection is
not execution: the default image does **not** contain those toolchains. A
repository whose test command is `go test ./...`, `cargo test`, `npm test`,
`mvn test`, `gradle test`, `composer test`, or `bundle exec rspec` will fail
deterministic verification until a matching image is configured.

This is intentional and fail-closed: the engine does not silently accept code it
could not test. It is also why the `doctor` output and the first code run should
be read before assuming multi-language execution works.

## Polyglot image

`make jobpod-polyglot-image` builds `athanor-jobpod-polyglot:latest` with Go,
Node/npm, Rust/Cargo, Java/Maven/Gradle, Ruby, PHP/Composer, plus the Python
tooling. Point the Core at it:

```yaml
job_pod:
  image: athanor-jobpod-polyglot:latest
```

The image is intentionally optional: it is large, and most local-first users run
Python or a single language.

## The `/tmp` noexec caveat

The Job Pod mounts `/tmp` with `noexec`. Python tests work because the
interpreter reads source. Compiled runners that build a binary and then execute
it from `/tmp` (for example `go test`, `cargo test`, or a JVM test that spawns a
helper) need an exec-capable scratch mount. The default hardening intentionally
does not weaken this. If you need compiled tests, add a project-specific Job Pod
spec or image that provides a separate exec-capable working volume rather than
turning off `noexec` globally.

## Verification

The engine records `tests_skipped` with the reason when no test command is
detected. If a command is detected but the toolchain is absent, the test run
fails with the command's stderr (typically `executable file not found`). Check
the job's evaluation records and the `tests_run_failed` audit event before
changing model or prompt settings.
