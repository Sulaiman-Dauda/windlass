// Package staging makes a project's staging copy and reports how the two
// compare. The link itself lives in the staging project's manifest
// (projects), and promotion is a deployment (deploy); this package runs the
// steps that need both, and Git and the proxy as well.
package staging

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/dbdetect"
	"github.com/windlass-dev/windlass/internal/deploy"
	"github.com/windlass-dev/windlass/internal/git"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/proxy"
	"github.com/windlass-dev/windlass/internal/store/db"
)

// DiskHeadroom is the free space a copy must leave behind, so staging never
// fills the disk live runs on.
const DiskHeadroom = 1 << 30

var (
	// ErrBlocked refuses a create the preflight found a blocker in.
	ErrBlocked = errors.New("staging cannot be created")
	// ErrNotAcknowledged refuses a create that would keep values reaching
	// the outside world without the person saying they checked them.
	ErrNotAcknowledged = errors.New("confirm the flagged values first")
)

type Service struct {
	q        *db.Queries
	agent    agent.Agent
	projects *projects.Service
	deploy   *deploy.Service
	git      *git.Service
	proxy    *proxy.Service
	logger   *slog.Logger
}

func New(q *db.Queries, ag agent.Agent, proj *projects.Service, dep *deploy.Service, gitSvc *git.Service,
	px *proxy.Service, logger *slog.Logger) *Service {
	return &Service{q: q, agent: ag, projects: proj, deploy: dep, git: gitSvc, proxy: px, logger: logger}
}

// DomainMap pairs one of live's domains with the staging domain that takes
// its place.
type DomainMap struct {
	Live    string `json:"live"`
	Staging string `json:"staging"`
}

// Request is what the person chose in the create dialog.
type Request struct {
	Branch  string      `json:"branch"`
	Domains []DomainMap `json:"domains"`
	// Env replaces values in the copied environment. Keys not named keep
	// live's value, with live's hostnames rewritten.
	Env         map[string]string `json:"env"`
	CopyData    bool              `json:"copy_data"`
	AutoPromote bool              `json:"auto_promote"`
	// Acknowledged confirms the flagged values were checked. It is required
	// only when there are flagged values.
	Acknowledged bool `json:"acknowledged"`
}

// FlaggedEnv is an environment value that probably reaches real people or
// services, shown so it can be changed before staging uses it.
type FlaggedEnv struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// DataItem is one thing a data copy moves, with its size when known.
type DataItem struct {
	Kind    string `json:"kind"` // "files" | "volume" | "database"
	Name    string `json:"name"`
	Service string `json:"service,omitempty"`
	Bytes   int64  `json:"bytes"`
	Known   bool   `json:"known"`
}

// Preflight is everything the create dialog shows before anything is made.
type Preflight struct {
	Live     string                     `json:"live"`
	Staging  string                     `json:"staging"`
	Branch   string                     `json:"branch,omitempty"`
	Git      bool                       `json:"git"`
	Blockers []projects.Issue           `json:"blockers"`
	Warnings []projects.Issue           `json:"warnings"`
	Flagged  []FlaggedEnv               `json:"flagged_env"`
	Domains  []DomainMap                `json:"domains"`
	Rewrites []projects.HostnameRewrite `json:"rewrites"`
	Data     []DataItem                 `json:"data"`
	// FreeBytes are what the projects directory and Docker have free; nil
	// when the server cannot tell, as inside a container.
	ProjectsFreeBytes *uint64 `json:"projects_free_bytes"`
	DockerFreeBytes   *uint64 `json:"docker_free_bytes"`
	CanCreate         bool    `json:"can_create"`
}

// Preflight checks live for a staging copy. req may be empty, in which case
// the defaults are checked: live's branch, staging- domains and a data copy.
func (s *Service) Preflight(ctx context.Context, liveName string, req Request) (Preflight, error) {
	return s.preflight(ctx, liveName, req, nil, 0)
}

