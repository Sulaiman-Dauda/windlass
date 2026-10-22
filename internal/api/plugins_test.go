package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windlass-dev/windlass/internal/auth"
)

func TestStripCookieKeepsOthers(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "theme", Value: "dark"})
	r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "jwt"})
	r.AddCookie(&http.Cookie{Name: "plugin_state", Value: "1"})
	stripCookie(r, auth.SessionCookie)
	if _, err := r.Cookie(auth.SessionCookie); err == nil {
		t.Fatal("session cookie still forwarded")
	}
	if len(r.Cookies()) != 2 {
		t.Fatalf("cookies = %v, want theme and plugin_state", r.Cookies())
	}
}
