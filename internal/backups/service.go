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
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/dbdetect"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store/db"
)

var (
	ErrNotFound         = errors.New("backup not found")
	ErrBackupInProgress = errors.New("a backup is already running for this project")
)

const s3SettingKey = "backups.s3"

type Service struct {
	q         *db.Queries
	agent     agent.Agent
	projects  *projects.Service
	box       *secrets.Box
	bus       *events.Bus
	logger    *slog.Logger
	runningMu sync.Mutex
	running   map[string]bool
}

func New(q *db.Queries, ag agent.Agent, proj *projects.Service, box *secrets.Box, bus *events.Bus, logger *slog.Logger) *Service {
	return &Service{q: q, agent: ag, projects: proj, box: box, bus: bus, logger: logger,
		running: map[string]bool{}}
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

// S3ConfigStatus returns the S3 destination with its keys removed, and
// whether one is configured.
func (s *Service) S3ConfigStatus(ctx context.Context) (S3Config, bool) {
	cfg, err := s.s3Config(ctx)
	if err != nil || !cfg.Configured() {
		return S3Config{}, false
	}
	cfg.AccessKey, cfg.SecretKey = "", ""
	return cfg, true
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
	if !s.start(projectName) {
		return db.Backup{}, ErrBackupInProgress
	}
	defer s.done(projectName)
	if destination != "s3" {
		destination = "local"
	}

	rec, err := s.q.CreateBackup(ctx, db.CreateBackupParams{
		ProjectID: p.ID, Kind: kind, Destination: destination,
	})
	if err != nil {
		return db.Backup{}, err
	}

	path, size, dump, err := s.execute(ctx, p, destination)
	status, errMsg := "done", ""
	if err != nil {
		status, errMsg = "failed", err.Error()
		s.logger.Error("backup failed", "project", projectName, "error", err)
	}
	dbDump := sql.NullString{String: dump.status, Valid: dump.status != ""}
	dbDumpErr := sql.NullString{String: dump.reason, Valid: dump.reason != ""}
	if ferr := s.q.FinishBackup(context.WithoutCancel(ctx), db.FinishBackupParams{
		Status: status, Path: path, Size: size,
		Error:  sql.NullString{String: errMsg, Valid: errMsg != ""},
		DbDump: dbDump, DbDumpError: dbDumpErr,
		ID: rec.ID,
	}); ferr != nil {
		return db.Backup{}, ferr
	}
	s.bus.Publish(events.Event{Topic: "backup", Type: "backup." + status, Resource: projectName})

	rec.Status = status
	rec.Path = path
	rec.Size = size
	rec.DbDump = dbDump
	rec.DbDumpError = dbDumpErr
	if err != nil {
		return rec, err
	}
	return rec, nil
}

func (s *Service) start(project string) bool {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	if s.running[project] {
		return false
	}
	s.running[project] = true
	return true
}

func (s *Service) done(project string) {
	s.runningMu.Lock()
	delete(s.running, project)
	s.runningMu.Unlock()
}

func (s *Service) execute(ctx context.Context, p db.Project, destination string) (string, int64, dumpResult, error) {
	// The dump is spooled to the backups dir and injected into the archive
	// without touching the live project. The archive walk skips the
	// project's own .windlass/backup, where a restore leaves the dump it
	// unpacked, so the archive holds this run's dump or none.
	dump := s.dumpDatabase(ctx, p.Name)
	extra := map[string]string{}
	if dump.path != "" {
		extra[dumpArchivePath] = dump.path
		defer func() {
			if err := s.agent.FS().RemoveArchive(context.WithoutCancel(ctx), dump.path); err != nil {
				s.logger.Warn("remove spooled database dump", "path", dump.path, "error", err)
			}
		}()
	}

	info, err := s.agent.FS().ArchiveProject(ctx, p.Name, extra)
	if err != nil {
		return "", 0, dump, fmt.Errorf("archive: %w", err)
	}

	if destination == "s3" {
		cfg, err := s.s3Config(ctx)
		if err != nil {
			return "", 0, dump, err
		}
		if !cfg.Configured() {
			return "", 0, dump, errors.New("S3 is not configured: an admin sets it in Settings, Backups")
		}
		key := p.Name + "/" + filepath.Base(info.Path)
		if err := newS3(cfg).PutFile(ctx, key, info.Path); err != nil {
			return "", 0, dump, err
		}
		return key, info.Size, dump, nil
	}
	return info.Path, info.Size, dump, nil
}

// Restore replaces the project directory with a backup's contents and
// redeploys nothing, the user chooses when to deploy the restored state.
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
		defer func() {
			if err := s.agent.FS().RemoveArchive(ctx, archivePath); err != nil {
				s.logger.Warn("remove downloaded backup", "path", archivePath, "error", err)
			}
		}()
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

// dumpArchivePath is where a backup archive holds the database dump.
const dumpArchivePath = ".windlass/backup/db_dump.sql"

// Values of backups.db_dump.
const (
	dumpIncluded = "included"
	dumpFailed   = "failed"
	dumpNone     = "none"
)

// dumpResult is what dumpDatabase did: its status, the reason a dump was
// not taken, and the spooled dump's path when it was.
type dumpResult struct {
	status, reason, path string
}

// dumpDatabase takes a native dump from the project's database container,
// if it has one, and spools it to the backups dir so its size is bounded by
// the disk rather than memory. The container is recognised by its image or
// service name, and the dump command runs with that container's own
// environment, so it finds the credentials whether they were written in
// compose.yaml, .env or another env_file. A failed dump does not fail the
// backup, since the file archive still holds compose and env, but the
// result says why so the record can.
func (s *Service) dumpDatabase(ctx context.Context, project string) dumpResult {
	failed := func(reason string, args ...any) dumpResult {
		reason = clip(reason, maxDumpReason)
		s.logger.Warn("backup has no database dump", append([]any{"project", project, "reason", reason}, args...)...)
		return dumpResult{status: dumpFailed, reason: reason}
	}
	containers, err := s.agent.Docker().ListContainers(ctx, agent.ContainerFilter{ComposeProject: project})
	if err != nil {
		return failed("could not list the project's containers: " + err.Error())
	}
	target, engine := databaseContainer(containers)
	if engine == "" {
		// No container, but the compose file may still name a database: a
		// project that is down, or whose database was removed. Its data is
		// then missing from the backup, which "none" would hide.
		if svc := s.databaseService(ctx, project); svc != "" {
			return failed(fmt.Sprintf("database service %s has no container", svc))
		}
		return dumpResult{status: dumpNone}
	}
	if target.State != "running" {
		return failed(fmt.Sprintf("database container %s is not running", target.Name), "engine", engine)
	}

	dumpCtx, cancel := context.WithTimeout(ctx, dumpTimeout)
	defer cancel()
	sess, err := s.agent.Exec().Start(dumpCtx, agent.ExecReq{
		ContainerID: target.ID,
		Cmd:         []string{"sh", "-c", dbdetect.DumpCommands[engine]},
	})
	if err != nil {
		return failed("could not start the dump: " + err.Error())
	}
	defer sess.Close()

	// The session's output feeds a pipe the spool reads. If the spool stops
	// early, the rest of the output is drained so the session can end.
	pr, pw := io.Pipe()
	go func() {
		for chunk := range sess.Output() {
			_, _ = pw.Write(chunk)
		}
		_ = pw.Close()
	}()
	path, size, spoolErr := s.agent.FS().SpoolFile(dumpCtx, pr)
	_ = pr.CloseWithError(errors.New("spool ended"))
	if spoolErr != nil {
		sess.Close()
	}
	code, waitErr := sess.Wait()
	remove := func() {
		if path == "" {
			return
		}
		if err := s.agent.FS().RemoveArchive(context.WithoutCancel(ctx), path); err != nil {
			s.logger.Warn("remove spooled database dump", "path", path, "error", err)
		}
	}
	switch {
	case ctx.Err() != nil:
		remove()
		return failed("the backup was cancelled during the dump")
	case dumpCtx.Err() != nil:
		remove()
		return failed(fmt.Sprintf("the dump took longer than %s", dumpTimeout))
	case spoolErr != nil:
		remove()
		return failed("could not write the dump to disk: " + spoolErr.Error())
	case waitErr != nil:
		remove()
		return failed("the dump failed: "+waitErr.Error(), "exit", code)
	case code != 0:
		remove()
		return failed(fmt.Sprintf("the dump exited with code %d", code))
	case size == 0:
		remove()
		return failed("the dump produced no output")
	}
	return dumpResult{status: dumpIncluded, path: path}
}

// maxDumpReason caps the stored reason; the stderr tail a failed dump
// returns can run to 4 KiB.
const maxDumpReason = 1000

// clip shortens s to at most n bytes without splitting a character.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// dumpTimeout bounds one database dump.
const dumpTimeout = 10 * time.Minute

// databaseService returns the compose service that looks like a database,
// by the same rules as the container match, or "" when there is none or the
// compose file cannot be read.
func (s *Service) databaseService(ctx context.Context, project string) string {
	cfg, err := s.agent.Compose().Config(ctx, project)
	if err != nil {
		s.logger.Warn("backup could not read compose config", "project", project, "error", err)
		return ""
	}
	var services []agent.Container
	for name, svc := range cfg.Services {
		services = append(services, agent.Container{ComposeService: name, Image: svc.Image})
	}
	best, engine := dbdetect.Best(services)
	if engine == "" {
		return ""
	}
	return best.ComposeService
}

// databaseContainer returns the project's database container and its engine.
func databaseContainer(containers []agent.Container) (agent.Container, string) {
	return dbdetect.Best(containers)
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
		// Without the touch the schedule stays due and would run every tick.
		if err := s.q.TouchScheduleRun(ctx, sched.ID); err != nil {
			s.logger.Error("record schedule run", "project", sched.ProjectName, "error", err)
			continue
		}
		if _, err := s.Run(ctx, sched.ProjectName, "scheduled", sched.Destination); err != nil {
			continue // already logged
		}
		s.prune(ctx, sched.ProjectID, sched.Destination, sched.RetentionCount)
	}
}

// prune keeps the newest N completed backups for one destination. Data is
// deleted before its record so a transient storage failure remains retryable.
func (s *Service) prune(ctx context.Context, projectID int64, destination string, keep int64) {
	rows, err := s.q.ListBackupsForPrune(ctx, db.ListBackupsForPruneParams{
		ProjectID: projectID, Destination: destination,
	})
	if err != nil || int64(len(rows)) <= keep {
		return
	}
	var remote *s3Client
	if destination == "s3" {
		cfg, err := s.s3Config(ctx)
		if err != nil || !cfg.Configured() {
			s.logger.Warn("prune S3 backups", "error", err)
			return
		}
		remote = newS3(cfg)
	}
	for _, old := range rows[keep:] {
		if remote != nil {
			if err := remote.DeleteObject(ctx, old.Path); err != nil {
				s.logger.Warn("prune S3 object", "path", old.Path, "error", err)
				continue
			}
		} else if err := s.agent.FS().RemoveArchive(ctx, old.Path); err != nil {
			s.logger.Warn("prune archive", "path", old.Path, "error", err)
			continue
		}
		if err := s.q.DeleteBackup(ctx, old.ID); err != nil {
			s.logger.Warn("prune backup record", "backup", old.ID, "error", err)
		}
	}
}
