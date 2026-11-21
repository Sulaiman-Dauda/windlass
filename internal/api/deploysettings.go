package api

import (
	"encoding/json"
	"net/http"

	"github.com/windlass-dev/windlass/internal/auth"
)

type deploySettings struct {
	TagSuggestions bool `json:"tag_suggestions"`
}

// handleGetDeploySettings serves the deploy settings.
func (a *API) handleGetDeploySettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, deploySettings{TagSuggestions: a.Deploy.TagSuggestEnabled(r.Context())})
}

// handleSetDeploySettings saves whether a failed deploy looks up nearby
// tags on Docker Hub. The field is required.
func (a *API) handleSetDeploySettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TagSuggestions *bool `json:"tag_suggestions"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if body.TagSuggestions == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "tag_suggestions is required")
		return
	}
	if err := a.Deploy.SetTagSuggestEnabled(r.Context(), *body.TagSuggestions); err != nil {
		a.internalError(w, "save deploy settings", err)
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "system.deploy_settings", "system", "deploy", remoteIP(r),
		deploySettings{TagSuggestions: *body.TagSuggestions})
	a.handleGetDeploySettings(w, r)
}
