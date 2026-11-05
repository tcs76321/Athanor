package ui

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// restartStateKey records that a config write landed but the running process
// still holds the old configuration. Cleared at boot, so a restart is the only
// thing that clears it.
const restartStateKey = "ui:restart_required"

type restartState struct {
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

func (u *UI) markRestartRequired(ctx context.Context, reason string) {
	if u.deps.Store == nil {
		return
	}
	raw, _ := json.Marshal(restartState{Reason: reason, At: time.Now().UTC()})
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _ = u.deps.Store.DB().ExecContext(cctx,
		`INSERT INTO system_state (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		restartStateKey, string(raw))
}

func (u *UI) restartRequired(ctx context.Context) string {
	if u.deps.Store == nil {
		return ""
	}
	cctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var raw string
	if err := u.deps.Store.DB().QueryRowContext(cctx,
		`SELECT value FROM system_state WHERE key = ?`, restartStateKey).Scan(&raw); err != nil {
		return ""
	}
	var st restartState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return "configuration saved"
	}
	if strings.TrimSpace(st.Reason) == "" {
		return "configuration saved"
	}
	return st.Reason
}

// restartHint is the operator command for the active runtime.
func (u *UI) restartHint() string {
	if strings.EqualFold(strings.TrimSpace(u.deps.Runtime), "Core Pod") {
		return "podman restart athanor-core"
	}
	return "restart the athanor serve process"
}
