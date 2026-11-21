package local

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
)

func TestHTTPCheckDialKeepsHost(t *testing.T) {
	var host, path, proto string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, path, proto = r.Host, r.URL.RequestURI(), r.Header.Get("X-Forwarded-Proto")
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	res, err := hostLocal{}.HTTPCheck(context.Background(), agent.HTTPCheckReq{
		URL: "https://staging.example.com/health?x=1", Dial: addr})
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || host != "staging.example.com" || path != "/health?x=1" || proto != "https" {
		t.Errorf("status %d host %q path %q proto %q", res.StatusCode, host, path, proto)
	}

	if _, err := (hostLocal{}).HTTPCheck(context.Background(), agent.HTTPCheckReq{
		URL: "https://staging.example.com/", Dial: "example.com:80"}); err == nil {
		t.Error("a hostname dial address was accepted")
	}
}

func TestHTTPCheckDialFallsBackToURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="Staging"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	// Port 1 on loopback refuses the connection, as an unreachable
	// container network does.
	res, err := hostLocal{}.HTTPCheck(context.Background(), agent.HTTPCheckReq{URL: srv.URL + "/health", Dial: "127.0.0.1:1"})
	if err != nil || !res.BehindPassword || res.StatusCode != 401 {
		t.Fatalf("fallback = %+v, %v", res, err)
	}
	// Without Dial a 401 is just a 401.
	res, err = hostLocal{}.HTTPCheck(context.Background(), agent.HTTPCheckReq{URL: srv.URL + "/health"})
	if err != nil || res.BehindPassword {
		t.Fatalf("plain check = %+v, %v", res, err)
	}
}
