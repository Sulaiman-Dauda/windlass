package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// JSON-RPC and MCP error codes. -32020 to -32099 belong to the MCP
// specification from 2026-07-28 on.
const (
	codeParseError         = -32700
	codeInvalidRequest     = -32600
	codeMethodNotFound     = -32601
	codeInvalidParams      = -32602
	codeInternal           = -32603
	codeHeaderMismatch     = -32020
	codeUnsupportedVersion = -32022
)

// Per-request _meta keys of the stateless revision.
const (
	metaProtocolVersion = "io.modelcontextprotocol/protocolVersion"
	metaClientCaps      = "io.modelcontextprotocol/clientCapabilities"
	metaServerInfo      = "io.modelcontextprotocol/serverInfo"
)

// maxBody bounds one JSON-RPC message. Tool arguments here are a project
// name and a number or two.
const maxBody = 1 << 20

// Cache hints for server/discover and tools/list. The catalogue only changes
// when Windlass is upgraded; "private" because tools/list depends on the
// caller's token.
const (
	cacheTTLMs = 3600000
	cacheScope = "private"
)

const instructions = "Windlass runs Docker Compose projects on one server. " +
	"The Compose file and Git are the source of truth: to change an app, change its code " +
	"or Compose file in Git and deploy; this server never edits files or env. " +
	"Start with list_projects, then get_project. Env values are never returned, and log " +
	"text has the project's env values and API tokens replaced with [redacted]. " +
	"With a deploy token, deploy, restart, rollback, promote and create_backup change the server: " +
	"ask the user before each one and pass confirm true only once they agree. " +
	"Prefer a project's staging and promote over changing live directly."

// supportedVersions is what an UnsupportedProtocolVersion error and
// server/discover advertise, newest first.
var supportedVersions = []string{ProtocolLatest, ProtocolLegacy}

// legacyVersions are the handshake-era revisions served with legacy
// semantics. 2025-06-18 has the same tool shapes as 2025-11-25.
var legacyVersions = map[string]bool{ProtocolLegacy: true, "2025-06-18": true}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"` // null when the request's id is unknown, as JSON-RPC requires
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// protoErr is a protocol failure: an HTTP status and a JSON-RPC error.
type protoErr struct {
	status int
	code   int
	msg    string
	data   any
}

func badRequest(code int, format string, a ...any) *protoErr {
	return &protoErr{status: http.StatusBadRequest, code: code, msg: fmt.Sprintf(format, a...)}
}

// call is one parsed JSON-RPC request.
type call struct {
	r      *http.Request
	id     json.RawMessage
	method string
	params map[string]json.RawMessage
	modern bool
}

func (s *Server) serveRPC(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		writeRPCError(w, http.StatusBadRequest, nil, codeParseError, "could not read the request body", nil)
		return
	}
	if len(body) > maxBody {
		writeRPCError(w, http.StatusRequestEntityTooLarge, nil, codeInvalidRequest, "request body is over 1 MiB", nil)
		return
	}
	if t := bytes.TrimLeft(body, " \t\r\n"); len(t) > 0 && t[0] == '[' {
		writeRPCError(w, http.StatusBadRequest, nil, codeInvalidRequest, "JSON-RPC batches are not supported", nil)
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPCError(w, http.StatusBadRequest, nil, codeParseError, "body is not a JSON-RPC message", nil)
		return
	}
	if len(req.ID) > 0 && !validID(req.ID) {
		writeRPCError(w, http.StatusBadRequest, nil, codeInvalidRequest, "id must be a string or an integer", nil)
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		writeRPCError(w, http.StatusBadRequest, req.ID, codeInvalidRequest, `need "jsonrpc": "2.0" and a method`, nil)
		return
	}
	params := map[string]json.RawMessage{}
	if len(req.Params) > 0 && string(req.Params) != "null" {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeRPCError(w, http.StatusBadRequest, req.ID, codeInvalidParams, "params must be an object", nil)
			return
		}
	}
	// Notifications (notifications/initialized and the like) need nothing
	// from a stateless server.
	if len(req.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	c := &call{r: r, id: req.ID, method: req.Method, params: params}
	result, perr := s.dispatch(c)
	if perr != nil {
		writeRPCError(w, perr.status, c.id, perr.code, perr.msg, perr.data)
		return
	}
	if c.modern {
		result["resultType"] = "complete"
		result["_meta"] = map[string]any{metaServerInfo: s.serverInfo()}
	}
	writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: c.id, Result: result})
}

