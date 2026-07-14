// Package proxy manages domains: metadata in SQLite, desired-state routes in
// Caddy. Routes re-sync whenever deployments finish or containers change, so
// upstream container IPs stay correct across restarts.
package proxy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/store/db"
)

var (
	ErrNotFound        = errors.New("domain not found")
	ErrConflict        = errors.New("hostname already in use")
	ErrInvalidHostname = errors.New("invalid hostname")
)

const panelDomainSettingKey = "panel.domain"

type Service struct {
	q        *db.Queries
	agent    agent.Agent
	projects *projects.Service
	bus      *events.Bus
	logger   *slog.Logger

	syncCh chan struct{}
}

func New(q *db.Queries, ag agent.Agent, projectSvc *projects.Service, bus *events.Bus, logger *slog.Logger) *Service {
	return &Service{q: q, agent: ag, projects: projectSvc, bus: bus, logger: logger, syncCh: make(chan struct{}, 1)}
}

type Domain struct {
	Hostname      string `json:"hostname"`
	Service       string `json:"service"`
	ContainerPort int64  `json:"container_port"`
	// Status: "active" (routed), "pending" (no running container yet),
	// "proxy_unavailable" (Caddy down).
	Status string `json:"status"`
}

func (s *Service) PanelDomain(ctx context.Context) (string, error) {
	value, err := s.q.GetSetting(ctx, panelDomainSettingKey)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return strings.TrimSpace(value), err
}

// SetPanelDomain persists desired state before applying it so a temporary
// Caddy outage converges automatically on the next proxy sync.
func (s *Service) SetPanelDomain(ctx context.Context, hostname string) error {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname != "" && !validHostname(hostname) {
		return fmt.Errorf("%w: %q", ErrInvalidHostname, hostname)
	}
	if err := s.q.SetSetting(ctx, db.SetSettingParams{Key: panelDomainSettingKey, Value: hostname}); err != nil {
		return err
	}
	s.RequestSync()
	if err := s.agent.Proxy().ApplyPanelDomain(ctx, hostname); err != nil {
		return fmt.Errorf("panel hostname saved but Caddy has not applied it yet: %w", err)
	}
	return nil
}

func (s *Service) Add(ctx context.Context, projectID int64, hostname, service string, port int64) (db.Domain, error) {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if !validHostname(hostname) {
		return db.Domain{}, fmt.Errorf("invalid hostname %q", hostname)
	}
	if service == "" || port <= 0 || port > 65535 {
		return db.Domain{}, errors.New("service and a valid container port are required")
	}
	project, err := s.q.ProjectByID(ctx, projectID)
	if err != nil {
		return db.Domain{}, err
	}
	config, err := s.agent.Compose().Config(ctx, project.Name)
	if err != nil {
		return db.Domain{}, err
	}
	resolved, ok := config.Services[service]
	if !ok {
		return db.Domain{}, fmt.Errorf("service %q is not defined in compose.yaml", service)
	}
	if len(resolved.ContainerPorts) > 0 {
		declared := false
		for _, candidate := range resolved.ContainerPorts {
			if int64(candidate) == port {
				declared = true
				break
			}
		}
		if !declared {
			return db.Domain{}, fmt.Errorf("port %d is not exposed by service %q", port, service)
		}
	}

	d, err := s.q.CreateDomain(ctx, db.CreateDomainParams{
		ProjectID: projectID, Hostname: hostname, Service: service, ContainerPort: port,
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return db.Domain{}, ErrConflict
		}
		return db.Domain{}, err
	}
	if err := s.persistDomains(ctx, project.Name, project.ID); err != nil {
		_ = s.q.DeleteDomain(ctx, d.ID)
		return db.Domain{}, err
	}

	s.RequestSync()
	s.bus.Publish(events.Event{Topic: "domain", Type: "domain.created", Resource: hostname})
	return d, nil
}

func (s *Service) Delete(ctx context.Context, projectName, hostname string) error {
	d, err := s.q.GetDomain(ctx, db.GetDomainParams{Name: projectName, Hostname: hostname})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := s.q.DeleteDomain(ctx, d.ID); err != nil {
		return err
	}
	project, err := s.q.GetProjectByName(ctx, projectName)
	if err != nil {
		return err
	}
	if err := s.persistDomains(ctx, projectName, project.ID); err != nil {
		return err
	}
	s.RequestSync()
	s.bus.Publish(events.Event{Topic: "domain", Type: "domain.deleted", Resource: hostname})
	return nil
}

func (s *Service) persistDomains(ctx context.Context, projectName string, projectID int64) error {
	rows, err := s.q.ListProjectDomains(ctx, projectID)
	if err != nil {
		return err
	}
	domains := make([]projects.DomainConfig, 0, len(rows))
	for _, row := range rows {
		domains = append(domains, projects.DomainConfig{Hostname: row.Hostname,
			Service: row.Service, ContainerPort: row.ContainerPort})
	}
	return s.projects.SetDomains(ctx, projectName, domains)
}

