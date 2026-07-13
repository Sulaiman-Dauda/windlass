package local

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/windlass-dev/windlass/internal/agent"
)

type hostLocal struct{ l *Local }

func (h hostLocal) Metrics(ctx context.Context) (agent.HostMetrics, error) {
	return readHostMetrics(ctx, h.l.cfg.ProjectsDir)
}

// GitSync clones or updates a repository inside the project directory.
// Authentication uses a per-invocation http.extraheader so tokens are never
// persisted into .git/config.
func (h hostLocal) GitSync(ctx context.Context, req agent.GitSyncReq, out agent.LogSink) (agent.GitSyncResult, error) {
	fs := fsLocal{h.l}
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

	var authArgs []string
	if req.Token != "" {
		// GitHub/GitLab accept token as basic auth; header form keeps it out
		// of process listings (unlike URL-embedded credentials).
		header := "Authorization: Basic " + basicAuth("x-access-token", req.Token)
		authArgs = []string{"-c", "http.extraheader=" + header}
	}

	run := func(args ...string) error {
		full := append(append([]string{}, authArgs...), args...)
		cmd := exec.CommandContext(ctx, "git", full...)
		cmd.Dir = target
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		return streamCmd(cmd, out)
	}

	if _, err := os.Stat(filepath.Join(target, ".git")); err == nil {
		// Existing checkout: fetch and hard-reset to the requested ref.
		if err := run("fetch", "--prune", "origin"); err != nil {
			return agent.GitSyncResult{}, fmt.Errorf("git fetch: %w", err)
		}
		ref := req.Commit
		if ref == "" {
			ref = "origin/" + req.Branch
		}
		if err := run("checkout", "--force", "--detach", ref); err != nil {
			return agent.GitSyncResult{}, fmt.Errorf("git checkout %s: %w", ref, err)
		}
	} else {
		cloneArgs := append(append([]string{}, authArgs...),
			"clone", "--branch", req.Branch, "--single-branch", req.URL, target)
		cmd := exec.CommandContext(ctx, "git", cloneArgs...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if err := streamCmd(cmd, out); err != nil {
			return agent.GitSyncResult{}, fmt.Errorf("git clone: %w", err)
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

func basicAuth(user, pass string) string {
	return base64Encode(user + ":" + pass)
}
