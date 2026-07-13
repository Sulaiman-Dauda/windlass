package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent/fake"
	"github.com/windlass-dev/windlass/internal/api"
	"github.com/windlass-dev/windlass/internal/audit"
	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/backups"
	"github.com/windlass-dev/windlass/internal/config"
	"github.com/windlass-dev/windlass/internal/deploy"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/git"
	"github.com/windlass-dev/windlass/internal/jobs"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/proxy"
	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/migrations"
)

type testEnv struct {
	handler    http.Handler
	queries    *db.Queries
	agent      *fake.Fake
	api        *api.API
	setupToken string
}

// captureToken pulls the setup token out of the startup log line.
type captureHandler struct {
	slog.Handler
	token *string
}

func (h captureHandler) Handle(ctx context.Context, r slog.Record) error {
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "setup_token" {
			*h.token = a.Value.String()
		}
		return true
	})
	return h.Handler.Handle(ctx, r)
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()

	sqlDB, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := store.Migrate(sqlDB, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	queries := db.New(sqlDB)

	var token string
	logger := slog.New(captureHandler{
		Handler: slog.NewTextHandler(io.Discard, nil),
		token:   &token,
	})

	key := bytes.Repeat([]byte{7}, 32)
	authSvc, err := auth.NewService(context.Background(), queries, key, logger)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}

	box, err := secrets.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	ag := fake.New()
	bus := events.NewBus()
	projectSvc := projects.New(queries, ag, box, bus, logger)
	gitSvc := git.New(queries, box, logger)
	runner := jobs.NewRunner(queries, logger)
	deploySvc := deploy.New(queries, ag, projectSvc, gitSvc, runner, bus, logger)

	runnerCtx, stopRunner := context.WithCancel(context.Background())
	t.Cleanup(stopRunner)
	go runner.Run(runnerCtx)

	a := &api.API{
		Auth:     authSvc,
		Audit:    audit.New(queries, logger),
		Projects: projectSvc,
		Deploy:   deploySvc,
		Proxy:    proxy.New(queries, ag, bus, logger),
		Git:      gitSvc,
		Backups:  backups.New(queries, ag, projectSvc, box, bus, logger),
		Agent:    ag,
		Bus:      bus,
		Logger:   logger,
	}
	h, err := New(config.Config{Addr: ":0", DataDir: dir}, logger, a)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	return &testEnv{handler: h, queries: queries, agent: ag, api: a, setupToken: token}
}

// login creates the admin (if needed) and returns a session cookie.
func (e *testEnv) login(t *testing.T) *http.Cookie {
	t.Helper()
	if e.setupToken != "" {
		rec := e.do(t, http.MethodPost, "/api/v1/auth/setup", map[string]string{
			"token": e.setupToken, "email": "admin@example.com", "password": "supersecret123",
		})
		if rec.Code == http.StatusNoContent {
			e.setupToken = ""
			return sessionCookie(t, rec)
		}
	}
	rec := e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "admin@example.com", "password": "supersecret123",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login failed: %d", rec.Code)
	}
	return sessionCookie(t, rec)
}

func (e *testEnv) do(t *testing.T, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie && c.Value != "" {
			return c
		}
	}
	t.Fatal("no session cookie in response")
	return nil
}

func TestHealth(t *testing.T) {
	e := newTestEnv(t)
	rec := e.do(t, http.MethodGet, "/api/v1/system/health", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestSPAFallback(t *testing.T) {
	e := newTestEnv(t)
	for _, path := range []string{"/", "/projects/foo", "/settings"} {
		rec := e.do(t, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(strings.ToLower(rec.Body.String()), "<!doctype html>") {
			t.Errorf("GET %s did not return HTML", path)
		}
	}
}

func TestFullAuthFlow(t *testing.T) {
	e := newTestEnv(t)

	// Fresh instance: needs setup, /auth/me is 401.
	rec := e.do(t, http.MethodGet, "/api/v1/auth/status", nil)
	if !strings.Contains(rec.Body.String(), `"needs_setup":true`) {
		t.Fatalf("status body = %s", rec.Body.String())
	}
	if rec := e.do(t, http.MethodGet, "/api/v1/auth/me", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("me before setup = %d, want 401", rec.Code)
	}

	// Wrong setup token is rejected.
	rec = e.do(t, http.MethodPost, "/api/v1/auth/setup", map[string]string{
		"token": "wrong", "email": "admin@example.com", "password": "supersecret123",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("setup with bad token = %d, want 403", rec.Code)
	}

	// Correct token claims the instance and signs in.
	rec = e.do(t, http.MethodPost, "/api/v1/auth/setup", map[string]string{
		"token": e.setupToken, "email": "admin@example.com", "password": "supersecret123",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("setup = %d: %s", rec.Code, rec.Body.String())
	}
	cookie := sessionCookie(t, rec)

	// Cookie works on /auth/me.
	rec = e.do(t, http.MethodGet, "/api/v1/auth/me", nil, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "admin@example.com") {
		t.Fatalf("me = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"role":"admin"`) {
		t.Errorf("first user is not admin: %s", rec.Body.String())
	}

	// Second setup attempt fails: instance is claimed.
	rec = e.do(t, http.MethodPost, "/api/v1/auth/setup", map[string]string{
		"token": e.setupToken, "email": "evil@example.com", "password": "supersecret123",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("second setup = %d, want 403", rec.Code)
	}

	// Login with wrong password fails.
	rec = e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "admin@example.com", "password": "wrong",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad login = %d, want 401", rec.Code)
	}

	// Login with correct password succeeds.
	rec = e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "admin@example.com", "password": "supersecret123",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login = %d: %s", rec.Code, rec.Body.String())
	}
	cookie2 := sessionCookie(t, rec)

	// Logout revokes the session server-side.
	rec = e.do(t, http.MethodPost, "/api/v1/auth/logout", nil, cookie2)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", rec.Code)
	}
	rec = e.do(t, http.MethodGet, "/api/v1/auth/me", nil, cookie2)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout = %d, want 401 (session not revoked)", rec.Code)
	}
}

func TestAuditTrailWritten(t *testing.T) {
	e := newTestEnv(t)

	e.do(t, http.MethodPost, "/api/v1/auth/setup", map[string]string{
		"token": e.setupToken, "email": "admin@example.com", "password": "supersecret123",
	})
	e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "admin@example.com", "password": "wrong",
	})

	rows, err := e.queries.ListAudit(context.Background(), db.ListAuditParams{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	actions := map[string]bool{}
	for _, r := range rows {
		actions[r.Action] = true
	}
	for _, want := range []string{"auth.setup", "auth.login_failed"} {
		if !actions[want] {
			t.Errorf("audit action %q missing; got %v", want, actions)
		}
	}
}

func TestTamperedCookieRejected(t *testing.T) {
	e := newTestEnv(t)
	rec := e.do(t, http.MethodPost, "/api/v1/auth/setup", map[string]string{
		"token": e.setupToken, "email": "admin@example.com", "password": "supersecret123",
	})
	cookie := sessionCookie(t, rec)
	cookie.Value = cookie.Value[:len(cookie.Value)-2] + "xx"

	rec = e.do(t, http.MethodGet, "/api/v1/auth/me", nil, cookie)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered cookie accepted: %d", rec.Code)
	}
}
