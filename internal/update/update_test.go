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

func checkJSON(t *testing.T) map[string]any {
	t.Helper()
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
	t.Setenv("WINDLASS_NO_SELF_UPDATE", "1")
	err := New(slog.New(slog.DiscardHandler), t.TempDir(), func() { t.Error("restart called") }).Apply(context.Background())
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("Apply = %v, want ErrNotSupported", err)
	}
}
