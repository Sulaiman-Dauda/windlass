package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/store/db"
)

func TestRecreateInterruptedStaging(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")
	ctx := context.Background()
	old, err := e.api.Projects.CreateStagingCopy(ctx, projects.CopyReq{
		Live: "app", Env: map[string]string{"VALUE": "keep-staging"}, AutoPromote: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ProjectID: old.Project.ID, ProjectID_2: old.Project.ID, TriggeredBy: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.queries.FinishDeployment(ctx, db.FinishDeploymentParams{
		ID: first.ID, Status: "failed",
		Error: sql.NullString{String: "data copy was interrupted; delete staging and create it again", Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	e.agent.Resolved["app-staging"] = e.agent.Resolved["app"]
	e.agent.Statuses["app-staging"] = []agent.ServiceStatus{{Service: "web", State: "running"}}
	path := "/api/v1/projects/app-staging/staging/recreate"
	for _, tc := range []struct{ password, code string }{
		{"", "password_required"}, {"wrong-password", "invalid_password"},
	} {
		rec := e.do(t, http.MethodPost, path, map[string]any{"password": tc.password, "acknowledged": true}, cookie)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), tc.code) {
			t.Fatalf("password guard = %d: %s", rec.Code, rec.Body.String())
		}
	}
	viewer := e.viewerCookie(t, cookie)
	if rec := e.do(t, http.MethodPost, path, map[string]any{"password": "viewerpassword123", "acknowledged": true}, viewer); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer recreate = %d: %s", rec.Code, rec.Body.String())
	}
	if slices.Contains(e.agent.Calls, "compose.down(app-staging)") {
		t.Fatal("authorization failure stopped staging")
	}
	rec := e.do(t, http.MethodPost, "/api/v1/users", map[string]string{
		"email": "member@example.com", "password": "memberpassword123", "role": "member",
	}, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	rec = e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "member@example.com", "password": "memberpassword123",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatal(rec.Body.String())
	}
	member := sessionCookie(t, rec)
	rec = e.do(t, http.MethodPost, path, map[string]any{
		"password": "memberpassword123", "acknowledged": true,
	}, member)
	if rec.Code != http.StatusCreated {
		t.Fatalf("recreate = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"app-staging"`) {
		t.Fatalf("recreated = %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "keep-staging") {
		t.Fatal("recreate response exposed an environment value")
	}
	var count int
	if err := e.sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'project.staging_recreate' AND resource_id = 'app-staging'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("recreate audit events = %d", count)
	}
}

// prepareStagingLive makes a deployable Git project "app" with one domain
// and an outside-world secret in its env, and returns its webhook secret.
func prepareStagingLive(t *testing.T, e *testEnv, cookie *http.Cookie) string {
	t.Helper()
	e.prepareDeployableProject(t, cookie, "app")
	secret := configureGit(t, e, cookie, "app", true)
	if rec := e.do(t, http.MethodPost, "/api/v1/projects/app/domains", map[string]any{
		"hostname": "app.example.com", "service": "web", "container_port": 80,
	}, cookie); rec.Code != http.StatusCreated {
		t.Fatalf("add domain = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, http.MethodPut, "/api/v1/projects/app/env", map[string]string{
		"STRIPE_SECRET_KEY": "sk_live_x", "PUBLIC_URL": "https://app.example.com",
	}, cookie); rec.Code != http.StatusNoContent {
		t.Fatalf("set env = %d: %s", rec.Code, rec.Body.String())
	}
	e.agent.Statuses["app-staging"] = []agent.ServiceStatus{{Service: "web", State: "running"}}
	e.agent.Resolved["app-staging"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
		"web": {Image: "nginx:alpine"},
	}}
	return secret
}

func decode[T any](t *testing.T, body string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return v
}

func TestStagingLifecycle(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	liveSecret := prepareStagingLive(t, e, cookie)

	rec := e.do(t, http.MethodPost, "/api/v1/projects/app/staging/preflight", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("preflight = %d: %s", rec.Code, rec.Body.String())
	}
	pf := decode[struct {
		CanCreate bool `json:"can_create"`
		Domains   []struct{ Live, Staging string }
		Flagged   []struct{ Key, Value string } `json:"flagged_env"`
	}](t, rec.Body.String())
	if !pf.CanCreate || len(pf.Domains) != 1 || pf.Domains[0].Staging != "staging-app.example.com" {
		t.Fatalf("preflight = %s", rec.Body.String())
	}
	if len(pf.Flagged) != 1 || pf.Flagged[0].Key != "STRIPE_SECRET_KEY" || pf.Flagged[0].Value != "sk_live_x" {
		t.Errorf("flagged = %+v", pf.Flagged)
	}

	// Live's payment key is flagged, so create needs an acknowledgement.
	body := map[string]any{"env": map[string]string{"STRIPE_SECRET_KEY": "sk_test_x"}}
	rec = e.do(t, http.MethodPost, "/api/v1/projects/app/staging", body, cookie)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "acknowledgement_required") {
		t.Fatalf("unacknowledged create = %d: %s", rec.Code, rec.Body.String())
	}
	body["acknowledged"] = true
	rec = e.do(t, http.MethodPost, "/api/v1/projects/app/staging", body, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	created := decode[struct {
		Project struct {
			Name      string `json:"name"`
			StagingOf string `json:"staging_of"`
		}
		Result struct {
			Deployment    int64  `json:"deployment"`
			WebhookSecret string `json:"webhook_secret"`
		}
	}](t, rec.Body.String())
	if created.Project.Name != "app-staging" || created.Project.StagingOf != "app" || created.Result.WebhookSecret == "" {
		t.Fatalf("created = %s", rec.Body.String())
	}
	waitForStatus(t, e, cookie, "app-staging", "1", "succeeded")

	env := string(e.agent.Files["app-staging"][".env"])
	for _, want := range []string{"STRIPE_SECRET_KEY=sk_test_x", "PUBLIC_URL=https://staging-app.example.com", "WINDLASS_ENVIRONMENT=staging"} {
		if !strings.Contains(env, want) {
			t.Errorf("staging .env lacks %s:\n%s", want, env)
		}
	}
	e.agent.Containers = []agent.Container{{
		ID: "c2", Name: "app-staging-web-1", State: "running",
		ComposeProject: "app-staging", ComposeService: "web", IPAddress: "172.18.0.6",
	}}
	if err := e.api.Proxy.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	var noindex bool
	for _, r := range e.agent.Routes {
		if r.Hostname == "staging-app.example.com" {
			noindex = r.NoIndex
		}
	}
	if !noindex {
		t.Errorf("staging route missing or indexable: %+v", e.agent.Routes)
	}
	if rec := e.do(t, http.MethodGet, "/api/v1/projects/app", nil, cookie); !strings.Contains(rec.Body.String(), `"staging":"app-staging"`) {
		t.Errorf("live project = %s", rec.Body.String())
	}

	// Pushes to live now wait for staging.
	push := []byte(`{"ref":"refs/heads/main"}`)
	rec = postWebhook(e, "app", githubSign(liveSecret, push), push)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "deploys through staging") {
		t.Fatalf("live webhook = %d: %s", rec.Code, rec.Body.String())
	}
	rec = postWebhook(e, "app-staging", githubSign(created.Result.WebhookSecret, push), push)
	if rec.Code != http.StatusCreated {
		t.Fatalf("staging webhook = %d: %s", rec.Code, rec.Body.String())
	}
	waitForStatus(t, e, cookie, "app-staging", "2", "succeeded")

	rec = e.do(t, http.MethodGet, "/api/v1/projects/app/staging", nil, cookie)
	status := decode[struct {
		Promotable *struct{ Number int64 } `json:"promotable"`
	}](t, rec.Body.String())
	if rec.Code != http.StatusOK || status.Promotable == nil || status.Promotable.Number != 2 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	rec = e.do(t, http.MethodPost, "/api/v1/projects/app-staging/deployments/2/promote", nil, cookie)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"promoted_from_number":2`) {
		t.Fatalf("promote = %d: %s", rec.Code, rec.Body.String())
	}
	waitForStatus(t, e, cookie, "app", "1", "succeeded")
	if rec := e.do(t, http.MethodGet, "/api/v1/projects/app/deployments/1", nil, cookie); !strings.Contains(rec.Body.String(), `"promoted_from_number":2`) {
		t.Errorf("live deployment = %s", rec.Body.String())
	}

	if rec := e.do(t, http.MethodPut, "/api/v1/projects/app-staging/staging", map[string]bool{"auto_promote": true}, cookie); rec.Code != http.StatusNoContent {
		t.Errorf("set auto-promote = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, http.MethodPut, "/api/v1/projects/app/staging", map[string]bool{"auto_promote": true}, cookie); rec.Code != http.StatusNotFound {
		t.Errorf("set auto-promote on live = %d", rec.Code)
	}

	var actions []string
	rows, err := e.sqlDB.Query(`SELECT action FROM audit_log WHERE action LIKE '%staging%' OR action LIKE '%promote%' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, a)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(actions, ",") != "project.staging_create,deployment.promote,project.staging_update" {
		t.Errorf("audit = %v", actions)
	}

	// Live cannot be deleted while it has a staging.
	pw := map[string]string{"password": "supersecret123"}
	rec = e.do(t, http.MethodDelete, "/api/v1/projects/app", pw, cookie)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "has_staging") {
		t.Fatalf("delete live = %d: %s", rec.Code, rec.Body.String())
	}

	// A staging compose file edited to name live's volume is not deleted:
	// down -v would remove the volume.
	resolved := e.agent.Resolved["app-staging"]
	shared := resolved
	shared.Volumes = map[string]agent.ResolvedVolume{"data": {Name: "app_data"}, "own": {Name: "app-staging_own"}}
	e.agent.Resolved["app-staging"] = shared
	rec = e.do(t, http.MethodDelete, "/api/v1/projects/app-staging", pw, cookie)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "names volume app_data") {
		t.Fatalf("delete staging naming live's volume = %d: %s", rec.Code, rec.Body.String())
	}
	if slices.Contains(e.agent.Calls, "compose.down(app-staging)") {
		t.Fatal("down ran for a staging naming live's volume")
	}
	e.agent.Resolved["app-staging"] = resolved
	if rec := e.do(t, http.MethodDelete, "/api/v1/projects/app-staging", pw, cookie); rec.Code != http.StatusNoContent {
		t.Fatalf("delete staging = %d: %s", rec.Code, rec.Body.String())
	}
	rec = postWebhook(e, "app", githubSign(liveSecret, push), push)
	if rec.Code != http.StatusCreated {
		t.Errorf("live webhook after staging went = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestStagingCreateUndoesAFailure(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	prepareStagingLive(t, e, cookie)

	// Queueing staging's first deployment fails after its files, domain and
	// Git link are in place.
	if _, err := e.sqlDB.Exec(`CREATE TRIGGER t_fail BEFORE INSERT ON jobs BEGIN SELECT RAISE(ABORT, 'disk I/O error'); END`); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodPost, "/api/v1/projects/app/staging", map[string]any{"acknowledged": true}, cookie)
	if rec.Code == http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := e.sqlDB.Exec("DROP TRIGGER t_fail"); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.agent.Files["app-staging"]; ok {
		t.Error("staging files left behind")
	}
	if rec := e.do(t, http.MethodGet, "/api/v1/projects/app-staging", nil, cookie); rec.Code != http.StatusNotFound {
		t.Errorf("staging project left behind: %d", rec.Code)
	}
	var domains int
	if err := e.sqlDB.QueryRow(`SELECT COUNT(*) FROM domains WHERE hostname = 'staging-app.example.com'`).Scan(&domains); err != nil {
		t.Fatal(err)
	}
	if domains != 0 {
		t.Error("staging domain left behind")
	}

	// A second attempt starts clean.
	rec = e.do(t, http.MethodPost, "/api/v1/projects/app/staging", map[string]any{"acknowledged": true}, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("retry = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestStagingPreflightBlocksSharedResources(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	prepareStagingLive(t, e, cookie)
	e.agent.Resolved["app"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
		"web": {Image: "nginx:alpine", ContainerName: "app-web"},
	}}
	rec := e.do(t, http.MethodPost, "/api/v1/projects/app/staging/preflight", nil, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"can_create":false`) ||
		!strings.Contains(rec.Body.String(), "container_name") {
		t.Fatalf("preflight = %d: %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodPost, "/api/v1/projects/app/staging", map[string]any{"acknowledged": true}, cookie)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "staging_blocked") {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	if _, ok := e.agent.Files["app-staging"]; ok {
		t.Error("blocked create left files")
	}
}

func TestStagingPreflightMasksSecretsWithoutTheScope(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	prepareStagingLive(t, e, cookie)
	write := e.createToken(t, cookie, map[string]any{"name": "agent", "scope": "write"})

	rec := e.doBearer(t, http.MethodPost, "/api/v1/projects/app/staging/preflight", write.Token, nil)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "sk_live_x") ||
		!strings.Contains(rec.Body.String(), "STRIPE_SECRET_KEY") {
		t.Fatalf("preflight = %d: %s", rec.Code, rec.Body.String())
	}

	// Sending the masked value back keeps live's, rather than writing the mask.
	pf := decode[struct {
		Flagged []struct{ Key, Value string } `json:"flagged_env"`
	}](t, rec.Body.String())
	rec = e.doBearer(t, http.MethodPost, "/api/v1/projects/app/staging", write.Token, map[string]any{
		"acknowledged": true, "env": map[string]string{"STRIPE_SECRET_KEY": pf.Flagged[0].Value},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	if env := string(e.agent.Files["app-staging"][".env"]); !strings.Contains(env, "STRIPE_SECRET_KEY=sk_live_x") {
		t.Errorf("staging .env = %s", env)
	}

	viewer := e.viewerCookie(t, cookie)
	if rec := e.do(t, http.MethodPost, "/api/v1/projects/app/staging/preflight", nil, viewer); rec.Code != http.StatusForbidden {
		t.Errorf("viewer preflight = %d", rec.Code)
	}
}

func (e *testEnv) viewerCookie(t *testing.T, admin *http.Cookie) *http.Cookie {
	t.Helper()
	if rec := e.do(t, http.MethodPost, "/api/v1/users", map[string]string{
		"email": "viewer@example.com", "password": "viewerpassword123", "role": "viewer",
	}, admin); rec.Code != http.StatusCreated {
		t.Fatalf("create viewer = %d: %s", rec.Code, rec.Body.String())
	}
	rec := e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "viewer@example.com", "password": "viewerpassword123",
	})
	return sessionCookie(t, rec)
}

// One GitHub App push reaches both projects on the repo; only staging deploys.
func TestGitHubAppPushDeploysThroughStaging(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	prepareStagingLive(t, e, cookie)
	if rec := e.do(t, http.MethodPost, "/api/v1/projects/app/staging", map[string]any{"acknowledged": true}, cookie); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	waitForStatus(t, e, cookie, "app-staging", "1", "succeeded")

	plain, _ := json.Marshal(map[string]any{"webhook_secret": "app-secret"})
	enc, err := e.api.Box.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, _ := json.Marshal(map[string]string{"enc": hex.EncodeToString(enc)})
	if err := e.queries.SetSetting(context.Background(), db.SetSettingParams{Key: "githubapp", Value: string(wrapped)}); err != nil {
		t.Fatal(err)
	}

	push := []byte(`{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/acme/app.git"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github-app", bytes.NewReader(push))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", githubSign("app-secret", push))
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"deployed":["app-staging"]`) {
		t.Fatalf("app webhook = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestStagingDiffIsWhatCopyToLiveWrites(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	prepareStagingLive(t, e, cookie)
	const compose = "services:\n  web:\n    image: nginx:alpine\n    environment:\n      ORIGIN: https://app.example.com\n"
	if rec := e.do(t, http.MethodPut, "/api/v1/projects/app/files/compose.yaml", map[string]string{"content": compose}, cookie); rec.Code != http.StatusNoContent {
		t.Fatalf("write live compose = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, http.MethodPost, "/api/v1/projects/app/staging", map[string]any{"acknowledged": true}, cookie); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	waitForStatus(t, e, cookie, "app-staging", "1", "succeeded")

	// Staging gains a service; its own hostname stays in its copy.
	changed := strings.Replace(string(e.agent.Files["app-staging"]["compose.yaml"]), "services:\n", "services:\n  worker:\n    image: busybox\n", 1)
	if !strings.Contains(changed, "https://staging-app.example.com") {
		t.Fatalf("staging compose was not rewritten:\n%s", changed)
	}
	if rec := e.do(t, http.MethodPut, "/api/v1/projects/app-staging/files/compose.yaml", map[string]string{"content": changed}, cookie); rec.Code != http.StatusNoContent {
		t.Fatalf("write staging compose = %d", rec.Code)
	}
	type status struct {
		Drift []struct{ File, State string }
	}
	if st := decode[status](t, e.do(t, http.MethodGet, "/api/v1/projects/app/staging", nil, cookie).Body.String()); len(st.Drift) != 1 || st.Drift[0].State != "changed" {
		t.Fatalf("drift = %+v", st.Drift)
	}

	for _, name := range []string{"app-staging", "app"} {
		rec := e.do(t, http.MethodGet, "/api/v1/projects/"+name+"/staging/diff/compose.yaml", nil, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("diff from %s = %d: %s", name, rec.Code, rec.Body.String())
		}
		d := decode[struct {
			Live    string `json:"live"`
			ForLive string `json:"for_live"`
		}](t, rec.Body.String())
		if d.Live != compose || !strings.Contains(d.ForLive, "worker:") || !strings.Contains(d.ForLive, "https://app.example.com") || strings.Contains(d.ForLive, "staging-app") {
			t.Fatalf("diff from %s = %+v", name, d)
		}
		if name == "app" {
			// Copy to live is the existing file write with for_live.
			if rec := e.do(t, http.MethodPut, "/api/v1/projects/app/files/compose.yaml", map[string]string{"content": d.ForLive}, cookie); rec.Code != http.StatusNoContent {
				t.Fatalf("copy to live = %d", rec.Code)
			}
		}
	}
	if st := decode[status](t, e.do(t, http.MethodGet, "/api/v1/projects/app/staging", nil, cookie).Body.String()); len(st.Drift) != 0 {
		t.Errorf("drift after copying = %+v", st.Drift)
	}

	for _, file := range []string{".env", "compose.rollback.yaml", "notes.txt"} {
		if rec := e.do(t, http.MethodGet, "/api/v1/projects/app/staging/diff/"+file, nil, cookie); rec.Code != http.StatusBadRequest {
			t.Errorf("diff %s = %d", file, rec.Code)
		}
	}
	if rec := e.do(t, http.MethodGet, "/api/v1/projects/app/staging/diff/compose.yaml", nil, e.viewerCookie(t, cookie)); rec.Code != http.StatusForbidden {
		t.Errorf("viewer diff = %d", rec.Code)
	}

	// A token without the secrets flag sees both files masked, and cannot
	// copy the mask over live's values.
	write := e.createToken(t, cookie, map[string]any{"name": "agent", "scope": "write"})
	rec := e.doBearer(t, http.MethodGet, "/api/v1/projects/app/staging/diff/compose.yaml", write.Token, nil)
	d := decode[struct {
		Live    string `json:"live"`
		ForLive string `json:"for_live"`
	}](t, rec.Body.String())
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "https://app.example.com") ||
		!strings.Contains(d.Live, "ORIGIN") || !strings.Contains(d.ForLive, "ORIGIN") {
		t.Fatalf("token diff = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.doBearer(t, http.MethodPut, "/api/v1/projects/app/files/compose.yaml", write.Token, map[string]string{"content": d.ForLive}); rec.Code != http.StatusBadRequest {
		t.Errorf("token copy of masked content = %d", rec.Code)
	}
}

func TestStagingPreflightLeavesOutASecondDatabase(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	prepareStagingLive(t, e, cookie)
	e.agent.Resolved["app"] = agent.ResolvedConfig{
		Volumes: map[string]agent.ResolvedVolume{"pgdata": {Name: "app_pgdata"}, "mydata": {Name: "app_mydata"}},
		Services: map[string]agent.ResolvedService{
			"web":    {Image: "nginx:alpine"},
			"db":     {Image: "postgres:16", Mounts: []agent.Mount{{Type: "volume", Source: "pgdata", Target: "/var/lib/postgresql/data"}}},
			"legacy": {Image: "mariadb:11", Mounts: []agent.Mount{{Type: "volume", Source: "mydata", Target: "/var/lib/mysql"}}},
		},
	}
	e.agent.Containers = []agent.Container{{ID: "live-db", ComposeProject: "app", ComposeService: "db", Image: "postgres:16", State: "running"}}
	rec := e.do(t, http.MethodPost, "/api/v1/projects/app/staging/preflight", nil, cookie)
	pf := decode[struct {
		Warnings []struct{ Service, Message string }
		Data     []struct{ Kind, Name string }
	}](t, rec.Body.String())
	if rec.Code != http.StatusOK || !slices.ContainsFunc(pf.Warnings, func(w struct{ Service, Message string }) bool {
		return w.Service == "legacy" && strings.Contains(w.Message, "volume mydata is not copied")
	}) {
		t.Fatalf("preflight = %d: %s", rec.Code, rec.Body.String())
	}
	for _, d := range pf.Data {
		if d.Name == "mydata" {
			t.Errorf("data lists the second database's volume: %+v", pf.Data)
		}
	}
}
