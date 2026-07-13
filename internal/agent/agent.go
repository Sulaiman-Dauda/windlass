// Package agent defines the privileged-operation boundary of Windlass.
//
// Every interaction with Docker, Docker Compose, Caddy, the project
// filesystem, or a shell goes through the Agent interface. Services never
// import the Docker SDK or touch privileged paths directly (enforced by
// depguard). The in-process implementation lives in agent/local; because
// every method takes a context, exchanges only serializable types, and
// expresses streaming as typed events, the same interface can later be
// served by a separate node-agent binary over mTLS/gRPC without any change
// to the packages above it.
package agent

import (
	"context"
	"io/fs"
	"regexp"
	"time"
)

// projectNameRe constrains names to what is safe as a directory name AND a
// compose project name.
var projectNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// ValidProjectName reports whether name is acceptable for a project. Every
// Agent implementation enforces this; services validate it up front too.
func ValidProjectName(name string) bool {
	return projectNameRe.MatchString(name)
}

type Agent interface {
	Compose() ComposeAgent
	Docker() DockerAgent
	Proxy() ProxyAgent
	FS() FSAgent
	Exec() ExecAgent
	Host() HostAgent

	// Ping reports node capabilities and component availability so features
	// can degrade gracefully (e.g. Caddy missing → warn, don't fail).
	Ping(ctx context.Context) (NodeInfo, error)
}

type NodeInfo struct {
	Hostname       string `json:"hostname"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	DockerVersion  string `json:"docker_version"`  // empty if unavailable
	ComposeVersion string `json:"compose_version"` // empty if unavailable
	CaddyVersion   string `json:"caddy_version"`   // empty if unavailable
}

// LogLine is one line of output from a long-running operation. LogSink
// callbacks receive lines as they happen; over a future wire transport this
// becomes a server-streaming RPC.
type LogLine struct {
	Stream string    `json:"stream"` // "stdout" | "stderr"
	Text   string    `json:"text"`
	Time   time.Time `json:"time"`
}

type LogSink func(LogLine)

// ---------------------------------------------------------------------------
// Compose

// ComposeAgent runs Docker Compose operations for a project. Project names
// map 1:1 to directories under the projects root and to compose project
// names; the compose CLI is the execution engine so behavior is identical to
// what a user gets running the same commands by hand.
type ComposeAgent interface {
	Up(ctx context.Context, req ComposeUpReq, out LogSink) error
	Down(ctx context.Context, project string, removeVolumes bool, out LogSink) error
	Stop(ctx context.Context, project string, out LogSink) error
	Restart(ctx context.Context, project string, out LogSink) error
	Pull(ctx context.Context, project string, out LogSink) error
	Build(ctx context.Context, project string, out LogSink) error
	// PS reports service status via `compose ps --format json`.
	PS(ctx context.Context, project string) ([]ServiceStatus, error)
	// Config validates and resolves the project via `compose config`,
	// returning the fully-resolved model (for image refs, ports, volumes).
	Config(ctx context.Context, project string) (ResolvedConfig, error)
}

type ComposeUpReq struct {
	Project string `json:"project"`
	// ExtraFiles are additional compose files layered after compose.yaml,
	// e.g. compose.rollback.yaml for digest-pinned rollbacks.
	ExtraFiles    []string `json:"extra_files,omitempty"`
	RemoveOrphans bool     `json:"remove_orphans"`
}

type ServiceStatus struct {
	Service    string `json:"service"`
	Name       string `json:"name"` // container name
	State      string `json:"state"`
	Health     string `json:"health"` // "", "starting", "healthy", "unhealthy"
	ExitCode   int    `json:"exit_code"`
	Image      string `json:"image"`
	PublishedPorts []PortBinding `json:"published_ports,omitempty"`
}

type PortBinding struct {
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
}

type ResolvedConfig struct {
	Services map[string]ResolvedService `json:"services"`
}

type ResolvedService struct {
	Image string `json:"image"`
	Build bool   `json:"build"` // service has a build context
}

// ---------------------------------------------------------------------------
// Docker

type DockerAgent interface {
	ListContainers(ctx context.Context, filter ContainerFilter) ([]Container, error)
	// Logs streams container output; follow=false returns the tail and ends.
	Logs(ctx context.Context, containerID string, opts LogOpts, out LogSink) error
	Stats(ctx context.Context, containerIDs []string) ([]ContainerStats, error)
	// ImageTag applies a tag to an existing image (rollback bookkeeping).
	ImageTag(ctx context.Context, source, target string) error
	// ImageDigest resolves an image reference to its content digest.
	ImageDigest(ctx context.Context, ref string) (string, error)
	// Events streams Docker daemon events (health, restarts) until ctx ends.
	Events(ctx context.Context, out func(DockerEvent)) error
}

type ContainerFilter struct {
	// ComposeProject filters by the compose project label; empty = all.
	ComposeProject string `json:"compose_project,omitempty"`
}

type Container struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Image          string    `json:"image"`
	State          string    `json:"state"`
	Health         string    `json:"health"`
	RestartCount   int       `json:"restart_count"`
	ComposeProject string    `json:"compose_project,omitempty"`
	ComposeService string    `json:"compose_service,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type LogOpts struct {
	Follow bool `json:"follow"`
	Tail   int  `json:"tail"` // 0 = default (e.g. 200 lines)
}

