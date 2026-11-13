// Package attention lists what on the server needs a person: a project whose
// last deploy failed, a disk nearly full, a project with volumes and no
// backup schedule, Caddy not answering, a panel update. The dashboard shows
// the list; alerts send the same conditions when they change.
//
// It only reads. One check costs a handful of SQLite queries, one container
// list, one host reading and one Caddy admin call, never a Docker or Compose
// call per project, and the result is cached so a dashboard polling it adds
// little.
package attention

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/internal/update"
)

// Kinds of item. Alerts key their debounce on Kind and Project.
const (
	KindDeployFailed = "deploy_failed"
	KindDisk         = "disk"
	KindNoBackup     = "no_backup"
	KindProxy        = "proxy"
	KindUpdate       = "update"
)

// Tones, most urgent first. The list is sorted by them.
const (
	ToneError = "error"
	ToneWarn  = "warn"
	ToneInfo  = "info"
)

// DiskPercentSetting is the settings key for the disk warning threshold, a
// whole percentage of the projects volume. DefaultDiskPercent applies when
// it is unset or not a number from 50 to 99.
const (
	DiskPercentSetting = "attention_disk_percent"
	DefaultDiskPercent = 85
)

// Item is one row. Title is the line a person reads first; Detail says what
// it means or what to do; Link and Action are where to go and the button's
// words.
type Item struct {
	Kind    string `json:"kind"`
	Tone    string `json:"tone"`
	Project string `json:"project,omitempty"`
	Title   string `json:"title"`
	Detail  string `json:"detail,omitempty"`
	Link    string `json:"link,omitempty"`
	Action  string `json:"action,omitempty"`
}

// List is the result of one check.
type List struct {
	CheckedAt time.Time `json:"checked_at"`
	Items     []Item    `json:"items"`
}

// UpdateChecker is the part of the update service the list needs.
type UpdateChecker interface {
	Check(ctx context.Context) (update.Release, error)
}

// PanelDomainer reports the panel's own hostname, "" when it has none.
type PanelDomainer interface {
	PanelDomain(ctx context.Context) (string, error)
}

// Service builds and caches the list.
type Service struct {
	q      *db.Queries
	agent  agent.Agent
	panel  PanelDomainer
	update UpdateChecker
	logger *slog.Logger

	// TTL is how long a list is served from cache. UpdateTTL is how long an
	// update check is kept, and UpdateRetry how long a failed one waits.
	TTL         time.Duration
	UpdateTTL   time.Duration
	UpdateRetry time.Duration

	mu     sync.Mutex
	cached List
	expiry time.Time

	upMu      sync.Mutex
	release   update.Release
	upExpiry  time.Time
	upRunning bool
}

// New returns a Service. update may be nil, which leaves the update row out.
func New(q *db.Queries, a agent.Agent, panel PanelDomainer, up UpdateChecker, logger *slog.Logger) *Service {
	return &Service{q: q, agent: a, panel: panel, update: up, logger: logger,
		TTL: 30 * time.Second, UpdateTTL: 6 * time.Hour, UpdateRetry: time.Hour}
}

// buildTimeout bounds one check of every source.
const buildTimeout = 20 * time.Second

// List returns the current list, from cache when it is fresh.
func (s *Service) List(ctx context.Context) (List, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Now().Before(s.expiry) {
		return s.cached, nil
	}
	// The list is cached for every user, so one closed tab must not cancel
	// the check and leave a false "Caddy is not answering" for the TTL.
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), buildTimeout)
	defer cancel()
	list, err := s.build(bctx)
	if err != nil {
		return List{}, err
	}
	s.cached, s.expiry = list, time.Now().Add(s.TTL)
	return list, nil
}

// Invalidate drops the cached list, so the next List checks again.
func (s *Service) Invalidate() {
	s.mu.Lock()
	s.expiry = time.Time{}
	s.mu.Unlock()
}

