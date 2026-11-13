package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/agent"
)

// defaultLogTail is the history sent when ?tail= is absent: the same 200
// lines the agent defaults to, now across every container shown.
const defaultLogTail = 200

// maxMergedLogTail caps the history of a merged stream, which is held in
// memory to sort it. One container streams straight through and is not
// capped, as before.
const maxMergedLogTail = 5000

// logEvent is one SSE "log" event. Time is Docker's timestamp for the line.
type logEvent struct {
	Stream    string    `json:"stream"`
	Text      string    `json:"text"`
	Time      time.Time `json:"time"`
	Service   string    `json:"service"`
	Container string    `json:"container"`
}

// handleProjectLogs streams container logs over SSE.
//
// ?service= selects one Compose service; without it every container of the
// project is shown, merged by time. A service scaled to several containers
// is merged the same way. ?follow=false returns the tail and closes;
// ?tail=N is how many lines of history to send in all.
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
	var targets []agent.Container
	for _, c := range containers {
		if service == "" || c.ComposeService == service {
			targets = append(targets, c)
		}
	}
	if len(targets) == 0 {
		writeError(w, http.StatusNotFound, "not_found", "no container for this service")
		return
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })

	follow := r.URL.Query().Get("follow") != "false"
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	if tail <= 0 {
		tail = defaultLogTail
	}

	sse, ok := newSSE(w)
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal", "streaming unsupported")
		return
	}
	send := func(c agent.Container, line agent.LogLine) {
		_ = sse.event("", "log", logEvent{Stream: line.Stream, Text: line.Text, Time: line.Time,
			Service: c.ComposeService, Container: c.Name})
	}
	fail := func(c agent.Container, err error) {
		text := err.Error()
		if len(targets) > 1 {
			text = c.Name + ": " + text
		}
		_ = sse.event("", "error", map[string]string{"text": text})
	}

	// One container streams straight through, as it always has.
	if len(targets) == 1 {
		c := targets[0]
		err := a.Agent.Docker().Logs(r.Context(), c.ID, agent.LogOpts{Follow: follow, Tail: tail},
			func(line agent.LogLine) { send(c, line) })
		if err != nil && r.Context().Err() == nil {
			fail(c, err)
		}
		return
	}

	// Several: read each one's tail, merge by time and keep the newest
	// tail lines in all. Each container is read up to tail lines, so the
	// merge is exact; trimming after each one keeps at most two tails held.
	tail = min(tail, maxMergedLogTail)
	started := time.Now()
	type stamped struct {
		c    agent.Container
		line agent.LogLine
	}
	var history []stamped
	last := make(map[string]time.Time, len(targets))
	for _, c := range targets {
		err := a.Agent.Docker().Logs(r.Context(), c.ID, agent.LogOpts{Tail: tail}, func(line agent.LogLine) {
			history = append(history, stamped{c, line})
			if line.Time.After(last[c.ID]) {
				last[c.ID] = line.Time
			}
		})
		if err != nil && r.Context().Err() == nil {
			fail(c, err)
		}
		sort.SliceStable(history, func(i, j int) bool { return history[i].line.Time.Before(history[j].line.Time) })
		if len(history) > tail {
			history = append(history[:0:0], history[len(history)-tail:]...)
		}
	}
	for _, h := range history {
		send(h.c, h.line)
	}
	if !follow || r.Context().Err() != nil {
		return
	}

	// Then follow every container from the time of the last line it sent.
	// Docker includes lines at exactly since, and Podman compares at a
	// coarser precision than it reports, so the follow repeats some history:
	// drop every line not newer than last. A new line stamped exactly last
	// is lost with it, which Docker's nanosecond stamps make all but
	// impossible. Lines reach the stream in the order they arrive;
	// the SSE writer is not safe for concurrent use, so one goroutine writes.
	type event struct {
		c    agent.Container
		line agent.LogLine
		err  error
	}
	events := make(chan event, 64)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	var wg sync.WaitGroup
	for _, c := range targets {
		since, ok := last[c.ID]
		if !ok {
			since = started
		}
		_, sent := last[c.ID]
		wg.Add(1)
		go func(c agent.Container, since time.Time, sent bool) {
			defer wg.Done()
			err := a.Agent.Docker().Logs(ctx, c.ID, agent.LogOpts{Follow: true, Since: since},
				func(line agent.LogLine) {
					if line.Time.Before(since) || (sent && line.Time.Equal(since)) {
						return
					}
					select {
					case events <- event{c: c, line: line}:
					case <-ctx.Done():
					}
				})
			if err != nil && ctx.Err() == nil {
				select {
				case events <- event{c: c, err: err}:
				case <-ctx.Done():
				}
			}
		}(c, since, sent)
	}
	go func() {
		wg.Wait()
		close(events)
	}()
	for ev := range events {
		if ev.err != nil {
			fail(ev.c, ev.err)
			continue
		}
		send(ev.c, ev.line)
	}
}
