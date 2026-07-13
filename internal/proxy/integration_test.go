//go:build integration

// Real-Caddy integration: starts caddy with a user-owned config, deploys a
// real nginx project, syncs domains, and proves (a) traffic routes through
// Caddy to the container and (b) the user's own Caddy routes are never
// clobbered by Windlass (plan risk #2).
package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/agent/local"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/migrations"
)

// userCaddyConfig simulates an admin's hand-written config: one server on
// :18080 with a marker route Windlass must not touch.
const userCaddyConfig = `{
  "admin": {"listen": "127.0.0.1:12019"},
  "apps": {
    "http": {
      "servers": {
        "usersrv": {
          "listen": [":18080"],
          "automatic_https": {"disable": true},
          "routes": [
            {
              "match": [{"host": ["user-owned.example.com"]}],
              "handle": [{"handler": "static_response", "body": "user route intact"}],
              "terminal": true
            }
          ]
        }
      }
    }
  }
}`

func TestCaddyRoutingEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("caddy"); err != nil {
		t.Skip("caddy not installed")
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "caddy.json")
	if err := os.WriteFile(cfgPath, []byte(userCaddyConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	caddy := exec.CommandContext(ctx, "caddy", "run", "--config", cfgPath)
	caddy.Stdout = io.Discard
	caddy.Stderr = io.Discard
	if err := caddy.Start(); err != nil {
		t.Fatalf("start caddy: %v", err)
	}
	t.Cleanup(func() { caddy.Process.Kill() })

	// Wait for the admin API.
	admin := "http://127.0.0.1:12019"
	for i := 0; ; i++ {
		if resp, err := http.Get(admin + "/config/"); err == nil {
			resp.Body.Close()
			break
		}
		if i > 50 {
			t.Fatal("caddy admin did not come up")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Stack setup with the real agent pointed at our Caddy.
	sqlDB, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := store.Migrate(sqlDB, migrations.FS); err != nil {
		t.Fatal(err)
	}
	q := db.New(sqlDB)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ag, err := local.New(local.Config{
		ProjectsDir: filepath.Join(dir, "projects"),
		CaddyAdmin:  admin,
	})
	if err != nil {
		t.Fatal(err)
	}
	box, _ := secrets.New(bytes.Repeat([]byte{1}, 32))
	bus := events.NewBus()
	proj := projects.New(q, ag, box, bus, logger)
	svc := New(q, ag, bus, logger)

	// Deploy a real nginx container via plain compose (pipeline covered
	// elsewhere; here we exercise routing).
	const name = "windlass-it-caddy"
	t.Cleanup(func() {
		exec.Command("docker", "compose", "-p", name, "down", "--remove-orphans").Run()
	})
	p, err := proj.Create(ctx, projects.CreateReq{Name: name, Compose: "services:\n  web:\n    image: nginx:alpine\n"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ag.Compose().Up(ctx, agent.ComposeUpReq{Project: name}, nil); err != nil {
		t.Fatalf("compose up: %v", err)
	}

	// Add a domain and sync routes.
	if _, err := svc.Add(ctx, p.ID, "it.example.com", "web", 80); err != nil {
		t.Fatalf("add domain: %v", err)
	}
	if err := svc.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Traffic for the Windlass domain reaches nginx through Caddy.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:18080/", nil)
	req.Host = "it.example.com"
	var body string
	for i := 0; i < 20; i++ { // container may still be booting
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			body = string(b)
			if resp.StatusCode == http.StatusOK && strings.Contains(body, "nginx") {
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !strings.Contains(body, "nginx") {
		t.Fatalf("windlass domain did not route to nginx; body: %.200s", body)
	}

	// The user's own route still works — Windlass didn't clobber it.
	req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:18080/", nil)
	req2.Host = "user-owned.example.com"
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("user route request: %v", err)
	}
	b2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if !strings.Contains(string(b2), "user route intact") {
		t.Fatalf("user-owned route was clobbered; body: %.200s", b2)
	}

	// Re-sync (idempotent PATCH path) keeps both working.
	if err := svc.Sync(ctx); err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	current, err := ag.Proxy().CurrentRoutes(ctx)
	if err != nil || len(current) != 1 || current[0].Hostname != "it.example.com" {
		t.Fatalf("current routes = %+v, %v", current, err)
	}

	fmt.Println("caddy integration ok")
}
