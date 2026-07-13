package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/windlass-dev/windlass/internal/store/db"
)

const (
	SessionCookie = "windlass_session"
	sessionTTL    = 7 * 24 * time.Hour
	timeFormat    = time.RFC3339
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrSetupComplete      = errors.New("instance already set up")
	ErrSetupToken         = errors.New("invalid setup token")
	ErrNotAuthenticated   = errors.New("not authenticated")
)

type Service struct {
	q      *db.Queries
	key    []byte // session-signing key
	logger *slog.Logger
	now    func() time.Time

	// setupToken guards first-run admin creation. Generated only while the
	// instance has zero users; a restart regenerates it.
	setupToken string
}

func NewService(ctx context.Context, q *db.Queries, key []byte, logger *slog.Logger) (*Service, error) {
	s := &Service{q: q, key: key, logger: logger, now: time.Now}

	n, err := q.CountUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("count users: %w", err)
	}
	if n == 0 {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		s.setupToken = hex.EncodeToString(buf)
		// Printed to the log on purpose: reading the server log proves host
		// access, which is the trust anchor for claiming a fresh instance.
		logger.Info("no users exist yet — claim this instance in the web UI",
			"setup_token", s.setupToken)
	}
	return s, nil
}

func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	n, err := s.q.CountUsers(ctx)
	return n == 0, err
}

// Setup creates the admin account using the one-time token and returns a
// signed session token for immediate sign-in.
func (s *Service) Setup(ctx context.Context, token, email, password, ip, userAgent string) (string, db.User, error) {
	needs, err := s.NeedsSetup(ctx)
	if err != nil {
		return "", db.User{}, err
	}
	if !needs || s.setupToken == "" {
		return "", db.User{}, ErrSetupComplete
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.setupToken)) != 1 {
		return "", db.User{}, ErrSetupToken
	}
	if len(password) < 10 {
		return "", db.User{}, errors.New("password must be at least 10 characters")
	}

	hash, err := HashPassword(password)
	if err != nil {
		return "", db.User{}, err
	}
	user, err := s.q.CreateUser(ctx, db.CreateUserParams{
		Email:        email,
		PasswordHash: sql.NullString{String: hash, Valid: true},
		Role:         "admin",
	})
	if err != nil {
		return "", db.User{}, fmt.Errorf("create admin: %w", err)
	}
	s.setupToken = ""

	cookie, err := s.startSession(ctx, user, ip, userAgent)
	return cookie, user, err
}

func (s *Service) Login(ctx context.Context, email, password, ip, userAgent string) (string, db.User, error) {
	user, err := s.q.GetUserByEmail(ctx, email)
	if errors.Is(err, sql.ErrNoRows) {
		// Burn comparable time so absent accounts are indistinguishable.
		_ = VerifyPassword("$argon2id$v=19$m=65536,t=3,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", password)
		return "", db.User{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", db.User{}, err
	}
	if !user.PasswordHash.Valid {
		return "", db.User{}, ErrInvalidCredentials // OAuth-only account
	}
	if err := VerifyPassword(user.PasswordHash.String, password); err != nil {
		return "", db.User{}, ErrInvalidCredentials
	}

	cookie, err := s.startSession(ctx, user, ip, userAgent)
	return cookie, user, err
}

func (s *Service) startSession(ctx context.Context, user db.User, ip, userAgent string) (string, error) {
	sid, err := NewSessionID()
	if err != nil {
		return "", err
	}
	expires := s.now().Add(sessionTTL)
	if err := s.q.CreateSession(ctx, db.CreateSessionParams{
		ID:        sid,
		UserID:    user.ID,
		ExpiresAt: expires.UTC().Format(timeFormat),
		Ip:        sql.NullString{String: ip, Valid: ip != ""},
		UserAgent: sql.NullString{String: userAgent, Valid: userAgent != ""},
	}); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return SignToken(s.key, Claims{SessionID: sid, UserID: user.ID, ExpiresAt: expires.Unix()})
}

// Authenticate validates a session token end-to-end: signature, expiry, the
// session row (revocation), and the user (disabled accounts).
func (s *Service) Authenticate(ctx context.Context, token string) (db.User, Claims, error) {
	claims, err := VerifyToken(s.key, token, s.now())
	if err != nil {
		return db.User{}, Claims{}, ErrNotAuthenticated
	}
	sess, err := s.q.GetSession(ctx, claims.SessionID)
	if err != nil {
		return db.User{}, Claims{}, ErrNotAuthenticated
	}
	if exp, err := time.Parse(timeFormat, sess.ExpiresAt); err != nil || !s.now().Before(exp) {
		return db.User{}, Claims{}, ErrNotAuthenticated
	}
	user, err := s.q.GetUserByID(ctx, sess.UserID)
	if err != nil || user.DisabledAt.Valid {
		return db.User{}, Claims{}, ErrNotAuthenticated
	}
	return user, claims, nil
}

func (s *Service) Logout(ctx context.Context, sessionID string) error {
	return s.q.DeleteSession(ctx, sessionID)
}

// SessionTTL is exposed for cookie Max-Age.
func SessionTTL() time.Duration { return sessionTTL }