// dispatch picks the protocol era for c and runs its method.
//
// initialize always means the legacy handshake. A request whose _meta names
// a protocol version is modern and must carry matching headers. A request
// with no _meta version and either no MCP-Protocol-Version header or a
// legacy one is a legacy client after its handshake; we serve it statelessly.
func (s *Server) dispatch(c *call) (map[string]any, *protoErr) {
	if c.method == "initialize" {
		return s.initialize(c), nil
	}
	meta := map[string]json.RawMessage{}
	if raw, ok := c.params["_meta"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, badRequest(codeInvalidParams, "params._meta must be an object")
		}
	}
	header := strings.TrimSpace(c.r.Header.Get("MCP-Protocol-Version"))
	_, hasVersion := meta[metaProtocolVersion]
	switch {
	case hasVersion || header == ProtocolLatest:
		c.modern = true
		if perr := validateModern(c, meta, header); perr != nil {
			return nil, perr
		}
		switch c.method {
		case "server/discover":
			return s.discover(c), nil
		case "tools/list":
			return s.toolsList(c)
		case "tools/call":
			return s.toolsCall(c)
		}
		return nil, &protoErr{status: http.StatusNotFound, code: codeMethodNotFound,
			msg: "Method not found: " + c.method}
	case header == "" || legacyVersions[header]:
		switch c.method {
		case "ping":
			return map[string]any{}, nil
		case "tools/list":
			return s.toolsList(c)
		case "tools/call":
			return s.toolsCall(c)
		}
		return nil, &protoErr{status: http.StatusOK, code: codeMethodNotFound,
			msg: "Method not found: " + c.method}
	default:
		return nil, unsupportedVersion(header)
	}
}

func unsupportedVersion(requested string) *protoErr {
	return &protoErr{status: http.StatusBadRequest, code: codeUnsupportedVersion,
		msg:  "Unsupported protocol version",
		data: map[string]any{"supported": supportedVersions, "requested": requested}}
}

// validateModern applies the 2026-07-28 rules: the version and client
// capabilities in _meta, and the MCP-Protocol-Version, Mcp-Method and
// Mcp-Name headers matching the body, so an intermediary routing on headers
// can never see a different request from the one we run.
func validateModern(c *call, meta map[string]json.RawMessage, header string) *protoErr {
	var version string
	if raw, ok := meta[metaProtocolVersion]; !ok || json.Unmarshal(raw, &version) != nil || version == "" {
		return badRequest(codeInvalidParams, "params._meta[%q] must name the protocol version", metaProtocolVersion)
	}
	if header == "" {
		return badRequest(codeHeaderMismatch, "Header mismatch: MCP-Protocol-Version header is required")
	}
	if header != version {
		return badRequest(codeHeaderMismatch,
			"Header mismatch: MCP-Protocol-Version header value '%s' does not match body value '%s'", header, version)
	}
	if version != ProtocolLatest {
		return unsupportedVersion(version)
	}
	if raw, ok := meta[metaClientCaps]; !ok || len(bytes.TrimSpace(raw)) == 0 || raw[0] != '{' {
		return badRequest(codeInvalidParams, "params._meta[%q] must be an object", metaClientCaps)
	}
	method := c.r.Header.Get("Mcp-Method")
	if method == "" {
		return badRequest(codeHeaderMismatch, "Header mismatch: Mcp-Method header is required")
	}
	if method != c.method {
		return badRequest(codeHeaderMismatch,
			"Header mismatch: Mcp-Method header value '%s' does not match body value '%s'", method, c.method)
	}
	field := ""
	switch c.method {
	case "tools/call", "prompts/get":
		field = "name"
	case "resources/read":
		field = "uri"
	}
	if field == "" {
		return nil
	}
	var bodyName string
	if raw, ok := c.params[field]; ok {
		_ = json.Unmarshal(raw, &bodyName)
	}
	values, ok := c.r.Header["Mcp-Name"]
	if !ok || len(values) != 1 {
		return badRequest(codeHeaderMismatch, "Header mismatch: one Mcp-Name header is required for %s", c.method)
	}
	name, ok := decodeHeaderValue(values[0])
	if !ok {
		return badRequest(codeHeaderMismatch, "Header mismatch: Mcp-Name header is not valid base64")
	}
	if name != bodyName {
		return badRequest(codeHeaderMismatch,
			"Header mismatch: Mcp-Name header value '%s' does not match body value '%s'", name, bodyName)
	}
	return nil
}

