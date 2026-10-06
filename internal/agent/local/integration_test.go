//go:build integration

// Integration tests exercise agent/local against a real Docker daemon.
// They run in CI on Linux: go test -tags integration ./internal/agent/local
package local

import (
	"context"
	"os/exec"
	"strings"
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
		t.Error("docker version empty, daemon not reachable")
	}
	if info.ComposeVersion == "" {
		t.Error("compose version empty, docker compose plugin missing")
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

// A non-TTY exec is how backups take a database dump, so its output must be
// exactly the command's stdout: no stream headers, no stderr mixed in, and a
// channel that closes when the command ends.
func TestExecWithoutTTYReturnsOnlyStdout(t *testing.T) {
	l, err := New(Config{ProjectsDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	name := "windlass-inttest-exec"
	exec.Command("docker", "rm", "-f", name).Run()
	out, err := exec.CommandContext(ctx, "docker", "run", "-d", "--name", name, "busybox", "sleep", "60").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", name).Run() })

	sess, err := l.Exec().Start(ctx, agent.ExecReq{
		ContainerID: name,
		Cmd:         []string{"sh", "-c", "printf 'line one\\nline two\\n'; echo warning >&2; exit 3"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Close()

	var got []byte
	for {
		select {
		case chunk, ok := <-sess.Output():
			if !ok {
				goto done
			}
			got = append(got, chunk...)
		case <-ctx.Done():
			t.Fatalf("output channel never closed; collected %q", got)
		}
	}
done:
	if string(got) != "line one\nline two\n" {
		t.Errorf("output = %q, want only the command's stdout", got)
	}
	code, err := sess.Wait()
	if code != 3 || err == nil || !strings.Contains(err.Error(), "warning") {
		t.Errorf("Wait = %d, %v; want 3 and an error carrying stderr", code, err)
	}
}

// A command that never finishes must not outlive the context it was started
// with.
func TestExecStopsAtContextDeadline(t *testing.T) {
	l, err := New(Config{ProjectsDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	name := "windlass-inttest-exec-deadline"
	exec.Command("docker", "rm", "-f", name).Run()
	out, err := exec.CommandContext(ctx, "docker", "run", "-d", "--name", name, "busybox", "sleep", "60").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", name).Run() })

	for _, cmd := range [][]string{{"sleep", "30"}} {
		execCtx, execCancel := context.WithTimeout(ctx, 2*time.Second)
		sess, err := l.Exec().Start(execCtx, agent.ExecReq{ContainerID: name, Cmd: cmd})
		if err != nil {
			execCancel()
			t.Fatalf("Start(%v): %v", cmd, err)
		}
		started := time.Now()
		closed := make(chan struct{})
		go func() {
			for range sess.Output() {
			}
			close(closed)
		}()
		select {
		case <-closed:
			if waited := time.Since(started); waited > 5*time.Second {
				t.Errorf("%v: output closed after %s, want soon after the 2s deadline", cmd, waited)
			}
		case <-time.After(15 * time.Second):
			t.Errorf("%v: output still open 15s after start with a 2s deadline", cmd)
		}
		sess.Close()
		execCancel()
	}
}

// Without a TTY nothing will ever write to stdin, so a command that reads it,
// like a password prompt, must see end of input rather than wait.
func TestExecWithoutTTYSeesEndOfInput(t *testing.T) {
	l, err := New(Config{ProjectsDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	name := "windlass-inttest-exec-stdin"
	exec.Command("docker", "rm", "-f", name).Run()
	out, err := exec.CommandContext(ctx, "docker", "run", "-d", "--name", name, "busybox", "sleep", "60").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", name).Run() })

	sess, err := l.Exec().Start(ctx, agent.ExecReq{ContainerID: name, Cmd: []string{"cat"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Close()
	closed := make(chan struct{})
	go func() {
		for range sess.Output() {
		}
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("cat still waiting on stdin after 10s")
	}
}
