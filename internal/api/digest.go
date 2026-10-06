package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/tcs76321/athanor/internal/digest"
)

// handleDigest serves GET /digest?hours=N: the §27.2 Morning Digest for the
// last N hours (default 12).
func (a *API) handleDigest(w http.ResponseWriter, r *http.Request) {
	hours := 12
	if h := r.URL.Query().Get("hours"); h != "" {
		if n, err := strconv.Atoi(h); err == nil && n > 0 && n <= 24*30 {
			hours = n
		}
	}
	until := time.Now()
	since := until.Add(-time.Duration(hours) * time.Hour)
	d, err := digest.Build(r.Context(), a.db, since, until)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}
