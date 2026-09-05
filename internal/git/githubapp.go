package git

// GitHub App support: the two-click manifest flow creates an app whose
// credentials arrive by API instead of copy-paste, installations act as git
// connections (repo access scoped to what the user picked), and the app's
// own webhook delivers push events for every covered repo, no per-repo
// webhook registration at all.
//
// Hand-rolled on net/http + crypto like the rest of the git/auth code: the
// whole surface is a JWT, three REST calls, and an HMAC check (principle 10).

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/windlass-dev/windlass/internal/store/db"
)

const appConfigKey = "githubapp"

// AppConfig is everything the manifest conversion returns, stored encrypted
// as one settings row.
type AppConfig struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	Owner         string `json:"owner"`
	HTMLURL       string `json:"html_url"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	WebhookSecret string `json:"webhook_secret"`
	PEM           string `json:"pem"`
}

var ErrNoApp = errors.New("no GitHub App configured")

// AppConfigured reports whether a GitHub App exists without decrypting it.
func (s *Service) AppConfig(ctx context.Context) (AppConfig, error) {
	raw, err := s.q.GetSetting(ctx, appConfigKey)
	if errors.Is(err, sql.ErrNoRows) {
		return AppConfig{}, ErrNoApp
	}
	if err != nil {
		return AppConfig{}, err
	}
	var wrapped struct {
		Enc string `json:"enc"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapped); err != nil {
		return AppConfig{}, err
	}
	encBytes, err := hex.DecodeString(wrapped.Enc)
	if err != nil {
		return AppConfig{}, err
	}
	plain, err := s.box.Decrypt(encBytes)
	if err != nil {
		return AppConfig{}, err
	}
	var cfg AppConfig
	err = json.Unmarshal(plain, &cfg)
	return cfg, err
}

func (s *Service) saveAppConfig(ctx context.Context, cfg AppConfig) error {
	plain, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	enc, err := s.box.Encrypt(plain)
	if err != nil {
		return err
	}
	wrapped, _ := json.Marshal(map[string]string{"enc": hex.EncodeToString(enc)})
	return s.q.SetSetting(ctx, db.SetSettingParams{Key: appConfigKey, Value: string(wrapped)})
}

// ConvertAppManifest exchanges the temporary code GitHub hands back after
// the admin clicks "Create GitHub App" for the app's credentials, and
// stores them. Returns the stored config.
func (s *Service) ConvertAppManifest(ctx context.Context, code string) (AppConfig, error) {
	cfg, err := s.exchangeManifest(ctx, code)
	if err != nil {
		return AppConfig{}, err
	}
	if err := s.saveAppConfig(ctx, cfg); err != nil {
		return AppConfig{}, err
	}
	return cfg, nil
}

