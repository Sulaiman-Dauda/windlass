package api

import (
	"net/http"

	spec "github.com/windlass-dev/windlass/api"
	"github.com/windlass-dev/windlass/internal/version"
)

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok", Version: version.Version})
}

func handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	w.Write(spec.Spec)
}
