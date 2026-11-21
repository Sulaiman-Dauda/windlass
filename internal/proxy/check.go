package proxy

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
)

// Check results.
const (
	CheckOK      = "ok"
	CheckWarn    = "warn"
	CheckFail    = "fail"
	CheckUnknown = "unknown"
)

// Check names, in the order a person reads them.
const (
	CheckDNS       = "dns"
	CheckPorts     = "ports"
	CheckCert      = "certificate"
	CheckRoute     = "route"
	CheckContainer = "container"
)

// CertWarnDays is how close to expiry a certificate becomes a warning.
// Caddy renews at 30 days left, so under 14 means renewal is failing.
const CertWarnDays = 14

// Check is one line of a domain check.
type Check struct {
	Name   string `json:"name"`
	Result string `json:"result"`
	Detail string `json:"detail"`
}

// DomainCheck is every check for one domain.
type DomainCheck struct {
	Hostname   string     `json:"hostname"`
	Service    string     `json:"service"`
	Result     string     `json:"result"`
	Checks     []Check    `json:"checks"`
	Addresses  []string   `json:"addresses"`
	CertExpiry *time.Time `json:"cert_expiry,omitempty"`
	CheckedAt  time.Time  `json:"checked_at"`
}

// probeParallel caps the probes one check runs at once.
const probeParallel = 4

// CheckDomains checks every domain of a project, or only hostname when it
// is not empty. ErrNotFound means the project has no such domain.
func (s *Service) CheckDomains(ctx context.Context, projectID int64, projectName, hostname string) ([]DomainCheck, error) {
	rows, err := s.q.ListProjectDomains(ctx, projectID)
	if err != nil {
		return nil, err
	}
	hostname = normalHostname(hostname)
	type target struct{ host, service string }
	var targets []target
	for _, r := range rows {
		if hostname == "" || r.Hostname == hostname {
			targets = append(targets, target{r.Hostname, r.Service})
		}
	}
	if hostname != "" && len(targets) == 0 {
		return nil, ErrNotFound
	}

	available, routed := s.routingStatus(ctx)
	running := map[string]bool{}
	containersErr := error(nil)
	if cs, err := s.agent.Docker().ListContainers(ctx, agent.ContainerFilter{ComposeProject: projectName}); err == nil {
		for _, c := range cs {
			if c.State == "running" {
				running[c.ComposeService] = true
			}
		}
	} else {
		containersErr = err
	}

	out := make([]DomainCheck, len(targets))
	sem := make(chan struct{}, probeParallel)
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			probe, err := s.agent.Host().ProbeDomain(ctx, t.host)
			if err != nil {
				probe = agent.DomainProbe{DNSError: err.Error(), CertError: err.Error()}
			}
			dc := DomainCheck{Hostname: t.host, Service: t.service, Addresses: probe.Addresses,
				CheckedAt: time.Now().UTC()}
			if dc.Addresses == nil {
				dc.Addresses = []string{}
			}
			if probe.Cert != nil {
				na := probe.Cert.NotAfter
				dc.CertExpiry = &na
			}
			dc.Checks = []Check{
				DNSCheck(probe),
				portsCheck(probe),
				certCheck(probe, time.Now()),
				routeCheck(available, routed[t.host]),
				containerCheck(t.service, running[t.service], containersErr),
			}
			dc.Result = worst(dc.Checks)
			out[i] = dc
		}()
	}
	wg.Wait()
	return out, nil
}

func worst(cs []Check) string {
	rank := map[string]int{CheckOK: 0, CheckUnknown: 1, CheckWarn: 2, CheckFail: 3}
	w := CheckOK
	for _, c := range cs {
		if rank[c.Result] > rank[w] {
			w = c.Result
		}
	}
	return w
}

// DNSCheck says whether the hostname's DNS points at this server. It is
// exported for the Needs you sweep, which runs the same rule.
func DNSCheck(p agent.DomainProbe) Check {
	c := Check{Name: CheckDNS}
	switch {
	case p.NotFound:
		c.Result, c.Detail = CheckFail, "The hostname has no A or AAAA record. Add one pointing at this server."
		return c
	case p.DNSError != "" && len(p.Addresses) == 0:
		c.Result, c.Detail = CheckUnknown, "The DNS lookup failed: "+p.DNSError
		return c
	case len(p.Addresses) == 0:
		c.Result, c.Detail = CheckFail, "The hostname has no A or AAAA record. Add one pointing at this server."
		return c
	}
	local := map[string]bool{}
	for _, a := range p.Local {
		local[a] = true
	}
	var other, cloudflare []string
	for _, a := range p.Addresses {
		switch {
		case local[a]:
		case IsCloudflare(a):
			cloudflare = append(cloudflare, a)
		default:
			other = append(other, a)
		}
	}
	answers := strings.Join(p.Addresses, ", ")
	switch {
	case len(other) == 0 && len(cloudflare) == 0:
		c.Result, c.Detail = CheckOK, "Points at this server ("+answers+")."
	case len(other) == 0 && len(p.Local) == 0:
		c.Result, c.Detail = CheckUnknown, "Proxied by Cloudflare ("+answers+"), so DNS does not show "+
			"which server is behind it. Check the record in Cloudflare points at this server."
	case len(p.Local) > 0:
		c.Result, c.Detail = CheckWarn, "Points at this server and also at "+strings.Join(other, ", ")+
			", which is not this server. Visitors sent there will not reach this project."
		if len(other) == 0 {
			c.Detail = "Points at this server and also through Cloudflare (" + strings.Join(cloudflare, ", ") +
				"). Mixing the two sends some visitors around Cloudflare."
		}
	case behindNAT(p.Outbound):
		c.Result, c.Detail = CheckUnknown, fmt.Sprintf("Points at %s. This server's own address is private (%s), "+
			"so its public address cannot be read here. Check the record matches it.", answers, p.Outbound)
	default:
		c.Result, c.Detail = CheckFail, "Points at "+answers+", which is not this server. Change the record "+
			"to this server's address."
	}
	return c
}

