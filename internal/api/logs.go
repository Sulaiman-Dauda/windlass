package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/agent"
)

// handleProjectLogs streams a service's container logs over SSE.
// ?service= selects the compose service; ?follow=false returns the tail and
// closes; ?tail=N controls history depth.
func (a *API) handleProjectLogs(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if _, err := a.Projects.Get(r.Context(), name); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}

	service := r.URL.Query().Get("service")
	containers, err := a.Agent.Docker().ListContainers(r.Context(), agent.ContainerFilter{ComposeProject: name})
	if err != nil {
		writeError(w, http.StatusBadGateway, "docker_unavailable", err.Error())
		return
	}
	var target *agent.Container
	for i, c := range containers {
		if service == "" || c.ComposeService == service {
			target = &containers[i]
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "not_found", "no container for this service")
		return
	}

	follow := r.URL.Query().Get("follow") != "false"
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))

	sse, ok := newSSE(w)
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal", "streaming unsupported")
		return
	}

	// Logs blocks until the container stream ends or the client leaves;
	// SSE writes fan out line by line.
	err = a.Agent.Docker().Logs(r.Context(), target.ID, agent.LogOpts{Follow: follow, Tail: tail},
		func(line agent.LogLine) {
			sse.event("", "log", map[string]string{
				"stream": line.Stream, "text": line.Text,
			})
		})
	if err != nil && r.Context().Err() == nil {
		sse.event("", "error", map[string]string{"text": err.Error()})
	}
}