func (s *Service) preflight(ctx context.Context, liveName string, req Request, retained *retainedSettings, replacingProjectID int64) (Preflight, error) {
	live, err := s.projects.Get(ctx, liveName)
	if err != nil {
		return Preflight{}, err
	}
	pf := Preflight{Live: live.Name, Staging: projects.StagingName(live.Name),
		Blockers: []projects.Issue{}, Warnings: []projects.Issue{}, Flagged: []FlaggedEnv{},
		Data: []DataItem{}, Git: live.Source == "git" && live.GitRepo.Valid}
	block := func(msg, fix string) {
		pf.Blockers = append(pf.Blockers, projects.Issue{Message: msg, Fix: fix})
	}

	if !projects.CanHaveStaging(live.Name) {
		block(fmt.Sprintf("%s cannot have a staging", live.Name),
			fmt.Sprintf("Staging is named after live, so live's name must be 55 characters or fewer and not end in %s.",
				projects.StagingSuffix))
		return pf, nil
	}
	if live.StagingOf.Valid {
		block(fmt.Sprintf("%s is itself the staging of %s", live.Name, live.StagingOf.String), "Create staging from the live project.")
		return pf, nil
	}
	replacing := false
	if current, ok, err := s.projects.StagingFor(ctx, live.Name); err != nil {
		return Preflight{}, err
	} else if ok {
		if replacingProjectID == 0 || current.ID != replacingProjectID {
			block(fmt.Sprintf("%s already has a staging", live.Name), "Open it, or delete it to start again.")
			return pf, nil
		}
		replacing = true
	}
	exists, err := s.projectExists(ctx, pf.Staging)
	if err != nil {
		return Preflight{}, err
	}
	if exists && !replacing {
		block(fmt.Sprintf("a project named %s already exists and is not linked to %s", pf.Staging, live.Name),
			"Rename or delete it first.")
	}

	if pf.Git {
		pf.Branch = req.Branch
		if pf.Branch == "" {
			pf.Branch = live.GitBranch.String
		}
	}

	cfg, err := s.agent.Compose().Config(ctx, live.Name)
	if err != nil {
		block("live's compose file does not resolve: "+err.Error(), "Fix the compose file, then try again.")
		return pf, nil
	}
	if !pf.Git {
		for name, svc := range cfg.Services {
			if svc.Build {
				block(fmt.Sprintf("%s builds on this server and %s is not connected to Git", name, live.Name),
					"Connect live to Git, so staging and live build the same commit.")
				break
			}
		}
	}
	blockers, warnings := projects.CheckIsolation(cfg, live.Name)
	pf.Blockers = append(pf.Blockers, blockers...)
	pf.Warnings = append(pf.Warnings, warnings...)

	liveDomains, err := s.projects.Domains(ctx, live.Name)
	if err != nil {
		return Preflight{}, err
	}
	pf.Domains = domainMap(liveDomains, req.Domains)
	if retained != nil {
		pf.Domains = make([]DomainMap, 0, len(retained.domains))
		for _, d := range retained.domains {
			pf.Domains = append(pf.Domains, DomainMap{Staging: d.Hostname})
		}
	}
	for _, d := range pf.Domains {
		if d.Staging == "" {
			block(fmt.Sprintf("%s has no staging domain", d.Live), "Give it one, such as "+DefaultHostname(d.Live)+".")
			continue
		}
		if strings.EqualFold(d.Staging, d.Live) {
			block(fmt.Sprintf("%s is live's domain", d.Staging), "Give staging a domain of its own.")
			continue
		}
		if err := s.proxy.CheckHostnameForProject(ctx, d.Staging, replacingProjectID); err != nil {
			block(err.Error(), "Choose another staging domain.")
		}
		if retained == nil {
			pf.Rewrites = append(pf.Rewrites, projects.HostnameRewrite{From: d.Live, To: strings.ToLower(d.Staging)})
		}
	}
	if retained != nil {
		pf.Rewrites = append([]projects.HostnameRewrite{}, retained.rewrites...)
	}
	if pf.Rewrites == nil {
		pf.Rewrites = []projects.HostnameRewrite{}
	}
	hosts := make([]string, 0, len(liveDomains))
	for _, d := range liveDomains {
		hosts = append(hosts, d.Hostname)
	}
	// Staging's health checks are live's with the hostnames rewritten, so
	// only a URL the rewrite misses still points at live.
	stagingCfg := cfg
	stagingCfg.HealthChecks = make([]agent.ApplicationHealthCheck, len(cfg.HealthChecks))
	for i, hc := range cfg.HealthChecks {
		hc.URL, _ = projects.RewriteHostnames(hc.URL, pf.Rewrites)
		stagingCfg.HealthChecks[i] = hc
	}
	pf.Blockers = append(pf.Blockers, projects.HealthURLsOnHosts(stagingCfg, hosts)...)

	env, err := s.plannedEnv(ctx, live.Name, pf.Rewrites, req.Env, retained)
	if err != nil {
		return Preflight{}, err
	}
	for _, key := range projects.OutsideWorldKeys(env) {
		pf.Flagged = append(pf.Flagged, FlaggedEnv{Key: key, Value: env[key]})
	}
	liveEnv, err := s.projects.GetEnv(ctx, live.Name)
	if err != nil {
		return Preflight{}, err
	}
	pf.Warnings = append(pf.Warnings, valuesOutsideEnv(cfg, liveEnv)...)

	if err := s.dataPlan(ctx, &pf, live.Name, cfg); err != nil {
		return Preflight{}, err
	}
	pf.CanCreate = len(pf.Blockers) == 0
	return pf, nil
}

