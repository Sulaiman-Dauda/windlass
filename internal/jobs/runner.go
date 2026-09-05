// Package jobs is a SQLite-backed in-process job runner. It exists so that
// deployments survive a crash or restart (principle 13): job state and step
// checkpoints are persisted, and on startup any job that was running is
// re-queued and resumed from its last checkpoint.
//
// It is deliberately not a distributed queue, one process, one worker loop,
// no extra services (principle 5).
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/windlass-dev/windlass/internal/store/db"
)

// Handler executes a job. It receives the persisted step checkpoint (empty
// for a fresh job) and a checkpoint function to persist progress before
// starting each step.
type Handler func(ctx context.Context, payload json.RawMessage, step string, checkpoint func(step string) error) error

const maxAttempts = 3

type Runner struct {
	q      *db.Queries
	logger *slog.Logger

	mu       sync.Mutex
	handlers map[string]Handler
	cancels  map[int64]context.CancelFunc // running jobs, for Cancel()

	wake chan struct{}
}

func NewRunner(q *db.Queries, logger *slog.Logger) *Runner {
	return &Runner{
		q:        q,
		logger:   logger,
		handlers: map[string]Handler{},
		cancels:  map[int64]context.CancelFunc{},
		wake:     make(chan struct{}, 1),
	}
}

func (r *Runner) Register(jobType string, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[jobType] = h
}

// Enqueue persists a job and wakes the worker.
func (r *Runner) Enqueue(ctx context.Context, jobType string, payload any) (int64, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	job, err := r.q.EnqueueJob(ctx, db.EnqueueJobParams{Type: jobType, Payload: string(body)})
	if err != nil {
		return 0, err
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
	return job.ID, nil
}

// Cancel aborts a running job's context. Queued jobs are left to their
// handler's own status checks.
func (r *Runner) Cancel(jobID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cancel, ok := r.cancels[jobID]; ok {
		cancel()
	}
}

// Run reclaims interrupted jobs and processes the queue until ctx ends.
func (r *Runner) Run(ctx context.Context) error {
	// Single-process: every 'running' job at startup is an interrupted job.
	if n, err := r.q.ReclaimRunningJobs(ctx); err != nil {
		return fmt.Errorf("reclaim jobs: %w", err)
	} else if n > 0 {
		r.logger.Info("reclaimed interrupted jobs", "count", n)
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		// Drain everything claimable, then wait for a wake or tick.
		for {
			if ctx.Err() != nil {
				return nil
			}
			job, err := r.q.ClaimNextJob(ctx)
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				r.logger.Error("claim job", "error", err)
				break
			}
			r.execute(ctx, job)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-r.wake:
		case <-ticker.C:
		}
	}
}

func (r *Runner) execute(parent context.Context, job db.Job) {
	r.mu.Lock()
	handler, ok := r.handlers[job.Type]
	r.mu.Unlock()
	if !ok {
		r.logger.Error("no handler for job type", "type", job.Type, "id", job.ID)
		r.finish(parent, job.ID, "dead")
		return
	}

	ctx, cancel := context.WithCancel(parent)
	r.mu.Lock()
	r.cancels[job.ID] = cancel
	r.mu.Unlock()
	defer func() {
		cancel()
		r.mu.Lock()
		delete(r.cancels, job.ID)
		r.mu.Unlock()
	}()

	checkpoint := func(step string) error {
		return r.q.CheckpointJob(context.WithoutCancel(ctx), db.CheckpointJobParams{Step: step, ID: job.ID})
	}

	r.logger.Info("job started", "type", job.Type, "id", job.ID, "attempt", job.Attempts, "resume_step", job.Step)
	err := handler(ctx, json.RawMessage(job.Payload), job.Step, checkpoint)

	switch {
	case err == nil:
		r.finish(parent, job.ID, "done")
	case job.Attempts >= maxAttempts:
		r.logger.Error("job dead after max attempts", "type", job.Type, "id", job.ID, "error", err)
		r.finish(parent, job.ID, "dead")
	case errors.Is(err, context.Canceled):
		// Cancelled on purpose (user cancel or shutdown). Shutdown-interrupted
		// jobs are reclaimed at next startup via their 'running' status,
		// leave the row as-is only for shutdown; explicit cancels finish.
		if parent.Err() != nil {
			return // shutting down: keep status=running for reclaim
		}
		r.finish(parent, job.ID, "failed")
	default:
		r.logger.Error("job failed", "type", job.Type, "id", job.ID, "error", err)
		r.finish(parent, job.ID, "failed")
	}
}

func (r *Runner) finish(ctx context.Context, id int64, status string) {
	if err := r.q.FinishJob(context.WithoutCancel(ctx), db.FinishJobParams{Status: status, ID: id}); err != nil {
		r.logger.Error("finish job", "id", id, "error", err)
	}
}
