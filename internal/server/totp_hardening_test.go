package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/store/db"
)

// enrolTOTP turns two-factor on for the session's user and returns the secret.
func (e *testEnv) enrolTOTP(t *testing.T, cookie *http.Cookie) string {
	t.Helper()
	rec := e.do(t, http.MethodPost, "/api/v1/auth/totp/setup", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("totp setup = %d: %s", rec.Code, rec.Body.String())
	}
	var setup struct {
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&setup); err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodPost, "/api/v1/auth/totp/verify", map[string]string{"code": totpCodeForTest(t, setup.Secret)}, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("totp verify = %d: %s", rec.Code, rec.Body.String())
	}
	return setup.Secret
}

func (e *testEnv) passwordAloneSignsIn(t *testing.T) bool {
	t.Helper()
	rec := e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "admin@example.com", "password": "supersecret123",
	})
	return rec.Code == http.StatusNoContent
}

// A session on its own, say one stolen from a browser, must not be able to
// remove the second factor.
func TestTOTPCannotBeStrippedWithASession(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	secret := e.enrolTOTP(t, cookie)

	// Starting setup again used to replace the secret and switch 2FA off.
	if rec := e.do(t, http.MethodPost, "/api/v1/auth/totp/setup", nil, cookie); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "totp_enabled") {
		t.Errorf("setup while on = %d: %s", rec.Code, rec.Body.String())
	}
	for _, body := range []any{map[string]string{}, map[string]string{"code": "000000"}} {
		if rec := e.do(t, http.MethodPost, "/api/v1/auth/totp/disable", body, cookie); rec.Code != http.StatusBadRequest {
			t.Errorf("disable with %v = %d: %s", body, rec.Code, rec.Body.String())
		}
	}
	if e.passwordAloneSignsIn(t) {
		t.Fatal("two-factor was removed without a code")
	}

	rec := e.do(t, http.MethodPost, "/api/v1/auth/totp/disable", map[string]string{"code": totpCodeForTest(t, secret)}, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("disable with code = %d: %s", rec.Code, rec.Body.String())
	}
	if !e.passwordAloneSignsIn(t) {
		t.Fatal("two-factor still required after turning it off")
	}
}

// fakeGitHub answers the two GitHub calls a sign-in makes.
type fakeGitHub struct{ email string }

