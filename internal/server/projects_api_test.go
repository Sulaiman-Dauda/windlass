package server

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"

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

	// Delete removes metadata and directory.
	rec = e.do(t, http.MethodDelete, "/api/v1/projects/crm", nil, cookie)
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
