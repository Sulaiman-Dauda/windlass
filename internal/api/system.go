package api

import (
	"net/http"

	"github.com/windlass-dev/windlass/internal/version"
)

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok", Version: version.Version})
}
