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
	"sort"
	"strings"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/git"
	"github.com/windlass-dev/windlass/internal/jobs"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/registries"
	"github.com/windlass-dev/windlass/internal/store/db"
)

const (
	JobType = "deploy"
	// InterruptedDataCopyMessage is durable: staging recovery recognises it
	// without treating an unrelated failed deployment as disposable.
	InterruptedDataCopyMessage = "data copy was interrupted; delete staging and create it again"

	stepPreparing = "preparing"
	stepSyncing   = "syncing"
	stepPulling   = "pulling"
	stepBuilding  = "building"
	stepApplying  = "applying"
	stepVerifying = "verifying"
)

// stepOrder drives resume: a reclaimed job re-executes from its checkpoint.
var stepOrder = []string{stepPreparing, stepSyncing, stepPulling, stepBuilding, stepApplying, stepVerifying}

// rollbackSteps skip sync/pull/build: pulled images are pinned to recorded
// digests, which compose up fetches again if they were pruned.
var rollbackSteps = []string{stepPreparing, stepApplying, stepVerifying}

// rollbackGitSteps also check out the recorded commit and build from it. A
// service built on the server has no digest a registry can return, and its
// old image is usually gone once a newer build replaced the tag, so building
// the old commit is the only way to get its earlier code back.
var rollbackGitSteps = []string{stepPreparing, stepSyncing, stepBuilding, stepApplying, stepVerifying}

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
	// Seed names the live project whose data a new staging copies, in the
	// applying step, before its first start.
	Seed string `json:"seed,omitempty"`
	// NoPromote keeps a new staging's first deployment from promoting
	// itself: nobody has looked at it yet.
	NoPromote bool `json:"no_promote,omitempty"`
}

// pinned reports whether d runs digests and a commit chosen when it was
// created, as a rollback and a promotion do, rather than what the branch
// and tags hold now.
func pinned(d db.Deployment) bool {
	return d.RollbackOf.Valid || d.PromotedFrom.Valid
}

type Service struct {
	q        *db.Queries
	agent    agent.Agent
	projects *projects.Service
	git      *git.Service
	registry *registries.Service
	runner   *jobs.Runner
	bus      *events.Bus
	logger   *slog.Logger
}

func New(q *db.Queries, ag agent.Agent, proj *projects.Service, gitSvc *git.Service, reg *registries.Service, runner *jobs.Runner, bus *events.Bus, logger *slog.Logger) *Service {
	s := &Service{q: q, agent: ag, projects: proj, git: gitSvc, registry: reg, runner: runner, bus: bus, logger: logger}
	runner.Register(JobType, s.runJob)
	return s
}

// Deploy creates a deployment and enqueues its job. One active deployment
// per project.
func (s *Service) Deploy(ctx context.Context, projectName, trigger string) (db.Deployment, error) {
	return s.deploy(ctx, projectName, trigger, payload{Project: projectName})
}

func (s *Service) deploy(ctx context.Context, projectName, trigger string, pl payload) (db.Deployment, error) {
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

	pl.DeploymentID, pl.Project = d.ID, projectName
	if _, err := s.runner.Enqueue(ctx, JobType, pl); err != nil {
		return db.Deployment{}, s.abandon(ctx, d, err)
	}

	s.publish(d.ID, projectName, "deployment.created", fmt.Sprintf("deployment #%d queued", d.Number))
	return d, nil
}

