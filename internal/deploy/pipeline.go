// Package deploy runs the deployment pipeline:
//
//	queued → preparing → syncing → pulling → building → applying → verifying → succeeded
//
// Each step checkpoints into the jobs table before executing, and every step
// is idempotent (git checkout to a pinned SHA, compose pull/build/up all
// converge), so a crash resumes by re-executing the checkpointed step.
package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/git"
	"github.com/windlass-dev/windlass/internal/jobs"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/store/db"
)

const (
	JobType = "deploy"

	stepPreparing = "preparing"
	stepSyncing   = "syncing"
	stepPulling   = "pulling"
	stepBuilding  = "building"
	stepApplying  = "applying"
	stepVerifying = "verifying"
)

// stepOrder drives resume: a reclaimed job re-executes from its checkpoint.
var stepOrder = []string{stepPreparing, stepSyncing, stepPulling, stepBuilding, stepApplying, stepVerifying}

// rollbackSteps skip sync/pull/build: images are pinned to recorded digests
// that already exist locally.
var rollbackSteps = []string{stepPreparing, stepApplying, stepVerifying}

const rollbackFile = "compose.rollback.yaml"

var (
	ErrDeployInProgress = errors.New("a deployment is already running for this project")
	ErrNotFound         = errors.New("deployment not found")
)

// VerifyTimeout is how long the verify step waits for services to be healthy.
var VerifyTimeout = 120 * time.Second
var verifyPollInterval = 2 * time.Second

type payload struct {
	DeploymentID int64  `json:"deployment_id"`
	Project      string `json:"project"`
}

type Service struct {
	q        *db.Queries
	agent    agent.Agent
	projects *projects.Service
	git      *git.Service
	runner   *jobs.Runner
	bus      *events.Bus
	logger   *slog.Logger
}

func New(q *db.Queries, ag agent.Agent, proj *projects.Service, gitSvc *git.Service, runner *jobs.Runner, bus *events.Bus, logger *slog.Logger) *Service {
	s := &Service{q: q, agent: ag, projects: proj, git: gitSvc, runner: runner, bus: bus, logger: logger}
	runner.Register(JobType, s.runJob)
	return s
}

// Deploy creates a deployment and enqueues its job. One active deployment
// per project.
func (s *Service) Deploy(ctx context.Context, projectName, trigger string) (db.Deployment, error) {
	p, err := s.projects.Get(ctx, projectName)
	if err != nil {
		return db.Deployment{}, err
	}

	active, err := s.q.CountActiveDeployments(ctx, p.ID)
	if err != nil {
		return db.Deployment{}, err
	}
	if active > 0 {
		return db.Deployment{}, ErrDeployInProgress
	}

	d, err := s.q.CreateDeployment(ctx, db.CreateDeploymentParams{
		ProjectID:   p.ID,
		ProjectID_2: p.ID,
		TriggeredBy: trigger,
		RollbackOf:  sql.NullInt64{},
	})
	if err != nil {
		return db.Deployment{}, err
	}

	if _, err := s.runner.Enqueue(ctx, JobType, payload{DeploymentID: d.ID, Project: projectName}); err != nil {
		return db.Deployment{}, err
	}

	s.publish(d.ID, projectName, "deployment.created", fmt.Sprintf("deployment #%d queued", d.Number))
	return d, nil
}

// Rollback creates a new deployment that re-applies the image digests
// recorded by deployment `number`.
func (s *Service) Rollback(ctx context.Context, projectName string, number int64) (db.Deployment, error) {
	p, err := s.projects.Get(ctx, projectName)
	if err != nil {
		return db.Deployment{}, err
	}
	target, err := s.Get(ctx, projectName, number)
	if err != nil {
		return db.Deployment{}, err
	}
	if target.Status != "succeeded" {
		return db.Deployment{}, fmt.Errorf("can only roll back to a succeeded deployment (this one is %s)", target.Status)
	}
	arts, err := s.q.ListDeploymentArtifacts(ctx, target.ID)
	if err != nil {
		return db.Deployment{}, err
	}
	if len(arts) == 0 {
		return db.Deployment{}, errors.New("deployment has no recorded image digests to roll back to")
	}

	active, err := s.q.CountActiveDeployments(ctx, p.ID)
	if err != nil {
		return db.Deployment{}, err
	}
	if active > 0 {
		return db.Deployment{}, ErrDeployInProgress
	}

	d, err := s.q.CreateDeployment(ctx, db.CreateDeploymentParams{
		ProjectID:   p.ID,
		ProjectID_2: p.ID,
		TriggeredBy: "rollback",
		RollbackOf:  sql.NullInt64{Int64: target.ID, Valid: true},
	})
	if err != nil {
		return db.Deployment{}, err
	}
	// Git rollbacks also pin the commit for traceability.
	if target.GitCommit.Valid {
		s.q.SetDeploymentCommit(ctx, db.SetDeploymentCommitParams{GitCommit: target.GitCommit, ID: d.ID})
	}

	if _, err := s.runner.Enqueue(ctx, JobType, payload{DeploymentID: d.ID, Project: projectName}); err != nil {
		return db.Deployment{}, err
	}
	s.publish(d.ID, projectName, "deployment.created",
		fmt.Sprintf("rollback to #%d queued as #%d", target.Number, d.Number))
	return d, nil
}

