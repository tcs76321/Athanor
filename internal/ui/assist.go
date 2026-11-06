package ui

import (
	"encoding/json"
	"net/http"
)

// handleAssist refines one field and returns a proposal. It never writes the
// field; the browser applies it and the user can revert.
func (u *UI) handleAssist(w http.ResponseWriter, r *http.Request) {
	if u.deps.Assist == nil {
		writeAssistError(w, http.StatusServiceUnavailable, "refinement is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req struct {
		Field string `json:"field"`
		Text  string `json:"text"`
		Mode  string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAssistError(w, http.StatusBadRequest, "invalid refinement request")
		return
	}
	res, err := u.deps.Assist.Refine(r.Context(), req.Field, req.Text, req.Mode)
	if err != nil {
		writeAssistError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(res)
}

func writeAssistError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
