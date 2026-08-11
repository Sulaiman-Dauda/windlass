// Package fake is a deterministic, in-memory implementation of agent.Agent
// for unit tests. It runs anywhere (no Docker, Windows-safe), records every
// call, and lets tests inject failures per operation.
package fake

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
)

// Fake implements agent.Agent. The zero value is usable; configure fields
// before use and inspect Calls afterwards. Inject errors with Fail, keyed by
// operation name ("compose.up", "fs.write", "proxy.apply", ...).
type Fake struct {
	// RegistryLogins records host -> username for each docker login performed.
	RegistryLogins map[string]string
	mu             sync.Mutex

	Node agent.NodeInfo

	// Fail maps operation names to errors returned by that operation.
	Fail map[string]error
	// Calls records operation names in order, e.g. "compose.up(myproj)".
	Calls []string

	// Files is the in-memory project filesystem: project → relpath → content.
	Files map[string]map[string][]byte

	// Containers returned by Docker().ListContainers.
	Containers []agent.Container
	// Statuses returned by Compose().PS, keyed by project.
	Statuses map[string][]agent.ServiceStatus
	// Resolved returned by Compose().Config, keyed by project.
	Resolved map[string]agent.ResolvedConfig
	// ComposeLog lines emitted to the LogSink of compose operations.
	ComposeLog []string

	// Routes last applied via Proxy().ApplyRoutes.
	Routes      []agent.Route
	PanelDomain string
	// ProxyAvailable controls Proxy().Available.
	ProxyAvailable bool

	// GitCommit is returned by Host().GitSync.
	GitCommit string

	// Archives maps archive paths to project-file snapshots.
	Archives map[string]map[string][]byte

	Metrics       agent.HostMetrics
	HTTPResponses map[string]agent.HTTPCheckResult
	ImageUsage    agent.ImageDiskUsage
	PruneResult   agent.ImagePruneResult
}

var _ agent.Agent = (*Fake)(nil)

func New() *Fake {
	return &Fake{
		Node: agent.NodeInfo{
			Hostname: "fake-node", OS: "linux", Arch: "amd64",
			DockerVersion: "27.0.0", ComposeVersion: "2.30.0", CaddyVersion: "2.8.0",
		},
		Fail:           map[string]error{},
		Files:          map[string]map[string][]byte{},
		Statuses:       map[string][]agent.ServiceStatus{},
		Resolved:       map[string]agent.ResolvedConfig{},
		ProxyAvailable: true,
		GitCommit:      "0000000000000000000000000000000000000000",
		Archives:       map[string]map[string][]byte{},
		HTTPResponses:  map[string]agent.HTTPCheckResult{},
	}
}

func (f *Fake) record(op string, err error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, op)
	if err != nil {
		return err
	}
	// Fail lookup uses the op name without arguments.
	key := op
	if i := strings.IndexByte(op, '('); i >= 0 {
		key = op[:i]
	}
	return f.Fail[key]
}

func (f *Fake) emit(out agent.LogSink, lines ...string) {
	if out == nil {
		return
	}
	for _, l := range lines {
		out(agent.LogLine{Stream: "stdout", Text: l, Time: time.Unix(0, 0)})
	}
}

func (f *Fake) Ping(ctx context.Context) (agent.NodeInfo, error) {
	return f.Node, f.record("ping", nil)
}

func (f *Fake) Compose() agent.ComposeAgent { return composeFake{f} }
func (f *Fake) Docker() agent.DockerAgent   { return dockerFake{f} }
func (f *Fake) Proxy() agent.ProxyAgent     { return proxyFake{f} }
func (f *Fake) FS() agent.FSAgent           { return fsFake{f} }
func (f *Fake) Exec() agent.ExecAgent       { return execFake{f} }
func (f *Fake) Host() agent.HostAgent       { return hostFake{f} }

// ---------------------------------------------------------------------------

type composeFake struct{ f *Fake }

func (c composeFake) Up(ctx context.Context, req agent.ComposeUpReq, out agent.LogSink) error {
	if err := c.f.record(fmt.Sprintf("compose.up(%s)", req.Project), nil); err != nil {
		return err
	}
	c.f.emit(out, c.f.ComposeLog...)
	return nil
}

