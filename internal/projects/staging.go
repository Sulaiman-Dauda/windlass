package projects

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/store/db"
)

// StagingConfig links a staging project to the live project it copies. It
// is written only into the staging project's manifest, so deleting the
// staging directory is all it takes to undo the link.
type StagingConfig struct {
	Of          string `json:"of"`
	AutoPromote bool   `json:"auto_promote"`
	// Rewrites are the hostnames replaced when staging was made, so a
	// compose file can be compared with live's, and copied back, without
	// the domains showing as a difference.
	Rewrites []HostnameRewrite `json:"rewrites,omitempty"`
	// Protect puts a password on staging's domains. Only the bcrypt hash is
	// kept, here in the project's files, so Caddy keeps asking for it if
	// Windlass stops.
	Protect *StagingProtect `json:"protect,omitempty"`
}

// StagingProtect is the HTTP basic auth account on a staging's domains.
type StagingProtect struct {
	User string `json:"user"`
	Hash string `json:"hash"` // bcrypt
}

// StagingUser is the user name a staging password goes with.
const StagingUser = "staging"

// stagingHashCost is the bcrypt cost of a staging password. Caddy checks
// the hash on a visitor's first request and caches the result, so the cost
// is paid once per visitor, not per request.
const stagingHashCost = 12

// MinStagingPassword is the shortest password a person may choose.
const MinStagingPassword = 12

// GenerateStagingPassword returns a random password of 20 letters and
// digits, about 119 bits.
func GenerateStagingPassword() (string, error) {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 20)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), nil
}

// NewStagingProtect hashes password for a staging's domains.
func NewStagingProtect(password string) (*StagingProtect, error) {
	if len(password) < MinStagingPassword {
		return nil, fmt.Errorf("%w: the password needs at least %d characters", ErrWeakPassword, MinStagingPassword)
	}
	if len(password) > 72 {
		return nil, fmt.Errorf("%w: the password can have at most 72 characters", ErrWeakPassword)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), stagingHashCost)
	if err != nil {
		return nil, err
	}
	return &StagingProtect{User: StagingUser, Hash: string(hash)}, nil
}

// ErrWeakPassword refuses a staging password that is too short or long.
var ErrWeakPassword = errors.New("invalid staging password")

// StagingSuffix names a project's staging: "app" has "app-staging".
const StagingSuffix = "-staging"

var (
	// ErrHasStaging refuses to delete a live project while its staging
	// exists, which would leave the staging promoting into nothing.
	ErrHasStaging = errors.New("project has a staging")
	ErrNotStaging = errors.New("project is not a staging")
	// ErrSharedVolume refuses to delete a staging whose compose file names
	// a volume that may be live's, which docker compose down -v would remove.
	ErrSharedVolume = errors.New("delete refused")
)

// StagingName is the fixed name of live's staging project.
func StagingName(live string) string { return live + StagingSuffix }

// CanHaveStaging reports whether live's staging name is a valid project
// name, which caps live names at 55 characters.
func CanHaveStaging(live string) bool {
	return agent.ValidProjectName(live) && agent.ValidProjectName(StagingName(live)) &&
		!strings.HasSuffix(live, StagingSuffix)
}

// stagingManifest reads name's manifest and returns its staging link when
// it is a well-formed staging of live: named live's staging, and pointing
// back at live.
func (s *Service) stagingManifest(ctx context.Context, name string) (Manifest, bool, error) {
	m, err := s.readManifest(ctx, name)
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, false, err
	}
	ok := m.Staging != nil && m.Staging.Of != "" && name == StagingName(m.Staging.Of)
	return m, ok, nil
}

// StagingFor returns live's staging project, when one exists. It reads the
// staging directory's manifest, not the index, so it agrees with the files
// even before a reconcile.
func (s *Service) StagingFor(ctx context.Context, live string) (db.Project, bool, error) {
	if !CanHaveStaging(live) {
		return db.Project{}, false, nil
	}
	name := StagingName(live)
	names, err := s.agent.FS().DiscoverProjects(ctx)
	if err != nil {
		return db.Project{}, false, err
	}
	if !containsProject(names, name) {
		return db.Project{}, false, nil
	}
	m, ok, err := s.stagingManifest(ctx, name)
	if err != nil || !ok || m.Staging.Of != live {
		return db.Project{}, false, err
	}
	p, err := s.Get(ctx, name)
	if err != nil {
		return db.Project{}, false, err
	}
	return p, true, nil
}

