package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash format: %q", hash)
	}
	if err := VerifyPassword(hash, "correct horse battery staple"); err != nil {
		t.Errorf("verify correct password: %v", err)
	}
	if err := VerifyPassword(hash, "wrong password"); !errors.Is(err, ErrPasswordMismatch) {
		t.Errorf("verify wrong password: %v, want ErrPasswordMismatch", err)
	}
}

func TestHashesAreSalted(t *testing.T) {
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Error("identical hashes for identical passwords, missing salt")
	}
}

func TestMalformedHash(t *testing.T) {
	for _, bad := range []string{"", "plaintext", "$argon2id$garbage", "$bcrypt$v=19$m=1,t=1,p=1$AA$BB"} {
		if err := VerifyPassword(bad, "x"); err == nil {
			t.Errorf("VerifyPassword(%q) = nil, want error", bad)
		}
	}
}
