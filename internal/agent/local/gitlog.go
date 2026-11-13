package local

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
)

// GitLog reads the commits between two commits from a project's checkout.
// It never fetches: staging's checkout already has every commit staging
// deployed, and a commit it lacks means the histories have parted.
func (h hostLocal) GitLog(ctx context.Context, req agent.GitLogReq) (agent.GitLogResult, error) {
	if !commitRe.MatchString(req.From) || !commitRe.MatchString(req.To) {
		return agent.GitLogResult{}, fmt.Errorf("invalid commit range %q..%q", req.From, req.To)
	}
	subdir := req.Subdir
	if subdir == "" {
		subdir = "src"
	}
	target, err := fsLocal(h).resolve(req.Project, subdir)
	if err != nil {
		return agent.GitLogResult{}, err
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
		return agent.GitLogResult{}, agent.ErrUnknownCommit
	}
	limit := req.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	git := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // G204: commits are checked hex
		cmd.Dir = target
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		return cmd.Output()
	}
	for _, c := range []string{req.From, req.To} {
		if _, err := git("cat-file", "-e", c+"^{commit}"); err != nil {
			return agent.GitLogResult{}, agent.ErrUnknownCommit
		}
	}
	// One more than the limit says whether there are others. %x1e ends each
	// record and %x1f separates fields, neither of which a subject holds.
	out, err := git("log", "--no-color", "-n", strconv.Itoa(limit+1),
		"--format=%H%x1f%an%x1f%aI%x1f%s%x1e", req.From+".."+req.To, "--")
	if err != nil {
		return agent.GitLogResult{}, fmt.Errorf("git log: %w", err)
	}
	res := agent.GitLogResult{Commits: []agent.GitCommit{}}
	for _, rec := range strings.Split(string(out), "\x1e") {
		f := strings.Split(strings.TrimLeft(rec, "\n"), "\x1f")
		if len(f) != 4 {
			continue
		}
		if len(res.Commits) == limit {
			res.More = true
			break
		}
		when, _ := time.Parse(time.RFC3339, f[2])
		res.Commits = append(res.Commits, agent.GitCommit{Hash: f[0], Author: f[1], Time: when, Subject: f[3]})
	}
	return res, nil
}

// GitRemoteHead asks the remote which commit a branch points at. The token
// travels as in GitSync, in the environment and never on disk.
func (h hostLocal) GitRemoteHead(ctx context.Context, req agent.GitRemoteHeadReq) (string, error) {
	if req.Branch == "" || strings.HasPrefix(req.Branch, "-") || strings.ContainsAny(req.Branch, " \t\r\n") {
		return "", fmt.Errorf("invalid branch %q", req.Branch)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if req.Token != "" {
		env = gitConfigEnv(env, "http.extraheader", "Authorization: Basic "+basicAuth("x-access-token", req.Token))
	}
	ref := "refs/heads/" + req.Branch
	// "--" keeps a URL from being read as an option such as --upload-pack.
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--", req.URL, ref) //nolint:gosec // G204: see above
	cmd.Dir = h.l.cfg.ProjectsDir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git ls-remote: %w", err)
	}
	// The pattern also matches a ref that merely ends in it, so take only
	// the exact one.
	for _, line := range strings.Split(string(out), "\n") {
		hash, name, ok := strings.Cut(line, "\t")
		if ok && name == ref && commitRe.MatchString(hash) {
			return hash, nil
		}
	}
	return "", agent.ErrBranchNotFound
}
