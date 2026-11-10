package plugins

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/migrations"
)

func newService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	sqlDB, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := store.Migrate(sqlDB, migrations.FS); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(db.New(sqlDB), dir, logger), dir
}

func writePlugin(t *testing.T, root, name, manifest string) {
	t.Helper()
	dir := filepath.Join(root, "plugins", name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryAndValidation(t *testing.T) {
	s, root := newService(t)
	ctx := context.Background()

	// No plugins dir → empty list, no error.
	list, err := s.List(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("empty = %v, %v", list, err)
	}

	writePlugin(t, root, "good", `{"name":"good","version":"1.0","command":"good"}`)
	writePlugin(t, root, "mismatch", `{"name":"other","version":"1.0","command":"x"}`)
	writePlugin(t, root, "escape", `{"name":"escape","version":"1.0","command":"../../evil"}`)
	writePlugin(t, root, "absolute", `{"name":"absolute","version":"1.0","command":"/bin/sh"}`)

	list, err = s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "good" {
		t.Errorf("list = %+v, want only 'good'", list)
	}
	if list[0].Enabled || list[0].Running {
		t.Errorf("fresh plugin should be disabled: %+v", list[0])
	}
}

func TestEnableMissingPlugin(t *testing.T) {
	s, _ := newService(t)
	if err := s.Enable(context.Background(), "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestProxyUnavailableWhenNotRunning(t *testing.T) {
	s, root := newService(t)
	writePlugin(t, root, "good", `{"name":"good","version":"1.0","command":"good"}`)

	rec := httptest.NewRecorder()
	s.Proxy("good", Client{}, rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 503 {
		t.Errorf("proxy to stopped plugin = %d, want 503", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON like every other API error", ct)
	}
}

// A plugin that is running but not listening answers in the API's error
// shape, not with the reverse proxy's empty 502.
func TestProxyUnreachablePluginAnswersJSON(t *testing.T) {
	s, _ := newService(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	s.running["good"] = &instance{addr: addr, proxy: newPluginProxy(addr, s.logger)}

	rec := httptest.NewRecorder()
	s.Proxy("good", Client{}, rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 502 {
		t.Fatalf("proxy to unreachable plugin = %d, want 502", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
	if !strings.Contains(rec.Body.String(), `"code":"plugin_unreachable"`) {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestDisableIsIdempotent(t *testing.T) {
	s, root := newService(t)
	writePlugin(t, root, "good", `{"name":"good","version":"1.0","command":"good"}`)
	ctx := context.Background()

	if err := s.Disable(ctx, "good"); err != nil {
		t.Errorf("disable never-enabled: %v", err)
	}
	list, _ := s.List(ctx)
	if len(list) != 1 || list[0].Enabled {
		t.Errorf("list = %+v", list)
	}
}

// A plugin must see Windlass's view of the client and nothing the client
// wrote about itself. The requests go through the service's own proxy to a
// listening plugin, so this checks what arrives.
func TestProxySendsOnlyWindlassForwardedHeaders(t *testing.T) {
	arrived := make(chan *http.Request, 1)
	plugin := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		arrived <- r.Clone(context.Background())
	}))
	defer plugin.Close()
	addr := strings.TrimPrefix(plugin.URL, "http://")
	s, _ := newService(t)
	s.running["good"] = &instance{addr: addr, proxy: newPluginProxy(addr, s.logger)}

	// Everything a client might send to pass itself off as someone else.
	spoofed := map[string]string{
		"X-Forwarded-For":            "10.0.0.1",
		"X-Forwarded-Proto":          "https",
		"X-Forwarded-Host":           "admin.internal",
		"X-Forwarded-Port":           "443",
		"X-Forwarded":                "for=10.0.0.1",
		"X-Real-IP":                  "10.0.0.1",
		"Forwarded":                  "for=10.0.0.1;proto=https",
		"X_Forwarded_For":            "10.0.0.1",
		"X_Real_IP":                  "10.0.0.1",
		"True-Client-IP":             "10.0.0.1",
		"CF-Connecting-IP":           "10.0.0.1",
		"X-Client-IP":                "10.0.0.1",
		"X-Original-Forwarded-For":   "10.0.0.1",
		"X-Scheme":                   "https",
		"X-Url-Scheme":               "https",
		"Front-End-Https":            "on",
		"X-Forwarded-Ssl":            "on",
		"Fastly-Client-IP":           "10.0.0.1",
		"X-Envoy-External-Address":   "10.0.0.1",
		"X-Remote-Addr":              "10.0.0.1",
		"X-Remote-IP":                "10.0.0.1",
		"X-ProxyUser-IP":             "10.0.0.1",
		"X-Original-Host":            "admin.internal",
		"X-Host":                     "admin.internal",
		"Forwarded-For":              "10.0.0.1",
		"X-Originating-IP":           "10.0.0.1",
		"Proxy-Client-IP":            "10.0.0.1",
		"WL-Proxy-Client-IP":         "10.0.0.1",
		"X-Appengine-User-Ip":        "10.0.0.1",
		"X-Azure-ClientIP":           "10.0.0.1",
		"X-Azure-SocketIP":           "10.0.0.1",
		"X-ARR-ClientIP":             "10.0.0.1",
		"X-Original-For":             "10.0.0.1",
		"CloudFront-Viewer-Address":  "10.0.0.1:443",
		"HTTP_X_FORWARDED_FOR":       "10.0.0.1",
		"HTTP_CLIENT_IP":             "10.0.0.1",
		"X-Https":                    "on",
		"X-Proto":                    "https",
		"X-Original-Proto":           "https",
		"X-Client-Proto":             "https",
		"X-Real-Proto":               "https",
		"X-Real-Scheme":              "https",
		"X-ARR-SSL":                  "1",
		"CloudFront-Forwarded-Proto": "https",
		"CF-Visitor":                 `{"scheme":"https"}`,
		"X-Real-Host":                "admin.internal",
		"X-Original-URL":             "/admin",
		"X-Rewrite-URL":              "/admin",
	}

	for _, c := range []struct {
		name       string
		client     Client
		remoteAddr string
		connection string
		wantFor    []string
		wantProto  string
	}{
		{"direct client", Client{IP: "203.0.113.9"}, "203.0.113.9:4321", "", []string{"203.0.113.9"}, "http"},
		{"behind a trusted proxy", Client{IP: "198.51.100.7", HTTPS: true}, "198.51.100.7", "", []string{"198.51.100.7"}, "https"},
		{"IPv6", Client{IP: "2001:db8::1", HTTPS: true}, "[2001:db8::1]:4321", "", []string{"2001:db8::1"}, "https"},
		// Naming the headers in Connection must not get them dropped as
		// hop-by-hop after Windlass has set them.
		{"Connection names the headers", Client{IP: "203.0.113.9", HTTPS: true}, "203.0.113.9:4321",
			"X-Forwarded-For, X-Forwarded-Proto", []string{"203.0.113.9"}, "https"},
		// No recorded client: nothing is claimed, and never HTTPS.
		{"no client", Client{}, "203.0.113.9:4321", "", nil, "http"},
	} {
		r := httptest.NewRequest(http.MethodGet, "http://panel.example.com/items", nil)
		r.RemoteAddr = c.remoteAddr
		for name, value := range spoofed {
			r.Header.Set(name, value)
		}
		r.Header.Set("X-Plugin-State", "kept")
		if c.connection != "" {
			r.Header.Set("Connection", c.connection)
		}

		rec := httptest.NewRecorder()
		s.Proxy("good", c.client, rec, r)

		var got *http.Request
		select {
		case got = <-arrived:
		default:
			t.Fatalf("%s: the plugin got no request (status %d)", c.name, rec.Code)
		}
		if v := got.Header.Values("X-Forwarded-For"); !slices.Equal(v, c.wantFor) {
			t.Errorf("%s: X-Forwarded-For = %q, want %q", c.name, v, c.wantFor)
		}
		if v := got.Header.Values("X-Forwarded-Proto"); len(v) != 1 || v[0] != c.wantProto {
			t.Errorf("%s: X-Forwarded-Proto = %q, want %q", c.name, v, c.wantProto)
		}
		for name := range spoofed {
			if name == "X-Forwarded-For" || name == "X-Forwarded-Proto" {
				continue
			}
			if v := got.Header.Values(name); len(v) != 0 {
				t.Errorf("%s: client-sent %s reached the plugin: %q", c.name, name, v)
			}
		}
		if got.Header.Get("X-Plugin-State") != "kept" {
			t.Errorf("%s: an unrelated header was dropped", c.name)
		}
		if got.Host != "panel.example.com" {
			t.Errorf("%s: Host = %q, want the host the caller asked for", c.name, got.Host)
		}
		if got.URL.Path != "/items" {
			t.Errorf("%s: path = %q, want /items", c.name, got.URL.Path)
		}
	}
}

// The plugin gets the query string exactly as the caller sent it. Go's
// reverse proxy re-encodes a query it cannot parse cleanly and drops the
// pairs it dislikes; Windlass does not read plugin queries, so it has no
// reason to change them.
func TestProxyLeavesTheQueryStringAlone(t *testing.T) {
	arrived := make(chan string, 1)
	plugin := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		arrived <- r.URL.RawQuery
	}))
	defer plugin.Close()
	addr := strings.TrimPrefix(plugin.URL, "http://")
	s, _ := newService(t)
	s.running["good"] = &instance{addr: addr, proxy: newPluginProxy(addr, s.logger)}

	for _, query := range []string{"", "b=2&a=1", "a=1;b=2", "q=50%", "q=a+b&q=%2f", "flag"} {
		r := httptest.NewRequest(http.MethodGet, "http://panel.example.com/items", nil)
		r.URL.RawQuery = query
		rec := httptest.NewRecorder()
		s.Proxy("good", Client{IP: "203.0.113.9"}, rec, r)
		select {
		case got := <-arrived:
			if got != query {
				t.Errorf("query %q reached the plugin as %q", query, got)
			}
		default:
			t.Fatalf("query %q: the plugin got no request (status %d)", query, rec.Code)
		}
	}
}

// A client can declare a forwarding header as a trailer, after a chunked
// body. It must not reach the plugin that way either.
func TestProxyDropsForwardingTrailers(t *testing.T) {
	arrived := make(chan http.Header, 1)
	plugin := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		arrived <- r.Trailer.Clone()
	}))
	defer plugin.Close()
	addr := strings.TrimPrefix(plugin.URL, "http://")
	s, _ := newService(t)
	s.running["good"] = &instance{addr: addr, proxy: newPluginProxy(addr, s.logger)}

	// A real server in front, so the trailer goes through Go's own parsing.
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Proxy("good", Client{IP: "203.0.113.9"}, w, r)
	}))
	defer panel.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(panel.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "POST /items HTTP/1.1\r\nHost: panel.example.com\r\n"+
		"Transfer-Encoding: chunked\r\nTrailer: X-Forwarded-For, X-Real-IP, X_Client_IP, X-Scheme, X-Plugin-Sum\r\nConnection: close\r\n\r\n"+
		"5\r\nhello\r\n0\r\nX-Forwarded-For: 10.0.0.1\r\nX-Real-IP: 10.0.0.1\r\nX_Client_IP: 10.0.0.1\r\nX-Scheme: https\r\nX-Plugin-Sum: abc\r\n\r\n")
	_, _ = io.ReadAll(conn)

	select {
	case got := <-arrived:
		for name := range got {
			if isForwardingHeader(name) {
				t.Errorf("trailer %s reached the plugin: %q", name, got[name])
			}
		}
		// The control: an ordinary trailer name is still declared, so the
		// check above is not passing on an empty set.
		if _, ok := got["X-Plugin-Sum"]; !ok {
			t.Errorf("the plugin's own trailer name was dropped; it saw %v", got)
		}
	default:
		t.Fatal("the plugin got no request")
	}
}