// decodeHeaderValue undoes the =?base64?...?= sentinel encoding clients use
// for header values that are not plain ASCII.
func decodeHeaderValue(v string) (string, bool) {
	const prefix, suffix = "=?base64?", "?="
	if !strings.HasPrefix(v, prefix) || !strings.HasSuffix(v, suffix) || len(v) < len(prefix)+len(suffix) {
		return v, true
	}
	b, err := base64.StdEncoding.DecodeString(v[len(prefix) : len(v)-len(suffix)])
	if err != nil {
		return "", false
	}
	return string(b), true
}

func (s *Server) serverInfo() map[string]string {
	return map[string]string{"name": "windlass", "title": "Windlass", "version": s.cfg.Version}
}

func capabilities() map[string]any {
	return map[string]any{"tools": map[string]any{"listChanged": false}}
}

// instructionsFor is the instructions plus a sentence on what the caller's
// token may do. Without it, a model with a read token sees only that the
// write tools are missing, and some take that as tools still to load and
// offer to deploy. If the token cannot be looked up, the sentence is left out.
func (s *Server) instructionsFor(r *http.Request) string {
	info, err := s.currentToken(r)
	if err != nil {
		return instructions
	}
	if info.CanDeploy {
		return instructions + " This token's scope is " + info.Scope + ": it can deploy, restart, roll back, promote and create backups."
	}
	owner := ""
	if info.Scope != "read" && info.User.Role == "viewer" {
		owner = ", but its owner is a viewer"
	}
	return instructions + " This token's scope is " + info.Scope + owner +
		": it cannot deploy, restart, roll back, promote or create backups, so those tools are not listed. " +
		"If the user asks for one of them, say this token cannot do it and suggest a token with deploy scope (Settings, API tokens) or the panel."
}

// discover answers server/discover.
func (s *Server) discover(c *call) map[string]any {
	return map[string]any{
		"supportedVersions": supportedVersions,
		"capabilities":      capabilities(),
		"instructions":      s.instructionsFor(c.r),
		"ttlMs":             cacheTTLMs,
		"cacheScope":        cacheScope,
	}
}

// initialize answers the legacy handshake. It agrees to the client's
// version when it is one we serve, and otherwise offers ProtocolLegacy. It
// never mints an Mcp-Session-Id: every later request stands alone.
func (s *Server) initialize(c *call) map[string]any {
	version := ProtocolLegacy
	var requested string
	if raw, ok := c.params["protocolVersion"]; ok && json.Unmarshal(raw, &requested) == nil && legacyVersions[requested] {
		version = requested
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    capabilities(),
		"serverInfo":      s.serverInfo(),
		"instructions":    s.instructionsFor(c.r),
	}
}

