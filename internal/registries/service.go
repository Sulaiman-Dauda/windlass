// Package registries stores container registry credentials and applies them to
// the host.
//
// Windlass pulls private images with `docker compose pull`, which reads the
// host's Docker config. Nothing ever wrote one, so a private image failed with
// "unauthorized" and the deployment died at the pulling step. On the first real
// installation that was four projects out of five, and it only showed when
// somebody tried to ship a change: the containers kept running the image that
// had been pulled by hand months earlier.
//
// The credential is applied with a real `docker login` rather than injected
// into Windlass's own pull. That is the whole point. docs/life-without-the-panel
// promises the panel is removable from the application runtime path, and an
// administrator can run `docker compose -p myapp up -d` by hand. A credential
// held only inside Windlass would have made that false for every private image.
package registries

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store/db"
)

type Service struct {
	q      *db.Queries
	box    *secrets.Box
	logger *slog.Logger
}

func New(q *db.Queries, box *secrets.Box, logger *slog.Logger) *Service {
	return &Service{q: q, box: box, logger: logger}
}

// Registrar is the one thing this package needs from the host.
//
// Narrower than agent.DockerAgent on purpose: a credential store has no
// business being handed container logs and image pruning, and a test should not
// have to implement eleven methods to check that a login happened.
type Registrar interface {
	RegistryLogin(ctx context.Context, host, username, secret string) error
}

// Credential is the safe shape: never carries the secret.
type Credential struct {
	ID         int64  `json:"id"`
	Host       string `json:"host"`
	Username   string `json:"username"`
	UpdatedAt  string `json:"updated_at"`
	VerifiedAt string `json:"verified_at,omitempty"`
}

// NormaliseHost turns what somebody types into the registry host Docker uses.
//
// "https://ghcr.io/", "ghcr.io/sulaiman-dauda" and "GHCR.IO" are all the same
// registry, and storing them as three rows would mean two of them never get
// applied while the panel cheerfully lists all three.
func NormaliseHost(raw string) string {
	host := strings.TrimSpace(strings.ToLower(raw))
	host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	return host
}

func (s *Service) Upsert(ctx context.Context, host, username, secret string) (Credential, error) {
	host = NormaliseHost(host)
	username = strings.TrimSpace(username)
	if host == "" || username == "" || secret == "" {
		return Credential{}, errors.New("registry, username and token are all required")
	}

	enc, err := s.box.Encrypt([]byte(secret))
	if err != nil {
		return Credential{}, err
	}
	row, err := s.q.UpsertRegistryCredential(ctx, db.UpsertRegistryCredentialParams{
		Host: host, Username: username, SecretEnc: enc,
	})
	if err != nil {
		return Credential{}, err
	}
	return toCredential(row), nil
}

func (s *Service) List(ctx context.Context) ([]Credential, error) {
	rows, err := s.q.ListRegistryCredentials(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Credential, 0, len(rows))
	for _, row := range rows {
		out = append(out, toCredential(row))
	}
	return out, nil
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.q.DeleteRegistryCredential(ctx, id)
}

// Apply logs the host in to every configured registry.
//
// Called before a pull and again after a credential is saved, so the host is
// authenticated whether or not a deployment happens to run.
//
// A failure on one registry does not stop the others: two registries where one
// token has expired should still leave the other working, and the deployment
// that needs the broken one will say so in its own words when the pull fails.
func (s *Service) Apply(ctx context.Context, docker Registrar) error {
	rows, err := s.q.ListRegistryCredentials(ctx)
	if err != nil {
		return err
	}

	var failures []string
	for _, row := range rows {
		secret, err := s.box.Decrypt(row.SecretEnc)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: could not decrypt the stored token", row.Host))
			continue
		}
		if err := docker.RegistryLogin(ctx, row.Host, row.Username, string(secret)); err != nil {
			s.logger.Warn("registry login failed", "host", row.Host, "error", err)
			failures = append(failures, fmt.Sprintf("%s: %v", row.Host, err))
			continue
		}
		if err := s.q.MarkRegistryVerified(ctx, row.Host); err != nil {
			s.logger.Warn("could not record a registry login", "host", row.Host, "error", err)
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("registry login failed for %s", strings.Join(failures, "; "))
	}
	return nil
}

// FillFrom stores a credential derived from something else the operator has
// already connected, and signs the host in with it.
//
// A GitHub connection is already most of a ghcr.io credential: the registry
// takes a GitHub token as the password. Asking somebody to paste a second token
// for the same account is friction with nothing behind it.
//
// It refuses to overwrite a credential that has actually worked. A connection
// token usually carries `repo` and not `read:packages`, so deriving one can
// easily produce a credential that cannot pull, and replacing a working one
// with that would turn a convenience into an outage.
//
// A derived credential that fails to sign in is still stored, deliberately. The
// screen shows "never signed in" against it, which is a visible thing somebody
// can fix, where storing nothing looks like the feature never ran.
func (s *Service) FillFrom(ctx context.Context, host, username, secret string, docker Registrar) error {
	host = NormaliseHost(host)
	existing, err := s.q.GetRegistryCredential(ctx, host)
	if err == nil && existing.VerifiedAt.Valid {
		return nil
	}

	if _, err := s.Upsert(ctx, host, username, secret); err != nil {
		return err
	}
	// Reported, not returned: connecting a repository must not fail because the
	// token happens not to carry read:packages.
	if err := docker.RegistryLogin(ctx, host, username, secret); err != nil {
		s.logger.Info("derived registry credential cannot sign in yet",
			"host", host, "error", err)
		return nil
	}
	return s.q.MarkRegistryVerified(ctx, host)
}

// Missing reports registries an image list needs that have no credential.
//
// Used to say "this project pulls from ghcr.io and nothing here can log in"
// before the pull fails, rather than leaving somebody to work it out from
// "unauthorized".
func (s *Service) Missing(ctx context.Context, images []string) ([]string, error) {
	rows, err := s.q.ListRegistryCredentials(ctx)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(rows))
	for _, row := range rows {
		known[row.Host] = true
	}

	seen := map[string]bool{}
	var missing []string
	for _, image := range images {
		host := RegistryOf(image)
		// Docker Hub is the default registry and its public images need no
		// credential, so flagging it would cry wolf on every stock image.
		if host == "" || host == "docker.io" || known[host] || seen[host] {
			continue
		}
		seen[host] = true
		missing = append(missing, host)
	}
	return missing, nil
}

// RegistryOf extracts the registry host from an image reference.
//
// "ghcr.io/owner/app:tag" is ghcr.io; "postgres:18-alpine" and "owner/app" are
// Docker Hub, which is returned as "" because they need no login to pull.
// The rule Docker itself uses: the first segment is a registry only when it
// contains a dot or a colon, or is exactly "localhost".
func RegistryOf(image string) string {
	ref := strings.TrimSpace(image)
	if ref == "" {
		return ""
	}
	first, rest, found := strings.Cut(ref, "/")
	if !found {
		return ""
	}
	_ = rest
	if first == "localhost" || strings.ContainsAny(first, ".:") {
		return strings.ToLower(first)
	}
	return ""
}

func toCredential(row db.RegistryCredential) Credential {
	c := Credential{
		ID:        row.ID,
		Host:      row.Host,
		Username:  row.Username,
		UpdatedAt: row.UpdatedAt,
	}
	if row.VerifiedAt.Valid {
		c.VerifiedAt = row.VerifiedAt.String
	}
	return c
}
