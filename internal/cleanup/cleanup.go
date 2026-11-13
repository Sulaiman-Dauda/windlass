// Package cleanup removes what builds and pulls leave on the Docker host:
// images nothing uses and build cache over a size limit. It runs after each
// successful deployment, unless switched off, and when an admin asks.
//
// Rollback and promotion depend on old images, so the images behind each
// project's last few successful deployments are always kept.
package cleanup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/store/db"
)

const (
	// SettingsKey holds Settings as JSON; LastKey the last Result.
	SettingsKey = "disk_cleanup"
	LastKey     = "disk_cleanup_last"

	// Limits on the settings, the same as the manual endpoint has had.
	MaxImageDays       = 3650
	MaxKeepDeployments = 100
	MaxBuildCacheGB    = 10000

	// runTimeout bounds one run. Trimming 20 GB of BuildKit cache took
	// under a minute on the host that prompted this; this is headroom.
	runTimeout = 15 * time.Minute
)

// Settings is what an admin sets in Settings > System.
type Settings struct {
	// Auto runs a clean-up after every successful deployment.
	Auto bool `json:"auto"`
	// ImageDays keeps unused images younger than this many days.
	ImageDays int `json:"image_days"`
	// KeepDeployments keeps the images of each project's last N
	// successful deployments, so rollback and promotion find them.
	KeepDeployments int `json:"keep_deployments"`
	// BuildCacheGB is the build cache left after a trim, in gigabytes
	// (10^9 bytes, as Docker reports sizes). 0 removes all unused cache.
	BuildCacheGB int `json:"build_cache_gb"`
}

// Defaults applies to an install that has never saved the settings. Auto is
// on, so a host that builds stops filling its disk without anyone visiting
// Settings.
var Defaults = Settings{Auto: true, ImageDays: 7, KeepDeployments: 5, BuildCacheGB: 10}

// Validate reports the first setting out of range.
func (s Settings) Validate() error {
	switch {
	case s.ImageDays < 1 || s.ImageDays > MaxImageDays:
		return fmt.Errorf("image_days must be between 1 and %d", MaxImageDays)
	case s.KeepDeployments < 1 || s.KeepDeployments > MaxKeepDeployments:
		return fmt.Errorf("keep_deployments must be between 1 and %d", MaxKeepDeployments)
	case s.BuildCacheGB < 0 || s.BuildCacheGB > MaxBuildCacheGB:
		return fmt.Errorf("build_cache_gb must be between 0 and %d", MaxBuildCacheGB)
	}
	return nil
}

// Result is one run, kept so the Settings page can say what the last run
// did, or why it did nothing.
type Result struct {
	// Trigger is "deploy" or "manual"; Project names the deployment that
	// triggered it.
	Trigger    string                      `json:"trigger"`
	Project    string                      `json:"project,omitempty"`
	StartedAt  time.Time                   `json:"started_at"`
	FinishedAt time.Time                   `json:"finished_at"`
	Settings   Settings                    `json:"settings"`
	Images     agent.ImagePruneResult      `json:"images"`
	BuildCache agent.BuildCachePruneResult `json:"build_cache"`
	// Error is set when a step could not run at all, such as Docker being
	// unreachable. Failures inside a step are in that step's Errors.
	Error string `json:"error,omitempty"`
	// Skipped says why an automatic run did nothing, such as another
	// deployment still running. The next successful deployment runs it.
	Skipped string `json:"skipped,omitempty"`
}

// ErrBusy means a clean-up is already running.
var ErrBusy = errors.New("a clean-up is already running")

// DeployingError means a deployment is still running, so nothing ran: an
// image it has pulled has no container yet and would look unused.
type DeployingError struct{ Projects []string }

func (e *DeployingError) Error() string {
	return "a deployment of " + strings.Join(e.Projects, ", ") + " is still running; clean up once it finishes"
}

type Service struct {
	// base ends automatic runs when Windlass stops.
	base   context.Context
	q      *db.Queries
	docker agent.DockerAgent
	logger *slog.Logger
	// audit records automatic runs; manual runs are audited by the API
	// with the admin who asked.
	audit func(ctx context.Context, action string, detail any)

	running sync.Mutex
	wg      sync.WaitGroup
	// finished is when the last run ended, in Unix nanoseconds, so cached
	// disk figures older than it can be read again.
	finished atomic.Int64
}

// New builds the service. Automatic runs stop when base ends.
func New(base context.Context, q *db.Queries, docker agent.DockerAgent, logger *slog.Logger, audit func(ctx context.Context, action string, detail any)) *Service {
	return &Service{base: base, q: q, docker: docker, logger: logger, audit: audit}
}

// Settings returns the saved settings, or Defaults when there are none or
// they no longer validate.
func (s *Service) Settings(ctx context.Context) Settings {
	raw, err := s.q.GetSetting(ctx, SettingsKey)
	if err != nil {
		return Defaults
	}
	var out Settings
	if json.Unmarshal([]byte(raw), &out) != nil || out.Validate() != nil {
		return Defaults
	}
	return out
}

func (s *Service) SaveSettings(ctx context.Context, settings Settings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return s.q.SetSetting(ctx, db.SetSettingParams{Key: SettingsKey, Value: string(raw)})
}

