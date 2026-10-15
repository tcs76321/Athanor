# SQLite + sqlite-vec Setup Findings

## Executive Decision

| Driver | CGO | sqlite-vec Support | FTS5 Support | Verdict |
|--------|-----|-------------------|--------------|---------|
| `mattn/go-sqlite3` | Required | ✅ Full (dynamic `LoadExtension`) | ⚠️ Needs `sqlite_fts5` build tag | **CHOSEN** |
| `modernc.org/sqlite` | None | ❌ No dynamic loading | ✅ Native | Rejected for vec0 |

**Rationale:** `modernc.org/sqlite` cannot load `.so`/`.dylib` extensions at runtime — it requires compiling sqlite-vec into the Wasm binary (complex custom build pipeline). `mattn/go-sqlite3` works out of the box with runtime `LoadExtension(lib, entry)`. FTS5 can be enabled in `mattn/go-sqlite3` via the `sqlite_fts5` build tag if needed.

## Working Configuration

### Go Module
```go
require github.com/mattn/go-sqlite3 v1.14.0
```

### Build Command
```bash
# No special build tags needed for basic operation!
CGO_ENABLED=1 go build .
CGO_ENABLED=1 go run main.go

# For FTS5 support, explicitly pass the build tag:
CGO_ENABLED=1 go build -tags sqlite_fts5 .
```

> **Note on FTS5:** While `mattn/go-sqlite3` may work with FTS5 out of the box on some systems (depending on how the system SQLite was compiled), **explicitly passing `-tags sqlite_fts5` is highly recommended for guaranteed cross-platform consistency**. This ensures the FTS5 module is compiled into the binary regardless of the host's SQLite configuration.

### Runtime Extension Loading Pattern (CRITICAL: Connection Affinity)
```go
db, _ := sql.Open("sqlite3", "file:data.db?_busy_timeout=5000&_journal_mode=WAL")
db.SetMaxOpenConns(1)

ctx := context.Background()
conn, _ := db.Conn(ctx)
defer conn.Close()

// Load extension on THIS connection
var loader interface{ LoadExtension(string, string) error }
conn.Raw(func(dc any) error {
    loader = dc.(interface{ LoadExtension(string, string) error })
    return nil
})
loader.LoadExtension("path/to/sqlite-vec.dylib", "sqlite3_vec_init")

// ALL subsequent vec0 operations MUST use this SAME connection
conn.ExecContext(ctx, "CREATE VIRTUAL TABLE items USING vec0(embedding float[4])")
conn.ExecContext(ctx, "INSERT INTO items(rowid, embedding) VALUES (1, '[0.1,0.2,0.3,0.4]')")
rows, _ := conn.QueryContext(ctx, "SELECT rowid, distance FROM items WHERE embedding MATCH ? AND k = 3", queryJSON)
```

**Critical:** Extensions are **per-connection**. After loading the extension on a connection, **all vector operations must use that same connection** (`conn.ExecContext`, `conn.QueryContext`). Using `db.Exec()` will grab a different connection from the pool where the extension is not loaded.

### KNN Query Syntax
```sql
-- vec0 requires explicit k parameter
SELECT rowid, distance FROM items WHERE embedding MATCH ? AND k = 3 ORDER BY distance
```

## Extension Binaries

Download from [GitHub Releases v0.1.6](https://github.com/asg017/sqlite-vec/releases/tag/v0.1.6):

```bash
# macOS ARM64
curl -L https://github.com/asg017/sqlite-vec/releases/download/v0.1.6/sqlite-vec-v0.1.6-loadable-macos-aarch64.tar.gz | tar xz -C build/
# Rename the extracted file (vec0.dylib -> vec.dylib or sqlite-vec.dylib)
mv build/vec0.dylib build/sqlite-vec.dylib

# Linux x86_64
curl -L https://github.com/asg017/sqlite-vec/releases/download/v0.1.6/sqlite-vec-v0.1.6-loadable-linux-x86_64.tar.gz | tar xz -C build/
```

### macOS Gatekeeper Fix
If you get `dlopen() failed: code signature invalid`:
```bash
xattr -d com.apple.quarantine build/sqlite-vec.dylib
```

## FTS5 Fallback (No Extension Needed)

If sqlite-vec proves problematic, **FTS5 works natively** with `modernc.org/sqlite` (pure Go, no CGO) and with `mattn/go-sqlite3` when built with `-tags sqlite_fts5`:

```sql
CREATE VIRTUAL TABLE fts_items USING fts5(content, title);
SELECT * FROM fts_items WHERE fts_items MATCH 'search term' ORDER BY rank;
```

Provides full-text search without vector similarity — sufficient for baseline text search.

## CI/CD Matrix (GitHub Actions)

```yaml
strategy:
  matrix:
    include:
      - os: macos-latest
        arch: arm64
        ext: vec.dylib
      - os: ubuntu-latest
        arch: amd64
        ext: sqlite-vec.so
```

---

## Spike Test Results (macOS ARM64)

### mattn/go-sqlite3
```
=== mattn/go-sqlite3 (CGO) ===
Platform: darwin/arm64
Extension: build/vec.dylib
[mattn] ✅ sqlite-vec loaded
[mattn] Testing vec0...
[mattn] Creating vec0 table...
[mattn] Inserting vectors...
[mattn] Running KNN query...
[mattn] KNN Results:
  rowid=1 distance=0.100000
  rowid=2 distance=0.700000
  rowid=4 distance=1.004988
[mattn] ✅ vec0 KNN works
```

### modernc.org/sqlite
```
=== modernc.org/sqlite (pure Go) ===
Platform: darwin/arm64
Extension: build/sqlite-vec.dylib
[modernc] ⚠️  load_extension failed (expected): SQL logic error: not authorized
[modernc] modernc cannot load dynamic .so/.dylib extensions at runtime.
[modernc] Would require custom Wasm build with sqlite-vec baked in.
[modernc] FTS5 Results:
  rowid=1 rank=-0.000001
[modernc] ✅ FTS5 works (fallback)
```