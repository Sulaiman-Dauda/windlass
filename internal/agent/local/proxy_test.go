package local

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestBuildRoutesObjectEmpty(t *testing.T) {
	obj := buildRoutesObject(nil)
	got, _ := json.Marshal(obj)
	want := `{"@id":"windlass_routes","handle":[{"handler":"subroute"}]}`
	if string(got) != want {
		t.Errorf("empty object = %s", got)
	}
}
