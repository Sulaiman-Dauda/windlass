package server

import (
	"net"
	"net/http"
	"strings"

	"github.com/windlass-dev/windlass/internal/api"
)

// ForwardedProto reports whether the request is HTTPS: either a direct TLS
// connection (r.TLS != nil) or a trusted proxy's X-Forwarded-Proto: https.
// The decision is read through internal/api's context key so the api
// package's cookies and URLs see the same value as the security headers.
func ForwardedProto(r *http.Request) bool {
	return api.ForwardedProto(r)
}

// trustedProxy reports whether the request's peer address is in trusted.
func trustedProxy(r *http.Request, trusted []*net.IPNet) bool {
	peer := r.RemoteAddr
	if host, _, err := net.SplitHostPort(peer); err == nil {
		peer = host
	}
	ip := net.ParseIP(strings.Trim(peer, "[]"))
	if ip == nil {
		return false
	}
	for _, network := range trusted {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// withForwardedProto records in the request context whether the peer is a
// trusted proxy whose X-Forwarded-Proto header may be believed. It runs as
// part of trustedRealIP so the check happens once, on the original peer,
// before RemoteAddr is rewritten.
func withForwardedProto(r *http.Request, trusted []*net.IPNet) *http.Request {
	proto := strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	return api.SetForwardedProto(r, trustedProxy(r, trusted) && proto)
}
