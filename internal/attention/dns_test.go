package attention

import (
	"context"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/store/db"
)

func (e *env) dnsDone(t *testing.T) {
	t.Helper()
	waitFor(t, func() bool {
		e.svc.dnsMu.Lock()
		defer e.svc.dnsMu.Unlock()
		return !e.svc.dnsRunning && !e.svc.dnsExpiry.IsZero()
	})
}

func TestDNSRows(t *testing.T) {
	e := newEnv(t)
	p := e.project(t, "shop")
	for _, h := range []string{"shop.example.com", "www.shop.example.com", "ok.shop.example.com"} {
		if _, err := e.q.CreateDomain(context.Background(), db.CreateDomainParams{ProjectID: p.ID,
			Hostname: h, Service: "web", ContainerPort: 80}); err != nil {
			t.Fatal(err)
		}
	}
	e.agent.Probes = map[string]agent.DomainProbe{
		"shop.example.com":     {NotFound: true, Outbound: "203.0.113.10"},
		"www.shop.example.com": {Addresses: []string{"198.51.100.7"}, Outbound: "203.0.113.10"},
		// Cloudflare cannot be judged, so it is no row.
		"ok.shop.example.com": {Addresses: []string{"104.16.1.1"}, Outbound: "203.0.113.10"},
	}

	if items := e.list(t); len(items) != 0 {
		t.Fatalf("first list = %s", kinds(items))
	}
	e.dnsDone(t)
	items := e.list(t)
	if kinds(items) != "dns:shop" {
		t.Fatalf("items = %s", kinds(items))
	}
	it := items[0]
	if it.Title != "shop: shop.example.com and www.shop.example.com do not point at this server" ||
		it.Tone != ToneWarn || it.Link != "/projects/shop/domains" {
		t.Errorf("row = %+v", it)
	}
}

func TestDNSBehindNATNoRow(t *testing.T) {
	e := newEnv(t)
	p := e.project(t, "shop")
	if _, err := e.q.CreateDomain(context.Background(), db.CreateDomainParams{ProjectID: p.ID,
		Hostname: "shop.example.com", Service: "web", ContainerPort: 80}); err != nil {
		t.Fatal(err)
	}
	e.agent.Probes = map[string]agent.DomainProbe{
		"shop.example.com": {Addresses: []string{"198.51.100.7"}, Outbound: "10.0.0.5"},
	}
	e.list(t)
	e.dnsDone(t)
	if items := e.list(t); len(items) != 0 {
		t.Errorf("behind NAT = %s", kinds(items))
	}
}
