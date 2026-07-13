// Package api contains the /api/v1 HTTP handlers. Handlers stay thin:
// validate the request, call a service, write a response.
package api

import (
	"github.com/go-chi/chi/v5"
)

func Routes(r chi.Router) {
	r.Get("/system/health", handleHealth)
}