// Last returns the last run, or nil before the first.
func (s *Service) Last(ctx context.Context) (*Result, error) {
	raw, err := s.q.GetSetting(ctx, LastKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out Result
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, nil // an unreadable record is no record
	}
	return &out, nil
}

// Run cleans up now with the saved settings. It returns ErrBusy rather
// than queue behind a run already going, and a *DeployingError while any
// deployment is still running.
func (s *Service) Run(ctx context.Context, trigger, project string) (Result, error) {
	if !s.running.TryLock() {
		return Result{}, ErrBusy
	}
	defer s.running.Unlock()
	// Checked under the lock and just before the images are listed, so
	// the window for a deployment to start in between is small. Compose
	// pulls or builds again an image removed in that window.
	active, err := s.q.ActiveDeploymentProjects(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("check for running deployments: %w", err)
	}
	if len(active) > 0 {
		return Result{}, &DeployingError{Projects: active}
	}
	return s.run(ctx, trigger, project), nil
}

// run does both steps; a failure in one does not stop the other.
func (s *Service) run(ctx context.Context, trigger, project string) (r Result) {
	settings := s.Settings(ctx)
	r = Result{Trigger: trigger, Project: project, StartedAt: time.Now().UTC(), Settings: settings}
	defer func() {
		r.FinishedAt = time.Now().UTC()
		s.save(context.WithoutCancel(ctx), r)
		s.finished.Store(r.FinishedAt.UnixNano())
	}()
	var problems []string

	digests, err := s.q.ListProtectedImageDigests(ctx, settings.KeepDeployments)
	if err != nil {
		problems = append(problems, "list the images recent deployments use: "+err.Error())
	} else {
		images, err := s.docker.PruneImages(ctx, agent.ImagePruneReq{
			OlderThanSeconds: int64(settings.ImageDays) * 24 * 60 * 60,
			ProtectedDigests: digests,
		})
		if err != nil {
			problems = append(problems, "remove unused images: "+err.Error())
		}
		r.Images = images
	}

	cache, err := s.docker.PruneBuildCache(ctx, int64(settings.BuildCacheGB)*1e9)
	if err != nil {
		problems = append(problems, "trim build cache: "+err.Error())
	}
	r.BuildCache = cache
	r.Error = strings.Join(problems, "; ")
	return r
}

// FinishedAt is when the last run in this process ended, or zero.
func (s *Service) FinishedAt() time.Time {
	if n := s.finished.Load(); n != 0 {
		return time.Unix(0, n)
	}
	return time.Time{}
}

func (s *Service) save(ctx context.Context, r Result) {
	raw, err := json.Marshal(r)
	if err == nil {
		err = s.q.SetSetting(ctx, db.SetSettingParams{Key: LastKey, Value: string(raw)})
	}
	if err != nil {
		s.logger.Error("save disk clean-up result", "error", err)
	}
	s.logger.Info("disk clean-up finished", "trigger", r.Trigger, "project", r.Project,
		"images_deleted", r.Images.Deleted, "images_failed", r.Images.Failed,
		"image_bytes", r.Images.ReclaimedBytes, "cache_bytes", r.BuildCache.ReclaimedBytes, "error", r.Error, "skipped", r.Skipped)
}

// AfterDeploy starts a clean-up once a deployment of project succeeds,
// unless the switch is off. It returns at once: the deployment is done and
// nothing waits on the clean-up.
//
// It does nothing while any deployment is still running, because an image
// that deployment has pulled has no container until it is brought up. It
// records why, and the next successful deployment runs it.
func (s *Service) AfterDeploy(project string) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(s.base, runTimeout)
		defer cancel()
		if !s.Settings(ctx).Auto {
			return
		}
		r, err := s.Run(ctx, "deploy", project)
		if errors.Is(err, ErrBusy) {
			return
		}
		if err != nil {
			now := time.Now().UTC()
			skip := Result{Trigger: "deploy", Project: project, StartedAt: now, FinishedAt: now,
				Settings: s.Settings(ctx), Skipped: err.Error()}
			var deploying *DeployingError
			if errors.As(err, &deploying) {
				skip.Skipped = "a deployment of " + strings.Join(deploying.Projects, ", ") + " was still running"
			}
			s.save(context.WithoutCancel(ctx), skip)
			return
		}
		if s.audit != nil {
			s.audit(ctx, "docker.cleanup", AuditDetail(r))
		}
	}()
}

// Wait blocks until automatic runs started so far have finished. Tests and
// shutdown use it.
func (s *Service) Wait() { s.wg.Wait() }

// AuditDetail is what the audit log records about a run.
func AuditDetail(r Result) map[string]any {
	d := map[string]any{
		"trigger":              r.Trigger,
		"images_deleted":       r.Images.Deleted,
		"images_failed":        r.Images.Failed,
		"image_bytes":          r.Images.ReclaimedBytes,
		"build_cache_bytes":    r.BuildCache.ReclaimedBytes,
		"build_cache_limit_gb": r.Settings.BuildCacheGB,
	}
	if r.Project != "" {
		d["project"] = r.Project
	}
	if r.Error != "" {
		d["error"] = r.Error
	}
	return d
}
