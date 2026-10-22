package auth

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
	ErrTOTPRequired       = errors.New("totp code required")
	ErrTOTPInvalid        = errors.New("invalid totp code")
	ErrTOTPEnabled        = errors.New("two-factor authentication is already on; turn it off first")
)

// SecretBox decrypts/encrypts stored secrets (TOTP seeds).
type SecretBox interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(ciphertext []byte) ([]byte, error)
}

type Service struct {
	q      *db.Queries
	key    []byte // session-signing key
	box    SecretBox
	logger *slog.Logger
	now    func() time.Time

	// setupToken guards first-run admin creation. Generated only while the
	// instance has zero users; a restart regenerates it.
	setupToken string
}

func NewService(ctx context.Context, q *db.Queries, key []byte, box SecretBox, logger *slog.Logger) (*Service, error) {
	s := &Service{q: q, key: key, box: box, logger: logger, now: time.Now}

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
		logger.Info("no users exist yet; claim this instance in the web UI",
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

func (s *Service) Login(ctx context.Context, email, password, totpCode, ip, userAgent string) (string, db.User, error) {
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

	// Second factor, when enrolled. The password was already verified, so
	// asking for the code leaks nothing an attacker doesn't have.
	if user.TotpEnabled != 0 {
		if totpCode == "" {
			return "", db.User{}, ErrTOTPRequired
		}
		ok, err := s.checkTOTP(user, totpCode)
		if err != nil {
			return "", db.User{}, err
		}
		if !ok {
			return "", db.User{}, ErrTOTPInvalid
		}
	}

	cookie, err := s.startSession(ctx, user, ip, userAgent)
	return cookie, user, err
}

// BeginTOTPEnrollment stores a new (disabled) secret and returns it with the
// otpauth URL. VerifyTOTPEnrollment activates it.
//
// It refuses while two-factor is on. Starting again would replace the live
// secret and switch two-factor off with no code, so anyone holding a session
// could remove it.
func (s *Service) BeginTOTPEnrollment(ctx context.Context, user db.User) (secret, otpauthURL string, err error) {
	fresh, err := s.q.GetUserByID(ctx, user.ID)
	if err != nil {
		return "", "", err
	}
	if fresh.TotpEnabled != 0 {
		return "", "", ErrTOTPEnabled
	}
	secret, err = GenerateTOTPSecret()
	if err != nil {
		return "", "", err
	}
	enc, err := s.box.Encrypt([]byte(secret))
	if err != nil {
		return "", "", err
	}
	if err := s.q.SetUserTOTP(ctx, db.SetUserTOTPParams{
		TotpSecretEnc: enc, TotpEnabled: 0, ID: user.ID,
	}); err != nil {
		return "", "", err
	}
	return secret, TOTPAuthURL(secret, user.Email), nil
}

func (s *Service) VerifyTOTPEnrollment(ctx context.Context, user db.User, code string) error {
	fresh, err := s.q.GetUserByID(ctx, user.ID)
	if err != nil {
		return err
	}
	if len(fresh.TotpSecretEnc) == 0 {
		return errors.New("no enrollment in progress")
	}
	secret, err := s.box.Decrypt(fresh.TotpSecretEnc)
	if err != nil {
		return err
	}
	if !VerifyTOTP(string(secret), code, s.now()) {
		return ErrTOTPInvalid
	}
	return s.q.SetUserTOTP(ctx, db.SetUserTOTPParams{
		TotpSecretEnc: fresh.TotpSecretEnc, TotpEnabled: 1, ID: user.ID,
	})
}

// DisableTOTP turns two-factor off after checking a current code, so a
// stolen session cannot strip the second factor from the account.
func (s *Service) DisableTOTP(ctx context.Context, user db.User, code string) error {
	fresh, err := s.q.GetUserByID(ctx, user.ID)
	if err != nil {
		return err
	}
	if fresh.TotpEnabled != 0 {
		ok, err := s.checkTOTP(fresh, code)
		if err != nil {
			return err
		}
		if !ok {
			return ErrTOTPInvalid
		}
	}
	return s.ResetTOTP(ctx, user.ID)
}

// ResetTOTP removes a user's second factor without a code. Only an admin
// does this, for someone who has lost their authenticator.
func (s *Service) ResetTOTP(ctx context.Context, userID int64) error {
	return s.q.SetUserTOTP(ctx, db.SetUserTOTPParams{TotpSecretEnc: nil, TotpEnabled: 0, ID: userID})
}

// checkTOTP verifies code against the user's enrolled secret.
func (s *Service) checkTOTP(user db.User, code string) (bool, error) {
	secret, err := s.box.Decrypt(user.TotpSecretEnc)
	if err != nil {
		return false, err
	}
	return VerifyTOTP(string(secret), code, s.now()), nil
}

// pendingTOTPTTL is how long a provider sign-in waits for its code.
const pendingTOTPTTL = 5 * time.Minute

// pendingKey signs pending two-factor tickets. It is derived from the
// session key with a fixed label, so a ticket can never pass as a session
// token and a session token can never pass as a ticket.
func (s *Service) pendingKey() []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte("windlass oauth totp pending v1"))
	return mac.Sum(nil)
}

// BeginPendingTOTP is called when a provider sign-in matches an account with
// two-factor on. It returns a short-lived ticket naming the user; no session
// exists until CompletePendingTOTP checks a code against it.
func (s *Service) BeginPendingTOTP(user db.User) (string, error) {
	nonce, err := NewSessionID()
	if err != nil {
		return "", err
	}
	return SignToken(s.pendingKey(), Claims{SessionID: nonce, UserID: user.ID,
		ExpiresAt: s.now().Add(pendingTOTPTTL).Unix()})
}

// CompletePendingTOTP checks the code for a ticket from BeginPendingTOTP and
// starts the session. A missing or wrong code still returns the ticket's
// user, so the failure can be audited against the account.
func (s *Service) CompletePendingTOTP(ctx context.Context, ticket, code, ip, userAgent string) (string, db.User, error) {
	claims, err := VerifyToken(s.pendingKey(), ticket, s.now())
	if err != nil {
		return "", db.User{}, ErrNotAuthenticated
	}
	user, err := s.q.GetUserByID(ctx, claims.UserID)
	if err != nil || user.DisabledAt.Valid {
		return "", db.User{}, ErrNotAuthenticated
	}
	if user.TotpEnabled != 0 {
		if code == "" {
			return "", user, ErrTOTPRequired
		}
		ok, err := s.checkTOTP(user, code)
		if err != nil {
			return "", db.User{}, err
		}
		if !ok {
			return "", user, ErrTOTPInvalid
		}
	}
	cookie, err := s.startSession(ctx, user, ip, userAgent)
	return cookie, user, err
}

// StartSessionFor creates a session directly (OAuth logins, where identity
// was proven by the provider). Callers check TotpEnabled first and use
// BeginPendingTOTP instead when it is set.
func (s *Service) StartSessionFor(ctx context.Context, user db.User, ip, userAgent string) (string, error) {
	return s.startSession(ctx, user, ip, userAgent)
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