// dataPlan lists what a copy moves and blocks when it would not fit.
func (s *Service) dataPlan(ctx context.Context, pf *Preflight, live string, cfg agent.ResolvedConfig) error {
	files, err := s.agent.FS().Usage(ctx, live, projects.StagingSkip)
	if err != nil {
		return err
	}
	pf.Data = append(pf.Data, DataItem{Kind: "files", Name: "project files", Bytes: files, Known: true})

	sizes, err := s.agent.Docker().VolumeSizes(ctx)
	if err != nil {
		s.logger.Warn("staging preflight: volume sizes", "project", live, "error", err)
		sizes = map[string]int64{}
	}
	containers, err := s.agent.Docker().ListContainers(ctx, agent.ContainerFilter{ComposeProject: live})
	if err != nil {
		return err
	}
	dbContainer, engine := dbdetect.Best(containers)
	dbVolumes := map[string]bool{}
	chosen := ""
	if engine != "" {
		if svc, ok := cfg.Services[dbContainer.ComposeService]; ok {
			chosen = dbContainer.ComposeService
			var bytes int64
			known := true
			for _, m := range svc.Mounts {
				if m.Type != "volume" {
					continue
				}
				dbVolumes[m.Source] = true
				size, ok := sizes[cfg.Volumes[m.Source].Name]
				known = known && ok
				bytes += size
			}
			pf.Data = append(pf.Data, DataItem{Kind: "database", Name: engine, Service: dbContainer.ComposeService,
				Bytes: bytes, Known: known})
			if dbContainer.State != "running" {
				pf.Warnings = append(pf.Warnings, projects.Issue{Service: dbContainer.ComposeService,
					Message: "is not running, so its data cannot be copied",
					Fix:     "Start live before creating staging, or create staging without data."})
			}
		}
	}
	otherDB := dbdetect.OtherVolumes(cfg, chosen)
	keys := make([]string, 0, len(cfg.Volumes))
	for key := range cfg.Volumes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var volumes int64
	for _, key := range keys {
		v := cfg.Volumes[key]
		if v.External || dbVolumes[key] {
			continue
		}
		if svc := otherDB[key]; svc != "" {
			pf.Warnings = append(pf.Warnings, projects.Issue{Service: svc,
				Message: fmt.Sprintf("is a second database, so its volume %s is not copied and staging's starts empty", key),
				Fix:     "Load its data into staging by hand once staging is running."})
			continue
		}
		size, ok := sizes[v.Name]
		pf.Data = append(pf.Data, DataItem{Kind: "volume", Name: key, Bytes: size, Known: ok})
		volumes += size
	}
	for _, item := range pf.Data {
		if item.Kind == "database" {
			volumes += item.Bytes
		}
	}

	if free, err := s.agent.FS().FreeSpace(ctx); err == nil {
		pf.ProjectsFreeBytes = &free
	}
	if free, err := s.agent.Docker().FreeSpace(ctx); err == nil {
		pf.DockerFreeBytes = &free
	}
	need := func(where string, bytes int64, free *uint64) {
		if free != nil && bytes > 0 && uint64(bytes)+DiskHeadroom > *free {
			pf.Blockers = append(pf.Blockers, projects.Issue{
				Message: fmt.Sprintf("the copy needs %s on the %s disk and %s is free, which would leave less than 1 GiB",
					humanBytes(uint64(bytes)), where, humanBytes(*free)),
				Fix: "Free some space, or create staging without data."})
		}
	}
	// One disk usually holds both, and then the two must fit together.
	if pf.ProjectsFreeBytes != nil && pf.DockerFreeBytes != nil && *pf.ProjectsFreeBytes == *pf.DockerFreeBytes {
		need("server's", files+volumes, pf.DockerFreeBytes)
	} else {
		need("projects", files, pf.ProjectsFreeBytes)
		need("Docker", volumes, pf.DockerFreeBytes)
	}
	return nil
}

