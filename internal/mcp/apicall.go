package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Bounds on what one in-process API call may hold in memory.
const (
	maxJSONBody = 4 << 20
	// maxEventBytes caps one SSE event; a bigger one is dropped and the
	// result marked truncated.
	maxEventBytes = 1 << 20
	// maxStreamBytes caps the SSE events kept from one call, before
	// scrubbing. Scrubbing grows text by at most 10/6 (a 6-byte value
	// becomes [redacted]), so this leaves room above MaxLogBytes.
	maxStreamBytes = 2 << 20
)

// defaultCallTimeout bounds any API call a tool makes, so a stuck handler
// cannot hold an MCP request open.
const defaultCallTimeout = 20 * time.Second

// toolCtx is what a tool's run function gets: the caller's request (for
// its Authorization header and address) and the validated arguments.
type toolCtx struct {
	s    *Server
	r    *http.Request
	args map[string]json.RawMessage
	tool *toolDef
}

func (t *toolCtx) str(name string) string {
	var s string
	if raw, ok := t.args[name]; ok {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

func (t *toolCtx) int(name string, def int64) int64 {
	raw, ok := t.args[name]
	if !ok || string(raw) == "null" {
		return def
	}
	var n json.Number
	if json.Unmarshal(raw, &n) != nil {
		return def
	}
	if v, err := n.Int64(); err == nil {
		return v
	}
	if f, err := n.Float64(); err == nil {
		return int64(f)
	}
	return def
}

// apiError is a non-2xx answer from the REST API, worded for the model.
type apiError struct {
	status  int
	code    string
	message string
	// body is the raw answer, for a route that sends a record with its
	// error (a failed backup answers 502 with the backup row).
	body []byte
}

func (e *apiError) Error() string {
	msg := e.message
	if msg == "" {
		msg = http.StatusText(e.status)
	}
	switch e.status {
	case http.StatusUnauthorized:
		return "the API token was refused: " + msg
	case http.StatusForbidden:
		return "this token is not allowed to do that: " + msg
	case http.StatusNotFound:
		return "not found: " + msg
	}
	if e.code != "" {
		return fmt.Sprintf("Windlass answered %d %s: %s", e.status, e.code, msg)
	}
	return fmt.Sprintf("Windlass answered %d: %s", e.status, msg)
}

func isNotFound(err error) bool {
	e, ok := err.(*apiError)
	return ok && e.status == http.StatusNotFound
}

// projectPath builds an API path under /projects/{name}. Names have been
// checked against projectNameRE, so they need no escaping, but escape
// anyway in case a caller forgets.
func projectPath(project string, rest ...string) string {
	p := "/projects/" + url.PathEscape(project)
	for _, r := range rest {
		p += "/" + r
	}
	return p
}

// newAPIRequest builds the in-process request. It carries only the
// caller's Authorization header and address, so the router authenticates
// the token again and applies its own role and token checks. Its context
// is fresh rather than derived from the MCP request: the router must not
// see the outer request's route or auth values. Cancelling the MCP request
// cancels it too, unless detached: a write Windlass has started is not cut
// off halfway because the client hung up.
func (t *toolCtx) newAPIRequest(method, path string, query url.Values, body []byte, timeout time.Duration, detached bool) (*http.Request, func(), error) {
	target := t.s.cfg.APIPrefix + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	stop := func() bool { return false }
	if !detached {
		stop = context.AfterFunc(t.r.Context(), cancel)
	}
	done := func() { stop(); cancel() }

	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		done()
		return nil, nil, err
	}
	if body == nil {
		// As net/http does for a server request: handlers may read r.Body
		// without checking it, and the backup route does.
		req.Body = http.NoBody
	}
	req.RequestURI = req.URL.RequestURI()
	req.Host = t.r.Host
	req.RemoteAddr = t.r.RemoteAddr
	req.TLS = t.r.TLS
	req.Header.Set("Authorization", t.r.Header.Get("Authorization"))
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, done, nil
}

// getJSON GETs an API path and returns its JSON body, or an apiError.
func (t *toolCtx) getJSON(path string, query url.Values) (json.RawMessage, error) {
	return t.sendJSON(http.MethodGet, path, query, nil)
}

func (t *toolCtx) sendJSON(method, path string, query url.Values, body any) (json.RawMessage, error) {
	var b []byte
	if body != nil {
		var err error
		if b, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	req, done, err := t.newAPIRequest(method, path, query, b, defaultCallTimeout, false)
	if err != nil {
		return nil, err
	}
	defer done()
	rec := newRecorder()
	t.s.api.ServeHTTP(rec, req)
	return rec.jsonResult()
}

// stream GETs an SSE route and returns the events it sent before it
// returned or timeout passed. A timeout is not an error: some routes stream
// until the client leaves, and what arrived by then is the answer.
func (t *toolCtx) stream(path string, query url.Values, timeout time.Duration, maxEvents int) (*sseResult, error) {
	req, done, err := t.newAPIRequest(http.MethodGet, path, query, nil, timeout, false)
	if err != nil {
		return nil, err
	}
	defer done()
	rec := newRecorder()
	rec.maxEvents = maxEvents
	t.s.api.ServeHTTP(rec, req)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if !rec.sse {
		// The route refused before it started streaming.
		if _, err := rec.jsonResultLocked(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("the API answered %d without an event stream", rec.status)
	}
	if t.r.Context().Err() != nil {
		return nil, t.r.Context().Err()
	}
	rec.parser.finish()
	return &sseResult{events: rec.parser.events, truncated: rec.parser.dropped}, nil
}

type sseEvent struct {
	typ  string
	data []byte
}

type sseResult struct {
	events    []sseEvent
	truncated bool
}

// recorder is the ResponseWriter for in-process calls. It holds a JSON
// body up to maxJSONBody, or parses an event stream as it arrives and keeps
// only the newest events within maxEvents and maxStreamBytes.
type recorder struct {
	mu        sync.Mutex
	header    http.Header
	status    int
	wrote     bool
	sse       bool
	body      bytes.Buffer
	overflow  bool
	maxEvents int
	parser    *sseParser
}

func newRecorder() *recorder {
	return &recorder{header: http.Header{}, status: http.StatusOK}
}

func (rw *recorder) Header() http.Header { return rw.header }

func (rw *recorder) WriteHeader(code int) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	rw.writeHeaderLocked(code)
}

func (rw *recorder) writeHeaderLocked(code int) {
	if rw.wrote || code < 200 {
		return
	}
	rw.wrote = true
	rw.status = code
	if strings.HasPrefix(rw.header.Get("Content-Type"), "text/event-stream") {
		rw.sse = true
		max := rw.maxEvents
		if max <= 0 {
			max = MaxListItems
		}
		rw.parser = &sseParser{maxEvents: max}
	}
}

func (rw *recorder) Write(p []byte) (int, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if !rw.wrote {
		rw.writeHeaderLocked(http.StatusOK)
	}
	if rw.sse {
		rw.parser.write(p)
		return len(p), nil
	}
	if rw.body.Len()+len(p) > maxJSONBody {
		rw.overflow = true
		return len(p), nil
	}
	rw.body.Write(p)
	return len(p), nil
}

// Flush satisfies http.Flusher, which the SSE routes require. Everything is
// already in memory, so there is nothing to do.
func (rw *recorder) Flush() {}

func (rw *recorder) jsonResult() (json.RawMessage, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	return rw.jsonResultLocked()
}

func (rw *recorder) jsonResultLocked() (json.RawMessage, error) {
	if rw.status < 200 || rw.status > 299 {
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rw.body.Bytes(), &env)
		return nil, &apiError{status: rw.status, code: env.Error.Code, message: env.Error.Message,
			body: bytes.Clone(rw.body.Bytes())}
	}
	if rw.overflow {
		return nil, fmt.Errorf("the API sent more than %d MiB; ask for less", maxJSONBody>>20)
	}
	if rw.status == http.StatusNoContent || rw.body.Len() == 0 {
		return json.RawMessage("null"), nil
	}
	return json.RawMessage(bytes.Clone(rw.body.Bytes())), nil
}

// sseParser reads text/event-stream incrementally. It keeps "event" and
// "data" fields, ignores ids and comments, and drops the oldest events once
// it holds maxEvents or maxStreamBytes.
type sseParser struct {
	maxEvents int
	pending   []byte
	skipLine  bool
	typ       string
	data      []byte
	tooBig    bool
	events    []sseEvent
	bytes     int
	dropped   bool
}

func (p *sseParser) write(b []byte) {
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			if p.skipLine {
				return
			}
			if len(p.pending)+len(b) > maxEventBytes {
				p.pending = p.pending[:0]
				p.skipLine = true
				p.tooBig = true
				return
			}
			p.pending = append(p.pending, b...)
			return
		}
		if p.skipLine {
			p.skipLine = false
		} else {
			line := append(p.pending, b[:i]...)
			p.line(bytes.TrimSuffix(line, []byte("\r")))
		}
		p.pending = p.pending[:0]
		b = b[i+1:]
	}
}

