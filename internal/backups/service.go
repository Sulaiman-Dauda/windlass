// Package backups archives project directories (with best-effort DB dumps
// for template databases), locally or to S3-compatible storage, manually or
// on a simple schedule.
package backups

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store/db"
)

var ErrNotFound = errors.New("backup not found")

const s3SettingKey = "backups.s3"

type Service struct {
	q        *db.Queries
	agent    agent.Agent
	projects *projects.Service
	box      *secrets.Box
	bus      *events.Bus
	logger   *slog.Logger
}

func New(q *db.Queries, ag agent.Agent, proj *projects.Service, box *secrets.Box, bus *events.Bus, logger *slog.Logger) *Service {
	return &Service{q: q, agent: ag, projects: proj, box: box, bus: bus, logger: logger}
}

// ---------------------------------------------------------------------------
// S3 settings (stored encrypted in the settings table)

func (s *Service) SetS3Config(ctx context.Context, cfg S3Config) error {
	plain, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	enc, err := s.box.Encrypt(plain)
	if err != nil {
		return err
	}
	wrapped, _ := json.Marshal(map[string]string{"enc": fmt.Sprintf("%x", enc)})
	return s.q.SetSetting(ctx, db.SetSettingParams{Key: s3SettingKey, Value: string(wrapped)})
}

func (s *Service) S3ConfigStatus(ctx context.Context) (configured bool, endpoint, bucket string) {
	cfg, err := s.s3Config(ctx)
	if err != nil || !cfg.Configured() {
		return false, "", ""
	}
	return true, cfg.Endpoint, cfg.Bucket
}

func (s *Service) s3Config(ctx context.Context) (S3Config, error) {
	raw, err := s.q.GetSetting(ctx, s3SettingKey)
	if errors.Is(err, sql.ErrNoRows) {
		return S3Config{}, nil
	}
	if err != nil {
		return S3Config{}, err
	}
	var wrapped map[string]string
	if err := json.Unmarshal([]byte(raw), &wrapped); err != nil {
		return S3Config{}, err
	}
	var enc []byte
	if _, err := fmt.Sscanf(wrapped["enc"], "%x", &enc); err != nil {
		return S3Config{}, err
	}
	plain, err := s.box.Decrypt(enc)
	if err != nil {
		return S3Config{}, err
	}
	var cfg S3Config
	err = json.Unmarshal(plain, &cfg)
	return cfg, err
}

// ---------------------------------------------------------------------------
// Backup / restore

// Run performs a backup synchronously and records it.
func (s *Service) Run(ctx context.Context, projectName, kind, destination string) (db.Backup, error) {
	p, err := s.projects.Get(ctx, projectName)
	if err != nil {
		return db.Backup{}, err
	}
	if destination != "s3" {
		destination = "local"
	}

	rec, err := s.q.CreateBackup(ctx, db.CreateBackupParams{
		ProjectID: p.ID, Kind: kind, Destination: destination,
	})
	if err != nil {
		return db.Backup{}, err
	}

	path, size, err := s.execute(ctx, p, destination)
	status, errMsg := "done", ""
	if err != nil {
		status, errMsg = "failed", err.Error()
		s.logger.Error("backup failed", "project", projectName, "error", err)
	}
	if ferr := s.q.FinishBackup(context.WithoutCancel(ctx), db.FinishBackupParams{
		Status: status, Path: path, Size: size,
		Error: sql.NullString{String: errMsg, Valid: errMsg != ""},
		ID:    rec.ID,
	}); ferr != nil {
		return db.Backup{}, ferr
	}
	s.bus.Publish(events.Event{Topic: "backup", Type: "backup." + status, Resource: projectName})

	rec.Status = status
	rec.Path = path
	rec.Size = size
	if err != nil {
		return rec, err
	}
	return rec, nil
}

func (s *Service) execute(ctx context.Context, p db.Project, destination string) (string, int64, error) {
	// Best-effort DB dump into the project dir so it lands in the archive.
	s.dumpDatabase(ctx, p.Name)

	info, err := s.agent.FS().ArchiveProject(ctx, p.Name)
	if err != nil {
		return "", 0, fmt.Errorf("archive: %w", err)
	}

	if destination == "s3" {
		cfg, err := s.s3Config(ctx)
		if err != nil {
			return "", 0, err
		}
		if !cfg.Configured() {
			return "", 0, errors.New("S3 is not configured (Settings → Backups)")
		}
		key := p.Name + "/" + filepath.Base(info.Path)
		if err := newS3(cfg).PutFile(ctx, key, info.Path); err != nil {
			return "", 0, err
		}
		return key, info.Size, nil
	}
	return info.Path, info.Size, nil
}

// Restore replaces the project directory with a backup's contents and
// redeploys nothing — the user chooses when to deploy the restored state.
func (s *Service) Restore(ctx context.Context, projectName string, backupID int64) error {
	b, err := s.q.GetBackup(ctx, db.GetBackupParams{Name: projectName, ID: backupID})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if b.Status != "done" {
		return errors.New("cannot restore an incomplete backup")
	}

	archivePath := b.Path
	if b.Destination == "s3" {
		cfg, err := s.s3Config(ctx)
		if err != nil || !cfg.Configured() {
			return errors.New("S3 is not configured")
		}
		dir, err := s.agent.FS().BackupsDir(ctx)
		if err != nil {
			return err
		}
		archivePath = filepath.Join(dir, "restore-"+filepath.Base(b.Path))
		if err := newS3(cfg).GetFile(ctx, b.Path, archivePath); err != nil {
			return err
		}
		defer s.agent.FS().RemoveArchive(ctx, archivePath)
	}

	if err := s.agent.FS().RestoreProject(ctx, projectName, archivePath); err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	s.bus.Publish(events.Event{Topic: "backup", Type: "backup.restored", Resource: projectName})
	return nil
}

