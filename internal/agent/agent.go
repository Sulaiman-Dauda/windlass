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
	"errors"
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
	// Create runs `compose create`, which makes the project's network,
	// volumes and containers without starting anything. A staging seed uses
	// it so the volumes exist, labelled as Compose's own, before data is
	// copied into them.
	Create(ctx context.Context, project string, out LogSink) error
}

type ComposeUpReq struct {
	Project string `json:"project"`
	// ExtraFiles are additional compose files layered after compose.yaml,
	// e.g. compose.rollback.yaml for digest-pinned rollbacks.
	ExtraFiles    []string `json:"extra_files,omitempty"`
	RemoveOrphans bool     `json:"remove_orphans"`
	// Services limits the up to these services; empty means all of them.
	Services []string `json:"services,omitempty"`
	// NoDeps starts only the named services, not what they depend on.
	NoDeps bool `json:"no_deps,omitempty"`
}

type ServiceStatus struct {
	Service        string        `json:"service"`
	Name           string        `json:"name"` // container name
	State          string        `json:"state"`
	Health         string        `json:"health"` // "", "starting", "healthy", "unhealthy"
	ExitCode       int           `json:"exit_code"`
	Image          string        `json:"image"`
	PublishedPorts []PortBinding `json:"published_ports,omitempty"`
	MemoryLimit    int64         `json:"memory_limit,omitempty"`
	CPULimit       float64       `json:"cpu_limit,omitempty"`
}

type PortBinding struct {
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
}

type ResolvedConfig struct {
	Services     map[string]ResolvedService `json:"services"`
	HealthChecks []ApplicationHealthCheck   `json:"health_checks,omitempty"`
	// Volumes and Networks are the top-level declarations, keyed by the
	// name the compose file uses.
	Volumes  map[string]ResolvedVolume  `json:"volumes,omitempty"`
	Networks map[string]ResolvedNetwork `json:"networks,omitempty"`
	// WorkingDir is the project directory on the node. Bind mounts inside it
	// belong to the project; anything outside it is shared with the host.
	WorkingDir string `json:"working_dir,omitempty"`
}

// ResolvedVolume is a top-level volume. Name is the Docker volume name:
// "<project>_<key>" unless the file sets name:, in which case two projects
// using the file share the one volume.
type ResolvedVolume struct {
	Name     string `json:"name"`
	External bool   `json:"external,omitempty"`
}

type ResolvedNetwork struct {
	Name     string `json:"name"`
	External bool   `json:"external,omitempty"`
}

