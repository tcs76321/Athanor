# Third-party UI assets

## HTMX

- File: `internal/ui/assets/htmx.min.js`
- Version: 2.0.4 (downloaded from `https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js`)
- License: BSD 2-Clause. Copyright (c) 2020 Big Sky Software.
- Why vendored: the UI must work offline, the CSP forbids remote scripts, and
  HTMX is a static asset (not a Go module), so the project's Go dependency
  allowlist is unchanged.
- Upgrade rule: replace the file, update this note and the version, and run the
  UI tests plus a manual HTMX interaction check (live search).
