package api

import (
	"net/http"

	"github.com/tcs76321/athanor/internal/alarms"
)

// handleAlarmList serves GET /alarms: the active §22.3 alarm queue, most
// severe first.
func (a *API) handleAlarmList(w http.ResponseWriter, r *http.Request) {
	if a.alarms == nil {
		writeError(w, http.StatusServiceUnavailable, "alarms are not configured")
		return
	}
	list, err := a.alarms.Active(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []alarms.Alarm{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"alarms": list})
}

// handleAlarmResolve serves POST /alarms/{id}/resolve.
func (a *API) handleAlarmResolve(w http.ResponseWriter, r *http.Request) {
	if a.alarms == nil {
		writeError(w, http.StatusServiceUnavailable, "alarms are not configured")
		return
	}
	if err := a.alarms.Resolve(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "resolved"})
}
