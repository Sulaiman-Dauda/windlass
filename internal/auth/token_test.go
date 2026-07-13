package auth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func TestTokenRoundTrip(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	claims := Claims{SessionID: "abc123", UserID: 42, ExpiresAt: now.Add(time.Hour).Unix()}

	token, err := SignToken(testKey, claims)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	got, err := VerifyToken(testKey, token, now)
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
	if got != claims {
		t.Errorf("claims = %+v, want %+v", got, claims)
	}
}

func TestExpiredToken(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	token, _ := SignToken(testKey, Claims{SessionID: "x", ExpiresAt: now.Add(-time.Minute).Unix()})
	if _, err := VerifyToken(testKey, token, now); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expired token verified: %v", err)
	}
}

func TestTamperedToken(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	token, _ := SignToken(testKey, Claims{SessionID: "abc", UserID: 1, ExpiresAt: now.Add(time.Hour).Unix()})

	// Flip a character in the payload segment.
	parts := strings.Split(token, ".")
	payload := []byte(parts[1])
	payload[0] ^= 1
	tampered := parts[0] + "." + string(payload) + "." + parts[2]

	if _, err := VerifyToken(testKey, tampered, now); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("tampered token verified: %v", err)
	}
}

func TestWrongKey(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	token, _ := SignToken(testKey, Claims{SessionID: "abc", ExpiresAt: now.Add(time.Hour).Unix()})
	otherKey := []byte("ffffffffffffffffffffffffffffffff")
	if _, err := VerifyToken(otherKey, token, now); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("token verified with wrong key: %v", err)
	}
}

func TestNewSessionIDUnique(t *testing.T) {
	a, err := NewSessionID()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewSessionID()
	if a == b {
		t.Error("session ids collide")
	}
	if len(a) != 32 {
		t.Errorf("id length = %d, want 32 hex chars", len(a))
	}
}
