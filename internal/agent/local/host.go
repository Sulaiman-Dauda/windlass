package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
)

type hostLocal struct{ l *Local }

func (h hostLocal) Metrics(ctx context.Context) (agent.HostMetrics, error) {
	return readHostMetrics(ctx, h.l.cfg.ProjectsDir)
}

func (h hostLocal) HTTPCheck(ctx context.Context, req agent.HTTPCheckReq) (agent.HTTPCheckResult, error) {
	parsed, err := url.Parse(req.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return agent.HTTPCheckResult{}, fmt.Errorf("invalid health-check URL %q", req.URL)
	}
	timeout := time.Duration(req.Timeout) * time.Second
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 10 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(requestCtx, http.MethodGet, req.URL, nil)
	if err != nil {
		return agent.HTTPCheckResult{}, err
	}
	response, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return agent.HTTPCheckResult{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return agent.HTTPCheckResult{}, err
	}
	return agent.HTTPCheckResult{StatusCode: response.StatusCode, Body: string(body)}, nil
}

// GitSync clones or updates a repository inside the project directory.
// Authentication uses a per-invocation http.extraheader so tokens are never
// persisted into .git/config. The header travels in the environment, not
// argv: any user on the host can read a process's argv from /proc, while its
// environment is readable only by the same user and root.
func (h hostLocal) GitSync(ctx context.Context, req agent.GitSyncReq, out agent.LogSink) (agent.GitSyncResult, error) {
	fs := fsLocal(h)
	dir, err := fs.EnsureProject(ctx, req.Project)
	if err != nil {
		return agent.GitSyncResult{}, err
	}
	subdir := req.Subdir
	if subdir == "" {
		subdir = "src"
	}
	if _, err := fs.resolve(req.Project, subdir); err != nil {
		return agent.GitSyncResult{}, err
	}
	target := filepath.Join(dir, subdir)

	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if req.Token != "" {
		// GitHub/GitLab accept token as basic auth.
		header := "Authorization: Basic " + basicAuth("x-access-token", req.Token)
		env = gitConfigEnv(env, "http.extraheader", header)
	}

	run := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // G204: git subcommands built in this function
		cmd.Dir = target
		cmd.Env = env
		return streamCmd(cmd, out)
	}

	// requireOnBranch runs after a fetch and before any checkout, so a
	// refused commit leaves the working tree as it was.
	requireOnBranch := func() error {
		if !req.RequireOnBranch {
			return nil
		}
		if !commitRe.MatchString(req.Commit) {
			return fmt.Errorf("invalid commit %q", req.Commit)
		}
		check := exec.CommandContext(ctx, "git", "merge-base", "--is-ancestor", req.Commit, "origin/"+req.Branch) //nolint:gosec // G204: commit is checked hex
		check.Dir = target
		check.Env = env
		err := check.Run()
		var exit *exec.ExitError
		if err == nil {
			return nil
		}
		if !errors.As(err, &exit) {
			return fmt.Errorf("git merge-base: %w", err)
		}
		// 1 means not an ancestor. A commit the branch's fetch did not bring
		// is on no branch here at all, so that is the same answer.
		if exit.ExitCode() == 1 {
			return agent.ErrNotOnBranch
		}
		known := exec.CommandContext(ctx, "git", "cat-file", "-e", req.Commit+"^{commit}") //nolint:gosec // G204: as above
		known.Dir = target
		if known.Run() != nil {
			return agent.ErrNotOnBranch
		}
		return fmt.Errorf("git merge-base: %w", err)
	}

	if _, err := os.Stat(filepath.Join(target, ".git")); err == nil {
		// Existing checkout: fetch and hard-reset to the requested ref. The
		// first clone is single-branch, so a plain fetch brings only that
		// branch; naming the branch also fetches one chosen since.
		fetch := []string{"fetch", "--prune", "origin"}
		if req.Branch != "" {
			fetch = append(fetch, "+refs/heads/"+req.Branch+":refs/remotes/origin/"+req.Branch)
		}
		if err := run(fetch...); err != nil {
			return agent.GitSyncResult{}, fmt.Errorf("git fetch: %w", err)
		}
		if err := requireOnBranch(); err != nil {
			return agent.GitSyncResult{}, err
		}
		ref := req.Commit
		if ref == "" {
			ref = "origin/" + req.Branch
		}
		if err := run("checkout", "--force", "--detach", ref); err != nil {
			return agent.GitSyncResult{}, fmt.Errorf("git checkout %s: %w", ref, err)
		}
	} else if entries, err := os.ReadDir(target); err == nil && len(entries) > 0 {
		// Files but no repository: what a restore leaves, since backups skip
		// .git. Clone refuses a non-empty directory, so make it a checkout in
		// place. Nothing is deleted; the checkout overwrites tracked files.
		ref := req.Commit
		if ref == "" {
			ref = "origin/" + req.Branch
		}
		steps := []struct {
			name string
			args []string
		}{
			{"git init", []string{"init", "-q"}},
			{"git remote add", []string{"remote", "add", "-t", req.Branch, "--", "origin", req.URL}},
			{"git fetch", []string{"fetch", "--prune", "origin"}},
			{"", nil}, // requireOnBranch
			{"git checkout " + ref, []string{"checkout", "--force", "--detach", ref}},
		}
		for _, step := range steps {
			var err error
			if step.args == nil {
				err = requireOnBranch()
			} else if err = run(step.args...); err != nil {
				err = fmt.Errorf("%s: %w", step.name, err)
			}
			if err != nil {
				// A half-made repository would pass for a checkout next
				// time, and every sync would then fail on its remote.
				_ = os.RemoveAll(filepath.Join(target, ".git"))
				return agent.GitSyncResult{}, err
			}
		}
	} else {
		// "--" keeps a URL from a hand-edited .windlass.json from being read as
		// an option such as --upload-pack.
		cmd := exec.CommandContext(ctx, "git", "clone", "--branch", req.Branch, "--single-branch", "--", req.URL, target) //nolint:gosec // G204: see above
		cmd.Dir = dir
		cmd.Env = env
		if err := streamCmd(cmd, out); err != nil {
			return agent.GitSyncResult{}, fmt.Errorf("git clone: %w", err)
		}
		if err := requireOnBranch(); err != nil {
			return agent.GitSyncResult{}, err
		}
		if req.Commit != "" {
			if err := run("checkout", "--force", "--detach", req.Commit); err != nil {
				return agent.GitSyncResult{}, fmt.Errorf("git checkout %s: %w", req.Commit, err)
			}
		}
	}

	rev := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	rev.Dir = target
	outBytes, err := rev.Output()
	if err != nil {
		return agent.GitSyncResult{}, fmt.Errorf("git rev-parse: %w", err)
	}
	return agent.GitSyncResult{Commit: strings.TrimSpace(string(outBytes))}, nil
}

// gitConfigEnv adds key=value to env as if passed with git -c. It uses
// GIT_CONFIG_PARAMETERS, the variable git itself sets to hand -c options to
// its child processes (git-remote-https among them). Every git release reads
// it, unlike GIT_CONFIG_COUNT, which needs git 2.31.
func gitConfigEnv(env []string, key, value string) []string {
	param := "'" + strings.ReplaceAll(key+"="+value, "'", `'\''`) + "'"
	const name = "GIT_CONFIG_PARAMETERS="
	for i, kv := range env {
		if strings.HasPrefix(kv, name) {
			out := append([]string{}, env...)
			out[i] = kv + " " + param
			return out
		}
	}
	return append(env, name+param)
}

// commitRe matches an abbreviated or full commit hash, which cannot be read
// as an option.
var commitRe = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

func basicAuth(user, pass string) string {
	return base64Encode(user + ":" + pass)
}