func (s *Service) build(ctx context.Context) (List, error) {
	list := List{CheckedAt: time.Now().UTC(), Items: []Item{}}

	// SQLite is the panel's own state: if it fails there is no list to give.
	failed, err := s.q.ListLatestFailedDeployments(ctx)
	if err != nil {
		return List{}, fmt.Errorf("list failed deployments: %w", err)
	}
	for _, f := range failed {
		list.Items = append(list.Items, deployItem(f))
	}
	projects, err := s.q.ListProjects(ctx)
	if err != nil {
		return List{}, fmt.Errorf("list projects: %w", err)
	}
	schedules, err := s.q.ListDueSchedules(ctx)
	if err != nil {
		return List{}, fmt.Errorf("list backup schedules: %w", err)
	}
	domains, err := s.q.ListAllDomains(ctx)
	if err != nil {
		return List{}, fmt.Errorf("list domains: %w", err)
	}

	// Each outside source degrades on its own: Docker being down must not
	// hide a full disk.
	if m, err := s.agent.Host().Metrics(ctx); err == nil {
		if item, ok := diskItem(m, s.diskPercent(ctx)); ok {
			list.Items = append(list.Items, item)
		}
	} else {
		s.logger.Debug("attention: host metrics", "error", err)
	}
	if containers, err := s.agent.Docker().ListContainers(ctx, agent.ContainerFilter{}); err == nil {
		list.Items = append(list.Items, backupItems(projects, schedules, containers)...)
	} else {
		s.logger.Debug("attention: list containers", "error", err)
	}
	if item, ok := s.proxyItem(ctx, len(domains) > 0); ok {
		list.Items = append(list.Items, item)
	}
	if item, ok := s.updateItem(); ok {
		list.Items = append(list.Items, item)
	}

	rank := map[string]int{ToneError: 0, ToneWarn: 1, ToneInfo: 2}
	sort.SliceStable(list.Items, func(i, j int) bool {
		return rank[list.Items[i].Tone] < rank[list.Items[j].Tone]
	})
	return list, nil
}

// stepWords turns a deploy step into the words after "failed".
var stepWords = map[string]string{
	"preparing": "while preparing",
	"syncing":   "while syncing Git",
	"pulling":   "while pulling images",
	"building":  "while building",
	"applying":  "while starting services",
	"verifying": "health checks",
}

func deployItem(f db.ListLatestFailedDeploymentsRow) Item {
	title := fmt.Sprintf("%s: deploy #%d failed", f.ProjectName, f.Number)
	if words, ok := stepWords[f.FailedStep.String]; ok {
		title += " " + words
		if f.FailedStep.String == "verifying" {
			title = fmt.Sprintf("%s: deploy #%d failed its health checks", f.ProjectName, f.Number)
		}
	}
	reason := f.ErrorLine.String
	if reason == "" {
		reason = f.Error.String
	}
	reason = firstLine(reason)
	var detail string
	switch {
	case f.LiveNumber > 0:
		detail = fmt.Sprintf("The last successful deploy is #%d.", f.LiveNumber)
	default:
		detail = "No deploy of this project has succeeded yet."
	}
	if reason != "" {
		detail = strings.TrimRight(reason, ".") + ". " + detail
	}
	return Item{Kind: KindDeployFailed, Tone: ToneError, Project: f.ProjectName, Title: title,
		Detail: detail, Link: "/projects/" + f.ProjectName + "/deployments", Action: "View deploy"}
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	const max = 240
	if len(s) > max {
		s = s[:max] + "..."
	}
	return s
}

