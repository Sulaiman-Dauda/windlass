// Unit tests for route synchronization, using the deterministic fake agent
// (no Docker or Caddy required).
package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/agent/fake"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/migrations"
)

// newTestService wires a Service onto a migrated in-memory-ish store and the
// fake agent, and returns both so tests can inject failures.
func newTestService(t *testing.T) (*Service, *fake.Fake, *db.Queries) {
	t.Helper()
	dir := t.TempDir()
	sqlDB, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := store.Migrate(sqlDB, migrations.FS); err != nil {
		t.Fatal(err)
	}
	q := db.New(sqlDB)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ag := fake.New()
	box, err := secrets.New(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	proj := projects.New(q, ag, box, bus, logger)
	return New(q, ag, proj, bus, logger), ag, q
}

// seedRoutedDomain creates a project with one domain backed by a running
// container, then syncs so the fake proxy holds a live route.
func seedRoutedDomain(t *testing.T, s *Service, ag *fake.Fake, q *db.Queries) {
	t.Helper()
	ctx := context.Background()

	p, err := q.CreateProject(ctx, db.CreateProjectParams{Name: "shop", Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateDomain(ctx, db.CreateDomainParams{
		ProjectID: p.ID, Hostname: "shop.example.com", Service: "web", ContainerPort: 3000,
	}); err != nil {
		t.Fatal(err)
	}
	ag.Containers = []agent.Container{{
		ComposeProject: "shop", ComposeService: "web",
		State: "running", IPAddress: "10.0.1.2",
	}}

	if err := s.Sync(ctx); err != nil {
		t.Fatalf("seed sync: %v", err)
	}
	if len(ag.Routes) != 1 || ag.Routes[0].Upstream != "10.0.1.2:3000" {
		t.Fatalf("seed did not route: %+v", ag.Routes)
	}
}

// The regression that took production down: Docker was briefly unreachable at
// boot, every upstream failed to resolve, and Sync applied an empty route set
// , deleting the routes of containers that were still serving traffic.
func TestSyncKeepsRoutesWhenDockerUnreachable(t *testing.T) {
	s, ag, q := newTestService(t)
	seedRoutedDomain(t, s, ag, q)
	before := append([]agent.Route(nil), ag.Routes...)

	ag.Fail["docker.list"] = errors.New("connection reset by peer")

	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("Sync must report an error when Docker is unreachable, so the caller retries")
	}
	if len(ag.Routes) != len(before) || ag.Routes[0].Upstream != before[0].Upstream {
		t.Fatalf("live routes were modified during a Docker outage: got %+v, want %+v", ag.Routes, before)
	}
}

// A genuinely absent container is not an infrastructure failure: that domain
// drops out of the route set and the sync still succeeds.
func TestSyncDropsRouteWhenContainerNotRunning(t *testing.T) {
	s, ag, q := newTestService(t)
	seedRoutedDomain(t, s, ag, q)

	ag.Containers = nil // scaled to zero, not a Docker failure

	if err := s.Sync(context.Background()); err != nil {
		t.Fatalf("Sync should succeed when a container is simply absent: %v", err)
	}
	if len(ag.Routes) != 0 {
		t.Fatalf("expected the route to be withdrawn, got %+v", ag.Routes)
	}
}

// Caddy not yet up at boot must be retryable rather than a silent success,
// otherwise the desired state is stranded until the next deployment.
func TestSyncErrorsWhenProxyUnavailable(t *testing.T) {
	s, ag, q := newTestService(t)
	seedRoutedDomain(t, s, ag, q)

	ag.ProxyAvailable = false

	if err := s.Sync(context.Background()); err == nil {
		t.Fatal("Sync must report an error when Caddy is unavailable, so the caller retries")
	}
}

func TestDeleteRestoresIndexWhenManifestWriteFails(t *testing.T) {
	s, ag, q := newTestService(t)
	seedRoutedDomain(t, s, ag, q)
	ag.Files["shop"] = map[string][]byte{
		"compose.yaml":   []byte("services: {}"),
		".windlass.json": []byte(`{"version":1,"source":"manual","domains":[{"hostname":"shop.example.com","service":"web","container_port":3000}]}`),
	}
	ag.Fail["fs.write"] = errors.New("disk full")

	if err := s.Delete(context.Background(), "shop", "shop.example.com"); err == nil {
		t.Fatal("delete succeeded despite manifest write failure")
	}
	project, err := q.GetProjectByName(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	domains, err := q.ListProjectDomains(context.Background(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(domains) != 1 || domains[0].Hostname != "shop.example.com" {
		t.Fatalf("domain index was not restored: %+v", domains)
	}
}