// Result reports a created staging.
type Result struct {
	Project    db.Project     `json:"-"`
	Deployment int64          `json:"deployment"`
	Rewritten  map[string]int `json:"rewritten"`
	Skipped    []string       `json:"skipped"`
	// WebhookSecret is set when the push webhook could not be registered for
	// the person, so they can add it by hand.
	WebhookSecret     string `json:"webhook_secret,omitempty"`
	WebhookRegistered bool   `json:"webhook_registered"`
}

// Create makes live's staging and queues its first deployment. origin is
// the panel's external base URL, for the webhook. Anything that fails before
// the deployment is queued is undone.
func (s *Service) Create(ctx context.Context, liveName string, req Request, origin string) (Result, error) {
	return s.create(ctx, liveName, req, origin, nil)
}

func (s *Service) create(ctx context.Context, liveName string, req Request, origin string, retained *retainedSettings) (Result, error) {
	pf, err := s.preflight(ctx, liveName, req, retained, 0)
	if err != nil {
		return Result{}, err
	}
	if err := validateCreate(pf, req); err != nil {
		return Result{}, err
	}
	live, err := s.projects.Get(ctx, liveName)
	if err != nil {
		return Result{}, err
	}
	env, err := s.plannedEnv(ctx, live.Name, pf.Rewrites, req.Env, retained)
	if err != nil {
		return Result{}, err
	}
	liveDomains, err := s.projects.Domains(ctx, live.Name)
	if err != nil {
		return Result{}, err
	}

	copied, err := s.projects.CreateStagingCopy(ctx, projects.CopyReq{
		Live: live.Name, Branch: pf.Branch, Rewrites: pf.Rewrites, Env: env, AutoPromote: req.AutoPromote,
	})
	if err != nil {
		return Result{}, err
	}
	staging := copied.Project
	undo := func(err error) (Result, error) {
		s.projects.DiscardStaging(ctx, staging.Name)
		s.proxy.RequestSync()
		return Result{}, err
	}

	targets := map[string]string{}
	for _, d := range pf.Domains {
		targets[d.Live] = strings.ToLower(d.Staging)
	}
	domains := make([]projects.DomainConfig, 0, len(liveDomains))
	for _, d := range liveDomains {
		d.Hostname = targets[d.Hostname]
		domains = append(domains, d)
	}
	if retained != nil {
		domains = retained.domains
	}
	for _, d := range domains {
		if _, err := s.proxy.Add(ctx, staging.ID, d.Hostname, d.Service, d.ContainerPort); err != nil {
			return undo(fmt.Errorf("add staging domain %s: %w", d.Hostname, err))
		}
	}

	result := Result{Project: staging, Rewritten: copied.Rewritten, Skipped: copied.Skipped}
	if result.Skipped == nil {
		result.Skipped = []string{}
	}
	var secret string
	if pf.Git {
		secret, err = s.git.Configure(ctx, staging, git.ProjectConfig{
			Repo: live.GitRepo.String, Branch: pf.Branch, AutoDeploy: live.AutoDeploy != 0,
			ConnectionID: live.GitConnectionID.Int64,
		})
		if err != nil {
			return undo(fmt.Errorf("connect staging to Git: %w", err))
		}
	}

	seedFrom := ""
	if req.CopyData {
		seedFrom = live.Name
	}
	d, err := s.deploy.DeployFirst(ctx, staging.Name, seedFrom)
	if err != nil {
		return undo(fmt.Errorf("queue staging's first deployment: %w", err))
	}
	result.Deployment = d.Number

	// From here staging exists and is deploying, so a webhook that cannot be
	// registered is reported, not undone.
	if pf.Git && live.AutoDeploy != 0 {
		if live.GitConnectionID.Valid {
			if err := s.git.RegisterWebhook(ctx, live.GitConnectionID.Int64, live.GitRepo.String, origin,
				staging.Name, secret); err != nil {
				s.logger.Warn("staging webhook registration", "project", staging.Name, "error", err)
			} else {
				result.WebhookRegistered = true
			}
		}
		if !result.WebhookRegistered {
			result.WebhookSecret = secret
		}
	}
	return result, nil
}

