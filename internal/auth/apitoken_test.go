package auth

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/migrations"
)

func TestAPITokenFormat(t *testing.T) {
	plain, publicID, hash, err := newAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != apiTokenLen || !strings.HasPrefix(plain, TokenPrefix+publicID) {
		t.Fatalf("token %q has the wrong shape", plain)
	}
	gotID, secret, ok := parseAPIToken(plain)
	if !ok || gotID != publicID || hashSecret(secret) != hash {
		t.Fatalf("parse = %q, %v; want the public id and a secret matching the hash", gotID, ok)
	}
	if strings.Contains(hash, secret) {
		t.Fatal("stored hash contains the secret")
	}

	// One wrong character anywhere fails the checksum or the alphabet check.
	for i := len(TokenPrefix); i < len(plain); i++ {
		b := []byte(plain)
		if b[i] == 'a' {
			b[i] = 'b'
		} else {
			b[i] = 'a'
		}
		if _, _, ok := parseAPIToken(string(b)); ok {
			t.Fatalf("token altered at %d still parses", i)
		}
	}
	for _, bad := range []string{"", plain[:len(plain)-1], plain + "0", "wl_pat_" + strings.Repeat("g", 60),
		strings.ToUpper(plain), "xx_pat_" + plain[7:]} {
		if _, _, ok := parseAPIToken(bad); ok {
			t.Errorf("parseAPIToken(%q) accepted", bad)
		}
	}
}

func newTokenTestService(t *testing.T) (*Service, *db.Queries) {
	t.Helper()
	sqlDB, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := store.Migrate(sqlDB, migrations.FS); err != nil {
		t.Fatal(err)
	}
	q := db.New(sqlDB)
	box, err := secrets.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewService(context.Background(), q, testKey, box, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s, q
}

func TestAPITokenAuthentication(t *testing.T) {
	ctx := context.Background()
	s, q := newTokenTestService(t)
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	admin, err := q.CreateUser(ctx, db.CreateUserParams{Email: "a@example.com", Role: "admin",
		PasswordHash: sql.NullString{}})
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.CreateAPIToken(ctx, admin, "ci", "deploy", false, DefaultTokenTTL); !errors.Is(err, ErrInvalidScope) {
		t.Errorf("unknown scope err = %v", err)
	}
	if _, _, err := s.CreateAPIToken(ctx, admin, "ci", "read", false, 400*24*time.Hour); !errors.Is(err, ErrInvalidTTL) {
		t.Errorf("400-day ttl err = %v", err)
	}
	if _, _, err := s.CreateAPIToken(ctx, admin, "  ", "read", false, DefaultTokenTTL); !errors.Is(err, ErrTokenName) {
		t.Errorf("blank name err = %v", err)
	}

	plain, tok, err := s.CreateAPIToken(ctx, admin, "agent", "write", true, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	user, info, err := s.AuthenticateAPIToken(ctx, plain, "203.0.113.9")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if user.Role != "member" {
		t.Errorf("admin's write token acts as %q, want member", user.Role)
	}
	if info.ID != tok.ID || info.Scope != "write" || !info.Secrets {
		t.Errorf("token info = %+v", info)
	}
	got, _ := q.GetAPIToken(ctx, tok.ID)
	if got.LastUsedIp.String != "203.0.113.9" || !got.LastUsedAt.Valid {
		t.Errorf("last use not recorded: %+v", got)
	}

	// Demotion caps the token at the user's new role.
	if err := q.UpdateUserRole(ctx, db.UpdateUserRoleParams{Role: "viewer", ID: admin.ID}); err != nil {
		t.Fatal(err)
	}
	if user, _, _ = s.AuthenticateAPIToken(ctx, plain, ""); user.Role != "viewer" {
		t.Errorf("after demotion token acts as %q, want viewer", user.Role)
	}

	// A guessed secret for a real public id fails.
	forged, _, _, _ := newAPIToken()
	forgedBody := TokenPrefix + tok.PublicID + forged[len(TokenPrefix)+publicIDHexLen:len(forged)-checksumHexLen]
	if _, _, err := s.AuthenticateAPIToken(ctx, forgedBody+checksum(forgedBody), ""); err == nil {
		t.Error("wrong secret for a real public id accepted")
	}

	// Expiry.
	now = now.Add(30 * 24 * time.Hour)
	if _, _, err := s.AuthenticateAPIToken(ctx, plain, ""); err == nil {
		t.Error("expired token accepted")
	}
	now = now.Add(-time.Hour)

	// Disabled owner.
	if err := q.DisableUser(ctx, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AuthenticateAPIToken(ctx, plain, ""); err == nil {
		t.Error("token of a disabled user accepted")
	}
}

func TestAPITokenRevocationAndScopeCeiling(t *testing.T) {
	ctx := context.Background()
	s, q := newTokenTestService(t)
	member, err := q.CreateUser(ctx, db.CreateUserParams{Email: "m@example.com", Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateAPIToken(ctx, member, "x", "admin", false, DefaultTokenTTL); !errors.Is(err, ErrScopeAbove) {
		t.Errorf("member minting admin token err = %v", err)
	}
	plain, tok, err := s.CreateAPIToken(ctx, member, "x", "read", false, DefaultTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.RevokeAPIToken(ctx, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AuthenticateAPIToken(ctx, plain, ""); err == nil {
		t.Error("revoked token accepted")
	}
}
