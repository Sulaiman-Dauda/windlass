//go:build integration

package local

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
)

// TestCleanupRealDocker builds twice, keeping the first image under two
// more tags with nothing running it, then removes it and trims the build
// cache. (A rebuild through the docker driver on the containerd image store
// drops the old image rather than leave it untagged, so the test cannot rely
// on an untagged one.) Behind the installer's socket
// proxy the build runs in a buildx_buildkit_default container, and the
// cache has to be found and trimmed there.
//
// It must not touch images other tests use on a shared daemon. Its images
// are built with SOURCE_DATE_EPOCH=0, so they read as created in 1970, the
// age cut-off is 20 years, and every other image present is protected.
func TestCleanupRealDocker(t *testing.T) {
	root := t.TempDir()
	l, err := New(Config{ProjectsDir: root})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	const project = "windlass-inttest-cleanup"
	dir := filepath.Join(root, project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"Dockerfile": "FROM busybox:1.37\nRUN dd if=/dev/urandom of=/blob bs=1M count=20\n" +
			"ARG V=1\nRUN echo $V > /v\nCMD [\"sleep\", \"600\"]\n",
		"compose.yaml": "services:\n  web:\n    build: .\n    image: " + project + "-web:latest\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	compose := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "docker", append([]string{"compose", "-p", project}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compose %v: %v\n%s", args, err, out)
		}
	}
	t.Cleanup(func() {
		cmd := exec.Command("docker", "compose", "-p", project, "down", "--rmi", "all")
		cmd.Dir = dir
		_ = cmd.Run()
		_ = exec.Command("docker", "image", "rm", project+"-old:1", project+"-old:2").Run()
	})
	imageID := func(ref string) string {
		id, err := l.Docker().ImageID(ctx, ref)
		if err != nil {
			t.Fatalf("image %s: %v", ref, err)
		}
		return id
	}

	compose("build", "--build-arg", "SOURCE_DATE_EPOCH=0", "--build-arg", "V=1")
	first := imageID(project + "-web:latest")
	// Two tags: Docker refuses to remove such an image unless forced.
	for _, tag := range []string{project + "-old:1", project + "-old:2"} {
		if out, err := exec.CommandContext(ctx, "docker", "tag", first, tag).CombinedOutput(); err != nil {
			t.Fatalf("tag: %v\n%s", err, out)
		}
	}
	compose("build", "--build-arg", "SOURCE_DATE_EPOCH=0", "--build-arg", "V=2")
	compose("up", "-d")
	second := imageID(project + "-web:latest")
	if first == second {
		t.Fatal("the second build made the same image")
	}

	before, err := l.Docker().DiskUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.BuildCache.Bytes <= 0 {
		t.Fatalf("build cache = %+v after two builds, want it counted wherever BuildKit keeps it", before.BuildCache)
	}

	// Protect everything but the first build.
	out, err := exec.CommandContext(ctx, "docker", "image", "ls", "-a", "-q", "--no-trunc").Output()
	if err != nil {
		t.Fatal(err)
	}
	var protected []string
	for _, id := range strings.Fields(string(out)) {
		if id != first {
			protected = append(protected, id)
		}
	}
	images, err := l.Docker().PruneImages(ctx, agent.ImagePruneReq{
		OlderThanSeconds: 20 * 365 * 24 * 60 * 60,
		ProtectedDigests: protected,
	})
	if err != nil {
		t.Fatal(err)
	}
	if images.Deleted != 1 || images.Failed != 0 || images.ReclaimedBytes <= 0 {
		t.Fatalf("image prune = %+v, want the first build removed", images)
	}
	if _, err := l.Docker().ImageID(ctx, first); err == nil {
		t.Error("the first build is still there")
	}
	if imageID(project+"-web:latest") != second {
		t.Error("the image a running container uses was touched")
	}

	cache, err := l.Docker().PruneBuildCache(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cache.Errors) != 0 || cache.ReclaimedBytes <= 0 {
		t.Fatalf("build cache prune = %+v", cache)
	}
	after, err := l.Docker().DiskUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("build cache %d -> %d bytes, buildx reported %d freed", before.BuildCache.Bytes, after.BuildCache.Bytes, cache.ReclaimedBytes)
	if after.BuildCache.Bytes >= before.BuildCache.Bytes {
		t.Errorf("build cache did not shrink: %d -> %d", before.BuildCache.Bytes, after.BuildCache.Bytes)
	}

	// Behind the proxy: a stopped builder keeps its cache, and the run
	// says so rather than report 0 freed and nothing else.
	const builder = "buildx_buildkit_default"
	if exec.CommandContext(ctx, "docker", "container", "inspect", builder).Run() != nil {
		return
	}
	if out, err := exec.CommandContext(ctx, "docker", "stop", builder).CombinedOutput(); err != nil {
		t.Fatalf("stop builder: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "start", builder).Run() })
	stopped, err := l.Docker().PruneBuildCache(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stopped.Errors) != 1 || !strings.Contains(stopped.Errors[0], builder+" is stopped") {
		t.Fatalf("stopped builder: %+v, want it named", stopped)
	}
}
