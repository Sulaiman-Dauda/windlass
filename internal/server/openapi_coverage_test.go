package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	spec "github.com/windlass-dev/windlass/api"
)

// specPaths extracts path keys from the embedded OpenAPI YAML without a
// YAML dependency: two-space-indented keys starting with '/'.
func specPaths(t *testing.T) map[string]bool {
	t.Helper()
	paths := map[string]bool{}
	inPaths := false
	for _, line := range strings.Split(string(spec.Spec), "\n") {
		if strings.HasPrefix(line, "paths:") {
			inPaths = true
			continue
		}
		if inPaths && len(line) > 0 && line[0] != ' ' {
			inPaths = false
		}
		if !inPaths {
			continue
		}
		trimmed := strings.TrimRight(line, "\r")
		if strings.HasPrefix(trimmed, "  /") && strings.HasSuffix(trimmed, ":") {
			paths[strings.TrimSuffix(strings.TrimSpace(trimmed), ":")] = true
		}
	}
	if len(paths) == 0 {
		t.Fatal("no paths parsed from openapi.yaml")
	}
	return paths
}

// TestEveryRouteIsDocumented walks the real router and asserts each /api/v1
// route appears in api/openapi.yaml. New endpoints must be documented or
// this fails CI.
func TestEveryRouteIsDocumented(t *testing.T) {
	e := newTestEnv(t)
	documented := specPaths(t)

	router, ok := e.handler.(chi.Router)
	if !ok {
		t.Fatal("handler is not a chi.Router")
	}

	var undocumented []string
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/v1/") {
			return nil // SPA fallback
		}
		path := strings.TrimPrefix(route, "/api/v1")
		path = strings.TrimSuffix(path, "/")
		if path == "" {
			return nil
		}
		// chi wildcard → the {path} parameter documented in the spec.
		path = strings.ReplaceAll(path, "/*", "/{path}")
		if !documented[path] {
			undocumented = append(undocumented, method+" "+path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(undocumented) > 0 {
		t.Errorf("routes missing from api/openapi.yaml:\n  %s", strings.Join(undocumented, "\n  "))
	}
}