// rollbackOverride renders the compose override pinning services to digests.
func rollbackOverride(arts []db.DeploymentArtifact) string {
	var b strings.Builder
	b.WriteString("# Generated by Windlass for a rollback deployment. Safe to delete;\n")
	b.WriteString("# it is only used with `docker compose -f compose.yaml -f " + rollbackFile + "`.\n")
	b.WriteString("services:\n")
	for _, a := range arts {
		b.WriteString("  " + a.Service + ":\n")
		b.WriteString("    image: " + pinnedRef(a.ImageRef, a.ImageDigest) + "\n")
	}
	return b.String()
}

// pinnedRef turns "nginx:1.25" + "sha256:abc" into "nginx@sha256:abc".
// Locally-built images (digest = image ID) are referenced by ID directly.
func pinnedRef(imageRef, digest string) string {
	if !strings.HasPrefix(digest, "sha256:") {
		return imageRef // shouldn't happen; fall back to the tag
	}
	repo := imageRef
	// Strip a trailing tag, careful with registry ports (host:5000/img:tag).
	if i := strings.LastIndexByte(repo, ':'); i > strings.LastIndexByte(repo, '/') {
		repo = repo[:i]
	}
	return repo + "@" + digest
}

func (s *Service) Get(ctx context.Context, projectName string, number int64) (db.Deployment, error) {
	d, err := s.q.GetDeployment(ctx, db.GetDeploymentParams{Name: projectName, Number: number})
	if errors.Is(err, sql.ErrNoRows) {
		return db.Deployment{}, ErrNotFound
	}
	return d, err
}

func (s *Service) List(ctx context.Context, projectName string, limit int64) ([]db.Deployment, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.q.ListDeployments(ctx, db.ListDeploymentsParams{Name: projectName, Limit: limit})
}

// Events returns stored deployment events after seq.
func (s *Service) Events(ctx context.Context, deploymentID, afterSeq int64) ([]db.DeploymentEvent, error) {
	return s.q.ListDeploymentEvents(ctx, db.ListDeploymentEventsParams{
		DeploymentID: deploymentID, Seq: afterSeq, Limit: 10000,
	})
}

// ---------------------------------------------------------------------------
// Job execution

func (s *Service) runJob(ctx context.Context, raw json.RawMessage, resumeStep string, checkpoint func(string) error) error {
	var pl payload
	if err := json.Unmarshal(raw, &pl); err != nil {
		return fmt.Errorf("bad payload: %w", err)
	}

	d, err := s.q.GetDeploymentByID(ctx, pl.DeploymentID)
	if err != nil {
		return fmt.Errorf("load deployment: %w", err)
	}

	// A finished deployment must not re-run (e.g. reclaimed job whose
	// deployment was cancelled while queued).
	switch d.Status {
	case "succeeded", "failed", "cancelled":
		return nil
	}

	// Superseded? Only the latest deployment may run.
	if latest, err := s.q.LatestDeploymentID(ctx, d.ProjectID); err == nil && latest != d.ID {
		s.finish(ctx, d, "cancelled", "superseded by a newer deployment")
		return nil
	}

	if d.StartedAt.Valid && resumeStep != "" {
		s.event(ctx, d.ID, "step", fmt.Sprintf("resuming interrupted deployment at step %q", resumeStep))
	}

	// Rollbacks skip sync/pull/build.
	steps := stepOrder
	if d.RollbackOf.Valid {
		steps = rollbackSteps
	}

	// Execute steps from the resume point.
	start := 0
	if resumeStep != "" {
		for i, st := range steps {
			if st == resumeStep {
				start = i
				break
			}
		}
	}

	if err := s.q.MarkDeploymentStarted(ctx, db.MarkDeploymentStartedParams{Status: steps[start], ID: d.ID}); err != nil {
		return err
	}

	for _, step := range steps[start:] {
		if ctx.Err() != nil {
			s.finish(ctx, d, "cancelled", "cancelled")
			return ctx.Err()
		}
		if err := checkpoint(step); err != nil {
			return err
		}
		if err := s.q.SetDeploymentStatus(ctx, db.SetDeploymentStatusParams{Status: step, ID: d.ID}); err != nil {
			return err
		}
		s.publish(d.ID, pl.Project, "deployment.step", step)

		if err := s.runStep(ctx, step, d, pl.Project); err != nil {
			if ctx.Err() != nil {
				s.finish(ctx, d, "cancelled", "cancelled")
				return ctx.Err()
			}
			s.event(ctx, d.ID, "error", err.Error())
			s.finish(ctx, d, "failed", err.Error())
			return nil // the deployment failed; the job itself is done
		}
	}

	s.finish(ctx, d, "succeeded", "")
	return nil
}

