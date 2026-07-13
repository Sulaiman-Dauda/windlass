package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const sseHeartbeat = 15 * time.Second

// sseWriter wraps the response for Server-Sent Events with per-event
// flushing (required for streaming through proxies).
type sseWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

func newSSE(w http.ResponseWriter) (*sseWriter, bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	f.Flush()
	return &sseWriter{w: w, f: f}, true
}

func (s *sseWriter) event(id, eventType string, data any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if id != "" {
		fmt.Fprintf(s.w, "id: %s\n", id)
	}
	if eventType != "" {
		fmt.Fprintf(s.w, "event: %s\n", eventType)
	}
	fmt.Fprintf(s.w, "data: %s\n\n", body)
	s.f.Flush()
	return nil
}

func (s *sseWriter) heartbeat() {
	fmt.Fprint(s.w, ": hb\n\n")
	s.f.Flush()
}

// lastEventID parses the SSE reconnect header (or ?after= fallback).
func lastEventID(r *http.Request) int64 {
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("after")
	}
	n, _ := strconv.ParseInt(raw, 10, 64)
	return n
}

// handleGlobalEvents streams the platform event bus. ?topics=a,b filters.
func (a *API) handleGlobalEvents(w http.ResponseWriter, r *http.Request) {
	sse, ok := newSSE(w)
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal", "streaming unsupported")
		return
	}

	var topics []string
	if t := r.URL.Query().Get("topics"); t != "" {
		topics = strings.Split(t, ",")
	}
	ch, cancel := a.Bus.Subscribe(topics...)
	defer cancel()

	ticker := time.NewTicker(sseHeartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			sse.heartbeat()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := sse.event("", ev.Type, ev); err != nil {
				return
			}
		}
	}
}

// streamStoredThenLive replays persisted deployment events after fromSeq,
// then live-tails the bus until the deployment finishes or the client goes
// away. Reconnects are lossless via Last-Event-ID.
func (a *API) streamDeploymentEvents(w http.ResponseWriter, r *http.Request, deploymentID, fromSeq int64) {
	sse, ok := newSSE(w)
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal", "streaming unsupported")
		return
	}

	// Subscribe before replay so nothing falls in the gap; dedup via seq.
	ch, cancel := a.Bus.Subscribe("deployment")
	defer cancel()

	lastSent := fromSeq
	replay := func() bool /* terminal */ {
		stored, err := a.Deploy.Events(r.Context(), deploymentID, lastSent)
		if err != nil {
			return false
		}
		terminal := false
		for _, ev := range stored {
			sse.event(strconv.FormatInt(ev.Seq, 10), "deployment."+ev.Type, map[string]any{
				"seq": ev.Seq, "type": ev.Type, "message": ev.Message, "ts": ev.Ts,
			})
			lastSent = ev.Seq
			if ev.Type == "done" {
				terminal = true
			}
		}
		return terminal
	}

	if replay() {
		return // deployment already finished; replay was the whole story
	}

	ticker := time.NewTicker(sseHeartbeat)
	defer ticker.Stop()

	idStr := strconv.FormatInt(deploymentID, 10)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			sse.heartbeat()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev.Resource != idStr {
				continue
			}
			// The bus is lossy by design; the store is not. Re-read from the
			// store so ordering and completeness are guaranteed.
			if replay() {
				return
			}
		}
	}
}
