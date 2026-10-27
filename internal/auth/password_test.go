package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/windlass-dev/windlass/internal/store/db"
)

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword(context.Background(), "correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash format: %q", hash)
	}
	if err := VerifyPassword(context.Background(), hash, "correct horse battery staple"); err != nil {
		t.Errorf("verify correct password: %v", err)
	}
	if err := VerifyPassword(context.Background(), hash, "wrong password"); !errors.Is(err, ErrPasswordMismatch) {
		t.Errorf("verify wrong password: %v, want ErrPasswordMismatch", err)
	}
}

func TestHashesAreSalted(t *testing.T) {
	a, _ := HashPassword(context.Background(), "same")
	b, _ := HashPassword(context.Background(), "same")
	if a == b {
		t.Error("identical hashes for identical passwords, missing salt")
	}
}

func TestMalformedHash(t *testing.T) {
	key := "$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	for _, bad := range []string{"", "plaintext", "$argon2id$garbage", "$bcrypt$v=19$m=1,t=1,p=1$AA$BB",
		"$argon2id$v=19$m=19456,t=2,p=1junk" + key,
		"$argon2id$v=19junk$m=19456,t=2,p=1" + key,
		"$argon2id$v=19$m=4194304,t=2,p=1" + key,
		"$argon2id$v=19$m=19456,t=1000,p=1" + key,
		"$argon2id$v=19$m=19456,t=0,p=1" + key,
		"$argon2id$v=19$m=19456,t=2,p=0" + key,
		"$argon2id$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAA",
	} {
		if err := VerifyPassword(context.Background(), bad, "x"); err == nil || errors.Is(err, ErrPasswordMismatch) {
			t.Errorf("VerifyPassword(%q) = %v, want a malformed-hash error", bad, err)
		}
	}
}

// legacyHash makes a hash with the parameters Windlass used before 19 MiB.
func legacyHash(t *testing.T, password string) string {
	t.Helper()
	salt := bytes.Repeat([]byte{7}, argonSaltLen)
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 1, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=65536,t=3,p=1$%s$%s", argon2.Version,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func TestNeedsRehash(t *testing.T) {
	current, err := HashPassword(context.Background(), "pw")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(current, "$m=19456,t=2,p=1$") {
		t.Errorf("new hash parameters: %s", current)
	}
	if NeedsRehash(current) {
		t.Error("current hash needs rehash")
	}
	if NeedsRehash(dummyHash) {
		t.Error("dummy hash is not on the current parameters")
	}
	old := legacyHash(t, "pw")
	if err := VerifyPassword(context.Background(), old, "pw"); err != nil {
		t.Fatalf("legacy hash no longer verifies: %v", err)
	}
	if !NeedsRehash(old) {
		t.Error("legacy hash does not need rehash")
	}
	if NeedsRehash("plaintext") {
		t.Error("malformed hash needs rehash")
	}
}

func TestLoginRehashesLegacyHash(t *testing.T) {
	ctx := context.Background()
	s, q := newTokenTestService(t)
	old := legacyHash(t, "a long password")
	user, err := q.CreateUser(ctx, db.CreateUserParams{Email: "a@example.com", Role: "admin",
		PasswordHash: sql.NullString{String: old, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.Login(ctx, "a@example.com", "wrong password", "", "", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password err = %v", err)
	}
	if got, _ := q.GetUserByID(ctx, user.ID); got.PasswordHash.String != old {
		t.Fatal("a failed login changed the hash")
	}

	if _, _, err := s.Login(ctx, "a@example.com", "a long password", "", "", ""); err != nil {
		t.Fatal(err)
	}
	got, err := q.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PasswordHash.String == old || NeedsRehash(got.PasswordHash.String) {
		t.Fatalf("hash after login = %s", got.PasswordHash.String)
	}
	if _, _, err := s.Login(ctx, "a@example.com", "a long password", "", "", ""); err != nil {
		t.Fatalf("login with the rehashed password: %v", err)
	}
}

func TestRehashKeepsAPasswordChangedMeanwhile(t *testing.T) {
	ctx := context.Background()
	s, q := newTokenTestService(t)
	old := legacyHash(t, "the old password")
	user, err := q.CreateUser(ctx, db.CreateUserParams{Email: "a@example.com", Role: "admin",
		PasswordHash: sql.NullString{String: old, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	// An admin sets a new password after this login verified the old one.
	changed, err := HashPassword(context.Background(), "the new password")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{
		PasswordHash: sql.NullString{String: changed, Valid: true}, ID: user.ID}); err != nil {
		t.Fatal(err)
	}
	s.rehashPassword(ctx, user, "the old password")
	got, err := q.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PasswordHash.String != changed {
		t.Fatal("the rehash overwrote the changed password")
	}
}

func TestHashesWaitForASlot(t *testing.T) {
	for range cap(hashSlots) {
		hashSlots <- struct{}{}
	}
	done := make(chan error, 1)
	go func() { done <- VerifyPassword(context.Background(), dummyHash, "x") }()
	select {
	case <-done:
		t.Fatal("a hash ran with every slot taken")
	case <-time.After(200 * time.Millisecond):
	}
	<-hashSlots
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the hash did not run once a slot was free")
	}
	for range cap(hashSlots) - 1 {
		<-hashSlots
	}
}

// fillSlots takes every hash slot until the test ends.
func fillSlots(t *testing.T) {
	t.Helper()
	for range cap(hashSlots) {
		hashSlots <- struct{}{}
	}
	t.Cleanup(func() {
		for range cap(hashSlots) {
			<-hashSlots
		}
	})
}

func TestHashWaitEnds(t *testing.T) {
	fillSlots(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := VerifyPassword(ctx, dummyHash, "x"); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled request err = %v", err)
	}
	old := hashWait
	hashWait = 50 * time.Millisecond
	t.Cleanup(func() { hashWait = old })
	if _, err := HashPassword(context.Background(), "x"); !errors.Is(err, ErrHashBusy) {
		t.Errorf("full slots err = %v", err)
	}
}

func TestLoginReportsBusy(t *testing.T) {
	ctx := context.Background()
	s, q := newTokenTestService(t)
	hash, err := HashPassword(ctx, "a long password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateUser(ctx, db.CreateUserParams{Email: "a@example.com", Role: "admin",
		PasswordHash: sql.NullString{String: hash, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	fillSlots(t)
	old := hashWait
	hashWait = 50 * time.Millisecond
	t.Cleanup(func() { hashWait = old })
	for _, email := range []string{"a@example.com", "nobody@example.com"} {
		if _, _, err := s.Login(ctx, email, "a long password", "", "", ""); !errors.Is(err, ErrHashBusy) {
			t.Errorf("%s: err = %v, want ErrHashBusy", email, err)
		}
	}
}
