package update

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/version"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// stubReleases answers the GitHub latest-release call with a newer version
// that carries a binary for this architecture.
func stubReleases(t *testing.T) {
	t.Helper()
	body := `{"tag_name":"v9999.0","assets":[` +
		`{"name":"windlass-linux-` + runtime.GOARCH + `","browser_download_url":"https://example.invalid/bin"},` +
		`{"name":"checksums.txt","browser_download_url":"https://example.invalid/sums"}]}`
	prev := http.DefaultClient.Transport
	http.DefaultClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			t.Errorf("unexpected request %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})
	t.Cleanup(func() { http.DefaultClient.Transport = prev })
}

// asRelease makes this test binary report a released version, since "dev"
// is never offered an update.
func asRelease(t *testing.T, v string) {
	t.Helper()
	prev := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = prev })
}

func checkJSON(t *testing.T) map[string]any {
	t.Helper()
	asRelease(t, "v0.62")
	rel, err := New(slog.New(slog.DiscardHandler), t.TempDir(), func() {}).Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(rel)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out["update_available"] != true {
		t.Fatalf("update_available = %v, want true", out["update_available"])
	}
	return out
}

func TestCheckReportsApplyUnsupportedWhenSelfUpdateDisabled(t *testing.T) {
	stubReleases(t)
	t.Setenv("WINDLASS_NO_SELF_UPDATE", "1")
	if got := checkJSON(t)["apply_supported"]; got != false {
		t.Fatalf("apply_supported = %v, want false", got)
	}
}

func TestCheckReportsApplySupportedOnLinuxBinaryInstall(t *testing.T) {
	stubReleases(t)
	t.Setenv("WINDLASS_NO_SELF_UPDATE", "")
	want := runtime.GOOS == "linux"
	if got := checkJSON(t)["apply_supported"]; got != want {
		t.Fatalf("apply_supported = %v, want %v", got, want)
	}
}

func TestApplyRefusesWhenSelfUpdateDisabled(t *testing.T) {
	// Stubbed so a regressed guard fails here instead of downloading a real
	// release over the test binary.
	stubReleases(t)
	t.Setenv("WINDLASS_NO_SELF_UPDATE", "1")
	err := New(slog.New(slog.DiscardHandler), t.TempDir(), func() { t.Error("restart called") }).Apply(context.Background())
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("Apply = %v, want ErrNotSupported", err)
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.63", "v0.62", true},
		{"v0.63", "0.62", true},
		{"v0.100", "v0.99", true},
		{"v0.99", "v0.100", false},
		{"v0.62", "v0.62", false},
		{"v0.62", "0.62", false},
		{"v0.61", "v0.62", false},
		{"v0.62.1", "v0.62", true},
		{"v0.62", "v0.62.0", false},
		{"v1.0", "v0.99", true},
		{"v0.63", "dev", false},
		{"v0.63", "", false},
		{"", "v0.62", false},
		{"nightly", "v0.62", false},
		{"v0.63-rc1", "v0.62", true},
	}
	for _, c := range cases {
		if got := Newer(c.latest, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestDevBuildIsNeverOfferedAnUpdate(t *testing.T) {
	stubReleases(t)
	asRelease(t, "dev")
	rel, err := New(slog.New(slog.DiscardHandler), t.TempDir(), func() {}).Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.UpdateReady {
		t.Fatal("dev build offered an update")
	}
}

func TestOlderReleaseIsNotOffered(t *testing.T) {
	stubReleases(t) // serves v9999.0
	asRelease(t, "v10000.0")
	rel, err := New(slog.New(slog.DiscardHandler), t.TempDir(), func() {}).Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.UpdateReady {
		t.Fatal("older release offered as an update")
	}
}
