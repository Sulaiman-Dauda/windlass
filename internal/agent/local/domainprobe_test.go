package local

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsLocalAddr(t *testing.T) {
	if !isLocalAddr(net.ParseIP("127.0.0.1")) {
		t.Error("127.0.0.1 is not local")
	}
	// TEST-NET-3 is never assigned to a host.
	if isLocalAddr(net.ParseIP("203.0.113.77")) {
		t.Error("203.0.113.77 is local")
	}
}

func TestServedCert(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "https://")

	// httptest's certificate is for example.com and not in the system roots:
	// it is described, and marked untrusted with the reason.
	info, errText := servedCert(context.Background(), addr, "example.com")
	if info == nil {
		t.Fatalf("no certificate: %s", errText)
	}
	if info.Trusted || info.VerifyError == "" || info.NotAfter.IsZero() {
		t.Errorf("info = %+v", info)
	}

	// Nothing listening: the reason comes back.
	srv.Close()
	if info, errText := servedCert(context.Background(), addr, "example.com"); info != nil || errText == "" {
		t.Errorf("closed = %+v %q", info, errText)
	}
}
