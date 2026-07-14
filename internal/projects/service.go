// Package projects manages compose project lifecycles. A project is a plain
// directory (compose.yaml + .env) the user can always edit by hand; this
// service is the panel's view onto it, plus encrypted env storage.
package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store/db"
)

var (
	ErrNotFound      = errors.New("project not found")
	ErrAlreadyExists = errors.New("project already exists")
)

const starterCompose = `# This file is yours. Windlass deploys exactly what is written here —
# edit it in the UI or directly on the server; both stay in sync.
services:
  web:
    image: nginx:alpine
    restart: unless-stopped
    # ports are optional when a Windlass domain routes to this service.
`

const manifestFile = ".windlass.json"

// Manifest is application configuration that is not part of the Compose
// specification. It lives beside compose.yaml so SQLite can be rebuilt.
type Manifest struct {
	Version    int            `json:"version"`
	Source     string         `json:"source"`
	GitRepo    string         `json:"git_repo,omitempty"`
	GitBranch  string         `json:"git_branch,omitempty"`
	AutoDeploy bool           `json:"auto_deploy"`
	Domains    []DomainConfig `json:"domains"`
}

type DomainConfig struct {
	Hostname      string `json:"hostname"`
	Service       string `json:"service"`
	ContainerPort int64  `json:"container_port"`
}

type Service struct {
	q           *db.Queries
	agent       agent.Agent
	box         *secrets.Box
	bus         *events.Bus
	logger      *slog.Logger
	reconcileMu sync.Mutex
}

func New(q *db.Queries, ag agent.Agent, box *secrets.Box, bus *events.Bus, logger *slog.Logger) *Service {
	return &Service{q: q, agent: ag, box: box, bus: bus, logger: logger}
}

type CreateReq struct {
	Name    string
	Source  string // "manual" | "git" | "template"; defaults to manual
	GitRepo string
	Branch  string
	// Compose overrides the starter compose.yaml (used by templates).
	Compose string
}

func (s *Service) Create(ctx context.Context, req CreateReq) (db.Project, error) {
	if req.Source == "" {
		req.Source = "manual"
	}
	if !agent.ValidProjectName(req.Name) {
		return db.Project{}, fmt.Errorf("invalid project name %q: use lowercase letters, digits, - and _", req.Name)
	}

	_, existingErr := s.q.GetProjectByName(ctx, req.Name)
	if existingErr != nil && !errors.Is(existingErr, sql.ErrNoRows) {
		return db.Project{}, existingErr
	}
	discovered, err := s.agent.FS().DiscoverProjects(ctx)
	if err != nil {
		return db.Project{}, err
	}
	if containsProject(discovered, req.Name) {
		return db.Project{}, ErrAlreadyExists
	}

	// Create the directory first: the agent validates the name, and a
	// failure must not leave a metadata row behind.
	if _, err := s.agent.FS().EnsureProject(ctx, req.Name); err != nil {
		return db.Project{}, fmt.Errorf("create project dir: %w", err)
	}

	compose := req.Compose
	if compose == "" {
		compose = starterCompose
	}
	if err := s.agent.FS().WriteFile(ctx, req.Name, "compose.yaml", []byte(compose), 0o644); err != nil {
		return db.Project{}, fmt.Errorf("write compose.yaml: %w", err)
	}
	manifest := Manifest{Version: 1, Source: req.Source, GitRepo: req.GitRepo,
		GitBranch: req.Branch, Domains: []DomainConfig{}}
	if err := s.writeManifest(ctx, req.Name, manifest); err != nil {
		return db.Project{}, fmt.Errorf("write project manifest: %w", err)
	}

	params := db.CreateProjectParams{
		Name:       req.Name,
		Source:     req.Source,
		GitRepo:    sql.NullString{String: req.GitRepo, Valid: req.GitRepo != ""},
		GitBranch:  sql.NullString{String: req.Branch, Valid: req.Branch != ""},
		AutoDeploy: 0,
	}
	var project db.Project
	if existingErr == nil {
		project, err = s.q.EnsureProjectIndex(ctx, params)
	} else {
		project, err = s.q.CreateProject(ctx, params)
	}
	if err != nil {
		return db.Project{}, err
	}

	s.bus.Publish(events.Event{Topic: "project", Type: "project.created", Resource: project.Name})
	return project, nil
}