func (s *Service) List(ctx context.Context, projectName string) ([]db.Backup, error) {
	if _, err := s.projects.Get(ctx, projectName); err != nil {
		return nil, err
	}
	return s.q.ListProjectBackups(ctx, projectName)
}

// dumpDatabase writes a native dump into the project dir when the project
// looks like a Windlass database template. Failure is non-fatal: the file
// archive still captures compose + env.
func (s *Service) dumpDatabase(ctx context.Context, project string) {
	env, err := s.projects.GetEnv(ctx, project)
	if err != nil {
		return
	}
	var cmd []string
	var outFile string
	switch {
	case env["POSTGRES_USER"] != "":
		cmd = []string{"sh", "-c", fmt.Sprintf("pg_dump -U %s %s", env["POSTGRES_USER"], env["POSTGRES_DB"])}
		outFile = "db_dump.sql"
	case env["MYSQL_ROOT_PASSWORD"] != "":
		cmd = []string{"sh", "-c", "mysqldump --all-databases -uroot -p\"$MYSQL_ROOT_PASSWORD\""}
		outFile = "db_dump.sql"
	default:
		return
	}

	containers, err := s.agent.Docker().ListContainers(ctx, agent.ContainerFilter{ComposeProject: project})
	if err != nil {
		return
	}
	var target string
	for _, c := range containers {
		if c.State == "running" {
			target = c.ID
			break
		}
	}
	if target == "" {
		return
	}

	dumpCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	sess, err := s.agent.Exec().Start(dumpCtx, agent.ExecReq{ContainerID: target, Cmd: cmd})
	if err != nil {
		s.logger.Warn("db dump exec", "project", project, "error", err)
		return
	}
	defer sess.Close()

	var out strings.Builder
	for chunk := range sess.Output() {
		out.Write(chunk)
		if out.Len() > 1<<30 {
			s.logger.Warn("db dump too large; skipping", "project", project)
			return
		}
	}
	if code, err := sess.Wait(); err != nil || code != 0 {
		s.logger.Warn("db dump failed", "project", project, "exit", code, "error", err)
		return
	}
	if err := s.agent.FS().WriteFile(ctx, project, outFile, []byte(out.String()), 0o600); err != nil {
		s.logger.Warn("db dump write", "project", project, "error", err)
	}
}

// ---------------------------------------------------------------------------
// Schedules

var intervalDurations = map[string]time.Duration{
	"hourly": time.Hour,
	"daily":  24 * time.Hour,
	"weekly": 7 * 24 * time.Hour,
}

func (s *Service) SetSchedule(ctx context.Context, projectName, interval, destination string, retention int64, enabled bool) error {
	p, err := s.projects.Get(ctx, projectName)
	if err != nil {
		return err
	}
	if _, ok := intervalDurations[interval]; !ok {
		return fmt.Errorf("interval must be hourly, daily, or weekly")
	}
	if destination != "s3" {
		destination = "local"
	}
	if retention <= 0 {
		retention = 7
	}
	en := int64(0)
	if enabled {
		en = 1
	}
	return s.q.UpsertBackupSchedule(ctx, db.UpsertBackupScheduleParams{
		ProjectID: p.ID, Interval: interval, Destination: destination,
		RetentionCount: retention, Enabled: en,
	})
}

func (s *Service) GetSchedule(ctx context.Context, projectName string) (db.BackupSchedule, error) {
	p, err := s.projects.Get(ctx, projectName)
	if err != nil {
		return db.BackupSchedule{}, err
	}
	sched, err := s.q.GetBackupSchedule(ctx, p.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.BackupSchedule{ProjectID: p.ID, Interval: "daily", Destination: "local", RetentionCount: 7}, nil
	}
	return sched, err
}

// RunScheduler checks due schedules once a minute until ctx ends.
func (s *Service) RunScheduler(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Service) tick(ctx context.Context) {
	due, err := s.q.ListDueSchedules(ctx)
	if err != nil {
		s.logger.Error("list schedules", "error", err)
		return
	}
	now := time.Now().UTC()
	for _, sched := range due {
		every := intervalDurations[sched.Interval]
		if sched.LastRunAt.Valid {
			last, err := time.Parse(time.RFC3339, sched.LastRunAt.String)
			if err == nil && now.Sub(last) < every {
				continue
			}
		}
		s.q.TouchScheduleRun(ctx, sched.ID)
		if _, err := s.Run(ctx, sched.ProjectName, "scheduled", sched.Destination); err != nil {
			continue // already logged
		}
		s.prune(ctx, sched.ProjectID, sched.RetentionCount)
	}
}

// prune keeps the newest N local backups per project.
func (s *Service) prune(ctx context.Context, projectID, keep int64) {
	rows, err := s.q.ListBackupsForPrune(ctx, projectID)
	if err != nil || int64(len(rows)) <= keep {
		return
	}
	for _, old := range rows[keep:] {
		if err := s.q.DeleteBackup(ctx, old.ID); err == nil {
			if err := s.agent.FS().RemoveArchive(ctx, old.Path); err != nil {
				s.logger.Warn("prune archive", "path", old.Path, "error", err)
			}
		}
	}
}
