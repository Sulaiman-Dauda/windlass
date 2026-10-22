package server

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/store/db"
)

// hashViewer inserts a viewer-role user directly into the store.
func hashViewer(t *testing.T, e *testEnv, ctx context.Context) {
	t.Helper()
	hash, err := auth.HashPassword("viewerpassword")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.queries.CreateUser(ctx, db.CreateUserParams{
		Email:        "viewer@example.com",
		PasswordHash: sql.NullString{String: hash, Valid: true},
		Role:         "viewer",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProjectLifecycleAPI(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)

	// Unauthenticated requests are rejected.
	if rec := e.do(t, http.MethodGet, "/api/v1/projects", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon list = %d, want 401", rec.Code)
	}

	// Create.
	rec := e.do(t, http.MethodPost, "/api/v1/projects", map[string]string{"name": "crm"}, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}

	// The starter compose.yaml exists on the (fake) filesystem.
	data, err := e.agent.FS().ReadFile(context.Background(), "crm", "compose.yaml")
	if err != nil || !strings.Contains(string(data), "services:") {
		t.Fatalf("starter compose missing: %q, %v", data, err)
	}

	// Duplicate name conflicts.
	rec = e.do(t, http.MethodPost, "/api/v1/projects", map[string]string{"name": "crm"}, cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate create = %d, want 409", rec.Code)
	}

	// Invalid names are rejected.
	for _, bad := range []string{"../evil", "UPPER", "a b", ""} {
		rec = e.do(t, http.MethodPost, "/api/v1/projects", map[string]string{"name": bad}, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("create %q = %d, want 400", bad, rec.Code)
		}
	}

	// List and get.
	rec = e.do(t, http.MethodGet, "/api/v1/projects", nil, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"crm"`) {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodGet, "/api/v1/projects/crm", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get = %d", rec.Code)
	}

	// Edit compose.yaml through the API.
	rec = e.do(t, http.MethodPut, "/api/v1/projects/crm/files/compose.yaml",
		map[string]string{"content": "services:\n  app:\n    image: redis:7\n"}, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("write file = %d: %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodGet, "/api/v1/projects/crm/files/compose.yaml", nil, cookie)
	if !strings.Contains(rec.Body.String(), "redis:7") {
		t.Fatalf("read file = %s", rec.Body.String())
	}

	// Binary/unknown file types are refused.
	rec = e.do(t, http.MethodPut, "/api/v1/projects/crm/files/app.bin",
		map[string]string{"content": "x"}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("write binary = %d, want 400", rec.Code)
	}

	// Delete requires the account password, then removes metadata and directory.
	rec = e.do(t, http.MethodDelete, "/api/v1/projects/crm", nil, cookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("delete without password = %d, want 403", rec.Code)
	}
	rec = e.do(t, http.MethodDelete, "/api/v1/projects/crm",
		map[string]string{"password": "wrong-password"}, cookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("delete with wrong password = %d, want 403", rec.Code)
	}
	rec = e.do(t, http.MethodDelete, "/api/v1/projects/crm",
		map[string]string{"password": "supersecret123"}, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}
	rec = e.do(t, http.MethodGet, "/api/v1/projects/crm", nil, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", rec.Code)
	}
	if _, err := e.agent.FS().ReadFile(context.Background(), "crm", "compose.yaml"); err == nil {
		t.Error("project directory still exists after delete")
	}
}

func TestProjectEnvEncryptedAtRest(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)

	e.do(t, http.MethodPost, "/api/v1/projects", map[string]string{"name": "app"}, cookie)

	rec := e.do(t, http.MethodPut, "/api/v1/projects/app/env",
		map[string]string{"DATABASE_URL": "postgres://u:sekret@db/x", "PORT": "3000"}, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("set env = %d: %s", rec.Code, rec.Body.String())
	}

	// Round trip through the API.
	rec = e.do(t, http.MethodGet, "/api/v1/projects/app/env", nil, cookie)
	if !strings.Contains(rec.Body.String(), "sekret") {
		t.Fatalf("env read back = %s", rec.Body.String())
	}

	// At rest the value is ciphertext: scan the raw table.
	rows, err := e.queries.ListEnvVars(context.Background(), 1)
	if err != nil || len(rows) != 2 {
		t.Fatalf("env rows = %d, %v", len(rows), err)
	}
	for _, row := range rows {
		if strings.Contains(string(row.ValueEnc), "sekret") || strings.Contains(string(row.ValueEnc), "3000") {
			t.Errorf("env var %s stored in plaintext", row.Key)
		}
	}

	// Invalid keys are rejected.
	rec = e.do(t, http.MethodPut, "/api/v1/projects/app/env",
		map[string]string{"1BAD": "x"}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid key = %d, want 400", rec.Code)
	}
}

func TestViewerIsReadOnly(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(t)
	e.do(t, http.MethodPost, "/api/v1/projects", map[string]string{"name": "app"}, admin)

	// Create a viewer directly in the store, then sign in.
	ctx := context.Background()
	hashViewer(t, e, ctx)
	rec := e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "viewer@example.com", "password": "viewerpassword",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("viewer login = %d", rec.Code)
	}
	viewer := sessionCookie(t, rec)

	// Viewer can read.
	if rec := e.do(t, http.MethodGet, "/api/v1/projects", nil, viewer); rec.Code != http.StatusOK {
		t.Errorf("viewer list = %d, want 200", rec.Code)
	}
	// Viewer cannot mutate.
	if rec := e.do(t, http.MethodPost, "/api/v1/projects", map[string]string{"name": "nope"}, viewer); rec.Code != http.StatusForbidden {
		t.Errorf("viewer create = %d, want 403", rec.Code)
	}
	if rec := e.do(t, http.MethodDelete, "/api/v1/projects/app", nil, viewer); rec.Code != http.StatusForbidden {
		t.Errorf("viewer delete = %d, want 403", rec.Code)
	}
}

// Env values are secrets (database URLs, API keys). A viewer can see which
// keys a project sets, but not their values, through either the env endpoint
// or the file viewer.
func TestViewerCannotReadEnvValues(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(t)
	e.do(t, http.MethodPost, "/api/v1/projects", map[string]string{"name": "app"}, admin)
	if rec := e.do(t, http.MethodPut, "/api/v1/projects/app/env",
		map[string]string{"DATABASE_URL": "postgres://u:sekret@db/x", "EMPTY": ""}, admin); rec.Code != http.StatusNoContent { //nolint:gosec // G101: a fake credential the test expects to be masked
		t.Fatalf("set env = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, http.MethodPut, "/api/v1/projects/app/files/prod.env",
		map[string]string{"content": "# prod\nexport API_KEY='other-sekret'\n"}, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("write prod.env = %d: %s", rec.Code, rec.Body.String())
	}

	hashViewer(t, e, context.Background())
	rec := e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "viewer@example.com", "password": "viewerpassword",
	})
	viewer := sessionCookie(t, rec)

	rec = e.do(t, http.MethodGet, "/api/v1/projects/app/env", nil, viewer)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || strings.Contains(body, "sekret") || !strings.Contains(body, "DATABASE_URL") {
		t.Errorf("viewer env = %d %s, want keys without values", rec.Code, body)
	}
	if !strings.Contains(body, `"EMPTY":""`) {
		t.Errorf("viewer env = %s, want an unset value to stay visibly empty", body)
	}
	for _, path := range []string{".env", "prod.env", "./.env"} {
		rec = e.do(t, http.MethodGet, "/api/v1/projects/app/files/"+path, nil, viewer)
		if strings.Contains(rec.Body.String(), "sekret") {
			t.Errorf("viewer read %s exposed a value: %s", path, rec.Body.String())
		}
	}
	rec = e.do(t, http.MethodGet, "/api/v1/projects/app/files/prod.env", nil, viewer)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "API_KEY") || !strings.Contains(rec.Body.String(), "# prod") {
		t.Errorf("viewer read prod.env = %d %s, want keys and comments kept", rec.Code, rec.Body.String())
	}

	// Members and admins still see and edit the real values.
	if rec := e.do(t, http.MethodGet, "/api/v1/projects/app/env", nil, admin); !strings.Contains(rec.Body.String(), "sekret") {
		t.Errorf("admin env = %s, want real values", rec.Body.String())
	}
}

// Compose files can set values inline. A viewer sees the file's structure,
// with environment and build args masked, and cannot write the masked copy
// back over the real one.
func TestViewerCannotReadComposeValues(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(t)
	e.do(t, http.MethodPost, "/api/v1/projects", map[string]string{"name": "app"}, admin)
	compose := "services:\n  web:\n    image: nginx\n    environment:\n      DB_PASSWORD: inline-sekret\n      FROM_ENV: ${API_KEY}\n    build:\n      args:\n        - NPM_TOKEN=npm-sekret\n"
	if rec := e.do(t, http.MethodPut, "/api/v1/projects/app/files/compose.yaml",
		map[string]string{"content": compose}, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("write compose = %d: %s", rec.Code, rec.Body.String())
	}

	hashViewer(t, e, context.Background())
	rec := e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "viewer@example.com", "password": "viewerpassword",
	})
	viewer := sessionCookie(t, rec)

	rec = e.do(t, http.MethodGet, "/api/v1/projects/app/files/compose.yaml", nil, viewer)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || strings.Contains(body, "sekret") || !strings.Contains(body, "DB_PASSWORD") ||
		!strings.Contains(body, "image: nginx") || !strings.Contains(body, "${API_KEY}") {
		t.Errorf("viewer compose = %d %s, want structure and references without values", rec.Code, body)
	}
	if rec := e.do(t, http.MethodGet, "/api/v1/projects/app/files/compose.yaml", nil, admin); !strings.Contains(rec.Body.String(), "inline-sekret") {
		t.Errorf("admin compose = %s, want real values", rec.Body.String())
	}

	// A write token without the secrets flag reads the masked copy too, and
	// sending it back is refused rather than overwriting the real values.
	tok := e.createToken(t, admin, map[string]any{"name": "ci", "scope": "write"})
	rec = e.doBearer(t, http.MethodGet, "/api/v1/projects/app/files/compose.yaml", tok.Token, nil)
	if strings.Contains(rec.Body.String(), "sekret") {
		t.Errorf("token compose read exposed a value: %s", rec.Body.String())
	}
	masked := strings.Replace(compose, "inline-sekret", `"********"`, 1)
	rec = e.doBearer(t, http.MethodPut, "/api/v1/projects/app/files/compose.yaml", tok.Token,
		map[string]string{"content": masked})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "masked_values") {
		t.Errorf("masked compose write = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, http.MethodGet, "/api/v1/projects/app/files/compose.yaml", nil, admin); !strings.Contains(rec.Body.String(), "inline-sekret") {
		t.Errorf("masked compose write changed the file: %s", rec.Body.String())
	}
}

// Session cookies are SameSite=Lax, which browsers send on a cross-site GET.
// The terminal is the one GET that acts (it opens a shell in a container),
// so its WebSocket handshake must refuse a page on another origin.
func TestTerminalRefusesCrossOriginHandshake(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(t)
	e.do(t, http.MethodPost, "/api/v1/projects", map[string]string{"name": "app"}, admin)
	e.agent.Containers = []agent.Container{{
		ID: "c1", Name: "app-web-1", State: "running", ComposeProject: "app", ComposeService: "web",
	}}

	handshake := func(origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/app/terminal?service=web", nil)
		req.Host = "panel.example.com"
		req.Header.Set("Origin", origin)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==") // gitleaks:allow, a handshake nonce
		req.AddCookie(admin)
		rec := httptest.NewRecorder()
		e.handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := handshake("https://evil.example"); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin terminal handshake = %d %s, want 403", rec.Code, rec.Body.String())
	}
	// Control: the panel's own origin gets past the origin check (the recorder
	// cannot be hijacked, so it fails later, but not with 403).
	if rec := handshake("https://panel.example.com"); rec.Code == http.StatusForbidden {
		t.Fatalf("same-origin terminal handshake = 403 %s: the test is not exercising the origin check", rec.Body.String())
	}
}
