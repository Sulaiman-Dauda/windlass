package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/store/db"
)

// doBearer sends a request with an API token and, optionally, cookies too.
func (e *testEnv) doBearer(t *testing.T, method, path, token string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

type createdToken struct {
	ID     int64  `json:"id"`
	Token  string `json:"token"`
	Prefix string `json:"prefix"`
	Scope  string `json:"scope"`
}

func (e *testEnv) createToken(t *testing.T, cookie *http.Cookie, body map[string]any) createdToken {
	t.Helper()
	rec := e.do(t, http.MethodPost, "/api/v1/auth/tokens", body, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create token = %d: %s", rec.Code, rec.Body.String())
	}
	var out createdToken
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAPITokenEndToEnd(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	ctx := context.Background()

	write := e.createToken(t, cookie, map[string]any{"name": "agent", "scope": "write"})
	if !strings.HasPrefix(write.Token, auth.TokenPrefix) || !strings.HasPrefix(write.Token, write.Prefix) {
		t.Fatalf("token %q / prefix %q", write.Token, write.Prefix)
	}

	// The plaintext is shown once: the list carries only the prefix.
	rec := e.do(t, http.MethodGet, "/api/v1/auth/tokens", nil, cookie)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), write.Token) ||
		!strings.Contains(rec.Body.String(), write.Prefix) {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body.String())
	}

	// A write token deploys the everyday workflow: create, edit, set env.
	rec = e.doBearer(t, http.MethodPost, "/api/v1/projects", write.Token, map[string]string{"name": "bot"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("token create project = %d: %s", rec.Code, rec.Body.String())
	}
	rec = e.doBearer(t, http.MethodPut, "/api/v1/projects/bot/files/compose.yaml", write.Token,
		map[string]string{"content": "services:\n  app:\n    image: redis:7\n"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("token write compose = %d: %s", rec.Code, rec.Body.String())
	}
	rec = e.doBearer(t, http.MethodPut, "/api/v1/projects/bot/env", write.Token,
		map[string]string{"API_KEY": "sekret-value"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("token set env = %d: %s", rec.Code, rec.Body.String())
	}

	// Without the secrets flag, env reads come back masked, both as the
	// env endpoint and as the raw .env file.
	for _, path := range []string{"/api/v1/projects/bot/env", "/api/v1/projects/bot/files/.env"} {
		rec = e.doBearer(t, http.MethodGet, path, write.Token, nil)
		if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "sekret-value") {
			t.Errorf("token without secrets read %s = %d: %s", path, rec.Code, rec.Body.String())
		}
	}

	// Sending the masked map back with one change keeps the stored secrets.
	rec = e.doBearer(t, http.MethodPut, "/api/v1/projects/bot/env", write.Token,
		map[string]string{"API_KEY": "********", "NEW": "plain"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("token round-trip env = %d: %s", rec.Code, rec.Body.String())
	}
	if rec = e.do(t, http.MethodGet, "/api/v1/projects/bot/env", nil, cookie); !strings.Contains(rec.Body.String(), "sekret-value") ||
		!strings.Contains(rec.Body.String(), "plain") {
		t.Errorf("masked round trip lost a value: %s", rec.Body.String())
	}
	// Writing the masked .env file back is refused rather than saved.
	rec = e.doBearer(t, http.MethodPut, "/api/v1/projects/bot/files/.env", write.Token,
		map[string]string{"content": "API_KEY=********\nNEW=plain\n"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "masked_values") {
		t.Errorf("masked .env write = %d: %s", rec.Code, rec.Body.String())
	}
	if rec = e.do(t, http.MethodGet, "/api/v1/projects/bot/env", nil, cookie); !strings.Contains(rec.Body.String(), "sekret-value") {
		t.Errorf("masked .env write changed the secret: %s", rec.Body.String())
	}

	secretsTok := e.createToken(t, cookie, map[string]any{"name": "ops", "scope": "write", "can_read_secrets": true})
	rec = e.doBearer(t, http.MethodGet, "/api/v1/projects/bot/env", secretsTok.Token, nil)
	if !strings.Contains(rec.Body.String(), "sekret-value") {
		t.Errorf("token with secrets flag got masked env: %s", rec.Body.String())
	}

	// The audit log names the token, not just the user.
	entries, err := e.queries.ListAudit(ctx, db.ListAuditParams{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var sawToken, sawSession bool
	for _, a := range entries {
		if a.Action == "project.create" && a.TokenID.Valid && a.TokenID.Int64 == write.ID {
			sawToken = true
		}
		if a.Action == "token.create" && !a.TokenID.Valid {
			sawSession = true
		}
	}
	if !sawToken || !sawSession {
		t.Errorf("audit token attribution: token=%v session=%v", sawToken, sawSession)
	}

	// /auth/tokens/current answers for a token, not for a session.
	rec = e.doBearer(t, http.MethodGet, "/api/v1/auth/tokens/current", write.Token, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"role":"member"`) {
		t.Errorf("current token = %d: %s", rec.Code, rec.Body.String())
	}
	if rec = e.do(t, http.MethodGet, "/api/v1/auth/tokens/current", nil, cookie); rec.Code != http.StatusNotFound {
		t.Errorf("current token with a session = %d, want 404", rec.Code)
	}

	// Revoke, then the token is dead even alongside a valid cookie.
	rec = e.do(t, http.MethodDelete, "/api/v1/auth/tokens/"+itoa(write.ID), nil, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d: %s", rec.Code, rec.Body.String())
	}
	if rec = e.doBearer(t, http.MethodGet, "/api/v1/projects", write.Token, nil, cookie); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked token with a cookie = %d, want 401", rec.Code)
	}
	// Revoking twice is fine.
	if rec = e.do(t, http.MethodDelete, "/api/v1/auth/tokens/"+itoa(write.ID), nil, cookie); rec.Code != http.StatusNoContent {
		t.Errorf("second revoke = %d", rec.Code)
	}
}

func TestAPITokenScopes(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.do(t, http.MethodPost, "/api/v1/projects", map[string]string{"name": "app"}, cookie)

	read := e.createToken(t, cookie, map[string]any{"name": "r", "scope": "read"})
	write := e.createToken(t, cookie, map[string]any{"name": "w", "scope": "write"})
	admin := e.createToken(t, cookie, map[string]any{"name": "a", "scope": "admin"})

	cases := []struct {
		token, method, path string
		want                int
	}{
		{read.Token, http.MethodGet, "/api/v1/projects", http.StatusOK},
		{read.Token, http.MethodPost, "/api/v1/projects", http.StatusForbidden},
		{read.Token, http.MethodPost, "/api/v1/projects/app/deployments", http.StatusForbidden},
		{write.Token, http.MethodGet, "/api/v1/users", http.StatusForbidden},
		{write.Token, http.MethodGet, "/api/v1/system/backups/s3", http.StatusForbidden},
		{admin.Token, http.MethodGet, "/api/v1/users", http.StatusOK},
		{admin.Token, http.MethodGet, "/api/v1/system/backups/s3", http.StatusOK},
	}
	for _, c := range cases {
		if rec := e.doBearer(t, c.method, c.path, c.token, map[string]string{}); rec.Code != c.want {
			t.Errorf("%s %s = %d, want %d: %s", c.method, c.path, rec.Code, c.want, rec.Body.String())
		}
	}

	// Malformed, mistyped and unknown tokens are refused outright, even
	// when a valid session cookie rides along.
	last := admin.Token[len(admin.Token)-1]
	typo := admin.Token[:len(admin.Token)-1] + map[bool]string{true: "1", false: "0"}[last == '0']
	bad := []string{auth.TokenPrefix + "nope", typo, admin.Token + "x"}
	for _, tok := range bad {
		if rec := e.doBearer(t, http.MethodGet, "/api/v1/projects", tok, nil, cookie); rec.Code != http.StatusUnauthorized {
			t.Errorf("bad token %q = %d, want 401", tok, rec.Code)
		}
	}
	// A bearer that is not a Windlass token is someone else's; the cookie decides.
	if rec := e.doBearer(t, http.MethodGet, "/api/v1/projects", "some-proxy-token", nil, cookie); rec.Code != http.StatusOK {
		t.Errorf("foreign bearer with cookie = %d, want 200", rec.Code)
	}
}

func TestAPITokenSessionOnlyRoutes(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.do(t, http.MethodPost, "/api/v1/projects", map[string]string{"name": "app"}, cookie)
	admin := e.createToken(t, cookie, map[string]any{"name": "a", "scope": "admin", "can_read_secrets": true})

	refused := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/auth/tokens"},
		{http.MethodPost, "/api/v1/auth/tokens"},
		{http.MethodDelete, "/api/v1/auth/tokens/1"},
		{http.MethodPost, "/api/v1/auth/totp/setup"},
		{http.MethodPost, "/api/v1/auth/totp/disable"},
		{http.MethodPost, "/api/v1/users"},
		{http.MethodPut, "/api/v1/users/1/role"},
		{http.MethodDelete, "/api/v1/users/2"},
		{http.MethodGet, "/api/v1/users/1/tokens"},
		{http.MethodDelete, "/api/v1/users/1/tokens/1"},
		{http.MethodPost, "/api/v1/projects/app/backups/1/restore"},
		{http.MethodDelete, "/api/v1/users/2/totp"},
		{http.MethodDelete, "/api/v1/projects/app"},
		{http.MethodPost, "/api/v1/projects/app-staging/staging/recreate"},
		{http.MethodGet, "/api/v1/projects/app/terminal"},
		{http.MethodPut, "/api/v1/system/backups/s3"},
		{http.MethodPost, "/api/v1/system/update"},
		{http.MethodPut, "/api/v1/system/panel-domain"},
		{http.MethodPut, "/api/v1/system/oauth/github"},
		{http.MethodGet, "/api/v1/system/github-app/create"},
		{http.MethodGet, "/api/v1/git/connections/github/connect"},
	}
	for _, c := range refused {
		rec := e.doBearer(t, c.method, c.path, admin.Token, map[string]string{})
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "session_required") {
			t.Errorf("%s %s with admin token = %d %s, want 403 session_required", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
	// The project survived.
	if rec := e.do(t, http.MethodGet, "/api/v1/projects/app", nil, cookie); rec.Code != http.StatusOK {
		t.Fatalf("project gone: %d", rec.Code)
	}
	// An admin can still see and revoke other users' tokens in a session.
	if rec := e.do(t, http.MethodGet, "/api/v1/users/1/tokens", nil, cookie); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), admin.Prefix) {
		t.Errorf("admin list user tokens = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, http.MethodDelete, "/api/v1/users/1/tokens/"+itoa(admin.ID), nil, cookie); rec.Code != http.StatusNoContent {
		t.Errorf("admin revoke user token = %d", rec.Code)
	}
	if rec := e.do(t, http.MethodDelete, "/api/v1/users/99/tokens/"+itoa(admin.ID), nil, cookie); rec.Code != http.StatusNotFound {
		t.Errorf("revoke via the wrong owner = %d, want 404", rec.Code)
	}
}

func TestOnlyAdminsCreateTokens(t *testing.T) {
	e := newTestEnv(t)
	e.login(t)
	ctx := context.Background()
	hash, err := auth.HashPassword(context.Background(), "memberpassword")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.queries.CreateUser(ctx, db.CreateUserParams{Email: "member@example.com",
		PasswordHash: sql.NullString{String: hash, Valid: true}, Role: "member"}); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "member@example.com", "password": "memberpassword"})
	member := sessionCookie(t, rec)
	rec = e.do(t, http.MethodPost, "/api/v1/auth/tokens", map[string]any{"name": "m", "scope": "read"}, member)
	if rec.Code != http.StatusForbidden {
		t.Errorf("member create token = %d, want 403", rec.Code)
	}
	if rec = e.do(t, http.MethodGet, "/api/v1/auth/tokens", nil, member); rec.Code != http.StatusOK {
		t.Errorf("member list own tokens = %d", rec.Code)
	}
}

func TestCrossOriginWritesRefused(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	send := func(header, value string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"name":"x"}`))
		req.Host = "panel.example.com"
		req.Header.Set(header, value)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		e.handler.ServeHTTP(rec, req)
		return rec.Code
	}
	// A sibling subdomain is same-site, so SameSite=Lax sends the cookie.
	if code := send("Sec-Fetch-Site", "same-site"); code != http.StatusForbidden {
		t.Errorf("same-site POST = %d, want 403", code)
	}
	if code := send("Origin", "https://blog.example.com"); code != http.StatusForbidden {
		t.Errorf("sibling Origin POST = %d, want 403", code)
	}
	if code := send("Sec-Fetch-Site", "same-origin"); code != http.StatusCreated {
		t.Errorf("same-origin POST = %d, want 201", code)
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// The plugin proxy forwards any method, so it applies the API's own rule:
// reading needs a signed-in caller, changing anything needs a member. No
// plugin is running, so a request that gets through answers 503.
func TestPluginProxyWritesNeedMember(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	read := e.createToken(t, cookie, map[string]any{"name": "r", "scope": "read"})
	write := e.createToken(t, cookie, map[string]any{"name": "w", "scope": "write"})

	for _, c := range []struct {
		token, method string
		want          int
	}{
		{read.Token, http.MethodGet, http.StatusServiceUnavailable},
		{read.Token, http.MethodPost, http.StatusForbidden},
		{read.Token, http.MethodPut, http.StatusForbidden},
		{read.Token, http.MethodDelete, http.StatusForbidden},
		{write.Token, http.MethodPost, http.StatusServiceUnavailable},
	} {
		if rec := e.doBearer(t, c.method, "/api/v1/plugins/hello/proxy/items", c.token, nil); rec.Code != c.want {
			t.Errorf("%s with a %s token = %d, want %d: %s", c.method, map[string]string{read.Token: "read", write.Token: "write"}[c.token], rec.Code, c.want, rec.Body.String())
		}
	}
	if rec := e.do(t, http.MethodGet, "/api/v1/plugins/hello/proxy", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("signed out = %d, want 401", rec.Code)
	}

	// A WebSocket starts as a GET, but once upgraded it carries anything,
	// writes included, so it needs a member too.
	for token, want := range map[string]int{read.Token: http.StatusForbidden, write.Token: http.StatusServiceUnavailable} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/plugins/hello/proxy/ws", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		rec := httptest.NewRecorder()
		e.handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("WebSocket upgrade with a %s token = %d, want %d", map[string]string{read.Token: "read", write.Token: "write"}[token], rec.Code, want)
		}
	}
}

// TestDeployScope: a deploy token reads like a read token and may use the
// five deploy routes, and nothing else that writes.
func TestDeployScope(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	ctx := context.Background()
	e.prepareDeployableProject(t, cookie, "app")
	if rec := e.do(t, http.MethodPost, "/api/v1/projects/app/deployments", nil, cookie); rec.Code != http.StatusCreated {
		t.Fatalf("first deploy = %d: %s", rec.Code, rec.Body.String())
	}
	waitForStatus(t, e, cookie, "app", "1", "succeeded")

	dep := e.createToken(t, cookie, map[string]any{"name": "ci", "scope": "deploy"})
	if dep.Scope != "deploy" {
		t.Fatalf("created scope = %q, want deploy", dep.Scope)
	}
	read := e.createToken(t, cookie, map[string]any{"name": "r", "scope": "read"})
	write := e.createToken(t, cookie, map[string]any{"name": "w", "scope": "write"})

	current := func(token string) (scope string, canDeploy bool) {
		t.Helper()
		rec := e.doBearer(t, http.MethodGet, "/api/v1/auth/tokens/current", token, nil)
		var out struct {
			Scope     string `json:"scope"`
			CanDeploy bool   `json:"can_deploy"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("current = %d: %s", rec.Code, rec.Body.String())
		}
		return out.Scope, out.CanDeploy
	}
	for _, c := range []struct {
		token, scope string
		can          bool
	}{{dep.Token, "deploy", true}, {read.Token, "read", false}, {write.Token, "write", true}} {
		if s, can := current(c.token); s != c.scope || can != c.can {
			t.Errorf("current for %s = %q can_deploy %v, want %q %v", c.scope, s, can, c.scope, c.can)
		}
	}

	// Allowed: deploy, rollback, restart, backup. Promote gets past the
	// role check and fails on the missing staging instead.
	if rec := e.doBearer(t, http.MethodPost, "/api/v1/projects/app/deployments", dep.Token, nil); rec.Code != http.StatusCreated {
		t.Fatalf("deploy with deploy token = %d: %s", rec.Code, rec.Body.String())
	}
	waitForStatus(t, e, cookie, "app", "2", "succeeded")
	if rec := e.doBearer(t, http.MethodPost, "/api/v1/projects/app/deployments/1/rollback", dep.Token, nil); rec.Code != http.StatusCreated {
		t.Errorf("rollback = %d: %s", rec.Code, rec.Body.String())
	}
	waitForStatus(t, e, cookie, "app", "3", "succeeded")
	if rec := e.doBearer(t, http.MethodPost, "/api/v1/projects/app/actions/restart", dep.Token, nil); rec.Code != http.StatusNoContent {
		t.Errorf("restart = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.doBearer(t, http.MethodPost, "/api/v1/projects/app/backups", dep.Token, nil); rec.Code == http.StatusForbidden {
		t.Errorf("backup = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.doBearer(t, http.MethodPost, "/api/v1/projects/app/deployments/3/promote", dep.Token, nil); rec.Code == http.StatusForbidden ||
		rec.Code == http.StatusUnauthorized {
		t.Errorf("promote = %d, want past the role check: %s", rec.Code, rec.Body.String())
	}

	// Refused: every other write a member could make.
	refused := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/projects"},
		{http.MethodPost, "/api/v1/projects/scan"},
		{http.MethodPut, "/api/v1/projects/app/env"},
		{http.MethodPut, "/api/v1/projects/app/files/compose.yaml"},
		{http.MethodPut, "/api/v1/projects/app/git"},
		{http.MethodPost, "/api/v1/projects/app/domains"},
		{http.MethodDelete, "/api/v1/projects/app/domains/app.example.com"},
		{http.MethodPost, "/api/v1/projects/app/staging"},
		{http.MethodPost, "/api/v1/projects/app/staging/preflight"},
		{http.MethodPut, "/api/v1/projects/app/staging"},
		{http.MethodGet, "/api/v1/projects/app/staging/diff/compose.yaml"},
		{http.MethodPost, "/api/v1/projects/app/actions/start"},
		{http.MethodPost, "/api/v1/projects/app/actions/stop"},
		{http.MethodPut, "/api/v1/projects/app/backups/schedule"},
		{http.MethodPost, "/api/v1/projects/app/backups/1/restore"},
		{http.MethodPost, "/api/v1/registries"},
		{http.MethodPost, "/api/v1/git/connections"},
	}
	for _, c := range refused {
		if rec := e.doBearer(t, c.method, c.path, dep.Token, map[string]string{}); rec.Code != http.StatusForbidden {
			t.Errorf("deploy token %s %s = %d, want 403: %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
	// Env reads stay masked: a deploy token is never a secrets reader.
	if err := e.api.Projects.SetEnv(ctx, "app", map[string]string{"DB_PASSWORD": "deploy-scope-secret"}); err != nil {
		t.Fatal(err)
	}
	if rec := e.doBearer(t, http.MethodGet, "/api/v1/projects/app/env", dep.Token, nil); strings.Contains(rec.Body.String(), "deploy-scope-secret") {
		t.Errorf("deploy token read an env value: %s", rec.Body.String())
	}
	// A read token still cannot deploy.
	if rec := e.doBearer(t, http.MethodPost, "/api/v1/projects/app/actions/restart", read.Token, nil); rec.Code != http.StatusForbidden {
		t.Errorf("read token restart = %d, want 403", rec.Code)
	}

	// The audit line for the token's deploy names the token.
	entries, err := e.queries.ListAudit(ctx, db.ListAuditParams{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	named := false
	for _, a := range entries {
		if a.Action == "deployment.create" && a.TokenID.Valid && a.TokenID.Int64 == dep.ID {
			named = true
		}
	}
	if !named {
		t.Error("no deployment.create audit entry names the deploy token")
	}

	// A viewer session cannot restart, and a deploy token whose owner is
	// demoted to viewer stops deploying at once.
	for _, u := range []struct{ email, role string }{{"viewer@example.com", "viewer"}, {"second@example.com", "admin"}} {
		if rec := e.do(t, http.MethodPost, "/api/v1/users", map[string]string{
			"email": u.email, "password": "longenoughpassword1", "role": u.role,
		}, cookie); rec.Code != http.StatusCreated {
			t.Fatalf("create %s = %d: %s", u.email, rec.Code, rec.Body.String())
		}
	}
	login := func(email string) *http.Cookie {
		return sessionCookie(t, e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
			"email": email, "password": "longenoughpassword1",
		}))
	}
	if rec := e.do(t, http.MethodPost, "/api/v1/projects/app/actions/restart", nil, login("viewer@example.com")); rec.Code != http.StatusForbidden {
		t.Errorf("viewer session restart = %d, want 403", rec.Code)
	}
	second := login("second@example.com")
	theirs := e.createToken(t, second, map[string]any{"name": "ci2", "scope": "deploy"})
	u, err := e.queries.GetUserByEmail(ctx, "second@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, http.MethodPut, "/api/v1/users/"+itoa(u.ID)+"/role", map[string]string{"role": "viewer"}, cookie); rec.Code/100 != 2 {
		t.Fatalf("demote = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.doBearer(t, http.MethodPost, "/api/v1/projects/app/actions/restart", theirs.Token, nil); rec.Code != http.StatusForbidden {
		t.Errorf("demoted owner's deploy token restart = %d, want 403", rec.Code)
	}
	if _, can := current(theirs.Token); can {
		t.Error("demoted owner's deploy token reports can_deploy")
	}
}
