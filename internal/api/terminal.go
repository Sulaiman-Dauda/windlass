package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/auth"
)

// Terminal protocol: client sends JSON text frames
//
//	{"type":"input","data":"<text>"} | {"type":"resize","cols":N,"rows":N}
//
// and receives raw binary frames of terminal output. This is the only
// WebSocket in Windlass; everything else streams over SSE.
type termClientMsg struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

func (a *API) handleTerminal(w http.ResponseWriter, r *http.Request) {
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
		if (service == "" || c.ComposeService == service) && c.State == "running" {
			target = &containers[i]
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "not_found", "no running container for this service")
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	sess, err := a.Agent.Exec().Start(ctx, agent.ExecReq{
		ContainerID: target.ID,
		Cmd:         []string{"/bin/sh", "-c", "command -v bash >/dev/null && exec bash || exec sh"},
		TTY:         true,
		Cols:        120,
		Rows:        32,
	})
	if err != nil {
		conn.Close(websocket.StatusInternalError, "exec failed: "+err.Error())
		return
	}
	defer sess.Close()

	user, _ := auth.UserFrom(ctx)
	a.Audit.Write(ctx, user.ID, "terminal.open", "project", name, remoteIP(r),
		map[string]string{"container": target.Name})

	// Container → browser.
	go func() {
		defer cancel()
		for chunk := range sess.Output() {
			writeCtx, done := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Write(writeCtx, websocket.MessageBinary, chunk)
			done()
			if err != nil {
				return
			}
		}
		conn.Close(websocket.StatusNormalClosure, "session ended")
	}()

	// Browser → container.
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg termClientMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "input":
			if err := sess.Write([]byte(msg.Data)); err != nil {
				return
			}
		case "resize":
			if msg.Cols > 0 && msg.Rows > 0 {
				_ = sess.Resize(msg.Cols, msg.Rows)
			}
		}
	}
}
