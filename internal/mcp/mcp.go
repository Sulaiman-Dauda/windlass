// Package mcp serves the Model Context Protocol at /api/v1/mcp so an AI agent
// can inspect and operate Windlass with a personal API token.
//
// The package owns the protocol and the tool catalogue, never business logic.
// Every tool is an in-process request to the /api/v1 router carrying the
// caller's own Authorization header, so role checks, token-only refusals, env
// masking and audit entries are exactly the REST API's. It imports nothing
// else from Windlass; the wiring hands it what it needs through Config.
package mcp

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Protocol revisions the server answers. Both are served statelessly: JSON
// responses only, no sessions, 405 on GET and DELETE.
const (
	// ProtocolLatest is stateless by design: no initialize, server/discover
	// instead, MCP-Protocol-Version, Mcp-Method and Mcp-Name on every request.
	ProtocolLatest = "2026-07-28"
	// ProtocolLegacy is what Claude.ai connectors and many clients still
	// send. It begins with initialize; we answer it without minting a session.
	ProtocolLegacy = "2025-11-25"
)

// Config is everything the server needs from the rest of Windlass.
type Config struct {
	// Version is the Windlass version reported to clients.
	Version string
	// APIPrefix is where the REST API is mounted, normally "/api/v1".
	APIPrefix string
	// EnvValues returns the current env values of a project, used only to
	// scrub them out of log output before it reaches a model. Keys are the
	// variable names, optionally after a prefix ending in a colon; a name
	// that says it holds a secret lowers the length scrubbed. It is called
	// after the logs request for that project has already succeeded, so the
	// caller is known to be allowed to read the project.
	EnvValues func(ctx context.Context, project string) (map[string]string, error)
	// Audit records that a write tool was called through MCP. r carries the
	// authenticated user and token, as set by the auth middleware.
	Audit func(r *http.Request, tool string, project string)
}

// Server is the MCP endpoint. It is an http.Handler mounted inside the
// authenticated /api/v1 group.
type Server struct {
	cfg     Config
	api     http.Handler
	tools   []toolDef
	byName  map[string]*toolDef
	limiter *limiter
	// backupLocks holds a *sync.Mutex per project, so that two create_backup
	// calls at once cannot both pass the backupGap check.
	backupLocks sync.Map
}

// Tool calls allowed per token per minute. An agent takes a model turn per
// call, so a legitimate one stays far below this; a runaway loop does not.
const callsPerMinute = 120

// New builds the server from cfg. The API handler is set later with SetAPI,
// because the router the tools call also mounts this server.
func New(cfg Config) *Server {
	if cfg.APIPrefix == "" {
		cfg.APIPrefix = "/api/v1"
	}
	cfg.APIPrefix = strings.TrimRight(cfg.APIPrefix, "/")
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	s := &Server{
		cfg:     cfg,
		tools:   catalogue(),
		limiter: newLimiter(callsPerMinute, time.Minute),
	}
	s.byName = make(map[string]*toolDef, len(s.tools))
	for i := range s.tools {
		s.byName[s.tools[i].Name] = &s.tools[i]
	}
	return s
}

// SetAPI gives the server the root HTTP handler its tools send in-process
// requests to. It must be called once, before the server takes traffic.
func (s *Server) SetAPI(h http.Handler) {
	s.api = h
}

// ServeHTTP answers one MCP request. It refuses a request without an
// Authorization bearer header (a session cookie is never enough), refuses any
// request carrying an Origin header with 403, answers GET and DELETE with 405,
// negotiates ProtocolLatest or ProtocolLegacy, and dispatches server/discover,
// initialize, tools/list and tools/call. Tool failures come back as results
// with isError set; protocol failures as JSON-RPC errors.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A browser sends Origin; an MCP client does not. Refusing every Origin
	// closes DNS rebinding and any page that got hold of a token.
	if _, ok := r.Header["Origin"]; ok {
		writeRPCError(w, http.StatusForbidden, nil, codeInvalidRequest,
			"browser requests are not accepted here", nil)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeRPCError(w, http.StatusMethodNotAllowed, nil, codeInvalidRequest,
			"POST one JSON-RPC message; this server holds no sessions or streams", nil)
		return
	}
	if _, ok := bearerToken(r); !ok {
		writeAPIError(w, http.StatusUnauthorized, "token_required",
			"MCP needs an API token in the Authorization header; a panel session is not enough")
		return
	}
	if s.api == nil {
		writeRPCError(w, http.StatusServiceUnavailable, nil, codeInternal, "server is starting", nil)
		return
	}
	s.serveRPC(w, r)
}

// tokenPrefix is the Windlass API token prefix. The auth middleware has
// already validated any bearer value carrying it; this package only checks
// that the request came with one rather than with a cookie.
const tokenPrefix = "wl_pat_"

// bearerToken returns the Windlass token in the Authorization header.
func bearerToken(r *http.Request) (string, bool) {
	scheme, value, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, strings.HasPrefix(value, tokenPrefix)
}