func (s *Service) runStep(ctx context.Context, step string, d db.Deployment, project string) error {
	sink := s.logSink(ctx, d.ID)

	switch step {
	case stepPreparing:
		s.event(ctx, d.ID, "step", "rendering environment and validating compose file")
		if err := s.projects.RenderEnvFile(ctx, project); err != nil {
			return fmt.Errorf("render .env: %w", err)
		}
		warnings, err := s.projects.ValidateEnv(ctx, project)
		if err != nil {
			return err
		}
		for _, warning := range warnings {
			s.event(ctx, d.ID, "log", "warning: "+warning)
		}
		if d.RollbackOf.Valid {
			arts, err := s.q.ListDeploymentArtifacts(ctx, d.RollbackOf.Int64)
			if err != nil {
				return err
			}
			s.event(ctx, d.ID, "step", "pinning images to recorded digests")
			if err := s.agent.FS().WriteFile(ctx, project, rollbackFile,
				[]byte(rollbackOverride(arts)), 0o644); err != nil {
				return fmt.Errorf("write rollback override: %w", err)
			}
		}
		if _, err := s.agent.Compose().Config(ctx, project); err != nil {
			return err
		}
		return nil

	case stepSyncing:
		p, err := s.projects.Get(ctx, project)
		if err != nil {
			return err
		}
		if p.Source != "git" || !p.GitRepo.Valid {
			return nil // nothing to sync
		}
		s.event(ctx, d.ID, "step", "syncing "+p.GitRepo.String)
		branch := p.GitBranch.String
		if branch == "" {
			branch = "main"
		}
		token, err := s.git.Token(ctx, p)
		if err != nil {
			return err
		}
		res, err := s.agent.Host().GitSync(ctx, agent.GitSyncReq{
			Project: project,
			URL:     p.GitRepo.String,
			Branch:  branch,
			Commit:  d.GitCommit.String, // pinned on resume/rollback, empty otherwise
			Token:   token,
		}, sink)
		if err != nil {
			return err
		}
		return s.q.SetDeploymentCommit(ctx, db.SetDeploymentCommitParams{
			GitCommit: sql.NullString{String: res.Commit, Valid: true}, ID: d.ID,
		})

	case stepPulling:
		s.event(ctx, d.ID, "step", "pulling images")
		return s.agent.Compose().Pull(ctx, project, sink)

	case stepBuilding:
		cfg, err := s.agent.Compose().Config(ctx, project)
		if err != nil {
			return err
		}
		hasBuild := false
		for _, svc := range cfg.Services {
			if svc.Build {
				hasBuild = true
				break
			}
		}
		if hasBuild {
			s.event(ctx, d.ID, "step", "building images")
			if err := s.agent.Compose().Build(ctx, project, sink); err != nil {
				return err
			}
		}
		// Record image digests for rollback, whether pulled or built.
		for name, svc := range cfg.Services {
			if svc.Image == "" {
				continue
			}
			digest, err := s.agent.Docker().ImageDigest(ctx, svc.Image)
			if err != nil {
				s.logger.Warn("digest unavailable", "image", svc.Image, "error", err)
				continue
			}
			if err := s.q.InsertDeploymentArtifact(ctx, db.InsertDeploymentArtifactParams{
				DeploymentID: d.ID, Service: name, ImageRef: svc.Image, ImageDigest: digest,
			}); err != nil {
				return err
			}
		}
		return nil

	case stepApplying:
		s.event(ctx, d.ID, "step", "starting services")
		req := agent.ComposeUpReq{Project: project, RemoveOrphans: true}
		if d.RollbackOf.Valid {
			req.ExtraFiles = []string{rollbackFile}
		}
		return s.agent.Compose().Up(ctx, req, sink)

	case stepVerifying:
		s.event(ctx, d.ID, "step", "waiting for services to become healthy")
		return s.verify(ctx, d.ID, project)
	}
	return fmt.Errorf("unknown step %q", step)
}

