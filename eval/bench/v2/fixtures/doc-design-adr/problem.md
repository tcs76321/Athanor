# Problem: choose a default persistence format for memory embeddings

Athanor stores memory embeddings for `query_memory` retrieval. Today they are
opaque BLOBs in the SQLite database, and similarity is computed in Go. Two
candidate directions:

1. Keep BLOBs and the in-Go cosine index; add an approximate index later.
2. Adopt the native `sqlite-vec` `vec0` virtual table, which requires the
   extension to be loaded on every connection.

Constraints: local-first, no network; the store holds a single connection
(WAL); the project keeps its direct-dependency set to ten; startup must fail
loudly if a required extension is missing. The decision must scale to tens of
thousands of chunks on consumer hardware.
