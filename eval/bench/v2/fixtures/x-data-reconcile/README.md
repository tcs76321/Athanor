# x-data-reconcile fixture

- `a.json` and `b.json` are the two sources.
- `schema.json` is the required output shape.
- Reconcile by `id`; `b` wins when both sources contain the same id.
- The output is an array sorted by `id`, with no duplicate ids and no extra
  fields.