func (p *sseParser) line(l []byte) {
	if len(l) == 0 {
		p.dispatch()
		return
	}
	if l[0] == ':' {
		return
	}
	field, value, _ := bytes.Cut(l, []byte(":"))
	value = bytes.TrimPrefix(value, []byte(" "))
	switch string(field) {
	case "event":
		p.typ = string(value)
	case "data":
		if p.tooBig {
			return
		}
		if len(p.data)+len(value)+1 > maxEventBytes {
			p.tooBig = true
			p.data = nil
			return
		}
		if p.data != nil {
			p.data = append(p.data, '\n')
		}
		p.data = append(p.data, value...)
		if p.data == nil {
			p.data = []byte{}
		}
	}
}

func (p *sseParser) dispatch() {
	typ, data, tooBig := p.typ, p.data, p.tooBig
	p.typ, p.data, p.tooBig = "", nil, false
	if tooBig {
		p.dropped = true
		return
	}
	if data == nil {
		return
	}
	if typ == "" {
		typ = "message"
	}
	p.events = append(p.events, sseEvent{typ: typ, data: data})
	p.bytes += len(data)
	for len(p.events) > p.maxEvents || (p.bytes > maxStreamBytes && len(p.events) > 1) {
		p.bytes -= len(p.events[0].data)
		p.events[0] = sseEvent{}
		p.events = p.events[1:]
		p.dropped = true
	}
}

// finish dispatches an event the stream ended without a blank line after.
func (p *sseParser) finish() {
	if len(p.pending) > 0 && !p.skipLine {
		p.line(p.pending)
		p.pending = nil
	}
	p.dispatch()
}

// queryInt is a small helper for building query strings.
func queryInt(n int64) string { return strconv.FormatInt(n, 10) }
