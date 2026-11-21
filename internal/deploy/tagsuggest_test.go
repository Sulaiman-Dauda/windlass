package deploy

import (
	"reflect"
	"testing"
)

func TestMissingRef(t *testing.T) {
	for _, c := range []struct{ line, want string }{
		{" Image nginx:1.27.999 Error manifest unknown: manifest unknown", "nginx:1.27.999"},
		{"Error response from daemon: manifest unknown: manifest unknown", ""},
		{"Error response from daemon: manifest for nginx:1.99 not found: manifest unknown: manifest unknown", "nginx:1.99"},
		{" web Error manifest for ghcr.io/acme/app:v2 not found: manifest unknown", "ghcr.io/acme/app:v2"},
		{`failed to resolve reference "docker.io/library/redis:9": docker.io/library/redis:9: not found`, "docker.io/library/redis:9"},
		{"pull access denied for acme/private, repository does not exist", ""},
		{"failed to solve: process did not complete successfully", ""},
	} {
		if got := missingRef(c.line); got != c.want {
			t.Errorf("missingRef(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}

func TestHubRepository(t *testing.T) {
	for _, c := range []struct {
		ref, repo, tag string
		ok             bool
	}{
		{"nginx:1.99", "library/nginx", "1.99", true},
		{"bitnami/redis:7.9", "bitnami/redis", "7.9", true},
		{"docker.io/library/redis:9", "library/redis", "9", true},
		{"index.docker.io/Acme/App:x", "acme/app", "x", true},
		{"ghcr.io/acme/app:v2", "", "", false},
		{"localhost:5000/app:1", "", "", false},
		{"registry.local/app:1", "", "", false},
		{"nginx@sha256:abc", "", "", false},
		{"nginx", "", "", false},
		{"a/b/c:1", "", "", false},
	} {
		repo, tag, ok := hubRepository(c.ref)
		if repo != c.repo || tag != c.tag || ok != c.ok {
			t.Errorf("hubRepository(%q) = %q %q %v", c.ref, repo, tag, ok)
		}
	}
}

func TestNearestTags(t *testing.T) {
	nginx := []string{"latest", "alpine", "1.27.3", "1.27.3-alpine", "1.27", "1.26.2", "1.26", "1.25.4", "1.25.3", "1", "mainline", "stable-alpine"}
	for _, c := range []struct {
		want string
		n    int
		out  []string
	}{
		// Same major and minor first, nearest patch next.
		{"1.25.9", 3, []string{"1.25.4", "1.25.3", "1.26.2"}},
		// Typo in the minor: nearest minor wins, its exact version first.
		{"1.28.3", 3, []string{"1.27.3", "1.27", "1.26.2"}},
		// The suffix must match.
		{"1.27.9-alpine", 5, []string{"1.27.3-alpine"}},
		// A word tag falls back to edit distance; nothing far is offered.
		{"alpin", 5, []string{"alpine"}},
		{"stabel-alpine", 1, []string{"stable-alpine"}},
		{"zzzzzz", 5, []string{}},
	} {
		if got := nearestTags(c.want, nginx, c.n); !reflect.DeepEqual(got, c.out) {
			t.Errorf("nearestTags(%q) = %v, want %v", c.want, got, c.out)
		}
	}
}
