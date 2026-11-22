//go:build integration

// A real crash-looping container against real Docker. Runs in CI:
//
//	go test -tags integration ./internal/alerts/
package alerts

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/windlass-dev/windlass/internal/agent/local"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/notify"
	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/migrations"
)

const crashCompose = `services:
  crash:
    image: busybox:latest
    command: ["sh", "-c", "sleep 1; exit 3"]
    restart: always
`

func TestRealCrashLoopIsOneAlertAndOneRecovery(t *testing.T) {
	dir := t.TempDir()
	conn, err := store.Open(filepath.Join(dir, "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := store.Migrate(conn, migrations.FS); err != nil {
		t.Fatal(err)
	}
	q := db.New(conn)
	ag, err := local.New(local.Config{ProjectsDir: filepath.Join(dir, "projects")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	cfg := LoadSettingsJSON(ctx, q)
	cfg.DebounceSeconds, cfg.RestartMinutes = 5, 1
	if err := SaveSettings(ctx, q, cfg); err != nil {
		t.Fatal(err)
	}

	const name = "windlass-it-alerts"
	file := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(file, []byte(crashCompose), 0o644); err != nil {
		t.Fatal(err)
	}
	compose := func(args ...string) {
		out, err := exec.CommandContext(ctx, "docker", append([]string{"compose", "-p", name, "-f", file}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("compose %v: %v\n%s", args, err, out)
		}
	}
	t.Cleanup(func() {
		exec.Command("docker", "compose", "-p", name, "-f", file, "down", "--remove-orphans").Run()
	})

	sent := &sink{}
	svc := New(q, ag, &attStub{}, sent, events.NewBus(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	go svc.watchDocker(ctx)
	time.Sleep(time.Second) // let the event stream attach

	compose("up", "-d")
	var all []notify.Event
	sweepFor := func(d time.Duration) {
		for end := time.Now().Add(d); time.Now().Before(end); {
			svc.Sweep(ctx)
			all = append(all, sent.take()...)
			time.Sleep(2 * time.Second)
		}
	}
	sweepFor(45 * time.Second)
	compose("down", "--remove-orphans")
	sweepFor(10 * time.Second)

	got := kinds(all)
	if len(got) != 2 || got[0] != notify.KindRestartLoop || got[1] != notify.KindRestartLoop+"+recovered" {
		t.Fatalf("sent %v, want one restart_loop and one recovery", got)
	}
}
