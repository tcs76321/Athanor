## Summary

<!-- What changed and why, in a sentence or two. -->

## Checklist

- [ ] `make check` is green (lint + vet + test-race)
- [ ] `make tidy-check` is clean
- [ ] One logical change; commit title matches the project style
      (`M#-T#:`, `chore:`, `docs:`, `ci:`, `fix:`)
- [ ] If a dependency was added or removed, `internal/deps/deps_test.go`
      was updated in the same commit
- [ ] If a design decision was made, an ADR was added under `docs/adr/`
- [ ] `ROADMAP.md` / `CHANGELOG.md` updated when milestone-level
- [ ] Security gates re-proven if `internal/` imports or the engine
      surface changed