// LiveFor returns the live project staging copies and its staging settings.
// ok is false when staging is not a staging, or its live project is gone,
// in which case it behaves as a project of its own.
func (s *Service) LiveFor(ctx context.Context, staging string) (db.Project, StagingConfig, bool, error) {
	m, ok, err := s.stagingManifest(ctx, staging)
	if err != nil || !ok {
		return db.Project{}, StagingConfig{}, false, err
	}
	live, err := s.Get(ctx, m.Staging.Of)
	if errors.Is(err, ErrNotFound) {
		return db.Project{}, *m.Staging, false, nil
	}
	if err != nil {
		return db.Project{}, StagingConfig{}, false, err
	}
	return live, *m.Staging, true, nil
}

// SetAutoPromote turns automatic promotion on or off for a staging.
func (s *Service) SetAutoPromote(ctx context.Context, staging string, on bool) error {
	m, ok, err := s.stagingManifest(ctx, staging)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotStaging
	}
	m.Staging.AutoPromote = on
	if err := s.writeManifest(ctx, staging, m); err != nil {
		return err
	}
	s.bus.Publish(events.Event{Topic: "project", Type: "project.staging_changed", Resource: staging})
	return nil
}

// SetStagingProtect sets or, with nil, removes the password on a staging's
// domains. The proxy re-syncs on the event.
func (s *Service) SetStagingProtect(ctx context.Context, staging string, p *StagingProtect) error {
	m, ok, err := s.stagingManifest(ctx, staging)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotStaging
	}
	m.Staging.Protect = p
	if err := s.writeManifest(ctx, staging, m); err != nil {
		return err
	}
	s.bus.Publish(events.Event{Topic: "project", Type: "project.staging_changed", Resource: staging})
	return nil
}

// StagingProtection returns the password account on a staging's domains,
// nil when it has none. A project that is not a staging has none.
func (s *Service) StagingProtection(ctx context.Context, name string) (*StagingProtect, error) {
	// Any protect in the manifest counts, even when the directory name and
	// staging.of disagree, so a renamed copy is never served open.
	m, err := s.readManifest(ctx, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil || m.Staging == nil {
		return nil, err
	}
	return m.Staging.Protect, nil
}

// Domains returns the domains the project's manifest routes.
func (s *Service) Domains(ctx context.Context, project string) ([]DomainConfig, error) {
	m, err := s.readManifest(ctx, project)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return m.Domains, nil
}

// StagingSkip names the top-level entries a staging copy leaves out of
// live's directory: the Git checkout and its metadata, which staging clones
// for itself, Windlass's own state and manifest, the environment file, which
// is written from the reviewed values, and a rollback's digest pins.
var StagingSkip = []string{"src", ".git", ".windlass", manifestFile, ".env", "compose.rollback.yaml"}

// CopyReq describes the staging project CreateStagingCopy makes.
type CopyReq struct {
	Live     string
	Branch   string
	Domains  []DomainConfig
	Rewrites []HostnameRewrite
	// Env is the staging environment, already reviewed.
	Env         map[string]string
	AutoPromote bool
	// Protect is the password on staging's domains, nil for none.
	Protect *StagingProtect
}

// CopyResult reports what the copy changed, for the person to check.
type CopyResult struct {
	Project db.Project
	// Rewritten maps each compose file to the hostnames replaced in it.
	Rewritten map[string]int
	// Skipped lists entries of live's directory that could not be copied.
	Skipped []string
}

// CreateStagingCopy makes live's staging project: a copy of live's files
// without the entries in StagingSkip, its compose files with live's
// hostnames replaced, its own .env and a manifest linking it to live. On a
// failure it removes what it made. It does not deploy.
func (s *Service) CreateStagingCopy(ctx context.Context, req CopyReq) (CopyResult, error) {
	if !CanHaveStaging(req.Live) {
		return CopyResult{}, fmt.Errorf("%q cannot have a staging: its name must be 55 characters or fewer and not end in %s",
			req.Live, StagingSuffix)
	}
	live, err := s.Get(ctx, req.Live)
	if err != nil {
		return CopyResult{}, err
	}
	liveManifest, err := s.readManifest(ctx, req.Live)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return CopyResult{}, err
	}
	name := StagingName(req.Live)
	names, err := s.agent.FS().DiscoverProjects(ctx)
	if err != nil {
		return CopyResult{}, err
	}
	if containsProject(names, name) {
		return CopyResult{}, ErrAlreadyExists
	}
	for key := range req.Env {
		if !validEnvKey(key) {
			return CopyResult{}, fmt.Errorf("invalid env key %q", key)
		}
	}

	if _, err := s.agent.FS().EnsureProject(ctx, name); err != nil {
		return CopyResult{}, fmt.Errorf("create %s: %w", name, err)
	}
	result, err := s.fillStagingCopy(ctx, name, live, liveManifest, req)
	if err != nil {
		s.DiscardStaging(ctx, name)
		return CopyResult{}, err
	}
	s.bus.Publish(events.Event{Topic: "project", Type: "project.created", Resource: name})
	return result, nil
}