// toolsList answers tools/list with the tools the caller's token can use.
func (s *Server) toolsList(c *call) (map[string]any, *protoErr) {
	if raw, ok := c.params["cursor"]; ok && string(raw) != "null" {
		// We never hand out cursors: the whole list fits in one page.
		return nil, &protoErr{status: http.StatusOK, code: codeInvalidParams, msg: "invalid cursor"}
	}
	tools := s.visibleTools(c.r)
	list := make([]Tool, 0, len(tools))
	for _, t := range tools {
		list = append(list, t.Tool)
	}
	result := map[string]any{"tools": list}
	if c.modern {
		result["ttlMs"] = cacheTTLMs
		result["cacheScope"] = cacheScope
	}
	return result, nil
}

// toolsCall runs one tool. Unknown tools and malformed params are protocol
// errors; anything the model can correct (bad arguments, a refusal from the
// API) is a result with isError set, so the model sees it and retries.
func (s *Server) toolsCall(c *call) (map[string]any, *protoErr) {
	var name string
	if raw, ok := c.params["name"]; !ok || json.Unmarshal(raw, &name) != nil || name == "" {
		return nil, &protoErr{status: http.StatusOK, code: codeInvalidParams, msg: "params.name must name a tool"}
	}
	def, ok := s.byName[name]
	if !ok {
		return nil, &protoErr{status: http.StatusOK, code: codeInvalidParams, msg: "Unknown tool: " + name}
	}
	args := map[string]json.RawMessage{}
	if raw, ok := c.params["arguments"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &protoErr{status: http.StatusOK, code: codeInvalidParams, msg: "params.arguments must be an object"}
		}
	}
	if !s.limiter.allow(tokenKey(c.r)) {
		return toolResult(nil, fmt.Errorf(
			"rate limited: at most %d tool calls a minute per token. Wait a minute and retry", callsPerMinute)), nil
	}
	// Scope before arguments, so a read token learns it needs another token
	// rather than fixing a confirm it can never use.
	if err := s.checkScope(c.r, def); err != nil {
		return toolResult(nil, err), nil
	}
	if err := def.checkArgs(args); err != nil {
		return toolResult(nil, err), nil
	}
	out, err := def.run(&toolCtx{s: s, r: c.r, args: args, tool: def})
	return toolResult(out, err), nil
}

// toolResult shapes a tool's output: structuredContent plus the same JSON
// as text for clients that read only text. API tokens are scrubbed from the
// JSON whatever the tool, which is safe on encoded JSON because a token is
// plain ASCII letters, digits and underscores.
func toolResult(out any, err error) map[string]any {
	if err != nil {
		return map[string]any{
			"content": []map[string]string{{"type": "text", "text": scrubTokens(err.Error())}},
			"isError": true,
		}
	}
	b, merr := json.Marshal(out)
	if merr != nil {
		return toolResult(nil, fmt.Errorf("could not encode the result: %v", merr))
	}
	text := scrubTokens(string(b))
	return map[string]any{
		"content":           []map[string]string{{"type": "text", "text": text}},
		"structuredContent": json.RawMessage(text),
		"isError":           false,
	}
}

// tokenKey identifies the caller's token for rate limiting by its public id.
func tokenKey(r *http.Request) string {
	tok, _ := bearerToken(r)
	if len(tok) >= len(tokenPrefix)+12 {
		return tok[len(tokenPrefix) : len(tokenPrefix)+12]
	}
	return tok
}

func validID(id json.RawMessage) bool {
	id = bytes.TrimSpace(id)
	if len(id) == 0 {
		return false
	}
	switch id[0] {
	case '"':
		var s string
		return json.Unmarshal(id, &s) == nil
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		var n json.Number
		return json.Unmarshal(id, &n) == nil
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeRPCError(w http.ResponseWriter, status int, id json.RawMessage, code int, msg string, data any) {
	writeJSON(w, status, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg, Data: data}})
}

// writeAPIError answers in the REST API's error shape, for refusals that
// happen before any JSON-RPC is read, like the auth middleware's 401.
func writeAPIError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}