func (f fakeGitHub) RoundTrip(r *http.Request) (*http.Response, error) {
	body := `{"message":"not found"}`
	status := http.StatusNotFound
	switch r.URL.Host + r.URL.Path {
	case "github.com/login/oauth/access_token":
		body, status = `{"access_token":"gho_test"}`, http.StatusOK
	case "api.github.com/user/emails":
		body, status = `[{"email":"`+f.email+`","primary":true,"verified":true}]`, http.StatusOK
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func TestOAuthSignInAsksForTOTP(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(t)
	if rec := e.do(t, http.MethodPut, "/api/v1/system/oauth/github",
		map[string]string{"client_id": "cid", "client_secret": "csec"}, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("configure oauth = %d: %s", rec.Code, rec.Body.String())
	}
	old := http.DefaultTransport
	http.DefaultTransport = fakeGitHub{email: "admin@example.com"}
	t.Cleanup(func() { http.DefaultTransport = old })

	signIn := func() (*http.Response, string) {
		t.Helper()
		rec := e.do(t, http.MethodGet, "/api/v1/auth/oauth/github/start", nil)
		target, _ := url.Parse(rec.Header().Get("Location"))
		var state *http.Cookie
		for _, c := range rec.Result().Cookies() {
			if c.Name == "windlass_oauth_state" {
				state = c
			}
		}
		if state == nil {
			t.Fatalf("start set no state cookie: %d", rec.Code)
		}
		rec = e.do(t, http.MethodGet, "/api/v1/auth/oauth/github/callback?code=c&state="+target.Query().Get("state"), nil, state)
		return rec.Result(), rec.Header().Get("Location")
	}

	// Without two-factor the provider sign-in is enough, as before.
	res, loc := signIn()
	if loc != "/" || cookieNamed(res, "windlass_session") == nil {
		t.Fatalf("plain oauth sign-in = %d %q", res.StatusCode, loc)
	}

	secret := e.enrolTOTP(t, admin)
	res, loc = signIn()
	if loc != "/?signin=totp" || cookieNamed(res, "windlass_session") != nil {
		t.Fatalf("oauth with 2FA = %d %q, session cookie %v; want the code prompt and no session",
			res.StatusCode, loc, cookieNamed(res, "windlass_session"))
	}
	ticket := cookieNamed(res, "windlass_oauth_totp")
	if ticket == nil {
		t.Fatal("no ticket cookie")
	}

	// The ticket is not a session.
	forged := &http.Cookie{Name: "windlass_session", Value: ticket.Value} //nolint:gosec // G124: a test request cookie
	if rec := e.do(t, http.MethodGet, "/api/v1/auth/me", nil, forged); rec.Code != http.StatusUnauthorized {
		t.Errorf("ticket used as a session = %d", rec.Code)
	}
	// And a session is not a ticket.
	if rec := e.do(t, http.MethodPost, "/api/v1/auth/oauth/totp", map[string]string{"code": totpCodeForTest(t, secret)},
		&http.Cookie{Name: "windlass_oauth_totp", Value: admin.Value}); rec.Code != http.StatusUnauthorized { //nolint:gosec // G124: a test request cookie
		t.Errorf("session used as a ticket = %d", rec.Code)
	}

	for _, code := range []string{"", "000000"} {
		rec := e.do(t, http.MethodPost, "/api/v1/auth/oauth/totp", map[string]string{"code": code}, ticket)
		if rec.Code != http.StatusUnauthorized || cookieNamed(rec.Result(), "windlass_session") != nil {
			t.Errorf("code %q = %d: %s", code, rec.Code, rec.Body.String())
		}
	}
	// The wrong code is recorded against the account it was tried on.
	entries, err := e.queries.ListAudit(context.Background(), db.ListAuditParams{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	var sawFailure bool
	for _, entry := range entries {
		if entry.Action == "auth.totp_failed" && entry.UserID.Valid && entry.UserID.Int64 != 0 &&
			entry.ResourceID.String != "" {
			sawFailure = true
		}
	}
	if !sawFailure {
		t.Error("failed oauth code not audited against the user")
	}
	rec := e.do(t, http.MethodPost, "/api/v1/auth/oauth/totp", map[string]string{"code": totpCodeForTest(t, secret)}, ticket)
	session := cookieNamed(rec.Result(), "windlass_session")
	if rec.Code != http.StatusNoContent || session == nil {
		t.Fatalf("correct code = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, http.MethodGet, "/api/v1/auth/me", nil, session); rec.Code != http.StatusOK {
		t.Errorf("session from oauth+code = %d", rec.Code)
	}
}

func cookieNamed(res *http.Response, name string) *http.Cookie {
	for _, c := range res.Cookies() {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	return nil
}

func TestAdminResetsAnotherUsersTOTP(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(t)
	ctx := context.Background()
	member, err := e.queries.CreateUser(ctx, db.CreateUserParams{Email: "m@example.com", Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.queries.SetUserTOTP(ctx, db.SetUserTOTPParams{TotpSecretEnc: []byte("x"), TotpEnabled: 1, ID: member.ID}); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, http.MethodDelete, "/api/v1/users/1/totp", nil, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("reset own 2FA = %d, want 400", rec.Code)
	}
	if rec := e.do(t, http.MethodDelete, "/api/v1/users/"+itoa(member.ID)+"/totp", nil, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("reset member 2FA = %d: %s", rec.Code, rec.Body.String())
	}
	if got, _ := e.queries.GetUserByID(ctx, member.ID); got.TotpEnabled != 0 || len(got.TotpSecretEnc) != 0 {
		t.Errorf("member 2FA still on: %+v", got)
	}
	if rec := e.do(t, http.MethodDelete, "/api/v1/users/999/totp", nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("reset unknown user = %d, want 404", rec.Code)
	}
}
