# x-data-schema-evolve fixture

`input.json` is the legacy shape; `schema.json` is the target schema.

- `id` must be an integer.
- `created` becomes `created_at` in RFC 3339 UTC (`Z` suffix).
- `severity` maps `WARN`/`warning` to `warning`, `critical`/`ERROR` to `error`,
  and `INFO`/`info` to `info`.
- Drop `legacy` and every field not in the target schema.
