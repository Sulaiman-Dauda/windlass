package attention

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/windlass-dev/windlass/internal/proxy"
	"github.com/windlass-dev/windlass/internal/store/db"
)

// KindDNS is a project with a domain whose DNS does not point at this
// server.
const KindDNS = "dns"

// DNSTTL is how long one DNS sweep is kept. A sweep probes every domain,
// so it runs in the background, never inside a List.
const DNSTTL = 10 * time.Minute

// dnsTimeout bounds one sweep of every domain.
const dnsTimeout = 2 * time.Minute

// dnsItems returns the rows from the last sweep and starts a new one in
// the background when it is stale. The first List after start has none.
func (s *Service) dnsItems(domains []db.ListAllDomainsRow) []Item {
	s.dnsMu.Lock()
	defer s.dnsMu.Unlock()
	if time.Now().After(s.dnsExpiry) && !s.dnsRunning {
		s.dnsRunning = true
		go s.sweepDNS(domains)
	}
	return s.dnsRows
}

func (s *Service) sweepDNS(domains []db.ListAllDomainsRow) {
	ctx, cancel := context.WithTimeout(context.Background(), dnsTimeout)
	defer cancel()
	byProject := map[string][]string{}
	for _, d := range domains {
		p, err := s.agent.Host().ProbeDomain(ctx, d.Hostname)
		if err != nil {
			s.logger.Debug("attention: probe domain", "hostname", d.Hostname, "error", err)
			continue
		}
		if c := proxy.DNSCheck(p); c.Result == proxy.CheckFail || c.Result == proxy.CheckWarn {
			byProject[d.ProjectName] = append(byProject[d.ProjectName], d.Hostname)
		}
	}
	rows := dnsRows(byProject)
	s.dnsMu.Lock()
	s.dnsRows, s.dnsExpiry, s.dnsRunning = rows, time.Now().Add(DNSTTL), false
	s.dnsMu.Unlock()
	s.Invalidate()
}

// dnsRows makes one row per project, so alerts debounce per project.
func dnsRows(byProject map[string][]string) []Item {
	projects := make([]string, 0, len(byProject))
	for p := range byProject {
		projects = append(projects, p)
	}
	sort.Strings(projects)
	var rows []Item
	for _, p := range projects {
		hosts := byProject[p]
		sort.Strings(hosts)
		title := p + ": " + hosts[0] + " does not point at this server"
		if len(hosts) > 1 {
			title = p + ": " + names(hosts) + " do not point at this server"
		}
		rows = append(rows, Item{Kind: KindDNS, Tone: ToneWarn, Project: p, Title: title,
			Detail: "Visitors cannot reach the project at " + strings.Join(hosts, ", ") +
				" and Caddy cannot get a certificate until the DNS record points here. The domain check says what DNS answers.",
			Link: "/projects/" + p + "/domains", Action: "Check domains"})
	}
	return rows
}
