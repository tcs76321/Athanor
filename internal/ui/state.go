package ui

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
)

// loadSystemState reads one UI-owned key from system_state. The bool is false
// when the key is absent (or the store is unavailable); a real read error is
// logged so a corrupt database is not silently mistaken for "unset".
func (u *UI) loadSystemState(ctx context.Context, key string) (string, bool) {
	if u.deps.Store == nil {
		return "", false
	}
	var raw string
	err := u.deps.Store.DB().QueryRowContext(ctx,
		`SELECT value FROM system_state WHERE key = ?`, key).Scan(&raw)
	if err == nil {
		return raw, true
	}
	if !errors.Is(err, sql.ErrNoRows) {
		slog.Error("ui: reading system state", "key", key, "err", err)
	}
	return "", false
}

// saveSystemState upserts one UI-owned key. The caller decides whether a write
// failure is fatal (settings writes surface it; best-effort progress writes
// ignore it).
func (u *UI) saveSystemState(ctx context.Context, key, value string) error {
	if u.deps.Store == nil {
		return nil
	}
	_, err := u.deps.Store.DB().ExecContext(ctx,
		`INSERT INTO system_state (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