func (s *Service) Get(ctx context.Context, name string) (db.Project, error) {
	names, discoverErr := s.agent.FS().DiscoverProjects(ctx)
	if discoverErr != nil {
		return db.Project{}, discoverErr
	}
	if !containsProject(names, name) {
		return db.Project{}, ErrNotFound
	}
	p, err := s.q.GetProjectByName(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		if reconcileErr := s.Reconcile(ctx); reconcileErr != nil {
			return db.Project{}, reconcileErr
		}
		p, err = s.q.GetProjectByName(ctx, name)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return db.Project{}, ErrNotFound
	}
	return p, err
}

func containsProject(names []string, target string) bool {
	for _, name := range names {
		if name == target {
			return true
		}
	}
	return false
}

func (s *Service) List(ctx context.Context) ([]db.Project, error) {
	if err := s.Reconcile(ctx); err != nil {
		return nil, err
	}
	names, err := s.agent.FS().DiscoverProjects(ctx)
	if err != nil {
		return nil, err
	}
	return s.q.ListProjectIndexes(ctx, names)
}

// Reconcile rebuilds the SQLite project/domain index from project
// directories and their manifests. It is safe at startup and on demand.
func (s *Service) Reconcile(ctx context.Context) error {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()

	names, err := s.agent.FS().DiscoverProjects(ctx)
	if err != nil {
		return fmt.Errorf("scan projects directory: %w", err)
	}
	for _, name := range names {
		manifest, err := s.readManifest(ctx, name)
		if errors.Is(err, fs.ErrNotExist) {
			manifest = Manifest{Version: 1, Source: "manual", Domains: []DomainConfig{}}
			if existing, getErr := s.q.GetProjectByName(ctx, name); getErr == nil {
				manifest.Source = existing.Source
				manifest.GitRepo = existing.GitRepo.String
				manifest.GitBranch = existing.GitBranch.String
				manifest.AutoDeploy = existing.AutoDeploy != 0
				if domains, domainErr := s.q.ListProjectDomains(ctx, existing.ID); domainErr == nil {
					for _, d := range domains {
						manifest.Domains = append(manifest.Domains, DomainConfig{
							Hostname: d.Hostname, Service: d.Service, ContainerPort: d.ContainerPort,
						})
					}
				}
			}
			if err := s.writeManifest(ctx, name, manifest); err != nil {
				return err
			}
		} else if err != nil {
			return fmt.Errorf("read %s/%s: %w", name, manifestFile, err)
		}
		if manifest.Source == "" {
			manifest.Source = "manual"
		}
		project, err := s.q.EnsureProjectIndex(ctx, db.CreateProjectParams{
			Name: name, Source: manifest.Source,
			GitRepo:    sql.NullString{String: manifest.GitRepo, Valid: manifest.GitRepo != ""},
			GitBranch:  sql.NullString{String: manifest.GitBranch, Valid: manifest.GitBranch != ""},
			AutoDeploy: boolInt(manifest.AutoDeploy),
		})
		if err != nil {
			return fmt.Errorf("index project %s: %w", name, err)
		}
		domains := make([]db.CreateDomainParams, 0, len(manifest.Domains))
		for _, d := range manifest.Domains {
			domains = append(domains, db.CreateDomainParams{ProjectID: project.ID,
				Hostname: d.Hostname, Service: d.Service, ContainerPort: d.ContainerPort})
		}
		if err := s.q.ReplaceProjectDomains(ctx, project.ID, domains); err != nil {
			return fmt.Errorf("index domains for %s: %w", name, err)
		}
	}
	// A missing mount or temporarily unavailable disk must never cascade-delete
	// deployment history, backups, or other platform state. List() hides stale
	// indexes; if the directory returns, its original history is reattached.
	return nil
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func (s *Service) readManifest(ctx context.Context, project string) (Manifest, error) {
	data, err := s.agent.FS().ReadFile(ctx, project, manifestFile)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	if manifest.Version != 1 {
		return Manifest{}, fmt.Errorf("unsupported manifest version %d", manifest.Version)
	}
	if manifest.Domains == nil {
		manifest.Domains = []DomainConfig{}
	}
	return manifest, nil
}

func (s *Service) writeManifest(ctx context.Context, project string, manifest Manifest) error {
	manifest.Version = 1
	if manifest.Source == "" {
		manifest.Source = "manual"
	}
	if manifest.Domains == nil {
		manifest.Domains = []DomainConfig{}
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return s.agent.FS().WriteFile(ctx, project, manifestFile, data, 0o600)
}

func (s *Service) SetGitMetadata(ctx context.Context, project, repo, branch string, autoDeploy bool) error {
	manifest, err := s.readManifest(ctx, project)
	if err != nil {
		return err
	}
	manifest.Source, manifest.GitRepo, manifest.GitBranch = "git", repo, branch
	manifest.AutoDeploy = autoDeploy
	return s.writeManifest(ctx, project, manifest)
}

func (s *Service) SetDomains(ctx context.Context, project string, domains []DomainConfig) error {
	manifest, err := s.readManifest(ctx, project)
	if err != nil {
		return err
	}
	manifest.Domains = append([]DomainConfig(nil), domains...)
	return s.writeManifest(ctx, project, manifest)
}

// Delete stops the project's containers (best effort until compose lands),
// removes the directory, and deletes metadata.
func (s *Service) Delete(ctx context.Context, name string) error {
	p, err := s.Get(ctx, name)
	if err != nil {
		return err
	}

	if err := s.agent.Compose().Down(ctx, name, false, nil); err != nil {
		// Compose may be unavailable (dev) or the project never deployed.
		s.logger.Warn("compose down during delete", "project", name, "error", err)
	}
	if err := s.agent.FS().RemoveProject(ctx, name); err != nil {
		return fmt.Errorf("remove project dir: %w", err)
	}
	if err := s.q.DeleteProject(ctx, p.ID); err != nil {
		return err
	}

	s.bus.Publish(events.Event{Topic: "project", Type: "project.deleted", Resource: name})
	return nil
}

// ---------------------------------------------------------------------------
// Files

// editableFile reports whether the panel exposes rel for editing. The server
// owner can edit anything over SSH; the web surface stays deliberately small
// (compose files, env examples, static config) and never binary blobs.
func editableFile(rel string) bool {
	rel = strings.ToLower(strings.TrimPrefix(rel, "./"))
	switch {
	case strings.HasSuffix(rel, ".yaml"), strings.HasSuffix(rel, ".yml"),
		strings.HasSuffix(rel, ".env"), rel == ".env",
		strings.HasSuffix(rel, ".conf"), strings.HasSuffix(rel, ".json"),
		strings.HasSuffix(rel, ".toml"), strings.HasSuffix(rel, ".txt"),
		strings.HasSuffix(rel, ".md"), rel == "caddyfile":
		return true
	}
	return false
}

func (s *Service) ReadFile(ctx context.Context, project, rel string) ([]byte, error) {
	if _, err := s.Get(ctx, project); err != nil {
		return nil, err
	}
	if !editableFile(rel) {
		return nil, fmt.Errorf("file type not editable via the panel: %s", rel)
	}
	return s.agent.FS().ReadFile(ctx, project, rel)
}

func (s *Service) WriteFile(ctx context.Context, project, rel string, content []byte) error {
	if _, err := s.Get(ctx, project); err != nil {
		return err
	}
	if !editableFile(rel) {
		return fmt.Errorf("file type not editable via the panel: %s", rel)
	}
	mode := fs.FileMode(0o644)
	if strings.ToLower(rel) == ".env" || strings.HasSuffix(strings.ToLower(rel), ".env") {
		mode = 0o600
	}
	if err := s.agent.FS().WriteFile(ctx, project, rel, content, mode); err != nil {
		return err
	}
	s.bus.Publish(events.Event{Topic: "project", Type: "project.file_changed", Resource: project,
		Data: map[string]string{"path": rel}})
	return nil
}

func (s *Service) ListFiles(ctx context.Context, project, rel string) ([]agent.FileInfo, error) {
	if _, err := s.Get(ctx, project); err != nil {
		return nil, err
	}
	return s.agent.FS().List(ctx, project, rel)
}

// ---------------------------------------------------------------------------
// Environment variables. The standard .env file is authoritative; encrypted
// SQLite rows are a rebuildable cache for platform features.

func (s *Service) GetEnv(ctx context.Context, project string) (map[string]string, error) {
	p, err := s.Get(ctx, project)
	if err != nil {
		return nil, err
	}
	data, fileErr := s.agent.FS().ReadFile(ctx, project, ".env")
	if fileErr == nil {
		vars, err := parseEnvFile(string(data))
		if err != nil {
			return nil, err
		}
		if err := s.cacheEnv(ctx, p.ID, vars); err != nil {
			s.logger.Warn("refresh env cache", "project", project, "error", err)
		}
		return vars, nil
	}
	if !errors.Is(fileErr, fs.ErrNotExist) {
		return nil, fileErr
	}

	// One-time migration for installations that predate filesystem-owned env.
	rows, err := s.q.ListEnvVars(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		plain, err := s.box.Decrypt(row.ValueEnc)
		if err != nil {
			return nil, fmt.Errorf("decrypt %s: %w", row.Key, err)
		}
		out[row.Key] = string(plain)
	}
	if err := s.writeEnvFile(ctx, project, out); err != nil {
		return nil, err
	}
	return out, nil
}

// SetEnv replaces the project's environment with vars.
func (s *Service) SetEnv(ctx context.Context, project string, vars map[string]string) error {
	p, err := s.Get(ctx, project)
	if err != nil {
		return err
	}
	for key := range vars {
		if !validEnvKey(key) {
			return fmt.Errorf("invalid env key %q", key)
		}
	}

	if err := s.writeEnvFile(ctx, project, vars); err != nil {
		return err
	}
	if err := s.cacheEnv(ctx, p.ID, vars); err != nil {
		s.logger.Warn("refresh env cache", "project", project, "error", err)
	}

	s.bus.Publish(events.Event{Topic: "project", Type: "project.env_changed", Resource: project})
	return nil
}

func (s *Service) cacheEnv(ctx context.Context, projectID int64, vars map[string]string) error {
	if err := s.q.DeleteProjectEnvVars(ctx, projectID); err != nil {
		return err
	}
	for key, value := range vars {
		enc, err := s.box.Encrypt([]byte(value))
		if err != nil {
			return err
		}
		if err := s.q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{
			ProjectID: projectID, Key: key, ValueEnc: enc,
		}); err != nil {
			return err
		}
	}
	return nil
}

// RenderEnvFile remains the deployment pipeline boundary, but no longer
// rewrites an existing .env. Reading it imports hand edits into the cache.
func (s *Service) RenderEnvFile(ctx context.Context, project string) error {
	_, err := s.GetEnv(ctx, project)
	return err
}

func (s *Service) writeEnvFile(ctx context.Context, project string, vars map[string]string) error {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("# Managed as a standard dotenv file. Windlass reads this file before every deploy.\n")
	b.WriteString("# Edit it here, in the panel, or over SSH.\n")
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(quoteEnvValue(vars[k]))
		b.WriteByte('\n')
	}
	return s.agent.FS().WriteFile(ctx, project, ".env", []byte(b.String()), 0o600)
}

