//go:build integration

// Integration tests exercise agent/local against a real Docker daemon.
// They run in CI on Linux: go test -tags integration ./internal/agent/local
package local

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
)

func TestPingRealDocker(t *testing.T) {
	l, err := New(Config{ProjectsDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	info, err := l.Ping(ctx)
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if info.DockerVersion == "" {
		t.Error("docker version empty — daemon not reachable")
	}
	if info.ComposeVersion == "" {
		t.Error("compose version empty — docker compose plugin missing")
	}
	t.Logf("node: %+v", info)
}

func TestListContainersRealDocker(t *testing.T) {
	l, err := New(Config{ProjectsDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Start a labelled container with the docker CLI, then find it via the agent.
	name := "windlass-inttest-list"
	exec.Command("docker", "rm", "-f", name).Run()
	out, err := exec.CommandContext(ctx, "docker", "run", "-d", "--name", name,
		"--label", "com.docker.compose.project=windlass-inttest",
		"--label", "com.docker.compose.service=web",
		"busybox", "sleep", "30").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", name).Run() })

	list, err := l.Docker().ListContainers(ctx, agent.ContainerFilter{ComposeProject: "windlass-inttest"})
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d containers, want 1", len(list))
	}
	c := list[0]
	if c.Name != name || c.ComposeService != "web" || c.State != "running" {
		t.Errorf("container = %+v", c)
	}

	// Logs (non-follow) must complete and deliver output.
	var lines []agent.LogLine
	if err := l.Docker().Logs(ctx, c.ID, agent.LogOpts{}, func(line agent.LogLine) {
		lines = append(lines, line)
	}); err != nil {
		t.Errorf("Logs: %v", err)
	}

	// Stats returns one sample for the running container.
	stats, err := l.Docker().Stats(ctx, []string{c.ID})
	if err != nil || len(stats) != 1 {
		t.Errorf("Stats = %+v, %v", stats, err)
	}
}