// List returns a project's domains with live routing status.
func (s *Service) List(ctx context.Context, projectID int64) ([]Domain, error) {
	rows, err := s.q.ListProjectDomains(ctx, projectID)
	if err != nil {
		return nil, err
	}

	info, _ := s.agent.Proxy().Available(ctx)
	routed := map[string]bool{}
	if info.Available {
		if current, err := s.agent.Proxy().CurrentRoutes(ctx); err == nil {
			for _, r := range current {
				routed[r.Hostname] = true
			}
		}
	}

	out := make([]Domain, 0, len(rows))
	for _, row := range rows {
		d := Domain{
			Hostname: row.Hostname, Service: row.Service, ContainerPort: row.ContainerPort,
		}
		switch {
		case !info.Available:
			d.Status = "proxy_unavailable"
		case routed[row.Hostname]:
			d.Status = "active"
		default:
			d.Status = "pending"
		}
		out = append(out, d)
	}
	return out, nil
}

func (s *Service) ProxyStatus(ctx context.Context) (agent.ProxyInfo, error) {
	return s.agent.Proxy().Available(ctx)
}

// ---------------------------------------------------------------------------
// Route synchronization

// Sync pushes the full desired state of all domains into Caddy. Upstreams
// resolve to the current container IP of each domain's service.
func (s *Service) Sync(ctx context.Context) error {
	info, err := s.agent.Proxy().Available(ctx)
	if err != nil || !info.Available {
		s.logger.Warn("proxy unavailable; skipping route sync")
		return nil // degrade gracefully (plan: warn, don't fail)
	}
	panelDomain, err := s.PanelDomain(ctx)
	if err != nil {
		return err
	}
	if err := s.agent.Proxy().ApplyPanelDomain(ctx, panelDomain); err != nil {
		return fmt.Errorf("apply panel domain: %w", err)
	}

	domains, err := s.q.ListAllDomains(ctx)
	if err != nil {
		return err
	}

	var routes []agent.Route
	for _, d := range domains {
		upstream, err := s.resolveUpstream(ctx, d.ProjectName, d.Service, d.ContainerPort)
		if err != nil {
			s.logger.Info("domain has no live upstream yet", "hostname", d.Hostname, "reason", err)
			continue
		}
		routes = append(routes, agent.Route{
			ID:       "windlass_route_" + d.Hostname,
			Hostname: d.Hostname,
			Upstream: upstream,
			TLS:      true,
		})
	}

	if err := s.agent.Proxy().ApplyRoutes(ctx, routes); err != nil {
		return fmt.Errorf("apply routes: %w", err)
	}
	s.logger.Debug("proxy routes synced", "count", len(routes))
	return nil
}

func (s *Service) resolveUpstream(ctx context.Context, project, service string, port int64) (string, error) {
	containers, err := s.agent.Docker().ListContainers(ctx, agent.ContainerFilter{ComposeProject: project})
	if err != nil {
		return "", err
	}
	for _, c := range containers {
		if c.ComposeService == service && c.State == "running" && c.IPAddress != "" {
			return fmt.Sprintf("%s:%d", c.IPAddress, port), nil
		}
	}
	return "", fmt.Errorf("no running container for service %s", service)
}

// RequestSync schedules a debounced sync (coalesces bursts of events).
func (s *Service) RequestSync() {
	select {
	case s.syncCh <- struct{}{}:
	default:
	}
}

// Run listens for deployment/container changes and keeps routes in sync
// until ctx ends.
func (s *Service) Run(ctx context.Context) {
	// Deployments finishing (or projects being deleted) change upstreams.
	busCh, cancel := s.bus.Subscribe("deployment", "project", "domain")
	defer cancel()

	// Container lifecycle events (restarts change IPs). Events blocks until
	// ctx is done; run it in the background and feed RequestSync.
	go func() {
		for ctx.Err() == nil {
			err := s.agent.Docker().Events(ctx, func(ev agent.DockerEvent) {
				switch ev.Action {
				case "start", "die", "stop":
					s.RequestSync()
				}
			})
			if ctx.Err() != nil {
				return
			}
			s.logger.Warn("docker event stream ended; retrying", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()

	// Initial sync on startup (Caddy restarts lose in-memory config).
	s.RequestSync()

	debounce := time.NewTimer(0)
	if !debounce.Stop() {
		<-debounce.C
	}
	pending := false

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-busCh:
			switch ev.Type {
			case "deployment.created", "deployment.step":
				// mid-deploy noise — the final sync happens on done
			default:
				s.RequestSync()
			}
		case <-s.syncCh:
			if !pending {
				pending = true
				debounce.Reset(2 * time.Second)
			}
		case <-debounce.C:
			pending = false
			syncCtx, cancelSync := context.WithTimeout(ctx, 30*time.Second)
			if err := s.Sync(syncCtx); err != nil {
				s.logger.Error("route sync", "error", err)
			}
			cancelSync()
		}
	}
}

func validHostname(h string) bool {
	if len(h) < 3 || len(h) > 253 || !strings.Contains(h, ".") {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for i, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			case r == '-':
				if i == 0 || i == len(label)-1 {
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}
