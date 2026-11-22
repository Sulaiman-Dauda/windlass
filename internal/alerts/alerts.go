// Package alerts decides when something on the server is worth an alert and
// hands it to internal/notify, which delivers it.
//
// Most conditions are levels, not moments: a disk is full, a deploy's last
// attempt failed, a container is unhealthy. One sweep a minute collects every
// condition that holds now, from the Needs you list, the container list and
// the last certificate check, and compares it with the open incidents in
// SQLite. A condition alerts once it has held for the debounce, sends one
// recovery message when it has been gone for two sweeps in a row, and
// re-alerts at most once per re-alert period while it stays open. Incidents
// live in SQLite so a restart of the panel neither repeats nor loses them.
//
// Two inputs are moments. A container that dies with a non-zero exit, and was
// not stopped on purpose, opens an incident that stays open while its
// service has no running container. A finished deploy triggers a sweep at
// once and, when it succeeded, an opt-in "deploy succeeded" message.
//
// Nothing here changes the thing it watches, and a failed alert is only
// logged: it is never a deploy's failure.
package alerts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/attention"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/notify"
	"github.com/windlass-dev/windlass/internal/store/db"
)

// Notifier queues an alert. notify.Service is the real one.
type Notifier interface {
	Enqueue(ctx context.Context, e notify.Event) error
}

// Attention is the part of the Needs you service a sweep reads.
type Attention interface {
	List(ctx context.Context) (attention.List, error)
	Invalidate()
}

// condition is one thing wrong now.
type condition struct {
	Key, Kind, Project, Title, Message, Tone string
}

// stoppedGrace is how long after a kill or stop a die event is taken as
// meant rather than a crash.
const stoppedGrace = time.Minute

// clearAfter is how many sweeps in a row a condition must be missing before
// its recovery is sent, so one failed reading does not end an incident.
const clearAfter = 2

const timeLayout = "2006-01-02T15:04:05.000Z"

type Service struct {
	q      *db.Queries
	agent  agent.Agent
	att    Attention
	notify Notifier
	bus    *events.Bus
	logger *slog.Logger

	// SweepEvery and CertEvery are how often the sweep and the certificate
	// check run; tests shorten them.
	SweepEvery time.Duration
	CertEvery  time.Duration
	now        func() time.Time

	mu       sync.Mutex
	missed   map[string]int
	dies     map[string][]time.Time // crash times by project|service
	stopping map[string]time.Time   // container ID -> kill or stop
	// watchSince is when the Docker event stream last attached; zero while
	// it is down, when restart loops can be neither seen nor ruled out.
	watchSince time.Time
	certs      map[string]certSeen // by hostname
	certsRan   bool

	sweepNow chan struct{}
}

type certSeen struct {
	project  string
	notAfter time.Time
}

func New(q *db.Queries, a agent.Agent, att Attention, n Notifier, bus *events.Bus, logger *slog.Logger) *Service {
	return &Service{q: q, agent: a, att: att, notify: n, bus: bus, logger: logger,
		SweepEvery: time.Minute, CertEvery: 24 * time.Hour, now: time.Now,
		missed: map[string]int{}, dies: map[string][]time.Time{},
		stopping: map[string]time.Time{}, certs: map[string]certSeen{},
		sweepNow: make(chan struct{}, 1)}
}

// Run watches until ctx ends.
func (s *Service) Run(ctx context.Context) {
	go s.watchDocker(ctx)
	go s.watchCerts(ctx)

	ch, cancel := s.bus.Subscribe("deployment")
	defer cancel()
	tick := time.NewTicker(s.SweepEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			if e.Type == "deployment.done" {
				s.deployDone(ctx, e)
			}
		case <-s.sweepNow:
			s.Sweep(ctx)
		case <-tick.C:
			s.Sweep(ctx)
		}
	}
}

func (s *Service) requestSweep() {
	select {
	case s.sweepNow <- struct{}{}:
	default:
	}
}

// deployDone sends "deploy succeeded" and sweeps, so a failed deploy alerts
// without waiting for the next tick.
func (s *Service) deployDone(ctx context.Context, e events.Event) {
	s.att.Invalidate()
	s.requestSweep()
	data, _ := e.Data.(map[string]any)
	if status, _ := data["message"].(string); status != "succeeded" {
		return
	}
	id, err := strconv.ParseInt(e.Resource, 10, 64)
	if err != nil {
		return
	}
	d, err := s.q.GetDeploymentByID(ctx, id)
	if err != nil {
		return
	}
	project := s.projectName(ctx, d.ProjectID)
	if project == "" {
		return
	}
	s.send(ctx, notify.Event{Kind: notify.KindDeploySucceeded, Project: project,
		Title:   fmt.Sprintf("deploy #%d succeeded", d.Number),
		Message: fmt.Sprintf("Deploy #%d of %s finished and is live.", d.Number, project)})
}