func parseEnvFile(input string) (map[string]string, error) {
	vars := map[string]string{}
	for lineNo, source := range strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(source)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		idx := strings.IndexByte(line, '=')
		if idx < 1 {
			return nil, fmt.Errorf(".env line %d: expected KEY=value", lineNo+1)
		}
		key, value := strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:])
		if !validEnvKey(key) {
			return nil, fmt.Errorf(".env line %d: invalid key %q", lineNo+1, key)
		}
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'')) {
			quote := value[0]
			value = value[1 : len(value)-1]
			if quote == '"' {
				value = strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t",
					`\"`, `"`, `\\`, `\`, `$$`, `$`).Replace(value)
			}
		}
		vars[key] = value
	}
	return vars, nil
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		switch {
		case r >= 'A' && r <= 'Z', r == '_':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// ValidateEnv rejects only unmistakable placeholder secrets. Application-
// specific completeness remains the application's responsibility.
func (s *Service) ValidateEnv(ctx context.Context, project string) ([]string, error) {
	vars, err := s.GetEnv(ctx, project)
	if err != nil {
		return nil, err
	}
	var warnings []string
	for key, value := range vars {
		upper := strings.ToUpper(key)
		if !strings.Contains(upper, "PASSWORD") && !strings.Contains(upper, "SECRET") &&
			!strings.Contains(upper, "TOKEN") && !strings.HasSuffix(upper, "_KEY") {
			continue
		}
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "changeme" || normalized == "password" || normalized == "secret" ||
			strings.HasPrefix(normalized, "replace-with") || strings.HasPrefix(normalized, "replace_me") {
			return nil, fmt.Errorf("%s still contains an insecure placeholder", key)
		}
		if len(value) > 0 && len(value) < 12 {
			warnings = append(warnings, key+" is shorter than 12 characters")
		}
	}
	return warnings, nil
}

// quoteEnvValue quotes values that need it for dotenv parsing.
func quoteEnvValue(v string) string {
	if v == "" || strings.ContainsAny(v, " \t\n\"'#$") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, `$`, `$$`).Replace(v) + `"`
	}
	return v
}
