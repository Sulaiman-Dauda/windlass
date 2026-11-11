package api

import (
	"context"
	"net/http"
)

// forwardedHTTPSKey is the context key for the server middleware's decision
// that a trusted proxy forwarded this request from an HTTPS client.
type forwardedHTTPSKey struct{}

// SetForwardedProto records whether a trusted proxy sent
// X-Forwarded-Proto: https with this request. The server's trustedRealIP
// middleware is the only caller: it decides on the original peer address,
// before RemoteAddr is rewritten, so nothing downstream reads the raw
// header. It lives here because internal/server imports internal/api and
// both must read the same key.
func SetForwardedProto(r *http.Request, https bool) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), forwardedHTTPSKey{}, https))
}

// ForwardedProto reports whether the client reached Windlass over HTTPS:
// a direct TLS connection, or a trusted proxy that said so. A request that
// never passed through trustedRealIP has no recorded decision and counts
// as plain HTTP unless it is TLS itself.
func ForwardedProto(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	https, _ := r.Context().Value(forwardedHTTPSKey{}).(bool)
	return https
}