// Rollback creates a new deployment that re-applies the image digests
// recorded by deployment `number`, and for a Git project its commit.
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
	if len(arts) == 0 && !target.GitCommit.Valid {
		return db.Deployment{}, errors.New("deployment has no recorded image digests or Git commit to roll back to")
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
	// Git rollbacks pin the commit: the sync step checks it out and the
	// build step builds it.
	if target.GitCommit.Valid {
		if err := s.q.SetDeploymentCommit(ctx, db.SetDeploymentCommitParams{GitCommit: target.GitCommit, ID: d.ID}); err != nil {
			return db.Deployment{}, s.abandon(ctx, d, err)
		}
	}
	// The rollback runs the same pulled images, so it records the same
	// digests, and can itself be rolled back to.
	for _, a := range arts {
		if err := s.q.InsertDeploymentArtifact(ctx, db.InsertDeploymentArtifactParams{
			DeploymentID: d.ID, Service: a.Service, ImageRef: a.ImageRef, ImageDigest: a.ImageDigest,
		}); err != nil {
			return db.Deployment{}, s.abandon(ctx, d, err)
		}
	}

	if _, err := s.runner.Enqueue(ctx, JobType, payload{DeploymentID: d.ID, Project: projectName}); err != nil {
		return db.Deployment{}, s.abandon(ctx, d, err)
	}
	s.publish(d.ID, projectName, "deployment.created",
		fmt.Sprintf("rollback to #%d queued as #%d", target.Number, d.Number))
	return d, nil
}

// abandon fails a deployment whose row was written but whose job was never
// queued. Left queued, it would count as active and refuse every later deploy.
func (s *Service) abandon(ctx context.Context, d db.Deployment, err error) error {
	s.finish(ctx, d, "failed", "could not be queued: "+err.Error())
	return err
}

