// Package update implements self-update: check GitHub releases, download,
// verify the checksum, atomically replace the running binary, and exit for
// systemd to restart into the new version. Application containers are never
// touched (principle 7).
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/windlass-dev/windlass/internal/version"
)

// Repo is the GitHub repository releases are fetched from.
var Repo = "windlass-dev/windlass"

// Token optionally authenticates GitHub API requests. Required when Repo is
// private; a fine-grained PAT with read access to the repository contents is
// enough.
var Token = ""

type Release struct {
	Version     string `json:"version"`
	CurrentVer  string `json:"current_version"`
	UpdateReady bool   `json:"update_available"`
	Notes       string `json:"notes,omitempty"`

	assetURL    string
	checksumURL string
}

type Service struct {
	logger  *slog.Logger
	dataDir string
	// restart asks the process to shut down gracefully; systemd
	// (Restart=always) brings the new binary up.
	restart func()
}

func New(logger *slog.Logger, dataDir string, restart func()) *Service {
	return &Service{logger: logger, dataDir: dataDir, restart: restart}
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		// APIURL downloads through the assets API, which is the only path
		// that works for private repositories.
		APIURL string `json:"url"`
	} `json:"assets"`
}

// Check queries the latest release.
func (s *Service) Check(ctx context.Context) (Release, error) {
	rel := Release{CurrentVer: version.Version}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return rel, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if Token != "" {
		req.Header.Set("Authorization", "Bearer "+Token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return rel, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return rel, fmt.Errorf("github releases: %s", resp.Status)
	}

	var gh ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&gh); err != nil {
		return rel, err
	}

	rel.Version = gh.TagName
	rel.Notes = gh.Body
	asset := fmt.Sprintf("windlass-linux-%s", runtime.GOARCH)
	for _, a := range gh.Assets {
		u := a.URL
		if Token != "" {
			u = a.APIURL
		}
		switch a.Name {
		case asset:
			rel.assetURL = u
		case "checksums.txt":
			rel.checksumURL = u
		}
	}
	rel.UpdateReady = rel.Version != "" &&
		rel.Version != version.Version &&
		"v"+strings.TrimPrefix(version.Version, "v") != rel.Version &&
		rel.assetURL != ""
	return rel, nil
}

var ErrNotSupported = errors.New("self-update is only supported for the Linux binary install")

// Apply downloads, verifies, and swaps the binary, then restarts.
func (s *Service) Apply(ctx context.Context) error {
	if runtime.GOOS != "linux" || os.Getenv("WINDLASS_NO_SELF_UPDATE") != "" {
		return ErrNotSupported
	}
	rel, err := s.Check(ctx)
	if err != nil {
		return err
	}
	if !rel.UpdateReady {
		return errors.New("no update available")
	}
	if rel.checksumURL == "" {
		return errors.New("release has no checksums.txt; refusing unverified update")
	}

	dir := filepath.Join(s.dataDir, "data", "update")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	newBin := filepath.Join(dir, "windlass-"+rel.Version)

	s.logger.Info("downloading update", "version", rel.Version)
	if err := download(ctx, rel.assetURL, newBin); err != nil {
		return err
	}

	// Verify against checksums.txt.
	sums, err := fetch(ctx, rel.checksumURL)
	if err != nil {
		return err
	}
	want := ""
	assetName := "windlass-linux-" + runtime.GOARCH
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == assetName {
			want = fields[0]
		}
	}
	if want == "" {
		return errors.New("checksum for this platform missing from checksums.txt")
	}
	got, err := fileSHA256(newBin)
	if err != nil {
		return err
	}
	if got != want {
		os.Remove(newBin)
		return fmt.Errorf("checksum mismatch: got %s want %s", got, want)
	}
	if err := os.Chmod(newBin, 0o755); err != nil {
		return err
	}

	// Atomic swap: keep the old binary for one-command rollback.
	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return err
	}
	prev := self + ".previous"
	os.Remove(prev)
	if err := os.Link(self, prev); err != nil {
		s.logger.Warn("could not keep previous binary", "error", err)
	}
	if err := os.Rename(newBin, self); err != nil {
		return fmt.Errorf("swap binary (is %s on the same filesystem?): %w", dir, err)
	}

	s.logger.Info("update installed; restarting", "version", rel.Version)
	go func() {
		time.Sleep(500 * time.Millisecond) // let the HTTP response flush
		s.restart()
	}()
	return nil
}

// authHeaders makes asset requests work against private repositories: the
// assets API returns the binary only with the octet-stream accept type.
func authHeaders(req *http.Request) {
	if Token != "" {
		req.Header.Set("Authorization", "Bearer "+Token)
		req.Header.Set("Accept", "application/octet-stream")
	}
}

func download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	authHeaders(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: %s", resp.Status)
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

func fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	authHeaders(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
