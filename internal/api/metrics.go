package api

import (
	"net/http"

	"github.com/windlass-dev/windlass/internal/agent"
)

type systemMetricsResponse struct {
	Host       agent.HostMetrics `json:"host"`
	Node       agent.NodeInfo    `json:"node"`
	Containers struct {
		Running int `json:"running"`
		Total   int `json:"total"`
	} `json:"containers"`
}

func (a *API) handleSystemMetrics(w http.ResponseWriter, r *http.Request) {
	var resp systemMetricsResponse

	// Each source degrades independently: a dead Docker daemon must not
	// blank the host metrics, and vice versa.
	if m, err := a.Agent.Host().Metrics(r.Context()); err == nil {
		resp.Host = m
	}
	if info, err := a.Agent.Ping(r.Context()); err == nil {
		resp.Node = info
	}
	if list, err := a.Agent.Docker().ListContainers(r.Context(), agent.ContainerFilter{}); err == nil {
		resp.Containers.Total = len(list)
		for _, c := range list {
			if c.State == "running" {
				resp.Containers.Running++
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
