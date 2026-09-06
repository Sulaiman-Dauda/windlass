package local

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
)

func TestEnsureTLSMergesWithoutClobberingUserNames(t *testing.T) {
	t.Parallel()

	names := []string{"user.example.com"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config/apps/tls/certificates/automate" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(names)
		case http.MethodPatch:
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &names); err != nil {
				t.Fatalf("decode PUT: %v", err)
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	p := proxyLocal{l: &Local{cfg: Config{CaddyAdmin: srv.URL}}}
	err := p.ensureTLS(context.Background(), []agent.Route{
		{Hostname: "app.example.com", TLS: true},
		{Hostname: "user.example.com", TLS: true},
		{Hostname: "plain.example.com", TLS: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"user.example.com", "app.example.com"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("automation names = %v, want %v", names, want)
	}
}

// Golden test: the exact JSON shape Windlass writes into Caddy. If this
// changes, zero-downtime PATCH semantics and route ownership must be
// re-verified against a real Caddy (integration suite).
func TestBuildRoutesObjectGolden(t *testing.T) {
	obj := buildRoutesObject([]agent.Route{
		{ID: "windlass_route_crm.example.com", Hostname: "crm.example.com", Upstream: "172.18.0.3:3000", TLS: true},
		{ID: "windlass_route_api.example.com", Hostname: "api.example.com", Upstream: "127.0.0.1:8081", TLS: true},
	})

	got, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	want := `{
  "@id": "windlass_routes",
  "handle": [
    {
      "handler": "subroute",
      "routes": [
        {
          "@id": "windlass_https_redirect",
          "match": [
            {
              "host": [
                "crm.example.com",
                "api.example.com"
              ],
              "protocol": "http"
            }
          ],
          "handle": [
            {
              "handler": "static_response",
              "status_code": 308,
              "headers": {
                "Location": [
                  "https://{http.request.host}{http.request.uri}"
                ]
              }
            }
          ],
          "terminal": true
        },
        {
          "@id": "windlass_route_crm.example.com",
          "match": [
            {
              "host": [
                "crm.example.com"
              ]
            }
          ],
          "handle": [
            {
              "handler": "reverse_proxy",
              "upstreams": [
                {
                  "dial": "172.18.0.3:3000"
                }
              ]
            }
          ],
          "terminal": true
        },
        {
          "@id": "windlass_route_api.example.com",
          "match": [
            {
              "host": [
                "api.example.com"
              ]
            }
          ],
          "handle": [
            {
              "handler": "reverse_proxy",
              "upstreams": [
                {
                  "dial": "127.0.0.1:8081"
                }
              ]
            }
          ],
          "terminal": true
        }
      ]
    }
  ]
}`
	if string(got) != want {
		t.Errorf("route JSON drifted:\n%s", got)
	}
}

// When Caddy has no usable server, install() must create one that actually
// terminates TLS. Regression test for application domains serving plaintext on
// :443 (host matchers are nested in the windlass_routes subroute, so Caddy's
// automatic-HTTPS never attaches a connection policy by itself).
func TestInstallCreatesServerWithTLSPolicy(t *testing.T) {
	t.Parallel()

	var created map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/config/apps/http/servers/windlass" {
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &created); err != nil {
				t.Fatalf("decode server PUT: %v", err)
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		// Every GET returns an empty object: no servers exist yet (forcing the
		// create-server path) and all config parents already exist.
		if r.Method == http.MethodGet {
			io.WriteString(w, "{}")
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := proxyLocal{l: &Local{cfg: Config{CaddyAdmin: srv.URL}}}
	obj := buildRoutesObject([]agent.Route{
		{ID: "windlass_route_app.example.com", Hostname: "app.example.com", Upstream: "10.0.0.2:3001", TLS: true},
	})
	if err := p.install(context.Background(), obj); err != nil {
		t.Fatal(err)
	}

	if created == nil {
		t.Fatal("windlass server was never created")
	}
	policies, ok := created["tls_connection_policies"].([]any)
	if !ok || len(policies) == 0 {
		t.Fatalf("created server missing tls_connection_policies (%#v): :443 would serve plaintext",
			created["tls_connection_policies"])
	}
	listen, _ := created["listen"].([]any)
	has443 := false
	for _, l := range listen {
		if l == ":443" {
			has443 = true
		}
	}
	if !has443 {
		t.Fatalf("created server must listen on :443, got %v", listen)
	}
}

// The subroute must begin with an HTTP->HTTPS redirect, scoped to the TLS
// hostnames and excluding ACME HTTP-01 challenges, ahead of the proxy routes.
func TestBuildRoutesObjectRedirectsHTTP(t *testing.T) {
	obj := buildRoutesObject([]agent.Route{
		{ID: "windlass_route_app.example.com", Hostname: "app.example.com", Upstream: "10.0.0.2:3001", TLS: true},
	})
	inner := obj.Handle[0].Routes
	if len(inner) != 2 {
		t.Fatalf("subroute should hold redirect + 1 proxy route, got %d", len(inner))
	}
	red := inner[0]
	if red.ID != httpsRedirectID {
		t.Fatalf("first subroute child must be the redirect, got %q", red.ID)
	}
	if red.Match[0].Protocol != "http" {
		t.Fatalf("redirect must match protocol http, got %q", red.Match[0].Protocol)
	}
	if len(red.Match[0].Host) != 1 || red.Match[0].Host[0] != "app.example.com" {
		t.Fatalf("redirect must be scoped to the TLS host, got %v", red.Match[0].Host)
	}
	if red.Handle[0].Handler != "static_response" || red.Handle[0].StatusCode != 308 {
		t.Fatalf("redirect must be static_response/308, got %s/%d", red.Handle[0].Handler, red.Handle[0].StatusCode)
	}
	if inner[1].ID != "windlass_route_app.example.com" {
		t.Fatalf("proxy route must follow the redirect, got %q", inner[1].ID)
	}
}

// A non-TLS route (or none) must not synthesise a redirect.
func TestBuildRoutesObjectNoRedirectWithoutTLS(t *testing.T) {
	obj := buildRoutesObject([]agent.Route{
		{ID: "windlass_route_plain.example.com", Hostname: "plain.example.com", Upstream: "10.0.0.2:80", TLS: false},
	})
	for _, r := range obj.Handle[0].Routes {
		if r.ID == httpsRedirectID {
			t.Fatal("no redirect should be created for non-TLS routes")
		}
	}
}

func TestBuildRoutesObjectEmpty(t *testing.T) {
	obj := buildRoutesObject(nil)
	got, _ := json.Marshal(obj)
	want := `{"@id":"windlass_routes","handle":[{"handler":"subroute"}]}`
	if string(got) != want {
		t.Errorf("empty object = %s", got)
	}
}

// A server that already exists but has no routes key cannot take an indexed
// insert: Caddy rejects PUT .../routes/0 rather than creating the array, which
// left installs failing with a 500 the operator could do nothing about. When
// the array is absent the whole array is written instead, and when it exists
// the route still goes in at index 0 so a user catch-all cannot shadow it.
func TestInstallHandlesServerWithoutRoutesArray(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		routes     string // what GET .../routes returns
		wantMethod string
		wantPath   string
		wantList   bool // body is the whole array rather than one route
	}{
		{"no routes key", "null", http.MethodPut, "/config/apps/http/servers/tlssrv/routes", true},
		{"empty routes array", "[]", http.MethodPost, "/config/apps/http/servers/tlssrv/routes", false},
		{"existing routes", `[{"@id":"user"}]`, http.MethodPut, "/config/apps/http/servers/tlssrv/routes/0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var gotMethod, gotPath string
			var gotBody []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/servers"):
					// One existing server that terminates TLS, so install grafts
					// onto it rather than creating its own.
					io.WriteString(w, `{"tlssrv":{"listen":[":443"],"tls_connection_policies":[{}]}}`)
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/routes"):
					io.WriteString(w, tc.routes)
				case r.Method == http.MethodGet:
					io.WriteString(w, "{}")
				case (r.Method == http.MethodPut || r.Method == http.MethodPost) && strings.Contains(r.URL.Path, "/servers/tlssrv/routes"):
					gotMethod, gotPath = r.Method, r.URL.Path
					gotBody, _ = io.ReadAll(r.Body)
					w.WriteHeader(http.StatusOK)
				default:
					w.WriteHeader(http.StatusOK)
				}
			}))
			defer srv.Close()

			p := proxyLocal{l: &Local{cfg: Config{CaddyAdmin: srv.URL}}}
			obj := buildRoutesObject([]agent.Route{
				{ID: "windlass_route_app.example.com", Hostname: "app.example.com", Upstream: "10.0.0.2:3001", TLS: true},
			})
			if err := p.install(context.Background(), obj); err != nil {
				t.Fatal(err)
			}
			if gotMethod != tc.wantMethod || gotPath != tc.wantPath {
				t.Errorf("wrote %s %q, want %s %q", gotMethod, gotPath, tc.wantMethod, tc.wantPath)
			}
			isList := len(bytes.TrimSpace(gotBody)) > 0 && bytes.TrimSpace(gotBody)[0] == '['
			if isList != tc.wantList {
				t.Errorf("body is array = %v, want %v: %s", isList, tc.wantList, gotBody)
			}
		})
	}
}
