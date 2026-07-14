// Package git manages provider connections (encrypted tokens), project git
// configuration, and webhook verification for auto-deploys.
package git

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store/db"
)

var (
	ErrNotFound         = errors.New("not found")
	ErrInvalidSignature = errors.New("invalid webhook signature")
)

type Service struct {
	q      *db.Queries
	box    *secrets.Box
	logger *slog.Logger
}

func New(q *db.Queries, box *secrets.Box, logger *slog.Logger) *Service {
	return &Service{q: q, box: box, logger: logger}
}

// ---------------------------------------------------------------------------
// Connections

type Connection struct {
	ID       int64  `json:"id"`
	Provider string `json:"provider"`
	Name     string `json:"name"`
}

func (s *Service) CreateConnection(ctx context.Context, provider, name, token string) (Connection, error) {
	if provider != "github" && provider != "gitlab" {
		return Connection{}, fmt.Errorf("unsupported provider %q", provider)
	}
	if name == "" || token == "" {
		return Connection{}, errors.New("name and token are required")
	}
	enc, err := s.box.Encrypt([]byte(token))
	if err != nil {
		return Connection{}, err
	}
	row, err := s.q.CreateGitConnection(ctx, db.CreateGitConnectionParams{
		Provider: provider, Name: name, TokenEnc: enc,
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Connection{}, errors.New("a connection with this name already exists")
		}
		return Connection{}, err
	}
	return Connection{ID: row.ID, Provider: row.Provider, Name: row.Name}, nil
}

// UpsertConnection stores a token under a stable name, refreshing the token
// when the connection already exists. Used by the OAuth connect flow, where
// reconnecting the same GitHub account should not fail or duplicate.
func (s *Service) UpsertConnection(ctx context.Context, provider, name, token string) (Connection, error) {
	if provider != "github" && provider != "gitlab" {
		return Connection{}, fmt.Errorf("unsupported provider %q", provider)
	}
	if name == "" || token == "" {
		return Connection{}, errors.New("name and token are required")
	}
	enc, err := s.box.Encrypt([]byte(token))
	if err != nil {
		return Connection{}, err
	}
	row, err := s.q.UpsertGitConnection(ctx, db.CreateGitConnectionParams{
		Provider: provider, Name: name, TokenEnc: enc,
	})
	if err != nil {
		return Connection{}, err
	}
	return Connection{ID: row.ID, Provider: row.Provider, Name: row.Name}, nil
}

func (s *Service) ListConnections(ctx context.Context) ([]Connection, error) {
	rows, err := s.q.ListGitConnections(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Connection, 0, len(rows))
	for _, r := range rows {
		out = append(out, Connection{ID: r.ID, Provider: r.Provider, Name: r.Name})
	}
	return out, nil
}

func (s *Service) DeleteConnection(ctx context.Context, id int64) error {
	return s.q.DeleteGitConnection(ctx, id)
}

// Token decrypts a project's git token, if it has a connection.
func (s *Service) Token(ctx context.Context, p db.Project) (string, error) {
	if !p.GitConnectionID.Valid {
		return "", nil // public repo
	}
	conn, err := s.q.GetGitConnection(ctx, p.GitConnectionID.Int64)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	plain, err := s.box.Decrypt(conn.TokenEnc)
	if err != nil {
		return "", fmt.Errorf("decrypt token: %w", err)
	}
	return string(plain), nil
}

// ---------------------------------------------------------------------------
// Project configuration

type ProjectConfig struct {
	Repo         string `json:"repo"`
	Branch       string `json:"branch"`
	AutoDeploy   bool   `json:"auto_deploy"`
	ConnectionID int64  `json:"connection_id,omitempty"`
	// WebhookSecret is returned only when (re)configuring.
	WebhookSecret string `json:"webhook_secret,omitempty"`
}

// Configure sets a project's git source and returns the webhook secret to
// paste into GitHub/GitLab.
func (s *Service) Configure(ctx context.Context, p db.Project, cfg ProjectConfig) (string, error) {
	if !strings.HasPrefix(cfg.Repo, "https://") {
		return "", errors.New("repo must be an https:// clone URL")
	}
	if cfg.Branch == "" {
		cfg.Branch = "main"
	}

	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	secret := hex.EncodeToString(buf)
	secretEnc, err := s.box.Encrypt([]byte(secret))
	if err != nil {
		return "", err
	}

	autoDeploy := int64(0)
	if cfg.AutoDeploy {
		autoDeploy = 1
	}
	err = s.q.ConfigureProjectGit(ctx, db.ConfigureProjectGitParams{
		GitRepo:          sql.NullString{String: cfg.Repo, Valid: true},
		GitBranch:        sql.NullString{String: cfg.Branch, Valid: true},
		AutoDeploy:       autoDeploy,
		GitConnectionID:  sql.NullInt64{Int64: cfg.ConnectionID, Valid: cfg.ConnectionID > 0},
		WebhookSecretEnc: secretEnc,
		ID:               p.ID,
	})
	if err != nil {
		return "", err
	}
	return secret, nil
}

func (s *Service) Clear(ctx context.Context, projectID int64) error {
	return s.q.ClearProjectGit(ctx, projectID)
}

// ---------------------------------------------------------------------------
// Webhooks

// VerifyWebhook checks a webhook request against the project's secret.
// GitHub signs the body (X-Hub-Signature-256); GitLab sends the secret
// verbatim (X-Gitlab-Token).
func (s *Service) VerifyWebhook(p db.Project, provider string, body []byte, signatureHeader string) error {
	if len(p.WebhookSecretEnc) == 0 {
		return errors.New("project has no webhook configured")
	}
	secret, err := s.box.Decrypt(p.WebhookSecretEnc)
	if err != nil {
		return err
	}

	switch provider {
	case "github":
		want := "sha256=" + hmacHex(secret, body)
		if subtle.ConstantTimeCompare([]byte(want), []byte(signatureHeader)) != 1 {
			return ErrInvalidSignature
		}
	case "gitlab":
		if subtle.ConstantTimeCompare(secret, []byte(signatureHeader)) != 1 {
			return ErrInvalidSignature
		}
	default:
		return fmt.Errorf("unsupported provider %q", provider)
	}
	return nil
}

func hmacHex(key, body []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// PushBranch extracts the branch from a push payload's ref field
// ("refs/heads/<branch>" for both providers).
func PushBranch(ref string) string {
	return strings.TrimPrefix(ref, "refs/heads/")
}
