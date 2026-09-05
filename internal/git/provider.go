package git

// Provider REST clients for GitHub and GitLab, hand-rolled on net/http like
// internal/auth's OAuth flow: listing repositories for the picker and
// registering push webhooks are each one or two requests, so a client
// library is not justified (principle 10).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Repo is a repository visible to a connection's token, trimmed to what the
// frontend picker needs.
type Repo struct {
	FullName      string `json:"full_name"`
	CloneURL      string `json:"clone_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

// providerAPI holds provider base URLs so tests can point at httptest servers.
type providerAPI struct {
	githubBase string
	gitlabBase string
}

func newProviderAPI() *providerAPI {
	return &providerAPI{
		githubBase: "https://api.github.com",
		gitlabBase: "https://gitlab.com/api/v4",
	}
}

const (
	providerTimeout = 20 * time.Second
	// repoPageLimit caps pagination; 3 pages of 100 covers any realistic
	// account without an unbounded crawl.
	repoPageLimit = 3
)

func (p *providerAPI) do(ctx context.Context, provider, method, apiURL, token string, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiURL, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	switch provider {
	case "github":
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
	case "gitlab":
		req.Header.Set("PRIVATE-TOKEN", token)
	}
	return http.DefaultClient.Do(req)
}

// ListRepos returns repositories the token can access, most recently active
// first.
func (p *providerAPI) ListRepos(ctx context.Context, provider, token string) ([]Repo, error) {
	ctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()

	var out []Repo
	for page := 1; page <= repoPageLimit; page++ {
		var pageURL string
		switch provider {
		case "github":
			pageURL = fmt.Sprintf("%s/user/repos?per_page=100&sort=pushed&page=%d", p.githubBase, page)
		case "gitlab":
			pageURL = fmt.Sprintf("%s/projects?membership=true&per_page=100&order_by=last_activity_at&page=%d", p.gitlabBase, page)
		default:
			return nil, fmt.Errorf("unsupported provider %q", provider)
		}
		resp, err := p.do(ctx, provider, http.MethodGet, pageURL, token, nil)
		if err != nil {
			return nil, err
		}
		batch, err := decodeRepoPage(provider, resp)
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return out, nil
}

func decodeRepoPage(provider string, resp *http.Response) ([]Repo, error) {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s repo listing failed (HTTP %d)", provider, resp.StatusCode)
	}
	body := io.LimitReader(resp.Body, 8<<20)

	if provider == "github" {
		var repos []struct {
			FullName      string `json:"full_name"`
			CloneURL      string `json:"clone_url"`
			DefaultBranch string `json:"default_branch"`
			Private       bool   `json:"private"`
		}
		if err := json.NewDecoder(body).Decode(&repos); err != nil {
			return nil, err
		}
		out := make([]Repo, 0, len(repos))
		for _, r := range repos {
			out = append(out, Repo(r))
		}
		return out, nil
	}

	var projects []struct {
		Path          string `json:"path_with_namespace"`
		HTTPURL       string `json:"http_url_to_repo"`
		DefaultBranch string `json:"default_branch"`
		Visibility    string `json:"visibility"`
	}
	if err := json.NewDecoder(body).Decode(&projects); err != nil {
		return nil, err
	}
	out := make([]Repo, 0, len(projects))
	for _, pr := range projects {
		out = append(out, Repo{
			FullName:      pr.Path,
			CloneURL:      pr.HTTPURL,
			DefaultBranch: pr.DefaultBranch,
			Private:       pr.Visibility != "public",
		})
	}
	return out, nil
}

// repoPath extracts "owner/name" from an https clone URL and checks the host
// matches the provider's hosted service, webhooks can only be registered
// where the token is valid.
func repoPath(provider, repoURL string) (string, error) {
	u, err := url.Parse(repoURL)
	if err != nil {
		return "", fmt.Errorf("invalid repo URL: %w", err)
	}
	wantHost := map[string]string{"github": "github.com", "gitlab": "gitlab.com"}[provider]
	if !strings.EqualFold(u.Host, wantHost) {
		return "", fmt.Errorf("cannot manage webhooks on %q with a %s connection", u.Host, provider)
	}
	path := strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/")
	if strings.Count(path, "/") < 1 {
		return "", errors.New("repo URL must include owner and repository name")
	}
	return path, nil
}

// EnsureWebhook creates the panel's push webhook on the repository, or
// updates it in place when one already points at hookURL (re-saving git
// settings rotates the secret).
func (p *providerAPI) EnsureWebhook(ctx context.Context, provider, token, repoURL, hookURL, secret string) error {
	ctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()

	path, err := repoPath(provider, repoURL)
	if err != nil {
		return err
	}
	if provider == "github" {
		return p.ensureGitHubHook(ctx, token, path, hookURL, secret)
	}
	return p.ensureGitLabHook(ctx, token, path, hookURL, secret)
}

func (p *providerAPI) ensureGitHubHook(ctx context.Context, token, repoPath, hookURL, secret string) error {
	listURL := p.githubBase + "/repos/" + repoPath + "/hooks?per_page=100"
	resp, err := p.do(ctx, "github", http.MethodGet, listURL, token, nil)
	if err != nil {
		return err
	}
	var hooks []struct {
		ID     int64 `json:"id"`
		Config struct {
			URL string `json:"url"`
		} `json:"config"`
	}
	if err := decodeJSON(resp, "github", &hooks); err != nil {
		return err
	}

	payload := map[string]any{
		"active": true,
		"events": []string{"push"},
		"config": map[string]string{
			"url":          hookURL,
			"content_type": "json",
			"secret":       secret,
		},
	}
	for _, h := range hooks {
		if h.Config.URL == hookURL {
			resp, err := p.do(ctx, "github", http.MethodPatch,
				fmt.Sprintf("%s/repos/%s/hooks/%d", p.githubBase, repoPath, h.ID), token, payload)
			if err != nil {
				return err
			}
			return checkStatus(resp, "github")
		}
	}
	payload["name"] = "web"
	resp2, err := p.do(ctx, "github", http.MethodPost, p.githubBase+"/repos/"+repoPath+"/hooks", token, payload)
	if err != nil {
		return err
	}
	return checkStatus(resp2, "github")
}

func (p *providerAPI) ensureGitLabHook(ctx context.Context, token, repoPath, hookURL, secret string) error {
	project := url.QueryEscape(repoPath) // owner/name → owner%2Fname
	listURL := p.gitlabBase + "/projects/" + project + "/hooks?per_page=100"
	resp, err := p.do(ctx, "gitlab", http.MethodGet, listURL, token, nil)
	if err != nil {
		return err
	}
	var hooks []struct {
		ID  int64  `json:"id"`
		URL string `json:"url"`
	}
	if err := decodeJSON(resp, "gitlab", &hooks); err != nil {
		return err
	}

	payload := map[string]any{
		"url":                     hookURL,
		"token":                   secret,
		"push_events":             true,
		"enable_ssl_verification": true,
	}
	for _, h := range hooks {
		if h.URL == hookURL {
			resp, err := p.do(ctx, "gitlab", http.MethodPut,
				fmt.Sprintf("%s/projects/%s/hooks/%d", p.gitlabBase, project, h.ID), token, payload)
			if err != nil {
				return err
			}
			return checkStatus(resp, "gitlab")
		}
	}
	resp2, err := p.do(ctx, "gitlab", http.MethodPost, p.gitlabBase+"/projects/"+project+"/hooks", token, payload)
	if err != nil {
		return err
	}
	return checkStatus(resp2, "gitlab")
}

func decodeJSON(resp *http.Response, provider string, v any) error {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s API request failed (HTTP %d)", provider, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v)
}

func checkStatus(resp *http.Response, provider string) error {
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s webhook registration failed (HTTP %d)", provider, resp.StatusCode)
	}
	return nil
}