// validateCreate applies the same blockers before replacement deletes the old
// staging and when creation checks live again afterward.
func validateCreate(pf Preflight, req Request) error {
	if len(pf.Blockers) > 0 {
		lines := make([]string, 0, len(pf.Blockers))
		for _, b := range pf.Blockers {
			lines = append(lines, strings.TrimSpace(b.Service+" "+b.Message))
		}
		return fmt.Errorf("%w: %s", ErrBlocked, strings.Join(lines, "; "))
	}
	if req.CopyData {
		for _, item := range pf.Data {
			if item.Kind == "database" && hasWarning(pf.Warnings, item.Service, "is not running") {
				return fmt.Errorf("%w: live's %s database is not running, so its data cannot be copied",
					ErrBlocked, item.Name)
			}
		}
	}
	if len(pf.Flagged) > 0 && !req.Acknowledged {
		return ErrNotAcknowledged
	}
	return nil
}

// valuesOutsideEnv warns about each service that gets a value the flagged
// list would catch from somewhere other than .env: an env_file or the compose
// file itself. Staging copies those files as they are, so it keeps live's
// value. Only key names are reported, never values.
func valuesOutsideEnv(cfg agent.ResolvedConfig, liveEnv map[string]string) []projects.Issue {
	names := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []projects.Issue
	for _, name := range names {
		env := cfg.Services[name].Environment
		var keys []string
		for _, key := range projects.OutsideWorldKeys(env) {
			if v, ok := liveEnv[key]; !ok || v != env[key] {
				keys = append(keys, key)
			}
		}
		if len(keys) == 0 {
			continue
		}
		out = append(out, projects.Issue{Service: name,
			Message: fmt.Sprintf("gets %s from an env_file or the compose file rather than .env, so staging keeps live's value",
				strings.Join(keys, ", ")),
			Fix: "Set each in .env and reference it as ${KEY}, so staging can have its own, or edit staging's copy once it exists."})
	}
	return out
}

