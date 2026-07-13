package api

import (
	"errors"
	"net/http"

	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/update"
)

func (a *API) handleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	rel, err := a.Update.Check(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "update_check_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rel)
}

func (a *API) handleApplyUpdate(w http.ResponseWriter, r *http.Request) {
	err := a.Update.Apply(r.Context())
	if errors.Is(err, update.ErrNotSupported) {
		writeError(w, http.StatusBadRequest, "not_supported", err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "update_failed", err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "system.update", "", "", remoteIP(r), nil)
	writeJSON(w, http.StatusOK, map[string]string{
		"result": "updating",
		"note":   "the panel restarts into the new version; deployed apps are unaffected",
	})
}
