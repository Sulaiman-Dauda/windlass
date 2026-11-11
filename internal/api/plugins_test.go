package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windlass-dev/windlass/internal/auth"
)

func TestStripCookieKeepsOthers(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "theme", Value: "dark"})           //nolint:gosec // G124: a test request cookie
	r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "jwt"}) //nolint:gosec // G124: a test request cookie
	r.AddCookie(&http.Cookie{Name: "plugin_state", Value: "1"})       //nolint:gosec // G124: a test request cookie
	stripCookie(r, auth.SessionCookie)
	if _, err := r.Cookie(auth.SessionCookie); err == nil {
		t.Fatal("session cookie still forwarded")
	}
	if len(r.Cookies()) != 2 {
		t.Fatalf("cookies = %v, want theme and plugin_state", r.Cookies())
	}
}

func TestRemoteIP(t *testing.T) {
	for addr, want := range map[string]string{
		"203.0.113.9:4321":   "203.0.113.9",
		"203.0.113.9":        "203.0.113.9",
		"[2001:db8::1]:4321": "2001:db8::1",
		"2001:db8::1":        "2001:db8::1",
		"::1":                "::1",
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = addr
		if got := remoteIP(r); got != want {
			t.Errorf("remoteIP(%q) = %q, want %q", addr, got, want)
		}
	}
}
