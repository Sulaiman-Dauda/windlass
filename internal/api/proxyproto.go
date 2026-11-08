package api

import (
	"net/http"
	"strings"
)

// forwardedProtoKey mirrors the server package's context key: the server
// middleware records there whether the peer is a trusted proxy whose
// X-Forwarded-Proto header may be believed. The key type is duplicated
// rather than shared so internal/api does not import internal/server
// (which imports internal/api and would be an import cycle).
type forwardedProtoKey struct{}

// forwardedProto reports whether X-Forwarded-Proto: https may be believed
// for r, i.e. the peer was in WINDLASS_TRUSTED_PROXIES when the request
// arrived. A direct client's header is ignored.
func forwardedProto(r *http.Request) bool {
	trusted, ok := r.Context().Value(forwardedProtoKey{}).(bool)
	return ok && trusted && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