func (s *Service) projectName(ctx context.Context, id int64) string {
	ps, err := s.q.ListProjects(ctx)
	if err != nil {
		return ""
	}
	for _, p := range ps {
		if p.ID == id {
			return p.Name
		}
	}
	return ""
}

func (s *Service) send(ctx context.Context, e notify.Event) {
	if err := s.notify.Enqueue(ctx, e); err != nil {
		s.logger.Warn("alerts: queue alert", "event", e.Kind, "project", e.Project, "error", err)
	}
}

func (s *Service) watchDocker(ctx context.Context) {
	for ctx.Err() == nil {
		s.mu.Lock()
		s.watchSince = s.now().UTC()
		s.mu.Unlock()
		err := s.agent.Docker().Events(ctx, func(ev agent.DockerEvent) { s.dockerEvent(ctx, ev) })
		s.mu.Lock()
		s.watchSince = time.Time{}
		s.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		s.logger.Warn("alerts: docker event stream ended; retrying", "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// dockerEvent records each crash for restart loops, and opens a
// container_died incident for one with a non-zero exit. Compose stopping or
// recreating a container sends kill or stop first, so a die soon after one
// of those is not a crash.
func (s *Service) dockerEvent(ctx context.Context, ev agent.DockerEvent) {
	if ev.Project == "" || ev.Service == "" {
		return
	}
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	switch ev.Action {
	case "kill", "stop":
		s.stopping[ev.ID] = now
		return
	case "destroy":
		delete(s.stopping, ev.ID)
		return
	case "die":
	default:
		return
	}
	stoppedAt, stopped := s.stopping[ev.ID]
	if stopped && now.Sub(stoppedAt) < stoppedGrace {
		return
	}
	svc := ev.Project + "|" + ev.Service
	s.dies[svc] = append(s.dies[svc], now)
	if ev.ExitCode == "" || ev.ExitCode == "0" {
		return
	}
	key := diedKey(ev.Project, ev.Service)
	at := now.Format(timeLayout)
	if err := s.q.UpsertAlertIncident(ctx, db.UpsertAlertIncidentParams{
		Key: key, Kind: notify.KindContainerDied, Project: ev.Project,
		Title:       fmt.Sprintf("%s stopped with exit code %s", ev.Service, ev.ExitCode),
		Message:     fmt.Sprintf("Container %s of service %s exited with code %s and is not running. Its logs say why.", ev.Name, ev.Service, ev.ExitCode),
		FirstSeenAt: at, SeenAt: at,
	}); err != nil {
		s.logger.Warn("alerts: record container died", "error", err)
	}
}

func diedKey(project, service string) string {
	return notify.KindContainerDied + "|" + project + "|" + service
}

func (s *Service) watchCerts(ctx context.Context) {
	// The first check waits a minute so a restart does not dial every
	// domain while the panel is starting.
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.CheckCerts(ctx)
			t.Reset(s.CertEvery)
		}
	}
}

// CheckCerts dials every domain once and keeps each certificate's expiry
// for the sweep. A domain that cannot be reached keeps its last reading;
// the Domains tab reports the dial failure.
func (s *Service) CheckCerts(ctx context.Context) {
	domains, err := s.q.ListAllDomains(ctx)
	if err != nil {
		s.logger.Warn("alerts: list domains", "error", err)
		return
	}
	seen := map[string]certSeen{}
	for _, d := range domains {
		pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		probe, err := s.agent.Host().ProbeDomain(pctx, d.Hostname)
		cancel()
		if err == nil && probe.Cert != nil {
			seen[d.Hostname] = certSeen{project: d.ProjectName, notAfter: probe.Cert.NotAfter}
			continue
		}
		s.mu.Lock()
		old, ok := s.certs[d.Hostname]
		s.mu.Unlock()
		if ok {
			seen[d.Hostname] = old
		}
	}
	s.mu.Lock()
	s.certs, s.certsRan = seen, true
	s.mu.Unlock()
	s.requestSweep()
}

// attentionKinds are the Needs you kinds a sweep takes from the list.
// noBackup and drift need the container list, so they only count as
// checked when it was read.
var attentionKinds = []string{
	notify.KindDeployFailed, notify.KindDisk, notify.KindProxy, notify.KindUpdate,
	notify.KindDNS, notify.KindCaddyResume, notify.KindBinaryOwner,
}

// Sweep compares what is wrong now with the open incidents and queues the
// alerts, recoveries and re-alerts that follow.
func (s *Service) Sweep(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	cfg := LoadSettings(ctx, s.q)

	conds := map[string]condition{}
	checked := map[string]bool{}
	add := func(c condition) {
		if _, ok := conds[c.Key]; !ok {
			conds[c.Key] = c
		}
	}

	containers, cerr := s.agent.Docker().ListContainers(ctx, agent.ContainerFilter{})
	if list, err := s.att.List(ctx); err == nil {
		for _, k := range attentionKinds {
			checked[k] = true
		}
		if cerr == nil {
			checked[notify.KindNoBackup], checked[notify.KindDrift] = true, true
		}
		for _, it := range list.Items {
			if !checked[it.Kind] {
				continue
			}
			add(condition{Key: it.Kind + "|" + it.Project, Kind: it.Kind, Project: it.Project,
				Title: it.Title, Message: it.Detail, Tone: it.Tone})
		}
	} else {
		s.logger.Debug("alerts: needs you list", "error", err)
	}

	open, err := s.q.ListAlertIncidents(ctx)
	if err != nil {
		s.logger.Warn("alerts: list incidents", "error", err)
		return
	}

	if cerr == nil {
		for _, k := range []string{notify.KindUnhealthy, notify.KindRestartLoop, notify.KindContainerDied} {
			checked[k] = true
		}
		for _, c := range s.containerConditions(now, cfg, containers, open) {
			add(c)
		}
	}
	if s.certsRan {
		checked[notify.KindCertExpiring] = true
		for host, c := range s.certs {
			left := c.notAfter.Sub(now)
			if left >= time.Duration(cfg.CertDays)*24*time.Hour {
				continue
			}
			title := fmt.Sprintf("certificate for %s expires %s", host, c.notAfter.Format("02/01/2006"))
			if left <= 0 {
				title = fmt.Sprintf("certificate for %s has expired", host)
			}
			add(condition{Key: notify.KindCertExpiring + "|" + c.project + "|" + host,
				Kind: notify.KindCertExpiring, Project: c.project, Title: title, Tone: attention.ToneError,
				Message: "Caddy renews 30 days before expiry, so renewal is failing. The Domains tab checks DNS, ports and the certificate."})
		}
	}

	at := now.Format(timeLayout)
	byKey := map[string]db.AlertIncident{}
	for _, inc := range open {
		byKey[inc.Key] = inc
	}

	for _, key := range slices.Sorted(maps.Keys(conds)) {
		c := conds[key]
		delete(s.missed, key)
		inc, existed := byKey[key]
		if !existed {
			inc = db.AlertIncident{Key: key, FirstSeenAt: at}
		}
		if err := s.q.UpsertAlertIncident(ctx, db.UpsertAlertIncidentParams{
			Key: key, Kind: c.Kind, Project: c.Project, Title: c.Title, Message: c.Message,
			FirstSeenAt: inc.FirstSeenAt, SeenAt: at,
		}); err != nil {
			s.logger.Warn("alerts: record incident", "error", err)
			continue
		}
		first := parseTime(inc.FirstSeenAt, now)
		switch {
		case !inc.AlertedAt.Valid:
			if now.Sub(first) < cfg.debounce(c.Kind) {
				continue
			}
			s.send(ctx, notify.Event{Kind: c.Kind, Project: c.Project, Title: c.Title, Message: c.Message})
		case realerts(c.Tone) && now.Sub(parseTime(inc.AlertedAt.String, now)) >= cfg.Realert:
			s.send(ctx, notify.Event{Kind: c.Kind, Project: c.Project, Title: "still open: " + c.Title,
				Message: fmt.Sprintf("Open for %s. %s", since(first, now), c.Message)})
		default:
			continue
		}
		s.mark(ctx, key, at)
	}

	for _, inc := range open {
		if _, ok := conds[inc.Key]; ok || !checked[inc.Kind] {
			continue
		}
		if !inc.AlertedAt.Valid {
			// Cleared inside the debounce: nothing was sent, so nothing to undo.
			s.drop(ctx, inc.Key)
			continue
		}
		s.missed[inc.Key]++
		if s.missed[inc.Key] < clearAfter {
			continue
		}
		s.send(ctx, notify.Event{Kind: inc.Kind, Project: inc.Project, Recovered: true,
			Title:   "cleared: " + inc.Title,
			Message: fmt.Sprintf("This cleared after %s.", since(parseTime(inc.FirstSeenAt, now), now))})
		s.drop(ctx, inc.Key)
	}
}

func (s *Service) mark(ctx context.Context, key, at string) {
	if err := s.q.MarkAlertIncidentAlerted(ctx, db.MarkAlertIncidentAlertedParams{
		AlertedAt: sql.NullString{String: at, Valid: true}, Key: key}); err != nil {
		s.logger.Warn("alerts: mark incident", "error", err)
	}
}

func (s *Service) drop(ctx context.Context, key string) {
	delete(s.missed, key)
	if err := s.q.DeleteAlertIncident(ctx, key); err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.logger.Warn("alerts: drop incident", "error", err)
	}
}

