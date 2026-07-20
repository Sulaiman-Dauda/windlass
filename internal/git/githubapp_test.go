package git

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
)

func TestAppJWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	token, err := appJWT(12345, string(pemKey))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT has %d segments", len(parts))
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
		Iss string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Iss != "12345" || claims.Exp <= claims.Iat {
		t.Errorf("bad claims: %+v", claims)
	}

	// The signature must verify with the public key over header.payload.
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
		t.Errorf("signature does not verify: %v", err)
	}
}

func TestAppJWTBadKey(t *testing.T) {
	if _, err := appJWT(1, "not a pem"); err == nil {
		t.Error("invalid PEM accepted")
	}
}

func TestInstallationSentinel(t *testing.T) {
	s := installationSentinel(42)
	if !isInstallation(s) {
		t.Errorf("sentinel %q not recognized", s)
	}
	var sent appSentinel
	if err := json.Unmarshal([]byte(s), &sent); err != nil || sent.InstallationID != 42 {
		t.Errorf("sentinel round-trip failed: %v %+v", err, sent)
	}
	if isInstallation("ghp_sometoken") || isInstallation("") {
		t.Error("plain token misidentified as installation")
	}
}
