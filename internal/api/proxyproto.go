package api

import (
	"context"
	"net/http"
	"strings"
)

// ForwardedProtoKey is the context key under which the server middleware
// records whether the peer is a trusted proxy that sent
// X-Forwarded-Proto: https. It lives here, exported, so internal/server
// (which already imports internal/api) can set it and both packages read
// the same value; internal/api cannot import internal/server, that would
// be an import cycle.
type ForwardedProtoKey struct{}

// SetForwardedProto records in the request context whether the peer is a
// trusted proxy whose X-Forwarded-Proto header may be believed. The
// server's trustedRealIP middleware calls it once, on the original peer,
// before RemoteAddr is rewritten.
func SetForwardedProto(r *http.Request, trusted bool) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ForwardedProtoKey{}, trusted))
}

// ForwardedProto reports whether the request is HTTPS: either a direct TLS
// connection (r.TLS != nil) or a trusted proxy's X-Forwarded-Proto: https.
// A direct client's header is not believed.
func ForwardedProto(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	trusted, ok := r.Context().Value(ForwardedProtoKey{}).(bool)
	return ok && trusted && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
