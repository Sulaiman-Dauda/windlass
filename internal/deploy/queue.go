package deploy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/store/db"
)

// ErrNothingWaiting is returned by CancelWaiting when the project has no
// deploy waiting.
var ErrNothingWaiting = errors.New("no deploy is waiting for this project")

// Waiting is a deploy asked for while another deployment of the same
// project was running. It lives in memory only: a restart drops it, and the
// drop is logged. It has no deployment number until it starts.
//
// A project holds at most one. A deploy that does not pin a commit or
// digests builds what the branch and files hold when it starts, so a second
// request while one waits would deploy the same thing: it is folded into
// the first and counted in Requests.
type Waiting struct {
	Project string `json:"project"`
	// Trigger is that of the first request: manual or webhook.
	Trigger  string    `json:"trigger"`
	QueuedAt time.Time `json:"queued_at"`
	Requests int       `json:"requests"`
	// Position is the place in the project's queue. With one slot per
	// project it is always 1; it is in the answer so a client need not
	// know that.
	Position int `json:"position"`
}

// DeployOrQueue starts a deployment, or, when one is already running for
// the project, holds the request until it finishes. Exactly one of the
// deployment and the waiting entry is set when err is nil.
func (s *Service) DeployOrQueue(ctx context.Context, projectName, trigger string) (db.Deployment, *Waiting, error) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	d, err := s.deploy(ctx, projectName, trigger, payload{Project: projectName})
	if !errors.Is(err, ErrDeployInProgress) {
		return d, nil, err
	}
	w := s.waiting[projectName]
	if w == nil {
		w = &Waiting{Project: projectName, Trigger: trigger, QueuedAt: time.Now().UTC(), Position: 1}
		s.waiting[projectName] = w
	}
	w.Requests++
	s.publish(0, projectName, "deployment.waiting",
		fmt.Sprintf("deploy waiting for the running deployment to finish (%d request(s))", w.Requests))
	s.logger.Info("deploy waiting", "project", projectName, "trigger", trigger, "requests", w.Requests)
	out := *w
	return db.Deployment{}, &out, nil
}

// WaitingFor returns the project's waiting deploy, or nil.
func (s *Service) WaitingFor(projectName string) *Waiting {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	w := s.waiting[projectName]
	if w == nil {
		return nil
	}
	out := *w
	return &out
}

// CancelWaiting drops the project's waiting deploy. A deployment already
// running is never touched.
func (s *Service) CancelWaiting(projectName string) (Waiting, error) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	w := s.waiting[projectName]
	if w == nil {
		return Waiting{}, ErrNothingWaiting
	}
	delete(s.waiting, projectName)
	s.publish(0, projectName, "deployment.waiting_cancelled", "waiting deploy cancelled")
	s.logger.Info("waiting deploy cancelled", "project", projectName)
	return *w, nil
}

// DropWaiting logs and forgets every waiting deploy. main calls it on
// shutdown, so the log says which requests a restart lost.
func (s *Service) DropWaiting(reason string) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	for name, w := range s.waiting {
		s.logger.Warn("waiting deploy dropped", "project", name, "trigger", w.Trigger,
			"requests", w.Requests, "queued_at", w.QueuedAt, "reason", reason)
		delete(s.waiting, name)
	}
}

// startWaiting runs once a deployment job of the project returns. If a
// deploy is waiting and nothing else is active, it starts now. When ctx has
// ended, Windlass is stopping: the entry stays for DropWaiting to log.
func (s *Service) startWaiting(ctx context.Context, projectName string) {
	if ctx.Err() != nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	w := s.waiting[projectName]
	if w == nil {
		return
	}
	d, err := s.deploy(ctx, projectName, w.Trigger, payload{Project: projectName})
	switch {
	case errors.Is(err, ErrDeployInProgress):
		// Another deployment (a rollback, a promotion) began first; this
		// one waits for it in turn.
		return
	case err != nil:
		delete(s.waiting, projectName)
		var adoption *projects.AdoptionError
		msg := err.Error()
		if errors.As(err, &adoption) {
			msg = adoption.Error()
		}
		s.logger.Warn("waiting deploy could not start", "project", projectName, "error", msg)
		s.publish(0, projectName, "deployment.waiting_failed", "waiting deploy could not start: "+msg)
		return
	}
	delete(s.waiting, projectName)
	s.logger.Info("waiting deploy started", "project", projectName, "deployment", d.Number, "requests", w.Requests)
}
