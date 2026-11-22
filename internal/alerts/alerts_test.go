package alerts

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/agent/fake"
	"github.com/windlass-dev/windlass/internal/attention"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/notify"
	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/migrations"
)

type attStub struct {
	items []attention.Item
	err   error
}

func (a *attStub) List(context.Context) (attention.List, error) {
	return attention.List{Items: a.items}, a.err
}
func (a *attStub) Invalidate() {}

type sink struct {
	mu   sync.Mutex
	sent []notify.Event
}

func (s *sink) Enqueue(_ context.Context, e notify.Event) error {
	s.mu.Lock()
	s.sent = append(s.sent, e)
	s.mu.Unlock()
	return nil
}

func (s *sink) take() []notify.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.sent
	s.sent = nil
	return out
}

type env struct {
	q     *db.Queries
	ag    *fake.Fake
	att   *attStub
	sink  *sink
	bus   *events.Bus
	clock time.Time
	svc   *Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	conn, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := store.Migrate(conn, migrations.FS); err != nil {
		t.Fatal(err)
	}
	e := &env{q: db.New(conn), ag: fake.New(), att: &attStub{}, sink: &sink{}, bus: events.NewBus(),
		clock: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)}
	e.svc = e.restart()
	return e
}

// restart is a new Service on the same database, as after a panel restart.
func (e *env) restart() *Service {
	s := New(e.q, e.ag, e.att, e.sink, e.bus, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.now = func() time.Time { return e.clock }
	// As if the Docker event stream attached long ago.
	s.watchSince = e.clock.Add(-24 * time.Hour)
	e.svc = s
	return s
}

// sweep moves the clock on by d and sweeps.
func (e *env) sweep(d time.Duration) []notify.Event {
	e.clock = e.clock.Add(d)
	e.svc.Sweep(context.Background())
	return e.sink.take()
}

func kinds(evs []notify.Event) []string {
	var out []string
	for _, e := range evs {
		k := e.Kind
		if e.Recovered {
			k += "+recovered"
		}
		out = append(out, k)
	}
	return out
}

func want(t *testing.T, got []notify.Event, kinds_ ...string) {
	t.Helper()
	if g := kinds(got); !slices.Equal(g, kinds_) {
		t.Fatalf("sent %v, want %v", g, kinds_)
	}
}

var disk = attention.Item{Kind: attention.KindDisk, Tone: attention.ToneWarn, Title: "disk 91% full", Detail: "Free space."}

func TestDebounceAlertOnceAndOneRecovery(t *testing.T) {
	e := newEnv(t)
	e.att.items = []attention.Item{disk}
	want(t, e.sweep(0))           // seen, inside the debounce
	want(t, e.sweep(time.Minute)) // still inside 120 s
	want(t, e.sweep(time.Minute), notify.KindDisk)
	for range 10 {
		want(t, e.sweep(time.Minute))
	}
	e.att.items = nil
	want(t, e.sweep(time.Minute)) // one missed sweep is not a recovery
	got := e.sweep(time.Minute)
	want(t, got, notify.KindDisk+"+recovered")
	if got[0].Title != "cleared: disk 91% full" {
		t.Fatalf("title %q", got[0].Title)
	}
	want(t, e.sweep(time.Minute))
}

func TestClearedInsideDebounceSendsNothing(t *testing.T) {
	e := newEnv(t)
	e.att.items = []attention.Item{disk}
	want(t, e.sweep(0))
	e.att.items = nil
	want(t, e.sweep(time.Minute))
	want(t, e.sweep(time.Minute))
	e.att.items = []attention.Item{disk}
	want(t, e.sweep(time.Minute)) // a fresh incident starts its own debounce
}

func TestFlapInsideClearWindowStaysOneIncident(t *testing.T) {
	e := newEnv(t)
	e.att.items = []attention.Item{disk}
	e.sweep(0)
	want(t, e.sweep(3*time.Minute), notify.KindDisk)
	for range 5 {
		e.att.items = nil
		want(t, e.sweep(time.Minute))
		e.att.items = []attention.Item{disk}
		want(t, e.sweep(time.Minute))
	}
}

func TestDeployFailedAlertsAtOnce(t *testing.T) {
	e := newEnv(t)
	e.att.items = []attention.Item{{Kind: attention.KindDeployFailed, Tone: attention.ToneError, Project: "shop", Title: "shop: deploy #4 failed"}}
	got := e.sweep(0)
	want(t, got, notify.KindDeployFailed)
	if got[0].Project != "shop" {
		t.Fatalf("project %q", got[0].Project)
	}
}

func TestRealertDailyForWarningsOnly(t *testing.T) {
	e := newEnv(t)
	e.att.items = []attention.Item{disk, {Kind: attention.KindUpdate, Tone: attention.ToneInfo, Title: "v0.64 is out"}}
	want(t, e.sweep(0), notify.KindUpdate)
	want(t, e.sweep(3*time.Minute), notify.KindDisk)
	want(t, e.sweep(23*time.Hour))
	got := e.sweep(time.Hour)
	want(t, got, notify.KindDisk)
	if got[0].Title != "still open: disk 91% full" {
		t.Fatalf("title %q", got[0].Title)
	}
	want(t, e.sweep(time.Hour))
}

func TestRestartDoesNotRepeatAnAlert(t *testing.T) {
	e := newEnv(t)
	e.att.items = []attention.Item{disk}
	e.sweep(0)
	want(t, e.sweep(3*time.Minute), notify.KindDisk)
	e.restart()
	want(t, e.sweep(time.Minute))
	e.att.items = nil
	e.sweep(time.Minute)
	want(t, e.sweep(time.Minute), notify.KindDisk+"+recovered")
}

func TestListFailureClearsNothing(t *testing.T) {
	e := newEnv(t)
	e.att.items = []attention.Item{disk, {Kind: attention.KindNoBackup, Tone: attention.ToneWarn, Project: "shop", Title: "no backups"}}
	e.sweep(0)
	want(t, e.sweep(3*time.Minute), notify.KindDisk, notify.KindNoBackup) // sorted by key

	e.att.items, e.att.err = nil, errors.New("sqlite busy")
	want(t, e.sweep(time.Minute))
	want(t, e.sweep(time.Minute))

	// The list works but Docker does not: no_backup needs the container
	// list, so only the disk recovers.
	e.att.err = nil
	e.ag.Fail = map[string]error{"docker.list": errors.New("docker down")}
	e.sweep(time.Minute)
	want(t, e.sweep(time.Minute), notify.KindDisk+"+recovered")
	want(t, e.sweep(time.Minute))
}

func crashing(state string) agent.Container {
	return agent.Container{ID: "c1", Name: "shop-web-1", State: state,
		ComposeProject: "shop", ComposeService: "web"}
}

func (e *env) die(code string) {
	e.svc.dockerEvent(context.Background(), agent.DockerEvent{Action: "die", ID: "c1", Name: "shop-web-1",
		Project: "shop", Service: "web", ExitCode: code})
}

// A crash-looping container gives one alert and one recovery, not one per
// restart, and its die events do not add a second "container died" alert.
func TestCrashLoopIsOneAlertAndOneRecovery(t *testing.T) {
	e := newEnv(t)
	var all []string
	e.ag.Containers = []agent.Container{crashing("restarting")}
	for range 20 {
		e.die("1")
		e.die("1")
		all = append(all, kinds(e.sweep(30*time.Second))...)
	}
	// It settles and stays up for a whole window.
	e.ag.Containers = []agent.Container{crashing("running")}
	for range 14 {
		all = append(all, kinds(e.sweep(time.Minute))...)
	}
	if len(all) != 2 || all[0] != notify.KindRestartLoop || all[1] != notify.KindRestartLoop+"+recovered" {
		t.Fatalf("sent %v, want one restart_loop and one recovery", all)
	}
}

func TestContainerDied(t *testing.T) {
	e := newEnv(t)
	e.ag.Containers = []agent.Container{crashing("exited")}
	e.die("137")
	want(t, e.sweep(time.Minute))
	got := e.sweep(2 * time.Minute)
	want(t, got, notify.KindContainerDied)
	if got[0].Title != "web stopped with exit code 137" {
		t.Fatalf("title %q", got[0].Title)
	}
	e.ag.Containers = []agent.Container{crashing("running")}
	e.sweep(time.Minute)
	want(t, e.sweep(time.Minute), notify.KindContainerDied+"+recovered")
}

// After a panel restart the crash history is gone, so an open loop stays
// open until the event stream has watched a whole quiet window.
func TestOpenLoopSurvivesRestartUntilAQuietWindow(t *testing.T) {
	e := newEnv(t)
	e.ag.Containers = []agent.Container{crashing("restarting")}
	for range 3 {
		e.die("1")
	}
	want(t, e.sweep(0))
	want(t, e.sweep(3*time.Minute), notify.KindRestartLoop)
	e.restart()
	e.svc.watchSince = e.clock
	for range 9 {
		want(t, e.sweep(time.Minute))
	}
	e.sweep(time.Minute)
	want(t, e.sweep(time.Minute), notify.KindRestartLoop+"+recovered")
}

func TestStoppedOnPurposeIsNotACrash(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.ag.Containers = []agent.Container{crashing("exited")}
	e.svc.dockerEvent(ctx, agent.DockerEvent{Action: "kill", ID: "c1", Project: "shop", Service: "web"})
	e.svc.dockerEvent(ctx, agent.DockerEvent{Action: "die", ID: "c1", Project: "shop", Service: "web", ExitCode: "143"})
	e.svc.dockerEvent(ctx, agent.DockerEvent{Action: "die", ID: "c2", Project: "shop", Service: "web", ExitCode: "0"})
	e.svc.dockerEvent(ctx, agent.DockerEvent{Action: "die", ID: "c3", Name: "other", ExitCode: "1"})
	want(t, e.sweep(5*time.Minute))
}

func TestUnhealthy(t *testing.T) {
	e := newEnv(t)
	c := crashing("running")
	c.Health = "unhealthy"
	e.ag.Containers = []agent.Container{c}
	e.sweep(0)
	want(t, e.sweep(3*time.Minute), notify.KindUnhealthy)
	c.Health = "healthy"
	e.ag.Containers = []agent.Container{c}
	e.sweep(time.Minute)
	want(t, e.sweep(time.Minute), notify.KindUnhealthy+"+recovered")
}

func TestCertificateExpiring(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p, err := e.q.CreateProject(ctx, db.CreateProjectParams{Name: "shop", Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"shop.example.com", "ok.example.com"} {
		if _, err := e.q.CreateDomain(ctx, db.CreateDomainParams{ProjectID: p.ID, Hostname: h, Service: "web", ContainerPort: 80}); err != nil {
			t.Fatal(err)
		}
	}
	e.ag.Probes = map[string]agent.DomainProbe{
		"shop.example.com": {Cert: &agent.CertInfo{NotAfter: e.clock.Add(10 * 24 * time.Hour)}},
		"ok.example.com":   {Cert: &agent.CertInfo{NotAfter: e.clock.Add(60 * 24 * time.Hour)}},
	}
	want(t, e.sweep(0)) // no check has run yet
	e.svc.CheckCerts(ctx)
	e.sweep(0)
	got := e.sweep(3 * time.Minute)
	want(t, got, notify.KindCertExpiring)
	if got[0].Title != "certificate for shop.example.com expires 02/12/2026" || got[0].Project != "shop" {
		t.Fatalf("got %+v", got[0])
	}
	// A dial that fails keeps the last reading rather than clearing it.
	e.ag.Probes["shop.example.com"] = agent.DomainProbe{CertError: "timeout"}
	e.svc.CheckCerts(ctx)
	want(t, e.sweep(time.Minute))
	want(t, e.sweep(time.Minute))
}

func TestDeploySucceededMessage(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p, _ := e.q.CreateProject(ctx, db.CreateProjectParams{Name: "shop", Source: "manual"})
	d, err := e.q.CreateDeployment(ctx, db.CreateDeploymentParams{ProjectID: p.ID, ProjectID_2: p.ID, TriggeredBy: "manual", RollbackOf: sql.NullInt64{}})
	if err != nil {
		t.Fatal(err)
	}
	done := func(status string) events.Event {
		return events.Event{Topic: "deployment", Type: "deployment.done", Resource: itoa(d.ID),
			Data: map[string]any{"message": status}}
	}
	e.svc.deployDone(ctx, done("failed"))
	want(t, e.sink.take())
	e.svc.deployDone(ctx, done("succeeded"))
	got := e.sink.take()
	want(t, got, notify.KindDeploySucceeded)
	if got[0].Project != "shop" {
		t.Fatalf("project %q", got[0].Project)
	}
}

func TestSettingsRanges(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	def := LoadSettingsJSON(ctx, e.q)
	if def != (SettingsJSON{120, 24, 3, 10, 14, 85}) {
		t.Fatalf("defaults %+v", def)
	}
	bad := def
	bad.CertDays = 30
	if err := SaveSettings(ctx, e.q, bad); err == nil {
		t.Fatal("cert days 30 accepted")
	}
	ok := def
	ok.DebounceSeconds, ok.DiskPercent = 0, 90
	if err := SaveSettings(ctx, e.q, ok); err != nil {
		t.Fatal(err)
	}
	if got := LoadSettingsJSON(ctx, e.q); got != ok {
		t.Fatalf("saved %+v, read %+v", ok, got)
	}
	// Zero debounce alerts on first sight.
	e.att.items = []attention.Item{disk}
	want(t, e.sweep(0), notify.KindDisk)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