// verify polls compose ps until everything is running/healthy or the
// timeout expires.
func (s *Service) verify(ctx context.Context, deploymentID int64, project string) error {
	deadline := time.Now().Add(VerifyTimeout)
	config, err := s.agent.Compose().Config(ctx, project)
	if err != nil {
		return err
	}
	stableSince := make(map[string]time.Time, len(config.HealthChecks))
	lastHealthError := ""
	for {
		statuses, err := s.agent.Compose().PS(ctx, project)
		if err != nil {
			return err
		}

		allGood, anyFatal := true, ""
		for _, st := range statuses {
			switch {
			case st.Health == "unhealthy":
				anyFatal = fmt.Sprintf("service %s is unhealthy", st.Service)
			case st.State == "exited" && st.ExitCode != 0:
				anyFatal = fmt.Sprintf("service %s exited with code %d", st.Service, st.ExitCode)
			case st.State == "running" && (st.Health == "" || st.Health == "healthy"):
				// good
			case st.State == "exited" && st.ExitCode == 0:
				// one-shot service (e.g. migrations) — fine
			default:
				allGood = false
			}
		}
		if anyFatal != "" {
			return errors.New(anyFatal)
		}
		if allGood && len(statuses) > 0 {
			checksGood := true
			for _, check := range config.HealthChecks {
				result, checkErr := s.agent.Host().HTTPCheck(ctx, agent.HTTPCheckReq{URL: check.URL, Timeout: 10})
				key := check.Service + "|" + check.URL
				switch {
				case checkErr != nil:
					lastHealthError = fmt.Sprintf("%s health URL: %v", check.Service, checkErr)
					stableSince[key] = time.Time{}
					checksGood = false
				case result.StatusCode != check.ExpectedStatus:
					lastHealthError = fmt.Sprintf("%s health URL returned HTTP %d, expected %d",
						check.Service, result.StatusCode, check.ExpectedStatus)
					stableSince[key] = time.Time{}
					checksGood = false
				case check.Contains != "" && !strings.Contains(result.Body, check.Contains):
					lastHealthError = fmt.Sprintf("%s health response did not contain required text", check.Service)
					stableSince[key] = time.Time{}
					checksGood = false
				default:
					if stableSince[key].IsZero() {
						stableSince[key] = time.Now()
					}
					if time.Since(stableSince[key]) < time.Duration(check.StabilitySeconds)*time.Second {
						checksGood = false
						lastHealthError = fmt.Sprintf("%s health check is waiting for its %ds stability window",
							check.Service, check.StabilitySeconds)
					}
				}
			}
			if checksGood {
				s.event(ctx, deploymentID, "step", "all services and application checks healthy")
				return nil
			}
		}
		if len(statuses) == 0 {
			return errors.New("no services were created")
		}

		if time.Now().After(deadline) {
			if lastHealthError != "" {
				return fmt.Errorf("application not healthy after %s: %s", VerifyTimeout, lastHealthError)
			}
			return fmt.Errorf("services not healthy after %s", VerifyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(verifyPollInterval):
		}
	}
}

// ---------------------------------------------------------------------------
// Event plumbing

// logSink persists compose/git output lines as deployment events and
// publishes them for live SSE tails.
func (s *Service) logSink(ctx context.Context, deploymentID int64) agent.LogSink {
	return func(line agent.LogLine) {
		s.event(ctx, deploymentID, "log", line.Text)
	}
}

func (s *Service) event(ctx context.Context, deploymentID int64, typ, message string) {
	seq, err := s.q.InsertDeploymentEvent(context.WithoutCancel(ctx), db.InsertDeploymentEventParams{
		DeploymentID: deploymentID, DeploymentID_2: deploymentID, Type: typ, Message: message,
	})
	if err != nil {
		s.logger.Error("insert deployment event", "error", err)
		return
	}
	s.bus.Publish(events.Event{
		Topic: "deployment", Type: "deployment." + typ,
		Resource: fmt.Sprintf("%d", deploymentID),
		Data:     map[string]any{"seq": seq, "message": message, "event_type": typ},
	})
}

func (s *Service) publish(deploymentID int64, project, typ, message string) {
	s.bus.Publish(events.Event{
		Topic: "deployment", Type: typ, Resource: project,
		Data: map[string]any{"deployment_id": deploymentID, "message": message},
	})
}

func (s *Service) finish(ctx context.Context, d db.Deployment, status, errMsg string) {
	ctx = context.WithoutCancel(ctx)
	if err := s.q.FinishDeployment(ctx, db.FinishDeploymentParams{
		Status: status,
		Error:  sql.NullString{String: errMsg, Valid: errMsg != ""},
		ID:     d.ID,
	}); err != nil {
		s.logger.Error("finish deployment", "error", err)
	}
	s.event(ctx, d.ID, "done", status)
	s.logger.Info("deployment finished", "deployment", d.ID, "status", status, "error", errMsg)
}