// rollbackOverride renders the compose override pinning pulled services to
// their digests. Services in built are left out.
func rollbackOverride(arts []db.DeploymentArtifact, built map[string]bool) string {
	var b strings.Builder
	b.WriteString("# Generated by Windlass for a rollback deployment. Safe to delete;\n")
	b.WriteString("# a rollback passes it with -f after the project's own compose files.\n")
	var services strings.Builder
	for _, a := range arts {
		if built[a.Service] {
			continue
		}
		services.WriteString("  " + a.Service + ":\n")
		services.WriteString("    image: " + pinnedRef(a.ImageRef, a.ImageDigest) + "\n")
	}
	if services.Len() == 0 {
		b.WriteString("services: {}\n")
	} else {
		b.WriteString("services:\n" + services.String())
	}
	return b.String()
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// shortSHA is the seven-character form Git shows.
func shortSHA(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
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

	// A seed is not safe to run twice: half-loaded data would be loaded
	// again on top of itself.
	if pl.Seed != "" && d.StartedAt.Valid && resumeStep == stepApplying {
		s.event(ctx, d.ID, "error", InterruptedDataCopyMessage)
		s.finish(ctx, d, "failed", InterruptedDataCopyMessage)
		return nil
	}

	// Rollbacks and promotions skip pull, and sync and build unless there is
	// a commit.
	steps := stepOrder
	if pinned(d) {
		steps = rollbackSteps
		if d.GitCommit.Valid {
			steps = rollbackGitSteps
		}
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

		if err := s.runStep(ctx, step, d, pl); err != nil {
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
	// The first deployment of a new staging is a copy of live, not a
	// change to it, so it is never promoted on its own. Nor is a rollback:
	// promoting it would roll live back too, and live has its own Rollback.
	if !pl.NoPromote && !d.RollbackOf.Valid {
		s.autoPromote(ctx, d, pl.Project)
	}
	return nil
}

func (s *Service) runStep(ctx context.Context, step string, d db.Deployment, pl payload) error {
	sink := s.logSink(ctx, d.ID)
	project := pl.Project

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
		cfg, err := s.agent.Compose().Config(ctx, project)
		if err != nil {
			return err
		}
		if err := s.checkStaging(ctx, d.ID, project, cfg); err != nil {
			return err
		}
		if pinned(d) {
			// A rollback pins its target's digests. A promotion pins the
			// ones copied onto it, which leave out services live lacks.
			source := d.RollbackOf.Int64
			if d.PromotedFrom.Valid {
				source = d.ID
			}
			arts, err := s.q.ListDeploymentArtifacts(ctx, source)
			if err != nil {
				return err
			}
			// A service built here is never pinned: Compose would try to
			// build it under a digest tag and fail.
			built := map[string]bool{}
			for name, svc := range cfg.Services {
				if svc.Build {
					built[name] = true
				}
			}
			// Git may have been disconnected since the target ran, and then
			// the sync step checks nothing out.
			p, err := s.projects.Get(ctx, project)
			if err != nil {
				return err
			}
			fromGit := p.Source == "git" && p.GitRepo.Valid
			s.event(ctx, d.ID, "step", "pinning images to recorded digests")
			for _, name := range sortedKeys(built) {
				switch {
				case d.GitCommit.Valid && fromGit:
					s.event(ctx, d.ID, "log", fmt.Sprintf("%s is built on this server; it is rebuilt at commit %s",
						name, shortCommit(d.GitCommit.String)))
				case d.GitCommit.Valid:
					s.event(ctx, d.ID, "warn", fmt.Sprintf("%s is built on this server and the project is no longer "+
						"connected to Git, so it is built from the files on disk as they are now", name))
				default:
					s.event(ctx, d.ID, "warn", fmt.Sprintf("%s is built on this server and there is no Git commit "+
						"to rebuild it from, so it keeps its current image", name))
				}
			}
			if err := s.agent.FS().WriteFile(ctx, project, rollbackFile,
				[]byte(rollbackOverride(arts, built)), 0o644); err != nil {
				return fmt.Errorf("write rollback override: %w", err)
			}
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
			// Live only ever runs code merged into the branch it deploys.
			RequireOnBranch: d.PromotedFrom.Valid,
		}, sink)
		if errors.Is(err, agent.ErrNotOnBranch) {
			return fmt.Errorf("commit %s is not on %s yet. Merge it, then promote", shortSHA(d.GitCommit.String), branch)
		}
		if err != nil {
			return err
		}
		return s.q.SetDeploymentCommit(ctx, db.SetDeploymentCommitParams{
			GitCommit: sql.NullString{String: res.Commit, Valid: true}, ID: d.ID,
		})

	case stepPulling:
		// Log the host in first. Without this a private image fails with a bare
		// "unauthorized", which reads as a broken registry rather than a
		// missing credential, and every project using one is undeployable.
		//
		// A login failure is reported and does not stop the step: a public
		// image still pulls fine, and the pull itself gives a better error for
		// the image that actually needed the credential.
		if s.registry != nil {
			if err := s.registry.Apply(ctx, s.agent.Docker()); err != nil {
				s.event(ctx, d.ID, "warn", err.Error())
			}
			if images, err := s.pullableImages(ctx, project); err == nil {
				if missing, err := s.registry.Missing(ctx, images); err == nil && len(missing) > 0 {
					s.event(ctx, d.ID, "warn", fmt.Sprintf(
						"no credential configured for %s; a private image there cannot be pulled",
						strings.Join(missing, ", ")))
				}
			}
		}
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
		// A rollback or promotion carries the digests copied when it was
		// created; the tags here name the current images, not those.
		if pinned(d) {
			return nil
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
		if pl.Seed != "" {
			if err := s.seed(ctx, d.ID, pl.Seed, project, sink); err != nil {
				return err
			}
		}
		s.event(ctx, d.ID, "step", "starting services")
		req := agent.ComposeUpReq{Project: project, RemoveOrphans: true}
		if pinned(d) {
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
				// one-shot service (e.g. migrations), fine
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

// pullableImages lists the images this project would pull, skipping anything
// built locally: a service with a build stanza has nothing to fetch, so a
// registry it names is not a missing credential.
func (s *Service) pullableImages(ctx context.Context, project string) ([]string, error) {
	cfg, err := s.agent.Compose().Config(ctx, project)
	if err != nil {
		return nil, err
	}
	var images []string
	for _, svc := range cfg.Services {
		if svc.Build || svc.Image == "" {
			continue
		}
		images = append(images, svc.Image)
	}
	return images, nil
}
