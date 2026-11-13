package api

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/attention"
	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/projects"
)

// handleAttention serves the Needs you list. Any signed-in user reads it;
// the update row is for admins, the only role that can act on it.
func (a *API) handleAttention(w http.ResponseWriter, r *http.Request) {
	list, err := a.Attention.List(r.Context())
	if err != nil {
		a.internalError(w, "attention list", err)
		return
	}
	if !auth.HasRole(r.Context(), "admin") {
		items := make([]attention.Item, 0, len(list.Items))
		for _, item := range list.Items {
			if item.Kind != attention.KindUpdate {
				items = append(items, item)
			}
		}
		list.Items = items
	}
	writeJSON(w, http.StatusOK, list)
}

// diskTTL is how long Docker's disk figures are reused. Counting volume
// sizes walks every volume, which takes seconds on a big host.
const diskTTL = 5 * time.Minute

// diskCache holds the last Docker disk reading.
type diskCache struct {
	mu     sync.Mutex
	usage  agent.DiskUsage
	at     time.Time
	expiry time.Time
}

type systemDiskResponse struct {
	// Host is the projects volume as the operating system sees it.
	Host struct {
		UsedBytes  uint64 `json:"used_bytes"`
		TotalBytes uint64 `json:"total_bytes"`
	} `json:"host"`
	// Docker is what Docker holds, by kind, as of CheckedAt. It is absent
	// when Docker cannot be reached.
	Docker    *agent.DiskUsage `json:"docker,omitempty"`
	CheckedAt *time.Time       `json:"checked_at,omitempty"`
}

// handleSystemDisk serves the Disk tile and card: host disk use, and
// Docker's images, containers, volumes and build cache.
func (a *API) handleSystemDisk(w http.ResponseWriter, r *http.Request) {
	var resp systemDiskResponse
	if m, err := a.Agent.Host().Metrics(r.Context()); err == nil {
		resp.Host.UsedBytes, resp.Host.TotalBytes = m.DiskUsed, m.DiskTotal
	}
	a.disk.mu.Lock()
	defer a.disk.mu.Unlock()
	// A clean-up since the last reading makes it stale.
	stale := a.Cleanup != nil && a.Cleanup.FinishedAt().After(a.disk.at)
	if stale || time.Now().After(a.disk.expiry) {
		usage, err := a.Agent.Docker().DiskUsage(r.Context())
		if err == nil {
			now := time.Now().UTC()
			a.disk.usage, a.disk.at, a.disk.expiry = usage, now, now.Add(diskTTL)
		} else {
			a.Logger.Debug("docker disk usage", "error", err)
		}
	}
	if !a.disk.at.IsZero() {
		usage, at := a.disk.usage, a.disk.at
		resp.Docker, resp.CheckedAt = &usage, &at
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleProjectDrift reports, read-only, which containers no longer match
// the project's files or images.
func (a *API) handleProjectDrift(w http.ResponseWriter, r *http.Request) {
	drift, err := a.Deploy.Drift(r.Context(), chi.URLParam(r, "name"))
	if errors.Is(err, projects.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "drift_unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, drift)
}