type ContainerStats struct {
	ContainerID string  `json:"container_id"`
	CPUPercent  float64 `json:"cpu_percent"`
	MemoryBytes uint64  `json:"memory_bytes"`
	MemoryLimit uint64  `json:"memory_limit"`
	NetRxBytes  uint64  `json:"net_rx_bytes"`
	NetTxBytes  uint64  `json:"net_tx_bytes"`
}

type DockerEvent struct {
	Type      string    `json:"type"`   // "container"
	Action    string    `json:"action"` // "die", "health_status: unhealthy", ...
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Time      time.Time `json:"time"`
}

// ---------------------------------------------------------------------------
// Proxy (Caddy)

// ProxyAgent manages the reverse-proxy routes Windlass owns. It applies the
// full desired state of Windlass-managed routes (tagged with windlass_* IDs
// in Caddy) via targeted admin-API calls and never rewrites configuration it
// does not own.
type ProxyAgent interface {
	// Available reports whether the Caddy admin API is reachable.
	Available(ctx context.Context) (ProxyInfo, error)
	ApplyRoutes(ctx context.Context, routes []Route) error
	CurrentRoutes(ctx context.Context) ([]Route, error)
}

type ProxyInfo struct {
	Available bool   `json:"available"`
	Version   string `json:"version"`
}

type Route struct {
	// ID is the stable Caddy @id ("windlass_<project>_<n>").
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
	// Upstream is the dial target, e.g. "crm-web-1:3000" or "127.0.0.1:3000".
	Upstream string `json:"upstream"`
	TLS      bool   `json:"tls"` // automatic HTTPS
}

// ---------------------------------------------------------------------------
// Filesystem (scoped to the projects root)

// FSAgent performs file operations strictly inside the projects root; paths
// are project-relative and validated against traversal.
type FSAgent interface {
	ReadFile(ctx context.Context, project, rel string) ([]byte, error)
	// WriteFile writes atomically (temp file + rename).
	WriteFile(ctx context.Context, project, rel string, data []byte, mode fs.FileMode) error
	List(ctx context.Context, project, rel string) ([]FileInfo, error)
	// EnsureProject creates the project directory if needed and returns its
	// absolute path on the node.
	EnsureProject(ctx context.Context, project string) (string, error)
	RemoveProject(ctx context.Context, project string) error
}

type FileInfo struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	IsDir   bool      `json:"is_dir"`
	ModTime time.Time `json:"mod_time"`
}

// ---------------------------------------------------------------------------
// Exec (interactive terminal)

// ExecAgent starts interactive sessions inside containers. This is the only
// bidirectional streaming surface; over a future wire transport it becomes a
// bidirectional RPC carrying opaque bytes.
type ExecAgent interface {
	Start(ctx context.Context, req ExecReq) (ExecSession, error)
}

type ExecReq struct {
	ContainerID string   `json:"container_id"`
	Cmd         []string `json:"cmd"` // e.g. ["/bin/sh"]
	TTY         bool     `json:"tty"`
	Cols        uint16   `json:"cols"`
	Rows        uint16   `json:"rows"`
}

type ExecSession interface {
	Write(p []byte) error
	Resize(cols, rows uint16) error
	Output() <-chan []byte
	// Wait blocks until the session ends and returns the exit code.
	Wait() (int, error)
	Close() error
}

// ---------------------------------------------------------------------------
// Host

type HostAgent interface {
	Metrics(ctx context.Context) (HostMetrics, error)
	// GitSync clones or updates a repository into a project directory.
	GitSync(ctx context.Context, req GitSyncReq, out LogSink) (GitSyncResult, error)
}

type HostMetrics struct {
	CPUPercent     float64 `json:"cpu_percent"`
	MemoryUsed     uint64  `json:"memory_used"`
	MemoryTotal    uint64  `json:"memory_total"`
	DiskUsed       uint64  `json:"disk_used"`
	DiskTotal      uint64  `json:"disk_total"`
	Load1          float64 `json:"load1"`
	UptimeSeconds  uint64  `json:"uptime_seconds"`
}

type GitSyncReq struct {
	Project string `json:"project"`
	// Subdir inside the project directory to sync into (default "src").
	Subdir string `json:"subdir,omitempty"`
	URL    string `json:"url"`
	Branch string `json:"branch"`
	// Commit pins the checkout; empty means branch head (the resolved commit
	// is returned so deployments are reproducible).
	Commit string `json:"commit,omitempty"`
	// Token is injected as HTTP basic auth for private repositories. It is
	// never written to disk.
	Token string `json:"token,omitempty"`
}

type GitSyncResult struct {
	Commit string `json:"commit"` // resolved HEAD after sync
}