// DiscardStaging removes a staging that was made but never deployed: its
// directory and its index row, with the domains and history that hang off
// it. It is the undo for a create that failed part way, so it logs rather
// than returns what it could not remove.
func (s *Service) DiscardStaging(ctx context.Context, name string) {
	ctx = context.WithoutCancel(ctx)
	if err := s.agent.FS().RemoveProject(ctx, name); err != nil {
		s.logger.Error("remove half-made staging", "project", name, "error", err)
	}
	if p, err := s.q.GetProjectByName(ctx, name); err == nil {
		if err := s.q.DeleteProject(ctx, p.ID); err != nil {
			s.logger.Error("remove half-made staging index", "project", name, "error", err)
		}
	}
	s.bus.Publish(events.Event{Topic: "project", Type: "project.deleted", Resource: name})
}

func (s *Service) fillStagingCopy(ctx context.Context, name string, live db.Project, liveManifest Manifest, req CopyReq) (CopyResult, error) {
	result := CopyResult{Rewritten: map[string]int{}}
	skipped, err := s.agent.FS().CopyProject(ctx, req.Live, name, StagingSkip)
	if err != nil {
		return result, fmt.Errorf("copy %s: %w", req.Live, err)
	}
	result.Skipped = skipped

	files, err := s.agent.FS().List(ctx, name, ".")
	if err != nil {
		return result, err
	}
	for _, f := range files {
		if f.IsDir || !IsComposeFile(f.Name) {
			continue
		}
		data, err := s.agent.FS().ReadFile(ctx, name, f.Name)
		if err != nil {
			return result, err
		}
		text, n := RewriteHostnames(string(data), req.Rewrites)
		if n == 0 {
			continue
		}
		if err := s.agent.FS().WriteFile(ctx, name, f.Name, []byte(text), 0o644); err != nil {
			return result, err
		}
		result.Rewritten[f.Name] = n
	}

	env := make(map[string]string, len(req.Env)+1)
	for k, v := range req.Env {
		env[k] = v
	}
	env[EnvironmentKey] = "staging"
	if err := s.writeEnvFile(ctx, name, env); err != nil {
		return result, fmt.Errorf("write .env: %w", err)
	}

	branch := req.Branch
	if branch == "" {
		branch = liveManifest.GitBranch
	}
	m := Manifest{Source: liveManifest.Source, GitRepo: liveManifest.GitRepo, AutoDeploy: liveManifest.AutoDeploy,
		Domains: req.Domains, Staging: &StagingConfig{Of: req.Live, AutoPromote: req.AutoPromote, Rewrites: req.Rewrites,
			Protect: req.Protect}}
	if m.GitRepo != "" {
		m.GitBranch = branch
	}
	if m.Source == "" {
		m.Source = live.Source
	}
	if err := s.writeManifest(ctx, name, m); err != nil {
		return result, fmt.Errorf("write manifest: %w", err)
	}
	if err := s.Reconcile(ctx); err != nil {
		return result, err
	}
	p, err := s.q.GetProjectByName(ctx, name)
	if err != nil {
		return result, err
	}
	result.Project = p
	return result, nil
}

// IsComposeFile reports whether name is a top-level compose file: compose
// and docker-compose files, overrides included.
func IsComposeFile(name string) bool {
	lower := strings.ToLower(name)
	if !strings.HasSuffix(lower, ".yaml") && !strings.HasSuffix(lower, ".yml") {
		return false
	}
	return strings.HasPrefix(lower, "compose") || strings.HasPrefix(lower, "docker-compose")
}

// setStagingOf records the manifest's staging link in the index.
func (s *Service) setStagingOf(ctx context.Context, p db.Project, m Manifest) error {
	var of sql.NullString
	if m.Staging != nil && m.Staging.Of != "" {
		of = sql.NullString{String: m.Staging.Of, Valid: true}
	}
	if of == p.StagingOf {
		return nil
	}
	return s.q.SetProjectStagingOf(ctx, db.SetProjectStagingOfParams{StagingOf: of, ID: p.ID})
}

// EnvironmentKey is set to "staging" in a staging's .env, so an app can
// guard itself with ${WINDLASS_ENVIRONMENT:-production}.
const EnvironmentKey = "WINDLASS_ENVIRONMENT"