// containerConditions finds unhealthy containers, restart loops and crashed
// services. A restart loop is RestartCount crashes of one service
// inside RestartWindow, counted from Docker die events. A crash recorded by dockerEvent stays a condition
// while its service has no running container, and gives way to a restart
// loop on the same service so a crash loop is one alert, not two.
func (s *Service) containerConditions(now time.Time, cfg Settings, cs []agent.Container, open []db.AlertIncident) []condition {
	var out []condition
	running := map[string]bool{}
	listed := map[string]bool{}
	looping := map[string]bool{}
	for _, c := range cs {
		if c.ComposeProject == "" {
			continue
		}
		svc := c.ComposeProject + "|" + c.ComposeService
		listed[svc] = true
		if c.State == "running" {
			running[svc] = true
		}
		if c.Health == "unhealthy" {
			out = append(out, condition{Key: notify.KindUnhealthy + "|" + c.ComposeProject + "|" + c.Name,
				Kind: notify.KindUnhealthy, Project: c.ComposeProject, Tone: attention.ToneWarn,
				Title:   fmt.Sprintf("%s is unhealthy", c.ComposeService),
				Message: fmt.Sprintf("Container %s fails its health check. Its logs and the health check command say why.", c.Name)})
		}
	}

	// Restart loops come from the crash times dockerEvent records. Docker's
	// container list carries no restart count, and inspecting every
	// container each minute would cost a call per container.
	windowStart := now.Add(-cfg.RestartWindow)
	watchedWindow := !s.watchSince.IsZero() && !s.watchSince.After(windowStart)
	for svc, times := range s.dies {
		for len(times) > 0 && times[0].Before(windowStart) {
			times = times[1:]
		}
		if len(times) == 0 {
			delete(s.dies, svc)
			continue
		}
		s.dies[svc] = times
	}
	openLoop := map[string]db.AlertIncident{}
	for _, inc := range open {
		if inc.Kind == notify.KindRestartLoop {
			openLoop[strings.TrimPrefix(inc.Key, notify.KindRestartLoop+"|")] = inc
		}
	}
	for svc := range listed {
		project, service, _ := strings.Cut(svc, "|")
		n := len(s.dies[svc])
		inc, isOpen := openLoop[svc]
		switch {
		case n >= cfg.RestartCount:
		case isOpen && !watchedWindow:
			// Not watched for a whole window (the panel restarted or the
			// event stream dropped): an open loop stays open until a full
			// quiet window proves it over.
			out = append(out, condition{Key: inc.Key, Kind: inc.Kind, Project: inc.Project,
				Title: inc.Title, Message: inc.Message, Tone: attention.ToneError})
			looping[svc] = true
			continue
		default:
			continue
		}
		looping[svc] = true
		out = append(out, condition{Key: notify.KindRestartLoop + "|" + svc, Kind: notify.KindRestartLoop,
			Project: project, Tone: attention.ToneError,
			Title:   fmt.Sprintf("%s is in a restart loop", service),
			Message: fmt.Sprintf("Service %s exited %d times in %s. Its logs say why it keeps exiting.", service, n, minutes(cfg.RestartWindow))})
	}
	for id, at := range s.stopping {
		if now.Sub(at) > stoppedGrace {
			delete(s.stopping, id)
		}
	}
	for _, inc := range open {
		if inc.Kind != notify.KindContainerDied {
			continue
		}
		svc := strings.TrimPrefix(inc.Key, notify.KindContainerDied+"|")
		if running[svc] || !listed[svc] || looping[svc] {
			continue
		}
		out = append(out, condition{Key: inc.Key, Kind: inc.Kind, Project: inc.Project,
			Title: inc.Title, Message: inc.Message, Tone: attention.ToneError})
	}
	return out
}

// realerts says whether an incident of this tone is repeated while open. An
// available update or drift is news once, not every day.
func realerts(tone string) bool { return tone == attention.ToneError || tone == attention.ToneWarn }

func parseTime(v string, fallback time.Time) time.Time {
	t, err := time.Parse(timeLayout, v)
	if err != nil {
		return fallback
	}
	return t
}

func since(from, to time.Time) string {
	d := to.Sub(from).Round(time.Minute)
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return minutes(d)
	case d < 48*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", h)
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}

func minutes(d time.Duration) string {
	m := int(d.Minutes())
	if m == 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", m)
}