func (c composeFake) Down(ctx context.Context, project string, removeVolumes bool, out agent.LogSink) error {
	return c.f.record(fmt.Sprintf("compose.down(%s)", project), nil)
}

func (c composeFake) Stop(ctx context.Context, project string, out agent.LogSink) error {
	return c.f.record(fmt.Sprintf("compose.stop(%s)", project), nil)
}

func (c composeFake) Restart(ctx context.Context, project string, out agent.LogSink) error {
	return c.f.record(fmt.Sprintf("compose.restart(%s)", project), nil)
}

func (c composeFake) Pull(ctx context.Context, project string, out agent.LogSink) error {
	if err := c.f.record(fmt.Sprintf("compose.pull(%s)", project), nil); err != nil {
		return err
	}
	c.f.emit(out, "Pulling images...")
	return nil
}

func (c composeFake) Build(ctx context.Context, project string, out agent.LogSink) error {
	return c.f.record(fmt.Sprintf("compose.build(%s)", project), nil)
}

func (c composeFake) PS(ctx context.Context, project string) ([]agent.ServiceStatus, error) {
	if err := c.f.record(fmt.Sprintf("compose.ps(%s)", project), nil); err != nil {
		return nil, err
	}
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	return c.f.Statuses[project], nil
}

func (c composeFake) Config(ctx context.Context, project string) (agent.ResolvedConfig, error) {
	if err := c.f.record(fmt.Sprintf("compose.config(%s)", project), nil); err != nil {
		return agent.ResolvedConfig{}, err
	}
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	return c.f.Resolved[project], nil
}

// ---------------------------------------------------------------------------

type dockerFake struct{ f *Fake }

func (d dockerFake) ListContainers(ctx context.Context, filter agent.ContainerFilter) ([]agent.Container, error) {
	if err := d.f.record("docker.list", nil); err != nil {
		return nil, err
	}
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	if filter.ComposeProject == "" {
		return d.f.Containers, nil
	}
	var out []agent.Container
	for _, c := range d.f.Containers {
		if c.ComposeProject == filter.ComposeProject {
			out = append(out, c)
		}
	}
	return out, nil
}

func (d dockerFake) Logs(ctx context.Context, id string, opts agent.LogOpts, out agent.LogSink) error {
	if err := d.f.record(fmt.Sprintf("docker.logs(%s)", id), nil); err != nil {
		return err
	}
	d.f.emit(out, "log line from "+id)
	return nil
}

func (d dockerFake) Stats(ctx context.Context, ids []string) ([]agent.ContainerStats, error) {
	return nil, d.f.record("docker.stats", nil)
}

func (d dockerFake) ImageTag(ctx context.Context, source, target string) error {
	return d.f.record(fmt.Sprintf("docker.tag(%s,%s)", source, target), nil)
}

func (d dockerFake) ImageDigest(ctx context.Context, ref string) (string, error) {
	if err := d.f.record(fmt.Sprintf("docker.digest(%s)", ref), nil); err != nil {
		return "", err
	}
	return "sha256:" + strings.Repeat("0", 64), nil
}

func (d dockerFake) ImageDiskUsage(ctx context.Context) (agent.ImageDiskUsage, error) {
	return d.f.ImageUsage, d.f.record("docker.diskusage", nil)
}

func (d dockerFake) PruneImages(ctx context.Context, req agent.ImagePruneReq) (agent.ImagePruneResult, error) {
	return d.f.PruneResult, d.f.record("docker.prune", nil)
}