// stagingEnv is live's environment with live's hostnames rewritten, then the
// person's edits on top.
func (s *Service) stagingEnv(ctx context.Context, live string, rewrites []projects.HostnameRewrite,
	edits map[string]string) (map[string]string, error) {
	vars, err := s.projects.GetEnv(ctx, live)
	if err != nil {
		return nil, err
	}
	env := make(map[string]string, len(vars)+len(edits))
	for k, v := range vars {
		env[k], _ = projects.RewriteHostnames(v, rewrites)
	}
	for k, v := range edits {
		env[k] = v
	}
	delete(env, "WINDLASS_ENVIRONMENT")
	return env, nil
}

func (s *Service) projectExists(ctx context.Context, name string) (bool, error) {
	names, err := s.agent.FS().DiscoverProjects(ctx)
	if err != nil {
		return false, err
	}
	for _, n := range names {
		if n == name {
			return true, nil
		}
	}
	return false, nil
}

// domainMap pairs each live domain with the staging domain the person chose,
// or the default.
func domainMap(live []projects.DomainConfig, chosen []DomainMap) []DomainMap {
	picked := map[string]string{}
	for _, c := range chosen {
		picked[strings.ToLower(c.Live)] = strings.TrimSpace(c.Staging)
	}
	out := make([]DomainMap, 0, len(live))
	for _, d := range live {
		to, ok := picked[strings.ToLower(d.Hostname)]
		if !ok {
			to = DefaultHostname(d.Hostname)
		}
		out = append(out, DomainMap{Live: d.Hostname, Staging: to})
	}
	return out
}

// DefaultHostname is the staging domain offered for a live one:
// app.example.com becomes staging-app.example.com, so one wildcard record for
// *.example.com covers every staging. An apex domain gets staging. in front,
// as staging-example.com would be someone else's domain.
func DefaultHostname(live string) string {
	if isApex(live) {
		return "staging." + live
	}
	return "staging-" + live
}

// secondLevelSuffixes are the common public suffixes of two labels, under
// which example.co.uk is an apex, not a subdomain of co.uk. A miss only
// changes the suggestion, which the person can edit.
var secondLevelSuffixes = map[string]bool{
	"co.uk": true, "org.uk": true, "me.uk": true, "ltd.uk": true, "plc.uk": true, "net.uk": true,
	"ac.uk": true, "gov.uk": true, "nhs.uk": true, "sch.uk": true,
	"com.au": true, "net.au": true, "org.au": true, "co.nz": true, "org.nz": true,
	"co.za": true, "co.in": true, "co.jp": true, "co.kr": true, "com.br": true, "com.mx": true,
	"com.sg": true, "com.hk": true, "com.tr": true, "com.ng": true, "com.cn": true,
}

func isApex(host string) bool {
	labels := strings.Split(host, ".")
	switch len(labels) {
	case 0, 1, 2:
		return true
	case 3:
		return secondLevelSuffixes[labels[1]+"."+labels[2]]
	}
	return false
}

