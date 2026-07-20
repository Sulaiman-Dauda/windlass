package git

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRepoPath(t *testing.T) {
	cases := []struct {
		provider, url, want string
		wantErr             bool
	}{
		{"github", "https://github.com/acme/app.git", "acme/app", false},
		{"github", "https://github.com/acme/app", "acme/app", false},
		{"gitlab", "https://gitlab.com/group/sub/app.git", "group/sub/app", false},
		{"github", "https://gitlab.com/acme/app.git", "", true}, // host mismatch
		{"github", "https://github.com/justowner", "", true},
		{"github", "://bad", "", true},
	}
	for _, c := range cases {
		got, err := repoPath(c.provider, c.url)
		if c.wantErr != (err != nil) {
			t.Errorf("repoPath(%s, %s): err=%v", c.provider, c.url, err)
			continue
		}
		if got != c.want {
			t.Errorf("repoPath(%s, %s) = %q, want %q", c.provider, c.url, got, c.want)
		}
	}
}

func TestListReposGitHub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("auth header = %q", got)
		}
		if r.URL.Path != "/user/repos" {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode([]map[string]any{{
			"full_name": "acme/app", "clone_url": "https://github.com/acme/app.git",
			"default_branch": "main", "private": true,
		}})
	}))
	defer srv.Close()

	api := &providerAPI{githubBase: srv.URL}
	repos, err := api.ListRepos(context.Background(), "github", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].FullName != "acme/app" || !repos[0].Private ||
		repos[0].DefaultBranch != "main" {
		t.Errorf("unexpected repos: %+v", repos)
	}
}

func TestListReposGitLab(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("PRIVATE-TOKEN"); got != "tok" {
			t.Errorf("token header = %q", got)
		}
		json.NewEncoder(w).Encode([]map[string]any{{
			"path_with_namespace": "group/app",
			"http_url_to_repo":    "https://gitlab.com/group/app.git",
			"default_branch":      "master", "visibility": "public",
		}})
	}))
	defer srv.Close()

	api := &providerAPI{gitlabBase: srv.URL}
	repos, err := api.ListRepos(context.Background(), "gitlab", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].FullName != "group/app" || repos[0].Private {
		t.Errorf("unexpected repos: %+v", repos)
	}
}

func TestEnsureWebhookGitHubCreates(t *testing.T) {
	var created map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/app/hooks":
			w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/app/hooks":
			json.NewDecoder(r.Body).Decode(&created)
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	api := &providerAPI{githubBase: srv.URL}
	// repoPath host check runs against github.com regardless of the test base.
	err := api.EnsureWebhook(context.Background(), "github", "tok",
		"https://github.com/acme/app.git", "https://panel.example/api/v1/webhooks/github/app", "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := created["config"].(map[string]any)
	if created["name"] != "web" || cfg["secret"] != "s3cret" ||
		cfg["url"] != "https://panel.example/api/v1/webhooks/github/app" {
		t.Errorf("unexpected create payload: %v", created)
	}
}

func TestEnsureWebhookGitHubUpdatesExisting(t *testing.T) {
	patched := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/app/hooks":
			w.Write([]byte(`[{"id": 7, "config": {"url": "https://panel.example/api/v1/webhooks/github/app"}}]`))
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/acme/app/hooks/7":
			patched = true
			w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	api := &providerAPI{githubBase: srv.URL}
	err := api.EnsureWebhook(context.Background(), "github", "tok",
		"https://github.com/acme/app.git", "https://panel.example/api/v1/webhooks/github/app", "new-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !patched {
		t.Error("existing hook was not updated in place")
	}
}

func TestEnsureWebhookGitLab(t *testing.T) {
	var created map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// chi is not in play here; the project id arrives percent-encoded.
		if !strings.Contains(r.URL.EscapedPath(), "group%2Fapp") {
			t.Errorf("project path not encoded: %s", r.URL.EscapedPath())
		}
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`[]`))
		case http.MethodPost:
			json.NewDecoder(r.Body).Decode(&created)
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	api := &providerAPI{gitlabBase: srv.URL}
	err := api.EnsureWebhook(context.Background(), "gitlab", "tok",
		"https://gitlab.com/group/app.git", "https://panel.example/api/v1/webhooks/gitlab/app", "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if created["token"] != "s3cret" || created["push_events"] != true {
		t.Errorf("unexpected create payload: %v", created)
	}
}

func TestEnsureWebhookRejectsForeignHost(t *testing.T) {
	api := newProviderAPI()
	err := api.EnsureWebhook(context.Background(), "github", "tok",
		"https://git.example.com/acme/app.git", "https://panel.example/hook", "s")
	if err == nil {
		t.Error("foreign host accepted; webhook registration must stay on the provider's service")
	}
}
