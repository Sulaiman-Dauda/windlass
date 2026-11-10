package server

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// X-Forwarded-Proto must only be believed when the peer is a trusted proxy,
// otherwise any direct client could get HSTS over plain HTTP by sending the
// header itself.
func TestForwardedProtoOnlyFromTrustedProxies(t *testing.T) {
	_, loopback, _ := net.ParseCIDR("127.0.0.0/8")
	trusted := []*net.IPNet{loopback}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	h := trustedRealIP(trusted)(securityHeaders(nil)(next))

	do := func(remoteAddr, proto string, forwardedFor ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = remoteAddr
		for _, addr := range forwardedFor {
			req.Header.Add("X-Forwarded-For", addr)
		}
		if proto != "" {
			req.Header.Set("X-Forwarded-Proto", proto)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// Trusted proxy (loopback) asking for https: HSTS is set.
	if rec := do("127.0.0.1:1234", "https"); rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("trusted proxy with X-Forwarded-Proto: https got no HSTS")
	}
	// Untrusted direct client sending the same header: no HSTS.
	if rec := do("203.0.113.9:1234", "https"); rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("untrusted client with X-Forwarded-Proto: https got HSTS")
	}
	// The decision rests on the peer that connected, not on the client
	// address the proxy forwards: RemoteAddr is rewritten to a public
	// address here and the request is still HTTPS.
	if rec := do("127.0.0.1:1234", "HTTPS", "198.51.100.7"); rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("trusted proxy forwarding a public client over https got no HSTS")
	}
	// An untrusted client cannot borrow trust by naming a trusted address
	// in X-Forwarded-For.
	if rec := do("203.0.113.9:1234", "https", "127.0.0.1"); rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("untrusted client claiming a loopback X-Forwarded-For got HSTS")
	}
	// Trusted proxy forwarding plain http: no HSTS.
	if rec := do("127.0.0.1:1234", "http"); rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("trusted proxy with X-Forwarded-Proto: http got HSTS")
	}
	// No header at all: no HSTS.
	if rec := do("127.0.0.1:1234", ""); rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("no X-Forwarded-Proto got HSTS")
	}
	// A proxy that appends its own value instead of replacing the header
	// leaves the client's claim farther left: only the last value counts.
	for _, tc := range []struct {
		values []string
		want   bool
	}{
		{[]string{"https", "http"}, false},
		{[]string{"https, http"}, false},
		{[]string{"http", "https"}, true},
		{[]string{"http, https"}, true},
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		for _, v := range tc.values {
			req.Header.Add("X-Forwarded-Proto", v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Strict-Transport-Security") != ""; got != tc.want {
			t.Errorf("X-Forwarded-Proto %q: HSTS = %v, want %v", tc.values, got, tc.want)
		}
	}
	// A direct TLS connection needs no header.
	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("direct TLS connection got no HSTS")
	}
}

// A proxy may add its own X-Forwarded-For line instead of appending to the
// client's (HAProxy's "option forwardfor" does). The client's line then comes
// first, and reading only the first line would take the client's word.
func TestClientAddressReadsEveryForwardedForLine(t *testing.T) {
	_, loopback, _ := net.ParseCIDR("127.0.0.0/8")
	for _, c := range []struct {
		name   string
		remote string
		lines  []string
		realIP string
		want   string
	}{
		{"proxy adds its own line after the client's", "127.0.0.1:4000", []string{"6.6.6.6", "203.0.113.5"}, "", "203.0.113.5"},
		{"proxy appends to the client's line", "127.0.0.1:4000", []string{"6.6.6.6, 203.0.113.5"}, "", "203.0.113.5"},
		{"client sends two lines, proxy adds a third", "127.0.0.1:4000", []string{"6.6.6.6", "7.7.7.7", "203.0.113.5"}, "", "203.0.113.5"},
		{"a second trusted hop on its own line", "127.0.0.1:4000", []string{"6.6.6.6", "203.0.113.5", "127.0.0.2"}, "", "203.0.113.5"},
		{"only trusted hops, X-Real-IP is used", "127.0.0.1:4000", []string{"127.0.0.2"}, "203.0.113.5", "203.0.113.5"},
		{"untrusted peer is never rewritten", "198.51.100.20:4000", []string{"6.6.6.6", "203.0.113.5"}, "6.6.6.6", "198.51.100.20:4000"},
	} {
		var got string
		h := trustedRealIP([]*net.IPNet{loopback})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got = r.RemoteAddr
		}))
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = c.remote
		for _, line := range c.lines {
			r.Header.Add("X-Forwarded-For", line)
		}
		if c.realIP != "" {
			r.Header.Set("X-Real-IP", c.realIP)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		if got != c.want {
			t.Errorf("%s: client address = %q, want %q", c.name, got, c.want)
		}
	}
}