func (d dockerFake) Events(ctx context.Context, out func(agent.DockerEvent)) error {
	if err := d.f.record("docker.events", nil); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

// ---------------------------------------------------------------------------

type proxyFake struct{ f *Fake }

func (p proxyFake) Available(ctx context.Context) (agent.ProxyInfo, error) {
	if err := p.f.record("proxy.available", nil); err != nil {
		return agent.ProxyInfo{}, err
	}
	p.f.mu.Lock()
	defer p.f.mu.Unlock()
	return agent.ProxyInfo{Available: p.f.ProxyAvailable, Version: "2.8.0"}, nil
}

func (p proxyFake) ApplyRoutes(ctx context.Context, routes []agent.Route) error {
	if err := p.f.record("proxy.apply", nil); err != nil {
		return err
	}
	p.f.mu.Lock()
	defer p.f.mu.Unlock()
	p.f.Routes = append([]agent.Route(nil), routes...)
	return nil
}

func (p proxyFake) CurrentRoutes(ctx context.Context) ([]agent.Route, error) {
	if err := p.f.record("proxy.current", nil); err != nil {
		return nil, err
	}
	p.f.mu.Lock()
	defer p.f.mu.Unlock()
	return append([]agent.Route(nil), p.f.Routes...), nil
}

func (p proxyFake) ApplyPanelDomain(ctx context.Context, hostname string) error {
	if err := p.f.record("proxy.panel("+hostname+")", nil); err != nil {
		return err
	}
	p.f.mu.Lock()
	defer p.f.mu.Unlock()
	p.f.PanelDomain = hostname
	return nil
}

// ---------------------------------------------------------------------------

type fsFake struct{ f *Fake }

func (s fsFake) DiscoverProjects(ctx context.Context) ([]string, error) {
	if err := s.f.record("fs.discover", nil); err != nil {
		return nil, err
	}
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	var out []string
	for name, files := range s.f.Files {
		if !agent.ValidProjectName(name) {
			continue
		}
		if _, ok := files["compose.yaml"]; !ok {
			if _, ok = files["compose.yml"]; !ok {
				continue
			}
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// validRel rejects absolute and traversal paths, mirroring agent/local.
func validRel(rel string) error {
	clean := path.Clean(strings.ReplaceAll(rel, "\\", "/"))
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("invalid path %q", rel)
	}
	return nil
}

func (s fsFake) ReadFile(ctx context.Context, project, rel string) ([]byte, error) {
	if err := s.f.record(fmt.Sprintf("fs.read(%s,%s)", project, rel), validRel(rel)); err != nil {
		return nil, err
	}
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	data, ok := s.f.Files[project][rel]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), data...), nil
}

func (s fsFake) WriteFile(ctx context.Context, project, rel string, data []byte, mode fs.FileMode) error {
	if err := s.f.record(fmt.Sprintf("fs.write(%s,%s)", project, rel), validRel(rel)); err != nil {
		return err
	}
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	if s.f.Files[project] == nil {
		s.f.Files[project] = map[string][]byte{}
	}
	s.f.Files[project][rel] = append([]byte(nil), data...)
	return nil
}

func (s fsFake) List(ctx context.Context, project, rel string) ([]agent.FileInfo, error) {
	if err := s.f.record(fmt.Sprintf("fs.list(%s,%s)", project, rel), validRel(rel)); err != nil {
		return nil, err
	}
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	var out []agent.FileInfo
	for name, data := range s.f.Files[project] {
		out = append(out, agent.FileInfo{Name: name, Size: int64(len(data))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s fsFake) EnsureProject(ctx context.Context, project string) (string, error) {
	var invalid error
	if !agent.ValidProjectName(project) {
		invalid = fmt.Errorf("invalid project name %q", project)
	}
	if err := s.f.record(fmt.Sprintf("fs.ensure(%s)", project), invalid); err != nil {
		return "", err
	}
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	if s.f.Files[project] == nil {
		s.f.Files[project] = map[string][]byte{}
	}
	return "/fake/projects/" + project, nil
}

func (s fsFake) ArchiveProject(ctx context.Context, project string) (agent.ArchiveInfo, error) {
	if err := s.f.record(fmt.Sprintf("fs.archive(%s)", project), nil); err != nil {
		return agent.ArchiveInfo{}, err
	}
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	files, ok := s.f.Files[project]
	if !ok {
		return agent.ArchiveInfo{}, fs.ErrNotExist
	}
	snapshot := map[string][]byte{}
	var size int64
	for name, data := range files {
		snapshot[name] = append([]byte(nil), data...)
		size += int64(len(data))
	}
	path := fmt.Sprintf("/fake/backups/%s-%d.tar.gz", project, len(s.f.Archives))
	s.f.Archives[path] = snapshot
	return agent.ArchiveInfo{Path: path, Size: size}, nil
}

func (s fsFake) BackupsDir(ctx context.Context) (string, error) {
	return "/fake/backups", s.f.record("fs.backupsdir", nil)
}

func (s fsFake) RemoveArchive(ctx context.Context, archivePath string) error {
	if err := s.f.record(fmt.Sprintf("fs.rmarchive(%s)", archivePath), nil); err != nil {
		return err
	}
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	delete(s.f.Archives, archivePath)
	return nil
}

func (s fsFake) RestoreProject(ctx context.Context, project, archivePath string) error {
	if err := s.f.record(fmt.Sprintf("fs.restore(%s)", project), nil); err != nil {
		return err
	}
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	snapshot, ok := s.f.Archives[archivePath]
	if !ok {
		return fs.ErrNotExist
	}
	restored := map[string][]byte{}
	for name, data := range snapshot {
		restored[name] = append([]byte(nil), data...)
	}
	s.f.Files[project] = restored
	return nil
}

func (s fsFake) RemoveProject(ctx context.Context, project string) error {
	if err := s.f.record(fmt.Sprintf("fs.remove(%s)", project), nil); err != nil {
		return err
	}
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	delete(s.f.Files, project)
	return nil
}

// ---------------------------------------------------------------------------

type execFake struct{ f *Fake }

// Start returns a session that echoes writes back to its output.
func (e execFake) Start(ctx context.Context, req agent.ExecReq) (agent.ExecSession, error) {
	if err := e.f.record(fmt.Sprintf("exec.start(%s)", req.ContainerID), nil); err != nil {
		return nil, err
	}
	return newEchoSession(), nil
}

type echoSession struct {
	out    chan []byte
	done   chan struct{}
	closed sync.Once
}

func newEchoSession() *echoSession {
	return &echoSession{out: make(chan []byte, 16), done: make(chan struct{})}
}

func (s *echoSession) Write(p []byte) error {
	select {
	case s.out <- append([]byte(nil), p...):
		return nil
	case <-s.done:
		return fmt.Errorf("session closed")
	}
}

func (s *echoSession) Resize(cols, rows uint16) error { return nil }
func (s *echoSession) Output() <-chan []byte          { return s.out }

func (s *echoSession) Wait() (int, error) {
	<-s.done
	return 0, nil
}

func (s *echoSession) Close() error {
	s.closed.Do(func() {
		close(s.done)
		close(s.out)
	})
	return nil
}

// ---------------------------------------------------------------------------

type hostFake struct{ f *Fake }

func (h hostFake) Metrics(ctx context.Context) (agent.HostMetrics, error) {
	if err := h.f.record("host.metrics", nil); err != nil {
		return agent.HostMetrics{}, err
	}
	return h.f.Metrics, nil
}

func (h hostFake) HTTPCheck(ctx context.Context, req agent.HTTPCheckReq) (agent.HTTPCheckResult, error) {
	if err := h.f.record("host.httpcheck("+req.URL+")", nil); err != nil {
		return agent.HTTPCheckResult{}, err
	}
	response, ok := h.f.HTTPResponses[req.URL]
	if !ok {
		return agent.HTTPCheckResult{StatusCode: 200}, nil
	}
	return response, nil
}

func (h hostFake) GitSync(ctx context.Context, req agent.GitSyncReq, out agent.LogSink) (agent.GitSyncResult, error) {
	if err := h.f.record(fmt.Sprintf("host.gitsync(%s)", req.Project), nil); err != nil {
		return agent.GitSyncResult{}, err
	}
	h.f.emit(out, "Cloning "+req.URL)
	commit := req.Commit
	if commit == "" {
		commit = h.f.GitCommit
	}
	return agent.GitSyncResult{Commit: commit}, nil
}

// RegistryLogin records the login so a test can assert the host was
// authenticated before a pull, without ever holding a real credential.
func (d dockerFake) RegistryLogin(ctx context.Context, host, username, secret string) error {
	if err := d.f.record("docker.registry_login", nil); err != nil {
		return err
	}
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	if d.f.RegistryLogins == nil {
		d.f.RegistryLogins = map[string]string{}
	}
	d.f.RegistryLogins[host] = username
	return nil
}
