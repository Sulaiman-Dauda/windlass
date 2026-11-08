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

	do := func(remoteAddr, proto string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = remoteAddr
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
	// Trusted proxy forwarding plain http: no HSTS.
	if rec := do("127.0.0.1:1234", "http"); rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("trusted proxy with X-Forwarded-Proto: http got HSTS")
	}
	// No header at all: no HSTS.
	if rec := do("127.0.0.1:1234", ""); rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("no X-Forwarded-Proto got HSTS")
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