func (s *Service) diskPercent(ctx context.Context) int {
	raw, err := s.q.GetSetting(ctx, DiskPercentSetting)
	if err != nil {
		return DefaultDiskPercent
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 50 || n > 99 {
		return DefaultDiskPercent
	}
	return n
}

func diskItem(m agent.HostMetrics, percent int) (Item, bool) {
	if m.DiskTotal == 0 {
		return Item{}, false
	}
	used := m.DiskUsed * 100 / m.DiskTotal
	if used < uint64(percent) {
		return Item{}, false
	}
	return Item{Kind: KindDisk, Tone: ToneWarn,
		Title:  fmt.Sprintf("Disk is %d%% full", used),
		Detail: fmt.Sprintf("%s free of %s on the projects volume.", gb(m.DiskTotal-m.DiskUsed), gb(m.DiskTotal)),
		Link:   "/settings/system", Action: "Disk use"}, true
}

func gb(b uint64) string {
	return fmt.Sprintf("%.1f GB", float64(b)/1e9)
}

// backupItems names each project, staging copies aside, that has containers
// mounting volumes and no enabled backup schedule.
func backupItems(projects []db.Project, schedules []db.ListDueSchedulesRow, containers []agent.Container) []Item {
	scheduled := map[string]bool{}
	for _, s := range schedules {
		scheduled[s.ProjectName] = true
	}
	volumes := map[string]map[string]bool{}
	for _, c := range containers {
		for _, v := range c.Volumes {
			if volumes[c.ComposeProject] == nil {
				volumes[c.ComposeProject] = map[string]bool{}
			}
			volumes[c.ComposeProject][v] = true
		}
	}
	var items []Item
	for _, p := range projects {
		if p.StagingOf.Valid || scheduled[p.Name] || len(volumes[p.Name]) == 0 {
			continue
		}
		names := make([]string, 0, len(volumes[p.Name]))
		for v := range volumes[p.Name] {
			names = append(names, strings.TrimPrefix(v, p.Name+"_"))
		}
		sort.Strings(names)
		title, noun := p.Name+" has a volume and no backup schedule", "Volume"
		if len(names) > 1 {
			title, noun = p.Name+" has volumes and no backup schedule", "Volumes"
		}
		items = append(items, Item{Kind: KindNoBackup, Tone: ToneInfo, Project: p.Name, Title: title,
			Detail: noun + " " + strings.Join(names, ", ") +
				". A scheduled backup keeps the project files and a dump of each database it finds.",
			Link: "/projects/" + p.Name + "/backups", Action: "Set up backups"})
	}
	return items
}

// proxyItem reports Caddy not answering, but only when something depends on
// it: a project domain or the panel's own.
func (s *Service) proxyItem(ctx context.Context, haveDomains bool) (Item, bool) {
	if !haveDomains && s.panel != nil {
		if host, err := s.panel.PanelDomain(ctx); err == nil && host != "" {
			haveDomains = true
		}
	}
	if !haveDomains {
		return Item{}, false
	}
	info, err := s.agent.Proxy().Available(ctx)
	if err == nil && info.Available {
		return Item{}, false
	}
	return Item{Kind: KindProxy, Tone: ToneError, Title: "Caddy is not answering",
		Detail: "Windlass cannot reach the Caddy admin API, so domain and HTTPS changes are not applied. " +
			"Check that the caddy service is running.",
		Link: "/settings/general", Action: "Panel settings"}, true
}

// updateItem reads the cached update check and starts a new one in the
// background when it is stale, so a slow GitHub never holds up the list.
func (s *Service) updateItem() (Item, bool) {
	if s.update == nil {
		return Item{}, false
	}
	s.upMu.Lock()
	rel, stale := s.release, time.Now().After(s.upExpiry)
	if stale && !s.upRunning {
		s.upRunning = true
		go s.checkUpdate()
	}
	s.upMu.Unlock()
	if !rel.UpdateReady {
		return Item{}, false
	}
	detail := "This server runs " + rel.CurrentVer + ". Deployed apps keep running while the panel restarts."
	if !rel.ApplySupported {
		detail = "This server runs " + rel.CurrentVer + ". This install updates outside the panel."
	}
	return Item{Kind: KindUpdate, Tone: ToneInfo, Title: "Windlass " + rel.Version + " is available",
		Detail: detail, Link: "/settings/system", Action: "Update"}, true
}

func (s *Service) checkUpdate() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rel, err := s.update.Check(ctx)
	s.upMu.Lock()
	defer s.upMu.Unlock()
	s.upRunning = false
	if err != nil {
		s.logger.Debug("attention: update check", "error", err)
		s.upExpiry = time.Now().Add(s.UpdateRetry)
		return
	}
	s.release, s.upExpiry = rel, time.Now().Add(s.UpdateTTL)
}
