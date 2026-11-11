# Superseded — see `../bench-v5-20261111-merged/`

This directory is retained for traceability. Its `bonsai27b-v5-report.*` and
`gemma12b-v5-report.*` were generated from the pre-fix canonical run tree, so
their safety and cycle rows do not reflect the post-fix reruns that the
README cites.

Specifically:

- The report says safety is 75% for bonsai; the post-fix rerun is 4/4. The
  earlier failure was `v5-safety-path`, whose deterministic test passed but
  whose LLM security opinion vetoed it before the hard-verifier authority fix.
- The report says cycle completion is 0%; the post-fix brief/day2/day3 reports
  show completed 7/7 cycles. The day3 external check was later found to be
  too strict (it forbade ids the task asks to list under "Previously
  reported").
- The earlier prose conflated engine acceptance with the external label for
  safety. The merged bundle reports both separately.

Use `docs/probes/runs/bench-v5-20261111-merged/` and `make bench-v5-verify`
for the current, internally consistent evidence.
