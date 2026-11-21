package proxy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
)

func TestDNSCheck(t *testing.T) {
	const pub = "203.0.113.10"
	for _, tc := range []struct {
		name   string
		probe  agent.DomainProbe
		result string
		has    string
	}{
		{"points here", agent.DomainProbe{Addresses: []string{pub}, Local: []string{pub}, Outbound: pub}, CheckOK, "Points at this server"},
		{"no record", agent.DomainProbe{NotFound: true, Outbound: pub}, CheckFail, "no A or AAAA record"},
		{"lookup failed", agent.DomainProbe{DNSError: "i/o timeout", Outbound: pub}, CheckUnknown, "i/o timeout"},
		{"elsewhere", agent.DomainProbe{Addresses: []string{"198.51.100.7"}, Outbound: pub}, CheckFail, "not this server"},
		{"cloudflare", agent.DomainProbe{Addresses: []string{"104.16.1.1", "2606:4700::1"}, Outbound: pub}, CheckUnknown, "Cloudflare"},
		{"half here", agent.DomainProbe{Addresses: []string{pub, "198.51.100.7"}, Local: []string{pub}, Outbound: pub}, CheckWarn, "also at 198.51.100.7"},
		{"here and cloudflare", agent.DomainProbe{Addresses: []string{pub, "104.16.1.1"}, Local: []string{pub}, Outbound: pub}, CheckWarn, "around Cloudflare"},
		{"behind NAT", agent.DomainProbe{Addresses: []string{"198.51.100.7"}, Outbound: "10.0.0.5"}, CheckUnknown, "private (10.0.0.5)"},
		{"CGNAT", agent.DomainProbe{Addresses: []string{"198.51.100.7"}, Outbound: "100.70.1.1"}, CheckUnknown, "private"},
	} {
		c := DNSCheck(tc.probe)
		if c.Result != tc.result || !strings.Contains(c.Detail, tc.has) {
			t.Errorf("%s: %s %q", tc.name, c.Result, c.Detail)
		}
	}
}

func TestCertCheck(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	cert := func(days int, trusted bool) agent.DomainProbe {
		return agent.DomainProbe{Cert: &agent.CertInfo{Issuer: "Let's Encrypt R11",
			NotAfter: now.Add(time.Duration(days) * 24 * time.Hour), Trusted: trusted, VerifyError: "unknown authority"}}
	}
	for _, tc := range []struct {
		name   string
		probe  agent.DomainProbe
		result string
		has    string
	}{
		{"fine", cert(60, true), CheckOK, "valid until 20/01/2027"},
		{"renewal failing", cert(10, true), CheckWarn, "in 10 days"},
		{"expired", cert(-1, true), CheckFail, "expired on 20/11/2026"},
		{"untrusted", cert(60, false), CheckWarn, "unknown authority"},
		{"none", agent.DomainProbe{CertError: "Caddy has no certificate for this hostname yet"}, CheckFail, "no certificate for this hostname"},
	} {
		c := certCheck(tc.probe, now)
		if c.Result != tc.result || !strings.Contains(c.Detail, tc.has) {
			t.Errorf("%s: %s %q", tc.name, c.Result, c.Detail)
		}
	}
}

func TestPortsCheck(t *testing.T) {
	for _, tc := range []struct {
		l80, l443 bool
		result    string
	}{{true, true, CheckOK}, {false, true, CheckWarn}, {true, false, CheckFail}, {false, false, CheckFail}} {
		if c := portsCheck(agent.DomainProbe{Listen80: tc.l80, Listen443: tc.l443}); c.Result != tc.result {
			t.Errorf("80=%v 443=%v: %s", tc.l80, tc.l443, c.Result)
		}
	}
}

func TestCheckDomains(t *testing.T) {
	s, ag, q := newTestService(t)
	seedRoutedDomain(t, s, ag, q)
	ctx := context.Background()
	p, err := q.GetProjectByName(ctx, "shop")
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.CheckDomains(ctx, p.ID, "shop", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Hostname != "shop.example.com" || got[0].Result != CheckOK || len(got[0].Checks) != 5 {
		t.Fatalf("healthy = %+v", got)
	}
	if got[0].CertExpiry == nil {
		t.Error("no certificate expiry")
	}

	// The container stops: the container check fails and so does the whole.
	ag.Containers[0].State = "exited"
	got, _ = s.CheckDomains(ctx, p.ID, "shop", "SHOP.example.com")
	if got[0].Result != CheckFail || got[0].Checks[4].Result != CheckFail {
		t.Errorf("stopped = %+v", got[0].Checks)
	}

	// Caddy down: the route cannot be read.
	ag.ProxyAvailable = false
	got, _ = s.CheckDomains(ctx, p.ID, "shop", "")
	if got[0].Checks[3].Name != CheckRoute || got[0].Checks[3].Result != CheckFail {
		t.Errorf("proxy down = %+v", got[0].Checks[3])
	}

	// A probe error still gives a full row.
	ag.Fail["host.probedomain"] = errors.New("boom")
	got, _ = s.CheckDomains(ctx, p.ID, "shop", "")
	if len(got[0].Checks) != 5 || got[0].Checks[0].Result != CheckUnknown {
		t.Errorf("probe error = %+v", got[0].Checks)
	}

	if _, err := s.CheckDomains(ctx, p.ID, "shop", "other.example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown hostname = %v", err)
	}
}
