package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Windlass sessions are revocable: the cookie holds a compact HS256 JWT whose
// only claim of substance is the session id; every request still hits the
// sessions table, so deleting the row kills the session immediately. The JWT
// layer exists to make cookies self-authenticating (tamper-evident) before
// the database is consulted. HMAC is hand-rolled on stdlib per principle 10 —
// a JWT dependency isn't justified for sign+verify of one claim shape.

type Claims struct {
	SessionID string `json:"sid"`
	UserID    int64  `json:"uid"`
	ExpiresAt int64  `json:"exp"`
}

var jwtHeader = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

func SignToken(key []byte, c Claims) (string, error) {
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	signing := jwtHeader + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(signing))
	return signing + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

var ErrInvalidToken = errors.New("invalid token")

func VerifyToken(key []byte, token string, now time.Time) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != jwtHeader {
		return Claims{}, ErrInvalidToken
	}

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(got, want) {
		return Claims{}, ErrInvalidToken
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return Claims{}, ErrInvalidToken
	}
	if c.ExpiresAt <= now.Unix() {
		return Claims{}, fmt.Errorf("%w: expired", ErrInvalidToken)
	}
	return c, nil
}

// NewSessionID returns a random 128-bit id, hex-encoded. The id itself is
// stored server-side; it reaches the client only inside the signed token.
func NewSessionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
