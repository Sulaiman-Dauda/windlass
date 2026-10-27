package staging

import (
	"context"
	"errors"

	"github.com/windlass-dev/windlass/internal/deploy"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/store/db"
)

var ErrNotRecreatable = errors.New("only staging's interrupted first data copy can be recreated")

type retainedSettings struct {
	env      map[string]string
	domains  []projects.DomainConfig
	rewrites []projects.HostnameRewrite
}

func recreatable(d db.Deployment) bool {
	return d.Number == 1 && d.Status == "failed" && d.Error.Valid &&
		d.Error.String == deploy.InterruptedDataCopyMessage
}

// Recreate discards the partial data copy, not another failed or previously
// used staging. Read everything first: Delete removes the authoritative files.
func (s *Service) Recreate(ctx context.Context, name string, acknowledged bool, origin string) (Result, error) {
	p, err := s.projects.Get(ctx, name)
	if err != nil {
		return Result{}, err
	}
	live, cfg, ok, err := s.projects.LiveFor(ctx, name)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{}, ErrNotRecreatable
	}
	list, err := s.deploy.List(ctx, name, 1)
	if err != nil {
		return Result{}, err
	}
	if len(list) == 0 || !recreatable(list[0]) {
		return Result{}, ErrNotRecreatable
	}
	env, err := s.projects.GetEnv(ctx, name)
	if err != nil {
		return Result{}, err
	}
	domains, err := s.projects.Domains(ctx, name)
	if err != nil {
		return Result{}, err
	}
	if len(projects.OutsideWorldKeys(env)) > 0 && !acknowledged {
		return Result{}, ErrNotAcknowledged
	}
	retained := &retainedSettings{env: env, domains: domains, rewrites: cfg.Rewrites}
	req := Request{Branch: p.GitBranch.String, CopyData: true,
		AutoPromote: cfg.AutoPromote, Acknowledged: acknowledged}
	if err := s.projects.Delete(ctx, name); err != nil {
		return Result{}, err
	}
	return s.create(ctx, live.Name, req, origin, retained)
}

func (s *Service) plannedEnv(ctx context.Context, live string, rewrites []projects.HostnameRewrite,
	edits map[string]string, retained *retainedSettings) (map[string]string, error) {
	if retained == nil {
		return s.stagingEnv(ctx, live, rewrites, edits)
	}
	// A new live value is not an edit the person chose for staging.
	env := make(map[string]string, len(retained.env))
	for k, v := range retained.env {
		env[k] = v
	}
	return env, nil
}
