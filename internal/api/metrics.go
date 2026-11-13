package api

import (
	"net/http"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/selfstat"
)

type systemMetricsResponse struct {
	Host       agent.HostMetrics `json:"host"`
	Node       agent.NodeInfo    `json:"node"`
	Containers struct {
		Running int `json:"running"`
		Total   int `json:"total"`
	} `json:"containers"`
	// Panel is the Windlass process itself. RSSBytes is 0 where it cannot
	// be read; History is once a minute for the last hour, oldest first.
	Panel struct {
		RSSBytes uint64            `json:"rss_bytes"`
		History  []selfstat.Sample `json:"history"`
	} `json:"panel"`
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
	resp.Panel.RSSBytes, _ = selfstat.RSS()
	resp.Panel.History = []selfstat.Sample{}
	if a.SelfStat != nil {
		resp.Panel.History = a.SelfStat.Samples()
	}
	writeJSON(w, http.StatusOK, resp)
}
