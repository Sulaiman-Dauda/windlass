package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	box, err := Load(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	plaintext := []byte("DATABASE_URL=postgres://user:pass@host/db")
	ct, err := box.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(ct, []byte("postgres")) {
		t.Error("ciphertext leaks plaintext")
	}

	got, err := box.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("round trip = %q", got)
	}
}

func TestTamperDetected(t *testing.T) {
	box, _ := Load(filepath.Join(t.TempDir(), "secret.key"))
	ct, _ := box.Encrypt([]byte("secret"))
	ct[len(ct)-1] ^= 0xff
	if _, err := box.Decrypt(ct); err == nil {
		t.Error("tampered ciphertext decrypted without error")
	}
}

func TestKeyPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.key")

	box1, err := Load(path)
	if err != nil {
		t.Fatalf("Load 1: %v", err)
	}
	ct, _ := box1.Encrypt([]byte("hello"))

	// A second Load must reuse the same key.
	box2, err := Load(path)
	if err != nil {
		t.Fatalf("Load 2: %v", err)
	}
	got, err := box2.Decrypt(ct)
	if err != nil || string(got) != "hello" {
		t.Errorf("decrypt with reloaded key: %q, %v", got, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if info.Size() != 32 {
		t.Errorf("key size = %d, want 32", info.Size())
	}
}

func TestUniqueNonces(t *testing.T) {
	box, _ := Load(filepath.Join(t.TempDir(), "secret.key"))
	a, _ := box.Encrypt([]byte("same"))
	b, _ := box.Encrypt([]byte("same"))
	if bytes.Equal(a, b) {
		t.Error("identical ciphertexts for identical plaintexts, nonce reuse")
	}
}
