package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/store/db"
)

func TestTOTPEnrollmentAndLogin(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)

	// Enroll.
	rec := e.do(t, http.MethodPost, "/api/v1/auth/totp/setup", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("totp setup = %d: %s", rec.Code, rec.Body.String())
	}
	var setup struct {
		Secret     string `json:"secret"`
		OTPAuthURL string `json:"otpauth_url"`
	}
	json.NewDecoder(rec.Body).Decode(&setup)
	if setup.Secret == "" || !strings.Contains(setup.OTPAuthURL, "otpauth://") {
		t.Fatalf("setup = %+v", setup)
	}

	// Wrong code doesn't activate.
	rec = e.do(t, http.MethodPost, "/api/v1/auth/totp/verify", map[string]string{"code": "000000"}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad verify = %d", rec.Code)
	}

	// Correct code activates (computed from the secret like an app would).
	code := totpCodeForTest(t, setup.Secret)
	rec = e.do(t, http.MethodPost, "/api/v1/auth/totp/verify", map[string]string{"code": code}, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("verify = %d: %s", rec.Code, rec.Body.String())
	}

	// Password alone is no longer enough.
	rec = e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "admin@example.com", "password": "supersecret123",
	})
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "totp_required") {
		t.Fatalf("login without code = %d: %s", rec.Code, rec.Body.String())
	}

	// Password + code signs in.
	rec = e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "admin@example.com", "password": "supersecret123",
		"totp_code": totpCodeForTest(t, setup.Secret),
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login with code = %d: %s", rec.Code, rec.Body.String())
	}
}

// totpCodeForTest mirrors an authenticator app.
func totpCodeForTest(t *testing.T, secret string) string {
	t.Helper()
	code, err := auth.TOTPCodeForTesting(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestUsersAdminAPI(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)

	// Create a member.
	rec := e.do(t, http.MethodPost, "/api/v1/users", map[string]string{
		"email": "dev@example.com", "password": "devpassword123", "role": "member",
	}, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user = %d: %s", rec.Code, rec.Body.String())
	}

	// Duplicate email conflicts; bad role rejected.
	rec = e.do(t, http.MethodPost, "/api/v1/users", map[string]string{
		"email": "dev@example.com", "password": "devpassword123", "role": "member",
	}, cookie)
	if rec.Code != http.StatusConflict {
		t.Errorf("duplicate = %d", rec.Code)
	}
	rec = e.do(t, http.MethodPost, "/api/v1/users", map[string]string{
		"email": "x@example.com", "password": "devpassword123", "role": "root",
	}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad role = %d", rec.Code)
	}

	// List includes both.
	rec = e.do(t, http.MethodGet, "/api/v1/users", nil, cookie)
	if !strings.Contains(rec.Body.String(), "dev@example.com") {
		t.Fatalf("list = %s", rec.Body.String())
	}

	// The member can log in but cannot manage users.
	rec = e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "dev@example.com", "password": "devpassword123",
	})
	member := sessionCookie(t, rec)
	if rec := e.do(t, http.MethodGet, "/api/v1/users", nil, member); rec.Code != http.StatusForbidden {
		t.Errorf("member users list = %d, want 403", rec.Code)
	}

	// Admin cannot delete themselves.
	rec = e.do(t, http.MethodDelete, "/api/v1/users/1", nil, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("self delete = %d", rec.Code)
	}

	// Role change + delete for the member.
	rec = e.do(t, http.MethodPut, "/api/v1/users/2/role", map[string]string{"role": "viewer"}, cookie)
	if rec.Code != http.StatusNoContent {
		t.Errorf("set role = %d", rec.Code)
	}
	rec = e.do(t, http.MethodDelete, "/api/v1/users/2", nil, cookie)
	if rec.Code != http.StatusNoContent {
		t.Errorf("delete = %d", rec.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newTestEnv(t)
	e.login(t) // claim instance

	var lastCode int
	for i := 0; i < 25; i++ {
		rec := e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
			"email": "admin@example.com", "password": "wrong",
		})
		lastCode = rec.Code
	}
	if lastCode != http.StatusTooManyRequests {
		t.Errorf("after 25 bad logins: %d, want 429", lastCode)
	}

	// The audit log recorded rate limiting.
	rows, _ := e.queries.ListAudit(context.Background(), db.ListAuditParams{Limit: 100, Offset: 0})
	found := false
	for _, r := range rows {
		if r.Action == "auth.rate_limited" {
			found = true
		}
	}
	if !found {
		t.Error("rate limiting not audited")
	}
}

func TestForwardedIPOnlyTrustedFromConfiguredProxy(t *testing.T) {
	doSetup := func(e *testEnv, remote, forwarded string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/setup",
			strings.NewReader(`{"token":"wrong","email":"x@example.com","password":"longpassword"}`))
		req.RemoteAddr = remote
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", forwarded)
		rec := httptest.NewRecorder()
		e.handler.ServeHTTP(rec, req)
		return rec.Code
	}

	direct := newTestEnv(t)
	for i := 0; i < 25; i++ {
		code := doSetup(direct, "198.51.100.20:1234", fmt.Sprintf("203.0.113.%d", i+1))
		if i == 24 && code != http.StatusTooManyRequests {
			t.Fatalf("spoofed forwarding headers bypassed direct-client limit: %d", code)
		}
	}

	proxied := newTestEnv(t)
	for i := 0; i < 25; i++ {
		code := doSetup(proxied, "127.0.0.1:1234", fmt.Sprintf("203.0.113.%d", i+1))
		if code == http.StatusTooManyRequests {
			t.Fatalf("trusted proxy clients collapsed into one rate-limit bucket at request %d", i+1)
		}
	}
}

func TestCredentialBodyLimit(t *testing.T) {
	e := newTestEnv(t)
	rec := e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "admin@example.com", "password": strings.Repeat("x", 70<<10),
	})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized login = %d, want 413", rec.Code)
	}
}
