package git

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store/db"
)

func testService(t *testing.T) (*Service, *secrets.Box) {
	t.Helper()
	box, err := secrets.New(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(nil, box, logger), box
}

func projectWithSecret(t *testing.T, box *secrets.Box, secret string) db.Project {
	t.Helper()
	enc, err := box.Encrypt([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return db.Project{ID: 1, Name: "app", WebhookSecretEnc: enc}
}

func TestVerifyGitHubWebhook(t *testing.T) {
	s, box := testService(t)
	p := projectWithSecret(t, box, "whsec")
	body := []byte(`{"ref":"refs/heads/main"}`)

	mac := hmac.New(sha256.New, []byte("whsec"))
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if err := s.VerifyWebhook(p, "github", body, good); err != nil {
		t.Errorf("valid signature rejected: %v", err)
	}
	for _, bad := range []string{"", "sha256=deadbeef", good + "x"} {
		if err := s.VerifyWebhook(p, "github", body, bad); !errors.Is(err, ErrInvalidSignature) {
			t.Errorf("signature %q accepted: %v", bad, err)
		}
	}
	// Body tamper invalidates the signature.
	if err := s.VerifyWebhook(p, "github", []byte(`{"ref":"refs/heads/evil"}`), good); !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("tampered body accepted: %v", err)
	}
}

func TestVerifyGitLabWebhook(t *testing.T) {
	s, box := testService(t)
	p := projectWithSecret(t, box, "whsec")

	if err := s.VerifyWebhook(p, "gitlab", nil, "whsec"); err != nil {
		t.Errorf("valid token rejected: %v", err)
	}
	if err := s.VerifyWebhook(p, "gitlab", nil, "wrong"); !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("wrong token accepted: %v", err)
	}
}

func TestVerifyWebhookNoSecret(t *testing.T) {
	s, _ := testService(t)
	if err := s.VerifyWebhook(db.Project{Name: "app"}, "github", nil, "sha256=x"); err == nil {
		t.Error("project without webhook secret accepted a webhook")
	}
}

func TestPushBranch(t *testing.T) {
	if got := PushBranch("refs/heads/main"); got != "main" {
		t.Errorf("got %q", got)
	}
	if got := PushBranch("refs/heads/feat/x"); got != "feat/x" {
		t.Errorf("got %q", got)
	}
}
