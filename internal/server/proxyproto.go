package server

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// forwardedProtoKey carries whether the peer is a trusted proxy that sent
// X-Forwarded-Proto: https. trustedRealIP sets it; the security-headers
// middleware reads it instead of the raw header, so a direct client cannot
// claim TLS it does not have. internal/api mirrors this key type to read
// the same value (importing internal/server from internal/api would be an
// import cycle).
type forwardedProtoKey struct{}

// ForwardedProto reports whether the request is HTTPS: either a direct TLS
// connection (r.TLS != nil) or a trusted proxy's X-Forwarded-Proto: https.
func ForwardedProto(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	trusted, ok := r.Context().Value(forwardedProtoKey{}).(bool)
	return ok && trusted
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
	ctx := context.WithValue(r.Context(), forwardedProtoKey{}, trustedProxy(r, trusted) && proto)
	return r.WithContext(ctx)
}
