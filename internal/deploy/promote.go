package deploy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/store/db"
)

// ErrNotPromotable refuses a promotion and says why.
var ErrNotPromotable = errors.New("cannot promote")

// Promote deploys to live exactly what staging deployment number ran: the
// same commit and the same image digests. It reuses the rollback path, so
// live skips pull and pins every pulled service to staging's digest. A
// service live does not define is left out, since a pin for it would make
// Compose start a service with nothing but an image.
func (s *Service) Promote(ctx context.Context, stagingName string, number int64, trigger string) (db.Deployment, error) {
	live, _, ok, err := s.projects.LiveFor(ctx, stagingName)
	if err != nil {
		return db.Deployment{}, err
	}
	if !ok {
		return db.Deployment{}, fmt.Errorf("%w: %s is not the staging of a project", ErrNotPromotable, stagingName)
	}
	staging, err := s.projects.Get(ctx, stagingName)
	if err != nil {
		return db.Deployment{}, err
	}
	target, err := s.Get(ctx, stagingName, number)
	if err != nil {
		return db.Deployment{}, err
	}
	if target.Status != "succeeded" {
		return db.Deployment{}, fmt.Errorf("%w: staging #%d is %s, and only a succeeded deployment can be promoted",
			ErrNotPromotable, target.Number, target.Status)
	}

	liveGit := live.Source == "git" && live.GitRepo.Valid
	if liveGit {
		if !target.GitCommit.Valid {
			return db.Deployment{}, fmt.Errorf("%w: staging #%d has no Git commit to promote", ErrNotPromotable, target.Number)
		}
		if staging.GitRepo.String != live.GitRepo.String {
			return db.Deployment{}, fmt.Errorf("%w: staging deploys %s and live deploys %s",
				ErrNotPromotable, staging.GitRepo.String, live.GitRepo.String)
		}
	}
	cfg, err := s.agent.Compose().Config(ctx, live.Name)
	if err != nil {
		return db.Deployment{}, err
	}
	if !liveGit {
		// Without Git, live would build from its own files, not what
		// staging tested.
		for _, name := range sortedServiceNames(cfg.Services) {
			if cfg.Services[name].Build {
				return db.Deployment{}, fmt.Errorf("%w: live builds %s on this server and is not connected to Git, "+
					"so there is nothing to promote but its own files", ErrNotPromotable, name)
			}
		}
	}

	arts, err := s.q.ListDeploymentArtifacts(ctx, target.ID)
	if err != nil {
		return db.Deployment{}, err
	}
	var keep []db.DeploymentArtifact
	for _, a := range arts {
		if _, ok := cfg.Services[a.Service]; ok {
			keep = append(keep, a)
		}
	}
	if len(keep) == 0 && !liveGit {
		return db.Deployment{}, fmt.Errorf("%w: staging #%d recorded no image digests for live's services",
			ErrNotPromotable, target.Number)
	}

	active, err := s.q.CountActiveDeployments(ctx, live.ID)
	if err != nil {
		return db.Deployment{}, err
	}
	if active > 0 {
		return db.Deployment{}, ErrDeployInProgress
	}

	d, err := s.q.CreateDeployment(ctx, db.CreateDeploymentParams{
		ProjectID: live.ID, ProjectID_2: live.ID, TriggeredBy: trigger,
	})
	if err != nil {
		return db.Deployment{}, err
	}
	if err := s.q.SetDeploymentPromotedFrom(ctx, db.SetDeploymentPromotedFromParams{
		PromotedFrom: sql.NullInt64{Int64: target.ID, Valid: true}, ID: d.ID,
	}); err != nil {
		return db.Deployment{}, s.abandon(ctx, d, err)
	}
	d.PromotedFrom = sql.NullInt64{Int64: target.ID, Valid: true}
	if liveGit {
		if err := s.q.SetDeploymentCommit(ctx, db.SetDeploymentCommitParams{GitCommit: target.GitCommit, ID: d.ID}); err != nil {
			return db.Deployment{}, s.abandon(ctx, d, err)
		}
		d.GitCommit = target.GitCommit
	}
	for _, a := range keep {
		if err := s.q.InsertDeploymentArtifact(ctx, db.InsertDeploymentArtifactParams{
			DeploymentID: d.ID, Service: a.Service, ImageRef: a.ImageRef, ImageDigest: a.ImageDigest,
		}); err != nil {
			return db.Deployment{}, s.abandon(ctx, d, err)
		}
	}

	if _, err := s.runner.Enqueue(ctx, JobType, payload{DeploymentID: d.ID, Project: live.Name}); err != nil {
		return db.Deployment{}, s.abandon(ctx, d, err)
	}
	s.publish(d.ID, live.Name, "deployment.created",
		fmt.Sprintf("staging #%d promoted as #%d", target.Number, d.Number))
	return d, nil
}

// DeployFirst queues a new staging's first deployment. With seedFrom set it
// copies that project's data in before anything starts. It never promotes
// itself, whatever auto-promote says.
func (s *Service) DeployFirst(ctx context.Context, stagingName, seedFrom string) (db.Deployment, error) {
	return s.deploy(ctx, stagingName, "manual", payload{Seed: seedFrom, NoPromote: true})
}

// autoPromote promotes a staging deployment that just succeeded, when the
// staging has auto-promote on. A refusal is recorded on the staging
// deployment, which still offers Promote, so nothing is lost silently.
func (s *Service) autoPromote(ctx context.Context, d db.Deployment, project string) {
	ctx = context.WithoutCancel(ctx)
	_, cfg, ok, err := s.projects.LiveFor(ctx, project)
	if err != nil {
		s.logger.Warn("auto-promote: read staging link", "project", project, "error", err)
		return
	}
	if !ok || !cfg.AutoPromote {
		return
	}
	promoted, err := s.Promote(ctx, project, d.Number, "webhook")
	if err != nil {
		s.event(ctx, d.ID, "warn", "auto-promote did not run: "+err.Error())
		return
	}
	s.event(ctx, d.ID, "log", fmt.Sprintf("auto-promoted to %s as #%d", cfg.Of, promoted.Number))
}

// checkStaging stops a staging deployment whose compose file would share
// something with live, or whose health check tests live. Compose files can
// be edited at any time, so this runs on every staging deployment, not only
// when staging is made.
func (s *Service) checkStaging(ctx context.Context, deploymentID int64, project string, cfg agent.ResolvedConfig) error {
	live, _, ok, err := s.projects.LiveFor(ctx, project)
	if err != nil || !ok {
		return err
	}
	blockers, warnings := projects.CheckIsolation(cfg, project)
	domains, err := s.projects.Domains(ctx, live.Name)
	if err != nil {
		return err
	}
	hosts := make([]string, 0, len(domains))
	for _, d := range domains {
		hosts = append(hosts, d.Hostname)
	}
	blockers = append(blockers, projects.HealthURLsOnHosts(cfg, hosts)...)
	for _, w := range warnings {
		s.event(ctx, deploymentID, "log", "staging: "+w.Service+" "+w.Message)
	}
	if len(blockers) == 0 {
		return nil
	}
	lines := make([]string, 0, len(blockers))
	for _, b := range blockers {
		lines = append(lines, fmt.Sprintf("%s %s. %s", b.Service, b.Message, b.Fix))
	}
	return fmt.Errorf("staging would share with live: %s", strings.Join(lines, "; "))
}

func sortedServiceNames(services map[string]agent.ResolvedService) []string {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
