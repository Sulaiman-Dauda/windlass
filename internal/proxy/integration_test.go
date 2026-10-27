//go:build integration

// Real-Caddy integration: starts caddy with a user-owned config, deploys a
// real nginx project, syncs domains, and proves (a) traffic routes through
// Caddy to the container and (b) the user's own Caddy routes are never
// clobbered by Windlass (plan risk #2).
package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/agent/local"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/migrations"
)

// userCaddyConfig simulates hand-written HTTP config plus a TLS server using
// Caddy's local CA. Windlass must add only its owned route and certificate.
const userCaddyConfig = `{
  "admin": {"listen": "127.0.0.1:12019"},
  "apps": {
    "tls": {"automation": {"policies": [{"issuers": [{"module": "internal"}]}]}},
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
        },
        "tlssrv": {
          "listen": [":18443"],
          "automatic_https": {"disable_redirects": true},
          "routes": [],
          "tls_connection_policies": [{}]
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
	caddy.Env = append(os.Environ(), "XDG_DATA_HOME="+filepath.Join(dir, "data"),
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"))
	caddy.Stdout = io.Discard
	var caddyStderr bytes.Buffer
	caddy.Stderr = &caddyStderr
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
		if i > 150 {
			t.Fatalf("caddy admin did not come up: %s", caddyStderr.String())
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
	panelBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "windlass panel route")
	}))
	defer panelBackend.Close()
	ag, err := local.New(local.Config{
		ProjectsDir:   filepath.Join(dir, "projects"),
		CaddyAdmin:    admin,
		PanelUpstream: strings.TrimPrefix(panelBackend.URL, "http://"),
	})
	if err != nil {
		t.Fatal(err)
	}
	box, _ := secrets.New(bytes.Repeat([]byte{1}, 32))
	bus := events.NewBus()
	proj := projects.New(q, ag, box, bus, logger)
	svc := New(q, ag, proj, bus, logger)

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

	// A staging copy of the project, whose route tells crawlers to stay out.
	t.Cleanup(func() {
		exec.Command("docker", "compose", "-p", name+"-staging", "down", "--remove-orphans").Run()
	})
	stg, err := proj.CreateStagingCopy(ctx, projects.CopyReq{Live: name})
	if err != nil {
		t.Fatalf("create staging copy: %v", err)
	}
	if err := ag.Compose().Up(ctx, agent.ComposeUpReq{Project: stg.Project.Name}, nil); err != nil {
		t.Fatalf("compose up staging: %v", err)
	}
	if _, err := svc.Add(ctx, stg.Project.ID, "staging-it.example.com", "web", 80); err != nil {
		t.Fatalf("add staging domain: %v", err)
	}
	if err := svc.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Trust Caddy's generated local root and prove the full TLS handshake plus
	// reverse proxy path, rather than merely accepting an insecure certificate.
	rootPath := filepath.Join(dir, "data", "caddy", "pki", "authorities", "local", "root.crt")
	var rootPEM []byte
	for i := 0; i < 50; i++ {
		rootPEM, err = os.ReadFile(rootPath)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("read caddy local root: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootPEM) {
		t.Fatal("parse caddy local root")
	}
	httpsClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: roots, ServerName: "it.example.com", MinVersion: tls.VersionTLS12,
	}}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://127.0.0.1:18443/", nil)
	req.Host = "it.example.com"
	var body string
	for i := 0; i < 20; i++ { // container may still be booting
		resp, err := httpsClient.Do(req)
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
		t.Fatalf("windlass HTTPS domain did not route to nginx; body: %.200s", body)
	}
	robots := func(host string) (string, int) {
		t.Helper()
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs: roots, ServerName: host, MinVersion: tls.VersionTLS12,
		}}}
		r, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://127.0.0.1:18443/", nil)
		r.Host = host
		for i := 0; ; i++ {
			resp, err := client.Do(r)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK || i >= 20 {
					return resp.Header.Get("X-Robots-Tag"), resp.StatusCode
				}
			} else if i >= 20 {
				t.Fatalf("%s: %v", host, err)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	if tag, code := robots("staging-it.example.com"); code != http.StatusOK || tag != "noindex" {
		t.Errorf("staging route: status %d, X-Robots-Tag %q, want noindex", code, tag)
	}
	if tag, _ := robots("it.example.com"); tag != "" {
		t.Errorf("live route has X-Robots-Tag %q", tag)
	}

	// The Settings-managed panel hostname is a separate owned route and also
	// receives a trusted certificate without touching application/user routes.
	if err := svc.SetPanelDomain(ctx, "panel.example.com"); err != nil {
		t.Fatalf("set panel domain: %v", err)
	}
	panelClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: roots, ServerName: "panel.example.com", MinVersion: tls.VersionTLS12,
	}}}
	panelReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://127.0.0.1:18443/", nil)
	panelReq.Host = "panel.example.com"
	panelBody := ""
	for i := 0; i < 20; i++ {
		panelResp, panelErr := panelClient.Do(panelReq)
		if panelErr == nil {
			data, _ := io.ReadAll(panelResp.Body)
			panelResp.Body.Close()
			panelBody = string(data)
			if panelResp.StatusCode == http.StatusOK {
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if panelBody != "windlass panel route" {
		t.Fatalf("panel HTTPS route body = %q", panelBody)
	}

	// The user's own route still works, Windlass didn't clobber it.
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
	if err != nil || len(current) != 2 {
		t.Fatalf("current routes = %+v, %v", current, err)
	}
	for _, r := range current {
		if r.NoIndex != (r.Hostname == "staging-it.example.com") {
			t.Errorf("read back route %s with NoIndex %v", r.Hostname, r.NoIndex)
		}
	}

	// An administrator reloading Caddy loads its own config, which drops
	// every Windlass object. The route check notices and a sync restores them.
	loadResp, err := http.Post(admin+"/load", "application/json", strings.NewReader(userCaddyConfig))
	if err != nil {
		t.Fatalf("reload caddy: %v", err)
	}
	loadResp.Body.Close()
	if installed, err := ag.Proxy().RoutesInstalled(ctx); err != nil || installed {
		t.Fatalf("after a Caddy reload, installed = %v, %v", installed, err)
	}
	svc.checkRoutes(ctx)
	select {
	case <-svc.syncCh:
	default:
		t.Fatal("route check did not ask for a sync after the reload")
	}
	if err := svc.Sync(ctx); err != nil {
		t.Fatalf("sync after reload: %v", err)
	}
	body = ""
	for i := 0; i < 20; i++ {
		resp, err := httpsClient.Do(req)
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
		t.Fatalf("routes not restored after a Caddy reload; body: %.200s", body)
	}

	// A brand-new SQLite file rebuilds its application index from the same
	// stacks directory while the already-running Compose app remains healthy.
	rebuiltDB, err := store.Open(filepath.Join(dir, "rebuilt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rebuiltDB.Close()
	if err := store.Migrate(rebuiltDB, migrations.FS); err != nil {
		t.Fatal(err)
	}
	rebuiltProjects := projects.New(db.New(rebuiltDB), ag, box, bus, logger)
	if err := rebuiltProjects.Reconcile(ctx); err != nil {
		t.Fatalf("rebuild index: %v", err)
	}
	listed, err := rebuiltProjects.List(ctx)
	if err != nil || len(listed) != 2 {
		t.Fatalf("rebuilt project index = %+v, %v", listed, err)
	}
	// The list is newest first, and live and staging can share a millisecond.
	for _, p := range listed {
		if want := map[string]string{name: "", name + "-staging": name}; want[p.Name] != p.StagingOf.String {
			t.Fatalf("rebuilt project index = %+v", listed)
		}
	}
	statuses, err := ag.Compose().PS(ctx, name)
	if err != nil || len(statuses) == 0 || statuses[0].State != "running" {
		t.Fatalf("running app did not survive database rebuild: %+v, %v", statuses, err)
	}

	fmt.Println("caddy integration ok")
}
