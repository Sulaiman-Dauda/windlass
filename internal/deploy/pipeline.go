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
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/events"
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

var (
	ErrDeployInProgress = errors.New("a deployment is already running for this project")
	ErrNotFound         = errors.New("deployment not found")
)

// VerifyTimeout is how long the verify step waits for services to be healthy.
var VerifyTimeout = 120 * time.Second

type payload struct {
	DeploymentID int64  `json:"deployment_id"`
	Project      string `json:"project"`
}

type Service struct {
	q        *db.Queries
	agent    agent.Agent
	projects *projects.Service
	runner   *jobs.Runner
	bus      *events.Bus
	logger   *slog.Logger
}

func New(q *db.Queries, ag agent.Agent, proj *projects.Service, runner *jobs.Runner, bus *events.Bus, logger *slog.Logger) *Service {
	s := &Service{q: q, agent: ag, projects: proj, runner: runner, bus: bus, logger: logger}
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

	// Execute steps from the resume point.
	start := 0
	if resumeStep != "" {
		for i, st := range stepOrder {
			if st == resumeStep {
				start = i
				break
			}
		}
	}

	if err := s.q.MarkDeploymentStarted(ctx, db.MarkDeploymentStartedParams{Status: stepOrder[start], ID: d.ID}); err != nil {
		return err
	}

	for _, step := range stepOrder[start:] {
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
		res, err := s.agent.Host().GitSync(ctx, agent.GitSyncReq{
			Project: project,
			URL:     p.GitRepo.String,
			Branch:  branch,
			Commit:  d.GitCommit.String, // pinned on resume/rollback, empty otherwise
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
		return s.agent.Compose().Up(ctx, agent.ComposeUpReq{Project: project, RemoveOrphans: true}, sink)

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
			s.event(ctx, deploymentID, "step", "all services healthy")
			return nil
		}
		if len(statuses) == 0 {
			return errors.New("no services were created")
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("services not healthy after %s", VerifyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
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
