package local

import (
	"encoding/json"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
)

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
