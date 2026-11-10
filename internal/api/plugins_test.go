package api

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
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

// A plugin must see Windlass's view of the client, never forwarding headers
// the client wrote. The request goes through the same kind of reverse proxy
// the plugin service builds, so this checks what arrives, not what was set.
func TestPluginGetsOnlyWindlassForwardedHeaders(t *testing.T) {
	arrived := make(chan http.Header, 1)
	plugin := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		arrived <- r.Header.Clone()
	}))
	defer plugin.Close()
	target, err := url.Parse(plugin.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)

	for _, c := range []struct {
		name, remoteAddr   string
		https              bool
		wantFor, wantProto string
	}{
		{"direct client", "203.0.113.9:4321", false, "203.0.113.9", "http"},
		{"behind a trusted proxy", "198.51.100.7", true, "198.51.100.7", "https"},
		{"IPv6 behind a trusted proxy", "2001:db8::1", true, "2001:db8::1", "https"},
		{"IPv6 direct", "[2001:db8::1]:4321", false, "2001:db8::1", "http"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/items", nil)
		r.RemoteAddr = c.remoteAddr
		r = SetForwardedProto(r, c.https)
		// Everything a client might send to pass itself off as someone else.
		r.Header.Set("X-Forwarded-For", "10.0.0.1")
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Host", "admin.internal")
		r.Header.Set("X-Forwarded-Port", "443")
		r.Header.Set("X-Real-IP", "10.0.0.1")
		r.Header.Set("Forwarded", "for=10.0.0.1;proto=https")

		setForwardedHeaders(r)
		proxy.ServeHTTP(httptest.NewRecorder(), r)

		var got http.Header
		select {
		case got = <-arrived:
		default:
			t.Fatalf("%s: the plugin got no request", c.name)
		}
		if v := got.Values("X-Forwarded-For"); len(v) != 1 || v[0] != c.wantFor {
			t.Errorf("%s: X-Forwarded-For = %q, want %q", c.name, v, c.wantFor)
		}
		if v := got.Values("X-Forwarded-Proto"); len(v) != 1 || v[0] != c.wantProto {
			t.Errorf("%s: X-Forwarded-Proto = %q, want %q", c.name, v, c.wantProto)
		}
		for _, name := range []string{"X-Forwarded-Host", "X-Forwarded-Port", "X-Real-Ip", "Forwarded"} {
			if v := got.Values(name); len(v) != 0 {
				t.Errorf("%s: client-sent %s reached the plugin: %q", c.name, name, v)
			}
		}
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
