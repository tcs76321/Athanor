# Pipeline fixture

The package exposes:

- `parse_csv(text) -> list[dict]` — parse a CSV with a `name,amount,category`
  header. Fields may be quoted and may contain commas.
- `total_by_category(rows) -> dict[str, int]` — sum integer `amount` per
  `category`.
- `top_categories(totals, n) -> list[str]` — the `n` categories with the highest
  totals, descending by total and then by name.

There are three bugs in this fixture. Fix them without changing the public API.
The hidden tests exercise quoted fields, integer amounts, and value ordering.
