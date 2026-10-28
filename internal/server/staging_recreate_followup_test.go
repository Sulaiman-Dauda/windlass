package server

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/store/db"
)

func prepareInterruptedStaging(t *testing.T, e *testEnv, cookie *http.Cookie) db.Project {
	t.Helper()
	e.prepareDeployableProject(t, cookie, "app")
	copy, err := e.api.Projects.CreateStagingCopy(context.Background(), projects.CopyReq{
		Live: "app", Env: map[string]string{"VALUE": "keep-staging"},
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := e.queries.CreateDeployment(context.Background(), db.CreateDeploymentParams{
		ProjectID: copy.Project.ID, ProjectID_2: copy.Project.ID, TriggeredBy: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.queries.FinishDeployment(context.Background(), db.FinishDeploymentParams{
		ID: d.ID, Status: "failed", Error: sql.NullString{
			String: "data copy was interrupted; delete staging and create it again", Valid: true,
		},
	}); err != nil {
		t.Fatal(err)
	}
	e.agent.Resolved[copy.Project.Name] = e.agent.Resolved["app"]
	return copy.Project
}

func TestRecreateAuditsDeletedStagingWhenCreateFails(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	old := prepareInterruptedStaging(t, e, cookie)
	e.agent.Fail["fs.copy"] = errors.New("injected copy failure after deletion")
	rec := e.do(t, http.MethodPost, "/api/v1/projects/app-staging/staging/recreate", map[string]any{
		"password": "supersecret123", "acknowledged": true,
	}, cookie)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("failed create = %d: %s", rec.Code, rec.Body.String())
	}
	if _, exists := e.agent.Files[old.Name]; exists {
		t.Fatal("injected create failure should occur after the old staging was deleted")
	}
	var deleted, recreated int
	if err := e.sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'project.delete' AND resource_id = 'app-staging'`).Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	if err := e.sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'project.staging_recreate' AND resource_id = 'app-staging'`).Scan(&recreated); err != nil {
		t.Fatal(err)
	}
	if deleted != 1 || recreated != 0 {
		t.Fatalf("audit after deleted staging fails to be recreated: deleted=%d recreated=%d; want 1 and 0", deleted, recreated)
	}
}

func TestRecreateLiveBlockersPreserveStaging(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*testing.T, *testEnv, *http.Cookie)
		message string
	}{
		{"compose", func(_ *testing.T, e *testEnv, _ *http.Cookie) {
			e.agent.Fail["compose.config"] = errors.New("invalid live compose")
		}, "live's compose file does not resolve"},
		{"isolation", func(_ *testing.T, e *testEnv, _ *http.Cookie) {
			e.agent.Resolved["app"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
				"web": {Image: "nginx", ContainerName: "fixed-live-container"},
			}}
		}, "container_name"},
		{"health URL", func(t *testing.T, e *testEnv, cookie *http.Cookie) {
			rec := e.do(t, http.MethodPost, "/api/v1/projects/app/domains", map[string]any{
				"hostname": "app.example.com", "service": "web", "container_port": 80,
			}, cookie)
			if rec.Code != http.StatusCreated {
				t.Fatal(rec.Body.String())
			}
			e.agent.Resolved["app"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
				"web": {Image: "nginx"},
			}, HealthChecks: []agent.ApplicationHealthCheck{{Service: "web", URL: "https://app.example.com/health"}}}
		}, "health"},
		{"database stopped", func(_ *testing.T, e *testEnv, _ *http.Cookie) {
			e.agent.Resolved["app"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
				"web": {Image: "nginx"}, "db": {Image: "postgres:16"},
			}}
			e.agent.Containers = []agent.Container{{
				ID: "live-db", Name: "app-db-1", Image: "postgres:16", State: "exited",
				ComposeProject: "app", ComposeService: "db",
			}}
		}, "database is not running"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			cookie := e.login(t)
			old := prepareInterruptedStaging(t, e, cookie)
			tc.prepare(t, e, cookie)
			before := make(map[string][]byte, len(e.agent.Files[old.Name]))
			for name, content := range e.agent.Files[old.Name] {
				before[name] = bytes.Clone(content)
			}
			callsBefore := len(e.agent.Calls)
			rec := e.do(t, http.MethodPost, "/api/v1/projects/app-staging/staging/recreate", map[string]any{
				"password": "supersecret123", "acknowledged": true,
			}, cookie)
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "staging_blocked") || !strings.Contains(rec.Body.String(), tc.message) {
				t.Fatalf("blocked recreate = %d: %s", rec.Code, rec.Body.String())
			}
			if !reflect.DeepEqual(e.agent.Files[old.Name], before) {
				t.Error("a known live blocker changed or deleted staging's authoritative files")
			}
			p, err := e.queries.GetProjectByName(context.Background(), old.Name)
			if err != nil || p.ID != old.ID {
				t.Errorf("known live blocker replaced old project: %+v, %v", p, err)
			}
			if slices.Contains(e.agent.Calls[callsBefore:], "compose.down(app-staging)") {
				t.Error("old staging was stopped before the known live blocker was checked")
			}
			var deletions int
			if err := e.sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'project.delete' AND resource_id = 'app-staging'`).Scan(&deletions); err != nil {
				t.Fatal(err)
			}
			if deletions != 0 {
				t.Errorf("blocked preflight recorded %d deletions", deletions)
			}
		})
	}
}