// behindNAT reports a private or carrier-grade NAT source address.
func behindNAT(outbound string) bool {
	ip := net.ParseIP(outbound)
	if ip == nil {
		return true // unknown: do not claim the record is wrong
	}
	return ip.IsPrivate() || cgnat.Contains(ip)
}

var cgnat = mustCIDR("100.64.0.0/10")

func portsCheck(p agent.DomainProbe) Check {
	c := Check{Name: CheckPorts}
	switch {
	case p.Listen80 && p.Listen443:
		c.Result, c.Detail = CheckOK, "Caddy accepts connections on 80 and 443. This is checked from the "+
			"server, so a firewall in front of it is not tested."
	case p.Listen443:
		c.Result, c.Detail = CheckWarn, "Nothing accepts connections on port 80, so HTTP does not redirect "+
			"and the HTTP certificate challenge cannot run."
	case p.Listen80:
		c.Result, c.Detail = CheckFail, "Nothing accepts connections on port 443, so HTTPS cannot be served."
	default:
		c.Result, c.Detail = CheckFail, "Nothing accepts connections on 80 or 443. Check that Caddy is running."
	}
	return c
}

func certCheck(p agent.DomainProbe, now time.Time) Check {
	c := Check{Name: CheckCert}
	if p.Cert == nil {
		c.Result, c.Detail = CheckFail, "No certificate: "+p.CertError+". Caddy gets one once DNS points here "+
			"and ports 80 and 443 are open."
		return c
	}
	days := int(p.Cert.NotAfter.Sub(now).Hours() / 24)
	expiry := p.Cert.NotAfter.Format("02/01/2006")
	switch {
	case !now.Before(p.Cert.NotAfter):
		c.Result, c.Detail = CheckFail, "The certificate from "+p.Cert.Issuer+" expired on "+expiry+"."
	case !p.Cert.Trusted:
		c.Result, c.Detail = CheckWarn, "The certificate from "+p.Cert.Issuer+" is not trusted by browsers: "+
			p.Cert.VerifyError+"."
	case days < CertWarnDays:
		c.Result, c.Detail = CheckWarn, fmt.Sprintf("The certificate expires on %s, in %d days. Caddy renews "+
			"at 30 days left, so renewal is failing: check DNS and ports.", expiry, days)
	default:
		c.Result, c.Detail = CheckOK, fmt.Sprintf("Trusted certificate from %s, valid until %s.", p.Cert.Issuer, expiry)
	}
	return c
}

func routeCheck(available, routed bool) Check {
	c := Check{Name: CheckRoute}
	switch {
	case !available:
		c.Result, c.Detail = CheckFail, "Caddy's admin API is not answering, so the route cannot be read."
	case routed:
		c.Result, c.Detail = CheckOK, "Caddy has the route."
	default:
		c.Result, c.Detail = CheckFail, "Caddy has no route for this hostname. Windlass adds it once the service "+
			"has a running container."
	}
	return c
}

func containerCheck(service string, running bool, err error) Check {
	c := Check{Name: CheckContainer}
	switch {
	case err != nil:
		c.Result, c.Detail = CheckUnknown, "Docker could not be read: "+err.Error()
	case running:
		c.Result, c.Detail = CheckOK, "The "+service+" container is running."
	default:
		c.Result, c.Detail = CheckFail, "No running container for the "+service+" service. Deploy the project."
	}
	return c
}

// cloudflareRanges is Cloudflare's published proxy ranges
// (cloudflare.com/ips), which change rarely.
var cloudflareRanges = func() []*net.IPNet {
	var out []*net.IPNet
	for _, s := range []string{
		"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18",
		"108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17",
		"162.158.0.0/15", "104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
		"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32",
		"2a06:98c0::/29", "2c0f:f248::/32",
	} {
		out = append(out, mustCIDR(s))
	}
	return out
}()

// IsCloudflare reports whether addr is in a Cloudflare proxy range.
func IsCloudflare(addr string) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	for _, n := range cloudflareRanges {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}
