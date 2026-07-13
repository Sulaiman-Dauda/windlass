// Package secrets encrypts sensitive values (env vars, tokens, TOTP seeds)
// at rest with AES-256-GCM. The key is a random 32-byte file in the data
// directory, created on first use with 0600 permissions. Ciphertexts are
// nonce-prefixed: nonce || sealed.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const keySize = 32

type Box struct {
	aead cipher.AEAD
}

// Load reads the key file at path, generating it if absent.
func Load(path string) (*Box, error) {
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, keySize)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		// Write atomically so a crash can't leave a truncated key.
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, key, 0o600); err != nil {
			return nil, err
		}
		if err := os.Rename(tmp, path); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}

	return New(key)
}

func New(key []byte) (*Box, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("secret key must be %d bytes, got %d", keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (b *Box) Decrypt(ciphertext []byte) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(ciphertext) < ns {
		return nil, errors.New("ciphertext too short")
	}
	return b.aead.Open(nil, ciphertext[:ns], ciphertext[ns:], nil)
}