func hasWarning(issues []projects.Issue, service, message string) bool {
	for _, i := range issues {
		if i.Service == service && strings.Contains(i.Message, message) {
			return true
		}
	}
	return false
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// DeploymentRef is the part of a deployment the staging card shows.
type DeploymentRef struct {
	Number int64  `json:"number"`
	Status string `json:"status"`
	Commit string `json:"commit,omitempty"`

	id           int64
	promotedFrom int64 // the staging deployment's ID, for a promoted live deployment
	recreatable  bool
}

// DriftFile is a compose file that differs between live and staging once
// the staging hostnames are taken into account.
type DriftFile struct {
	File  string `json:"file"`
	State string `json:"state"` // "changed" | "staging_only" | "live_only"
}

// Status compares a live project with its staging.
type Status struct {
	Live        string         `json:"live"`
	Staging     string         `json:"staging"`
	AutoPromote bool           `json:"auto_promote"`
	Branch      string         `json:"branch,omitempty"`
	LiveBranch  string         `json:"live_branch,omitempty"`
	LiveCurrent *DeploymentRef `json:"live_current"`
	LiveLatest  *DeploymentRef `json:"live_latest"`
	Latest      *DeploymentRef `json:"staging_latest"`
	Promotable  *DeploymentRef `json:"promotable"`
	// PromoteBlocked says why Promote is unavailable; empty when it is.
	PromoteBlocked string `json:"promote_blocked,omitempty"`
	CanRecreate    bool   `json:"can_recreate"`
	// RecreateFlaggedKeys names the retained values to review, never their contents.
	RecreateFlaggedKeys []string                   `json:"recreate_flagged_keys,omitempty"`
	Rewrites            []projects.HostnameRewrite `json:"rewrites"`
	Drift               []DriftFile                `json:"drift"`
}

// ErrNoStaging means the project is neither a staging nor has one.
var ErrNoStaging = errors.New("project has no staging")

// Status reports on the pair name belongs to, whether name is the live
// project or its staging.
func (s *Service) Status(ctx context.Context, name string) (Status, error) {
	live, staging, cfg, err := s.pair(ctx, name)
	if err != nil {
		return Status{}, err
	}
	st := Status{Live: live.Name, Staging: staging.Name, AutoPromote: cfg.AutoPromote,
		Branch: staging.GitBranch.String, LiveBranch: live.GitBranch.String,
		Rewrites: cfg.Rewrites, Drift: []DriftFile{}}
	if st.Rewrites == nil {
		st.Rewrites = []projects.HostnameRewrite{}
	}

	st.LiveCurrent, err = s.latestSucceeded(ctx, live.ID)
	if err != nil {
		return Status{}, err
	}
	st.Promotable, err = s.latestSucceeded(ctx, staging.ID)
	if err != nil {
		return Status{}, err
	}
	if st.LiveLatest, err = s.latest(ctx, live.Name); err != nil {
		return Status{}, err
	}
	if st.Latest, err = s.latest(ctx, staging.Name); err != nil {
		return Status{}, err
	}
	st.CanRecreate = st.Latest != nil && st.Latest.recreatable
	if st.CanRecreate {
		env, err := s.projects.GetEnv(ctx, staging.Name)
		if err != nil {
			return Status{}, err
		}
		st.RecreateFlaggedKeys = projects.OutsideWorldKeys(env)
	}
	st.PromoteBlocked = promoteBlocked(st)

	if st.Drift, err = s.drift(ctx, live.Name, staging.Name, st.Rewrites); err != nil {
		return Status{}, err
	}
	return st, nil
}

func promoteBlocked(st Status) string {
	switch {
	case st.Promotable == nil:
		return "staging has no succeeded deployment yet"
	case st.Latest != nil && active(st.Latest.Status):
		return fmt.Sprintf("staging #%d is still deploying", st.Latest.Number)
	case st.LiveLatest != nil && active(st.LiveLatest.Status):
		return fmt.Sprintf("live #%d is still deploying", st.LiveLatest.Number)
	case st.LiveCurrent != nil && st.LiveCurrent.promotedFrom == st.Promotable.id:
		return fmt.Sprintf("live already runs staging #%d", st.Promotable.Number)
	case st.LiveCurrent != nil && st.Promotable.Commit != "" && st.LiveCurrent.Commit == st.Promotable.Commit:
		return "live already runs this commit"
	}
	return ""
}

func active(status string) bool {
	return status != "succeeded" && status != "failed" && status != "cancelled"
}

func (s *Service) latestSucceeded(ctx context.Context, projectID int64) (*DeploymentRef, error) {
	d, err := s.q.LatestSucceededDeployment(ctx, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ref(d), nil
}

func (s *Service) latest(ctx context.Context, project string) (*DeploymentRef, error) {
	list, err := s.deploy.List(ctx, project, 1)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return ref(list[0]), nil
}

func ref(d db.Deployment) *DeploymentRef {
	return &DeploymentRef{Number: d.Number, Status: d.Status, Commit: d.GitCommit.String,
		id: d.ID, promotedFrom: d.PromotedFrom.Int64, recreatable: recreatable(d)}
}

// drift lists the top-level compose files that differ, comparing live's
// with its hostnames rewritten to staging's.
func (s *Service) drift(ctx context.Context, live, staging string, rewrites []projects.HostnameRewrite) ([]DriftFile, error) {
	read := func(project string) (map[string]string, error) {
		files, err := s.agent.FS().List(ctx, project, ".")
		if err != nil {
			return nil, err
		}
		out := map[string]string{}
		for _, f := range files {
			if f.IsDir || !projects.IsComposeFile(f.Name) || f.Name == "compose.rollback.yaml" {
				continue
			}
			data, err := s.agent.FS().ReadFile(ctx, project, f.Name)
			if err != nil {
				return nil, err
			}
			out[f.Name] = string(data)
		}
		return out, nil
	}
	liveFiles, err := read(live)
	if err != nil {
		return nil, err
	}
	stagingFiles, err := read(staging)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for n := range liveFiles {
		names[n] = true
	}
	for n := range stagingFiles {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	drift := []DriftFile{}
	for _, n := range sorted {
		l, inLive := liveFiles[n]
		st, inStaging := stagingFiles[n]
		switch {
		case !inLive:
			drift = append(drift, DriftFile{File: n, State: "staging_only"})
		case !inStaging:
			drift = append(drift, DriftFile{File: n, State: "live_only"})
		default:
			if rewritten, _ := projects.RewriteHostnames(l, rewrites); rewritten != st {
				drift = append(drift, DriftFile{File: n, State: "changed"})
			}
		}
	}
	return drift, nil
}

// pair finds the live project and its staging from either name.
func (s *Service) pair(ctx context.Context, name string) (live, staging db.Project, cfg projects.StagingConfig, err error) {
	live, cfg, ok, err := s.projects.LiveFor(ctx, name)
	if err != nil {
		return live, staging, cfg, err
	}
	if ok {
		staging, err = s.projects.Get(ctx, name)
		return live, staging, cfg, err
	}
	staging, ok, err = s.projects.StagingFor(ctx, name)
	if err != nil {
		return live, staging, cfg, err
	}
	if !ok {
		return live, staging, cfg, ErrNoStaging
	}
	live, cfg, _, err = s.projects.LiveFor(ctx, staging.Name)
	return live, staging, cfg, err
}

// FileDiff is one compose file as live has it and as copying staging's
// version across would make it. A side without the file is empty.
type FileDiff struct {
	File    string `json:"file"`
	Live    string `json:"live"`
	ForLive string `json:"for_live"`
}

// ErrNotComposeFile means a drift diff was asked for a file the drift list
// does not compare.
var ErrNotComposeFile = errors.New("not a compose file staging compares")

// Diff returns file from both projects, with staging's hostnames in
// staging's copy put back to live's, so what is shown is what Copy to live
// writes.
func (s *Service) Diff(ctx context.Context, name, file string) (FileDiff, error) {
	if !projects.IsComposeFile(file) || file == "compose.rollback.yaml" || strings.ContainsAny(file, "/\\") {
		return FileDiff{}, ErrNotComposeFile
	}
	live, staging, cfg, err := s.pair(ctx, name)
	if err != nil {
		return FileDiff{}, err
	}
	read := func(project string) (string, error) {
		data, err := s.agent.FS().ReadFile(ctx, project, file)
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return string(data), err
	}
	d := FileDiff{File: file}
	if d.Live, err = read(live.Name); err != nil {
		return FileDiff{}, err
	}
	st, err := read(staging.Name)
	if err != nil {
		return FileDiff{}, err
	}
	d.ForLive = ForLive(st, cfg.Rewrites)
	return d, nil
}

// ForLive turns a staging compose file into live's, with the staging
// hostnames put back, for copying a change across.
func ForLive(content string, rewrites []projects.HostnameRewrite) string {
	back := make([]projects.HostnameRewrite, 0, len(rewrites))
	for _, r := range rewrites {
		back = append(back, projects.HostnameRewrite{From: r.To, To: r.From})
	}
	out, _ := projects.RewriteHostnames(content, back)
	return out
}
