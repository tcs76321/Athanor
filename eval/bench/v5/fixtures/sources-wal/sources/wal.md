# SQLite write-ahead logging — source note

Source ID: wal.md
Published: 2026-11-10

Write-ahead logging (WAL) lets readers and a writer proceed concurrently.
Readers see a consistent snapshot; the writer appends to the WAL and checkpoints
back into the database file. WAL usually improves concurrency and write
throughput for local applications.

The trade-offs are additional files, checkpoint management, and the fact that WAL
does not work on network filesystems where all processes do not share memory.
A checkpoint moves WAL frames into the main database; long-lived readers can
delay it.

For local-first tools, WAL is a good default when the database lives on a local
disk and the application controls checkpointing. It is a poor fit for a database
file on a network share.
