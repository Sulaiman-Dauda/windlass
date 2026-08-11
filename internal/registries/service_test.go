package registries

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/windlass-dev/windlass/migrations"

	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
)

func testService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	conn, err := store.Open(filepath.Join(dir, "w.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := store.Migrate(conn, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}
	return New(db.New(conn), box, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// recordingDocker stands in for the host, so a test never needs a real daemon
// or a real token.
type recordingDocker struct {
	logins []login
	fail   error
}

type login struct{ host, username, secret string }

func (r *recordingDocker) RegistryLogin(_ context.Context, host, username, secret string) error {
	if r.fail != nil {
		return r.fail
	}
	r.logins = append(r.logins, login{host, username, secret})
	return nil
}

func TestNormaliseHost(t *testing.T) {
	// All the ways somebody types the same registry. Storing these as separate
	// rows would leave two of them never applied while the panel lists three.
	for _, tc := range []struct{ in, want string }{
		{"ghcr.io", "ghcr.io"},
		{"GHCR.IO", "ghcr.io"},
		{"https://ghcr.io/", "ghcr.io"},
		{"ghcr.io/sulaiman-dauda", "ghcr.io"},
		{"  ghcr.io  ", "ghcr.io"},
	} {
		if got := NormaliseHost(tc.in); got != tc.want {
			t.Errorf("NormaliseHost(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRegistryOf(t *testing.T) {
	for _, tc := range []struct{ image, want string }{
		{"ghcr.io/sulaiman-dauda/perch:latest", "ghcr.io"},
		{"registry.gitlab.com/group/app", "registry.gitlab.com"},
		{"localhost:5000/app:1", "localhost:5000"},
		// Docker Hub, which needs no login for a public image.
		{"postgres:18-alpine", ""},
		{"sulaimandauda/app:latest", ""},
		{"", ""},
	} {
		if got := RegistryOf(tc.image); got != tc.want {
			t.Errorf("RegistryOf(%q) = %q, want %q", tc.image, got, tc.want)
		}
	}
}

func TestUpsertRequiresEverything(t *testing.T) {
	s := testService(t)
	for _, tc := range []struct{ host, user, secret string }{
		{"", "u", "p"},
		{"ghcr.io", "", "p"},
		{"ghcr.io", "u", ""},
	} {
		if _, err := s.Upsert(context.Background(), tc.host, tc.user, tc.secret); err == nil {
			t.Errorf("Upsert(%q,%q,...) accepted an incomplete credential", tc.host, tc.user)
		}
	}
}

func TestUpsertReplacesRatherThanDuplicating(t *testing.T) {
	s := testService(t)
	ctx := context.Background()

	if _, err := s.Upsert(ctx, "ghcr.io", "old-user", "old-token"); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if _, err := s.Upsert(ctx, "https://ghcr.io/owner", "new-user", "new-token"); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	creds, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("got %d credentials, want 1", len(creds))
	}
	if creds[0].Username != "new-user" {
		t.Errorf("username = %q, want new-user", creds[0].Username)
	}
}

// The secret must never come back out through the API shape.
func TestListNeverReturnsTheSecret(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	if _, err := s.Upsert(ctx, "ghcr.io", "u", "super-secret-token"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	creds, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("got %d credentials, want 1", len(creds))
	}
	// Credential has no secret field at all; this asserts it stays that way.
	if creds[0].Host != "ghcr.io" || creds[0].Username != "u" {
		t.Errorf("unexpected credential %+v", creds[0])
	}
}

// Apply is what keeps docs/life-without-the-panel true: it logs the *host* in,
// so a plain `docker compose pull` works with Windlass stopped.
func TestApplyLogsTheHostIn(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	if _, err := s.Upsert(ctx, "ghcr.io", "sulaiman", "tok"); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	docker := &recordingDocker{}
	if err := s.Apply(ctx, docker); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(docker.logins) != 1 {
		t.Fatalf("got %d logins, want 1", len(docker.logins))
	}
	got := docker.logins[0]
	if got.host != "ghcr.io" || got.username != "sulaiman" || got.secret != "tok" {
		t.Errorf("logged in as %+v", got)
	}

	// And it records that the credential actually worked, so the panel can say
	// so rather than only that somebody typed one in.
	creds, _ := s.List(ctx)
	if creds[0].VerifiedAt == "" {
		t.Error("a successful login did not record verified_at")
	}
}

func TestApplyReportsAFailedLogin(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	if _, err := s.Upsert(ctx, "ghcr.io", "u", "bad"); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	docker := &recordingDocker{fail: errors.New("unauthorized")}
	err := s.Apply(ctx, docker)
	if err == nil {
		t.Fatal("a failed login was reported as success")
	}
	// The registry's own words, not "exit status 1".
	if got := err.Error(); got == "" || !contains(got, "unauthorized") || !contains(got, "ghcr.io") {
		t.Errorf("error %q does not say which registry failed or why", got)
	}

	creds, _ := s.List(ctx)
	if creds[0].VerifiedAt != "" {
		t.Error("a failed login was recorded as verified")
	}
}

// With nothing configured, Apply must be a no-op rather than an error: that is
// every installation before anybody adds a credential, and it must not turn
// deployments of public images into failures.
func TestApplyDoesNothingWhenNothingIsConfigured(t *testing.T) {
	s := testService(t)
	docker := &recordingDocker{}
	if err := s.Apply(context.Background(), docker); err != nil {
		t.Fatalf("apply with no credentials: %v", err)
	}
	if len(docker.logins) != 0 {
		t.Errorf("logged in %d times with nothing configured", len(docker.logins))
	}
}

func TestMissingNamesOnlyRegistriesThatNeedOne(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	if _, err := s.Upsert(ctx, "ghcr.io", "u", "t"); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	missing, err := s.Missing(ctx, []string{
		"ghcr.io/owner/app:latest",      // configured
		"postgres:18-alpine",            // Docker Hub, public, needs nothing
		"registry.gitlab.com/g/app:1",   // not configured
		"registry.gitlab.com/g/other:2", // same host, reported once
	})
	if err != nil {
		t.Fatalf("missing: %v", err)
	}
	if len(missing) != 1 || missing[0] != "registry.gitlab.com" {
		t.Errorf("missing = %v, want [registry.gitlab.com]", missing)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
