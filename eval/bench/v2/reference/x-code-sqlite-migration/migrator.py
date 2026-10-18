"""Transactional SQLite migration runner with checksums and rollback."""

import hashlib
import json
import sqlite3


class MigrationError(RuntimeError):
    pass


class Migration:
    def __init__(self, version, up, down, name=""):
        self.version = version
        self.up = list(up)
        self.down = list(down)
        self.name = name


def _checksum(m):
    return hashlib.sha256(json.dumps([m.up, m.down], sort_keys=True).encode()).hexdigest()


def _ensure(conn):
    conn.execute(
        "CREATE TABLE IF NOT EXISTS schema_migrations ("
        "version INTEGER PRIMARY KEY, checksum TEXT NOT NULL, "
        "applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)"
    )
    conn.commit()


def apply(db_path, migrations, target=None):
    migrations = sorted(migrations, key=lambda m: m.version)
    if target is None:
        target = migrations[-1].version if migrations else 0
    conn = sqlite3.connect(db_path)
    try:
        _ensure(conn)
        for m in migrations:
            if m.version > target:
                break
            row = conn.execute(
                "SELECT checksum FROM schema_migrations WHERE version = ?", (m.version,)
            ).fetchone()
            if row:
                if row[0] != _checksum(m):
                    raise MigrationError("checksum mismatch for version %s" % m.version)
                continue
            try:
                conn.execute("BEGIN")
                for stmt in m.up:
                    conn.execute(stmt)
                conn.execute(
                    "INSERT INTO schema_migrations (version, checksum) VALUES (?, ?)",
                    (m.version, _checksum(m)),
                )
                conn.commit()
            except Exception as exc:
                conn.rollback()
                raise MigrationError("version %s failed: %s" % (m.version, exc)) from exc
    finally:
        conn.close()


def rollback(db_path, migrations, target):
    migrations = sorted(migrations, key=lambda m: m.version)
    conn = sqlite3.connect(db_path)
    try:
        _ensure(conn)
        rows = conn.execute(
            "SELECT version FROM schema_migrations ORDER BY version DESC"
        ).fetchall()
        for (version,) in rows:
            if version <= target:
                continue
            m = next((x for x in migrations if x.version == version), None)
            if m is None:
                raise MigrationError("no migration for applied version %s" % version)
            try:
                conn.execute("BEGIN")
                for stmt in m.down:
                    conn.execute(stmt)
                conn.execute("DELETE FROM schema_migrations WHERE version = ?", (version,))
                conn.commit()
            except Exception as exc:
                conn.rollback()
                raise MigrationError("rollback %s failed: %s" % (version, exc)) from exc
    finally:
        conn.close()