// exchangeManifest performs the credential exchange with GitHub.
func (s *Service) exchangeManifest(ctx context.Context, code string) (AppConfig, error) {
	ctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()

	// Note the plural: /app-manifests/{code}/conversions. The singular form
	// 404s, which reads like an expired code rather than a wrong path.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.api.githubBase+"/app-manifests/"+code+"/conversions", nil)
	if err != nil {
		return AppConfig{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return AppConfig{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		// GitHub explains the refusal in the body; without it every failure
		// looks identical in the log.
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return AppConfig{}, fmt.Errorf("manifest conversion failed (HTTP %d): %s",
			resp.StatusCode, strings.TrimSpace(string(detail)))
	}

	var gh struct {
		ID    int64  `json:"id"`
		Slug  string `json:"slug"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		HTMLURL       string `json:"html_url"`
		ClientID      string `json:"client_id"`
		ClientSecret  string `json:"client_secret"`
		WebhookSecret string `json:"webhook_secret"`
		PEM           string `json:"pem"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gh); err != nil {
		return AppConfig{}, err
	}
	if gh.ClientID == "" || gh.PEM == "" {
		return AppConfig{}, errors.New("manifest conversion returned incomplete credentials")
	}
	return AppConfig{
		ID: gh.ID, Slug: gh.Slug, Owner: gh.Owner.Login, HTMLURL: gh.HTMLURL,
		ClientID: gh.ClientID, ClientSecret: gh.ClientSecret,
		WebhookSecret: gh.WebhookSecret, PEM: gh.PEM,
	}, nil
}

// ---------------------------------------------------------------------------
// App JWT and installation tokens

// appJWT builds the short-lived RS256 JWT GitHub Apps authenticate with.
func appJWT(appID int64, pemKey string) (string, error) {
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return "", errors.New("invalid app private key")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse app private key: %w", err)
	}

	b64 := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	now := time.Now()
	header := b64([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload := b64(fmt.Appendf(nil, `{"iat":%d,"exp":%d,"iss":"%d"}`,
		now.Add(-60*time.Second).Unix(), now.Add(9*time.Minute).Unix(), appID))
	signingInput := header + "." + payload
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + b64(sig), nil
}

// installationToken mints a one-hour token for an installation.
func (s *Service) installationToken(ctx context.Context, installationID int64) (string, error) {
	cfg, err := s.AppConfig(ctx)
	if err != nil {
		return "", err
	}
	jwt, err := appJWT(cfg.ID, cfg.PEM)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", s.api.githubBase, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("installation token request failed (HTTP %d)", resp.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Token == "" {
		return "", errors.New("installation token response unreadable")
	}
	return out.Token, nil
}

// ---------------------------------------------------------------------------
// Installation-backed connections
//
// An installation connection stores a sentinel JSON payload where a PAT
// would go; resolveToken swaps it for a fresh installation token whenever
// the connection is used. No schema change, and callers never see the
// difference.

type appSentinel struct {
	InstallationID int64 `json:"github_app_installation"`
}

func installationSentinel(id int64) string {
	b, _ := json.Marshal(appSentinel{InstallationID: id})
	return string(b)
}

// resolveToken turns a stored credential into a usable bearer token.
func (s *Service) resolveToken(ctx context.Context, provider, stored string) (string, error) {
	if provider == "github" && strings.HasPrefix(stored, `{"github_app_installation"`) {
		var sent appSentinel
		if err := json.Unmarshal([]byte(stored), &sent); err != nil || sent.InstallationID == 0 {
			return "", errors.New("corrupt installation connection")
		}
		return s.installationToken(ctx, sent.InstallationID)
	}
	return stored, nil
}

// isInstallation reports whether a stored credential is an app sentinel.
func isInstallation(stored string) bool {
	return strings.HasPrefix(stored, `{"github_app_installation"`)
}

// CreateInstallationConnection verifies an installation belongs to our app
// and stores it as a git connection named after the account it covers.
func (s *Service) CreateInstallationConnection(ctx context.Context, installationID int64) (Connection, error) {
	cfg, err := s.AppConfig(ctx)
	if err != nil {
		return Connection{}, err
	}
	jwt, err := appJWT(cfg.ID, cfg.PEM)
	if err != nil {
		return Connection{}, err
	}
	url := fmt.Sprintf("%s/app/installations/%d", s.api.githubBase, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Connection{}, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Connection{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Connection{}, fmt.Errorf("installation not found for this app (HTTP %d)", resp.StatusCode)
	}
	var inst struct {
		Account struct {
			Login string `json:"login"`
		} `json:"account"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&inst); err != nil || inst.Account.Login == "" {
		return Connection{}, errors.New("installation response unreadable")
	}
	return s.UpsertConnection(ctx, "github", "github-app-"+inst.Account.Login,
		installationSentinel(installationID))
}

// listInstallationRepos lists the repositories an installation covers.
func (s *Service) listInstallationRepos(ctx context.Context, token string) ([]Repo, error) {
	ctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()

	var out []Repo
	for page := 1; page <= repoPageLimit; page++ {
		url := fmt.Sprintf("%s/installation/repositories?per_page=100&page=%d", s.api.githubBase, page)
		resp, err := s.api.do(ctx, "github", http.MethodGet, url, token, nil)
		if err != nil {
			return nil, err
		}
		var body struct {
			Repositories []struct {
				FullName      string `json:"full_name"`
				CloneURL      string `json:"clone_url"`
				DefaultBranch string `json:"default_branch"`
				Private       bool   `json:"private"`
			} `json:"repositories"`
		}
		if err := decodeJSON(resp, "github", &body); err != nil {
			return nil, err
		}
		for _, r := range body.Repositories {
			out = append(out, Repo(r))
		}
		if len(body.Repositories) < 100 {
			break
		}
	}
	return out, nil
}

// VerifyAppWebhook checks a GitHub App webhook delivery against the app's
// webhook secret (X-Hub-Signature-256, same HMAC scheme as repo hooks).
func (s *Service) VerifyAppWebhook(ctx context.Context, body []byte, signatureHeader string) error {
	cfg, err := s.AppConfig(ctx)
	if err != nil {
		return err
	}
	if cfg.WebhookSecret == "" {
		return errors.New("app has no webhook secret")
	}
	want := "sha256=" + hmacHex([]byte(cfg.WebhookSecret), body)
	if !hmacEqual(want, signatureHeader) {
		return ErrInvalidSignature
	}
	return nil
}
