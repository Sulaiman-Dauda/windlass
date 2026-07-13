// Package projects manages compose project lifecycles. A project is a plain
// directory (compose.yaml + .env) the user can always edit by hand; this
// service is the panel's view onto it, plus encrypted env storage.
package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

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

type Service struct {
	q      *db.Queries
	agent  agent.Agent
	box    *secrets.Box
	bus    *events.Bus
	logger *slog.Logger
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

	if _, err := s.q.GetProjectByName(ctx, req.Name); err == nil {
		return db.Project{}, ErrAlreadyExists
	} else if !errors.Is(err, sql.ErrNoRows) {
		return db.Project{}, err
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

	project, err := s.q.CreateProject(ctx, db.CreateProjectParams{
		Name:       req.Name,
		Source:     req.Source,
		GitRepo:    sql.NullString{String: req.GitRepo, Valid: req.GitRepo != ""},
		GitBranch:  sql.NullString{String: req.Branch, Valid: req.Branch != ""},
		AutoDeploy: 0,
	})
	if err != nil {
		return db.Project{}, err
	}

	s.bus.Publish(events.Event{Topic: "project", Type: "project.created", Resource: project.Name})
	return project, nil
}

func (s *Service) Get(ctx context.Context, name string) (db.Project, error) {
	p, err := s.q.GetProjectByName(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Project{}, ErrNotFound
	}
	return p, err
}

func (s *Service) List(ctx context.Context) ([]db.Project, error) {
	return s.q.ListProjects(ctx)
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
	if err := s.agent.FS().WriteFile(ctx, project, rel, content, 0o644); err != nil {
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
// Environment variables (encrypted at rest, rendered to .env at deploy)

func (s *Service) GetEnv(ctx context.Context, project string) (map[string]string, error) {
	p, err := s.Get(ctx, project)
	if err != nil {
		return nil, err
	}
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

	if err := s.q.DeleteProjectEnvVars(ctx, p.ID); err != nil {
		return err
	}
	for key, value := range vars {
		enc, err := s.box.Encrypt([]byte(value))
		if err != nil {
			return err
		}
		if err := s.q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{
			ProjectID: p.ID, Key: key, ValueEnc: enc,
		}); err != nil {
			return err
		}
	}

	s.bus.Publish(events.Event{Topic: "project", Type: "project.env_changed", Resource: project})
	return nil
}

// RenderEnvFile writes the decrypted environment to the project's .env,
// which docker compose picks up natively. Called by the deploy pipeline.
func (s *Service) RenderEnvFile(ctx context.Context, project string) error {
	vars, err := s.GetEnv(ctx, project)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("# Generated by Windlass from the project's environment settings.\n")
	b.WriteString("# Hand edits are read back into the panel on the next deploy.\n")
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(quoteEnvValue(vars[k]))
		b.WriteByte('\n')
	}
	return s.agent.FS().WriteFile(ctx, project, ".env", []byte(b.String()), 0o600)
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

// quoteEnvValue quotes values that need it for dotenv parsing.
func quoteEnvValue(v string) string {
	if v == "" || strings.ContainsAny(v, " \t\n\"'#$") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, `$`, `$$`).Replace(v) + `"`
	}
	return v
}
