package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"
	"time"

	"github.com/windlass-dev/windlass/internal/store/db"
)

// Personal API tokens look like
//
//	wl_pat_<12 hex public id><40 hex secret><8 hex CRC32>
//
// The prefix lets secret scanners (GitHub push protection, gitleaks) and
// people recognise a leaked token. The public id is the database lookup key;
// only a SHA-256 of the secret is stored. The CRC32 over everything before it
// rejects typos and truncated pastes without a database read.
const (
	TokenPrefix     = "wl_pat_"
	publicIDHexLen  = 12
	secretHexLen    = 40
	checksumHexLen  = 8
	apiTokenLen     = len(TokenPrefix) + publicIDHexLen + secretHexLen + checksumHexLen
	MaxTokenTTL     = 365 * 24 * time.Hour
	DefaultTokenTTL = 90 * 24 * time.Hour
	// touchInterval bounds last_used writes to one per token per minute, so
	// a busy script does not turn every read into a database write.
	touchInterval = time.Minute
)

var (
	ErrInvalidScope = errors.New("invalid scope")
	ErrScopeAbove   = errors.New("scope exceeds the user's role")
	ErrInvalidTTL   = errors.New("expiry must be between 1 and 365 days")
	ErrTokenName    = errors.New("name is required (at most 100 characters)")
)

// scopeRole maps a token scope to the highest role it may act as.
var scopeRole = map[string]string{"read": "viewer", "write": "member", "admin": "admin"}

// ValidScope reports whether scope is one of read, write, admin.
func ValidScope(scope string) bool { _, ok := scopeRole[scope]; return ok }

// TokenInfo describes the API token behind a request.
type TokenInfo struct {
	ID       int64
	PublicID string
	Name     string
	Scope    string
	// Secrets allows unmasked env reads.
	Secrets bool
}

// TokenFrom returns the API token that authenticated the request, if any.
// ok is false for browser sessions.
func TokenFrom(ctx context.Context) (TokenInfo, bool) {
	t, ok := ctx.Value(tokenKey).(TokenInfo)
	return t, ok
}

// CanReadSecrets reports whether the request may see unmasked env values: a
// member or above, and, for an API token, one created with the secrets flag.
func CanReadSecrets(ctx context.Context) bool {
	if !HasRole(ctx, "member") {
		return false
	}
	if t, ok := TokenFrom(ctx); ok {
		return t.Secrets
	}
	return true
}

func newAPIToken() (plaintext, publicID, secretHash string, err error) {
	buf := make([]byte, (publicIDHexLen+secretHexLen)/2)
	if _, err := rand.Read(buf); err != nil {
		return "", "", "", err
	}
	publicID = hex.EncodeToString(buf[:publicIDHexLen/2])
	secret := hex.EncodeToString(buf[publicIDHexLen/2:])
	body := TokenPrefix + publicID + secret
	return body + checksum(body), publicID, hashSecret(secret), nil
}

func checksum(body string) string {
	return fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(body)))
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// parseAPIToken splits a token into its public id and secret, rejecting
// anything with the wrong shape or checksum.
func parseAPIToken(s string) (publicID, secret string, ok bool) {
	if len(s) != apiTokenLen || !strings.HasPrefix(s, TokenPrefix) {
		return "", "", false
	}
	rest := s[len(TokenPrefix):]
	for _, c := range rest {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", "", false
		}
	}
	body := s[:len(s)-checksumHexLen]
	if subtle.ConstantTimeCompare([]byte(checksum(body)), []byte(s[len(body):])) != 1 {
		return "", "", false
	}
	return rest[:publicIDHexLen], rest[publicIDHexLen : publicIDHexLen+secretHexLen], true
}

// CreateAPIToken issues a token for user. The plaintext is returned once and
// never stored. scope may not exceed the user's own role.
func (s *Service) CreateAPIToken(ctx context.Context, user db.User, name, scope string, secrets bool, ttl time.Duration) (string, db.ApiToken, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return "", db.ApiToken{}, ErrTokenName
	}
	role, ok := scopeRole[scope]
	if !ok {
		return "", db.ApiToken{}, ErrInvalidScope
	}
	if roleRank[role] > roleRank[user.Role] {
		return "", db.ApiToken{}, ErrScopeAbove
	}
	if ttl < 24*time.Hour || ttl > MaxTokenTTL {
		return "", db.ApiToken{}, ErrInvalidTTL
	}
	plaintext, publicID, hash, err := newAPIToken()
	if err != nil {
		return "", db.ApiToken{}, err
	}
	var flag int64
	if secrets {
		flag = 1
	}
	tok, err := s.q.CreateAPIToken(ctx, db.CreateAPITokenParams{
		PublicID:       publicID,
		UserID:         user.ID,
		Name:           name,
		SecretHash:     hash,
		Scope:          scope,
		CanReadSecrets: flag,
		ExpiresAt:      s.now().Add(ttl).UTC().Format(timeFormat),
	})
	if err != nil {
		return "", db.ApiToken{}, fmt.Errorf("create api token: %w", err)
	}
	return plaintext, tok, nil
}

// AuthenticateAPIToken validates a bearer token: shape and checksum, the
// stored hash, revocation, expiry and the owning user. The returned user has
// its role capped at the token's scope, so every existing role check applies
// to tokens unchanged.
func (s *Service) AuthenticateAPIToken(ctx context.Context, raw, ip string) (db.User, TokenInfo, error) {
	publicID, secret, ok := parseAPIToken(raw)
	if !ok {
		return db.User{}, TokenInfo{}, ErrNotAuthenticated
	}
	tok, err := s.q.GetAPITokenByPublicID(ctx, publicID)
	if err != nil {
		return db.User{}, TokenInfo{}, ErrNotAuthenticated
	}
	if subtle.ConstantTimeCompare([]byte(hashSecret(secret)), []byte(tok.SecretHash)) != 1 {
		return db.User{}, TokenInfo{}, ErrNotAuthenticated
	}
	if tok.RevokedAt.Valid {
		return db.User{}, TokenInfo{}, ErrNotAuthenticated
	}
	now := s.now()
	if exp, err := time.Parse(timeFormat, tok.ExpiresAt); err != nil || !now.Before(exp) {
		return db.User{}, TokenInfo{}, ErrNotAuthenticated
	}
	user, err := s.q.GetUserByID(ctx, tok.UserID)
	if err != nil || user.DisabledAt.Valid {
		return db.User{}, TokenInfo{}, ErrNotAuthenticated
	}
	role, ok := scopeRole[tok.Scope]
	if !ok {
		return db.User{}, TokenInfo{}, ErrNotAuthenticated
	}
	if roleRank[role] < roleRank[user.Role] {
		user.Role = role
	}

	if last, err := time.Parse(timeFormat, tok.LastUsedAt.String); !tok.LastUsedAt.Valid || err != nil ||
		now.Sub(last) >= touchInterval {
		if err := s.q.TouchAPIToken(ctx, db.TouchAPITokenParams{
			LastUsedAt: sql.NullString{String: now.UTC().Format(timeFormat), Valid: true},
			LastUsedIp: sql.NullString{String: ip, Valid: ip != ""},
			ID:         tok.ID,
		}); err != nil {
			s.logger.Warn("record api token use", "token", tok.PublicID, "error", err)
		}
	}

	return user, TokenInfo{
		ID:       tok.ID,
		PublicID: tok.PublicID,
		Name:     tok.Name,
		Scope:    tok.Scope,
		Secrets:  tok.CanReadSecrets != 0,
	}, nil
}