// Mount is one entry of a service's volumes. Source is the volume key for
// a named volume, an absolute path for a bind, and empty for an anonymous
// volume or tmpfs.
type Mount struct {
	Type     string `json:"type"` // "volume" | "bind" | "tmpfs" | ...
	Source   string `json:"source,omitempty"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// PublishedPort is a port the service binds on the host.
type PublishedPort struct {
	HostIP    string `json:"host_ip,omitempty"`
	Published string `json:"published"`
	Target    int    `json:"target"`
}

type ApplicationHealthCheck struct {
	Service          string `json:"service"`
	URL              string `json:"url"`
	ExpectedStatus   int    `json:"expected_status"`
	Contains         string `json:"contains,omitempty"`
	StabilitySeconds int    `json:"stability_seconds"`
}

type ResolvedService struct {
	Image          string  `json:"image"`
	Build          bool    `json:"build"` // service has a build context
	ContainerPorts []int   `json:"container_ports,omitempty"`
	MemoryLimit    int64   `json:"memory_limit,omitempty"`
	CPULimit       float64 `json:"cpu_limit,omitempty"`

	ContainerName  string          `json:"container_name,omitempty"`
	PublishedPorts []PublishedPort `json:"published_ports,omitempty"`
	NetworkMode    string          `json:"network_mode,omitempty"`
	// Networks are the keys of the top-level networks the service joins.
	Networks []string `json:"networks,omitempty"`
	Mounts   []Mount  `json:"mounts,omitempty"`
	// Environment is what the container gets, env_file values included. It
	// holds secrets, so it is never encoded.
	Environment map[string]string `json:"-"`
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
	ImageDiskUsage(ctx context.Context) (ImageDiskUsage, error)
	PruneImages(ctx context.Context, req ImagePruneReq) (ImagePruneResult, error)
	// Events streams Docker daemon events (health, restarts) until ctx ends.
	Events(ctx context.Context, out func(DockerEvent)) error
	// RegistryLogin authenticates the host to a container registry.
	//
	// A real `docker login`, so the credential lands in the host's Docker
	// config and a plain `docker compose pull` works with Windlass stopped.
	// Holding it only inside the panel would make the panel load-bearing for
	// the application runtime, which is the one thing it promises not to be.
	RegistryLogin(ctx context.Context, host, username, secret string) error
	// CopyVolume copies every file of volume from into volume to, keeping
	// ownership and modes, in a short-lived helper container with no
	// network that mounts from read-only. Both volumes must exist.
	CopyVolume(ctx context.Context, from, to string, out LogSink) error
	// VolumeSizes reports the size in bytes of each volume Docker knows,
	// keyed by volume name. A size Docker cannot tell is left out.
	VolumeSizes(ctx context.Context) (map[string]int64, error)
	// FreeSpace reports the bytes free where Docker keeps its volumes.
	FreeSpace(ctx context.Context) (uint64, error)
}

type ImageDiskUsage struct {
	TotalCount       int64 `json:"total_count"`
	ActiveCount      int64 `json:"active_count"`
	TotalBytes       int64 `json:"total_bytes"`
	ReclaimableBytes int64 `json:"reclaimable_bytes"`
}

type ImagePruneReq struct {
	OlderThanSeconds int64    `json:"older_than_seconds"`
	ProtectedDigests []string `json:"protected_digests,omitempty"`
}

type ImagePruneResult struct {
	Deleted        int   `json:"deleted"`
	ReclaimedBytes int64 `json:"reclaimed_bytes"`
}

type ContainerFilter struct {
	// ComposeProject filters by the compose project label; empty = all.
	ComposeProject string `json:"compose_project,omitempty"`
}

type Container struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Image          string `json:"image"`
	State          string `json:"state"`
	Health         string `json:"health"`
	RestartCount   int    `json:"restart_count"`
	ComposeProject string `json:"compose_project,omitempty"`
	ComposeService string `json:"compose_service,omitempty"`
	// IPAddress is the container's address on its first network, reachable
	// from the host on standard bridge networking; used for proxy upstreams.
	IPAddress string    `json:"ip_address,omitempty"`
	CreatedAt time.Time `json:"created_at"`
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
	Type   string    `json:"type"`   // "container"
	Action string    `json:"action"` // "die", "health_status: unhealthy", ...
	ID     string    `json:"id"`
	Name   string    `json:"name"`
	Time   time.Time `json:"time"`
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
	// RoutesInstalled reports whether Caddy still holds the Windlass routes
	// object. A Caddy reload or restart loads its own config file and drops
	// every object added through the admin API.
	RoutesInstalled(ctx context.Context) (bool, error)
	// ApplyPanelDomain converges the separately owned panel route. An empty
	// hostname removes that route without touching application/user routes.
	ApplyPanelDomain(ctx context.Context, hostname string) error
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
	// NoIndex adds "X-Robots-Tag: noindex" to every response, so search
	// engines leave a staging site out.
	NoIndex bool `json:"no_index,omitempty"`
}

// ---------------------------------------------------------------------------
// Filesystem (scoped to the projects root)

// FSAgent performs file operations strictly inside the projects root; paths
// are project-relative and validated against traversal.
type FSAgent interface {
	// DiscoverProjects returns valid project directory names that contain a
	// compose.yaml or compose.yml. It is the basis for rebuilding SQLite's
	// project index from the filesystem.
	DiscoverProjects(ctx context.Context) ([]string, error)
	ReadFile(ctx context.Context, project, rel string) ([]byte, error)
	// WriteFile writes atomically (temp file + rename).
	WriteFile(ctx context.Context, project, rel string, data []byte, mode fs.FileMode) error
	List(ctx context.Context, project, rel string) ([]FileInfo, error)
	// EnsureProject creates the project directory if needed and returns its
	// absolute path on the node.
	EnsureProject(ctx context.Context, project string) (string, error)
	RemoveProject(ctx context.Context, project string) error
	// ArchiveProject writes a tar.gz of the project directory into the
	// node's backups dir and returns its absolute path and size. Extra files
	// are injected into the archive without modifying the live project.
	ArchiveProject(ctx context.Context, project string, extraFiles map[string][]byte) (ArchiveInfo, error)
	// RestoreProject replaces the project directory contents with the
	// archive's (which must have been produced by ArchiveProject).
	RestoreProject(ctx context.Context, project, archivePath string) error
	// BackupsDir returns the node's backups directory, creating it if
	// needed. Remote-destination restores download into it.
	BackupsDir(ctx context.Context) (string, error)
	// RemoveArchive deletes an archive file inside the backups dir.
	RemoveArchive(ctx context.Context, archivePath string) error
	// CopyProject copies project from into project to, which must exist,
	// leaving out the top-level entries named in skip. Symbolic links are
	// kept only when they point inside to. Entries that cannot be read are
	// returned rather than failing the copy.
	CopyProject(ctx context.Context, from, to string, skip []string) (skipped []string, err error)
	// Usage reports the bytes a project directory holds, leaving out the
	// top-level entries named in skip.
	Usage(ctx context.Context, project string, skip []string) (int64, error)
	// FreeSpace reports the bytes free on the projects volume.
	FreeSpace(ctx context.Context) (uint64, error)
}

type ArchiveInfo struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
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
	// Stdin attaches standard input without a TTY, for streaming data into
	// a command. The caller ends the input with CloseWrite.
	Stdin bool `json:"stdin,omitempty"`
}

type ExecSession interface {
	Write(p []byte) error
	Resize(cols, rows uint16) error
	// Output delivers the command's output and is closed when the command
	// ends. With a TTY it is the terminal stream; without one it is stdout
	// only.
	Output() <-chan []byte
	// Wait blocks until the session ends and returns the exit code. Without a
	// TTY, a non-zero exit also returns an error carrying the end of stderr.
	Wait() (int, error)
	// CloseWrite ends the command's input, as end of file, while its output
	// keeps flowing.
	CloseWrite() error
	Close() error
}

// ---------------------------------------------------------------------------
// Host

type HostAgent interface {
	Metrics(ctx context.Context) (HostMetrics, error)
	HTTPCheck(ctx context.Context, req HTTPCheckReq) (HTTPCheckResult, error)
	// GitSync clones or updates a repository into a project directory.
	GitSync(ctx context.Context, req GitSyncReq, out LogSink) (GitSyncResult, error)
}

type HTTPCheckReq struct {
	URL     string `json:"url"`
	Timeout int    `json:"timeout_seconds"`
}

type HTTPCheckResult struct {
	StatusCode int    `json:"status_code"`
	Body       string `json:"body"`
}

type HostMetrics struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryUsed    uint64  `json:"memory_used"`
	MemoryTotal   uint64  `json:"memory_total"`
	DiskUsed      uint64  `json:"disk_used"`
	DiskTotal     uint64  `json:"disk_total"`
	Load1         float64 `json:"load1"`
	UptimeSeconds uint64  `json:"uptime_seconds"`
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
	// RequireOnBranch refuses Commit, before anything is checked out, unless
	// it is on Branch at the remote. A promotion uses it so live only ever
	// runs code that has been merged into the branch it deploys.
	RequireOnBranch bool `json:"require_on_branch,omitempty"`
}

// ErrNotOnBranch is returned by GitSync when RequireOnBranch refuses a commit.
var ErrNotOnBranch = errors.New("commit is not on the branch")

type GitSyncResult struct {
	Commit string `json:"commit"` // resolved HEAD after sync
}
