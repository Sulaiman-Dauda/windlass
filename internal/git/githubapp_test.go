package git

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
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

// TestConvertAppManifestPath pins the conversion endpoint. The singular
// /app-manifest/ 404s, and a 404 here is indistinguishable from an expired
// code, so the flow failed silently at the last step once already.
func TestConvertAppManifestPath(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id": 42, "slug": "windlass-test", "client_id": "cid",
			"client_secret": "csec", "webhook_secret": "whsec", "pem": "KEY",
			"owner": map[string]string{"login": "acme"},
		})
	}))
	defer srv.Close()

	s, _ := testService(t)
	s.api = &providerAPI{githubBase: srv.URL}
	cfg, err := s.exchangeManifest(context.Background(), "tempcode")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientID != "cid" || cfg.PEM != "KEY" || cfg.Owner != "acme" || cfg.ID != 42 {
		t.Errorf("credentials not carried through: %+v", cfg)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/app-manifests/tempcode/conversions" {
		t.Errorf("path = %q, want /app-manifests/tempcode/conversions", gotPath)
	}
}

// TestConvertAppManifestReportsBody keeps GitHub's explanation in the error;
// without it every refusal looks the same in the log.
func TestConvertAppManifestReportsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"message":"Code has expired"}`))
	}))
	defer srv.Close()

	s, _ := testService(t)
	s.api = &providerAPI{githubBase: srv.URL}
	_, err := s.exchangeManifest(context.Background(), "stale")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "422") || !strings.Contains(err.Error(), "Code has expired") {
		t.Errorf("error lost GitHub's detail: %v", err)
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

func testAppKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}

// TestFetchAppIdentity reads the app's current name and owner, which change
// when the app is renamed or transferred to an organisation.
func TestFetchAppIdentity(t *testing.T) {
	var gotPath, gotMethod, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotAuth = r.URL.Path, r.Method, r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]any{
			"slug": "acme-windlass", "html_url": "https://github.com/apps/acme-windlass",
			"owner": map[string]string{"login": "acme-org"},
		})
	}))
	defer srv.Close()

	s, _ := testService(t)
	s.api = &providerAPI{githubBase: srv.URL}
	id, err := s.fetchAppIdentity(context.Background(), AppConfig{ID: 7, PEM: testAppKey(t)})
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodGet || gotPath != "/app" {
		t.Errorf("request = %s %s, want GET /app", gotMethod, gotPath)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") || strings.Count(gotAuth, ".") != 2 {
		t.Errorf("app JWT not sent: %q", gotAuth)
	}
	if id.Slug != "acme-windlass" || id.Owner.Login != "acme-org" ||
		id.HTMLURL != "https://github.com/apps/acme-windlass" {
		t.Errorf("identity not parsed: %+v", id)
	}
}

func TestFetchAppIdentityRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	s, _ := testService(t)
	s.api = &providerAPI{githubBase: srv.URL}
	if _, err := s.fetchAppIdentity(context.Background(), AppConfig{ID: 7, PEM: testAppKey(t)}); err == nil ||
		!strings.Contains(err.Error(), "401") {
		t.Errorf("expected a 401 error, got %v", err)
	}
}

// TestWithIdentity updates only the display fields, and reports no change
// when GitHub agrees with what is stored so nothing is rewritten.
func TestWithIdentity(t *testing.T) {
	stored := AppConfig{
		ID: 7, Slug: "old-name", Owner: "someone", HTMLURL: "https://github.com/apps/old-name",
		ClientID: "cid", ClientSecret: "csec", WebhookSecret: "whsec", PEM: "KEY",
	}
	var id appIdentity
	id.Slug, id.Owner.Login, id.HTMLURL = "new-name", "acme-org", "https://github.com/apps/new-name"

	next, changed := withIdentity(stored, id)
	if !changed {
		t.Error("rename not detected")
	}
	if next.Slug != "new-name" || next.Owner != "acme-org" || next.HTMLURL != "https://github.com/apps/new-name" {
		t.Errorf("display fields not updated: %+v", next)
	}
	if next.ID != 7 || next.ClientID != "cid" || next.ClientSecret != "csec" ||
		next.WebhookSecret != "whsec" || next.PEM != "KEY" {
		t.Errorf("credentials changed: %+v", next)
	}

	if _, changed := withIdentity(next, id); changed {
		t.Error("unchanged identity reported as a change")
	}
}
