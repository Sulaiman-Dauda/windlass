//go:build integration

package backups

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/windlass-dev/windlass/internal/agent/local"
)

// Each database writes its credentials in compose.yaml with no .env at all,
// the shape that used to produce backups without a dump (#42), and seeds a
// marker row the dump must contain.
var dumpStacks = map[string]string{
	"postgres": `services:
  db:
    image: postgres:17-alpine
    environment:
      POSTGRES_USER: app
      POSTGRES_PASSWORD: secret
      POSTGRES_DB: appdb
    volumes:
      - ./seed.sql:/docker-entrypoint-initdb.d/seed.sql:ro
    healthcheck:
      test: ["CMD", "pg_isready", "-h", "127.0.0.1", "-U", "app", "-d", "appdb"]
      interval: 1s
      retries: 120
`,
	// Password authentication even on the local socket, so the dump only
	// works if it passes the container's POSTGRES_PASSWORD.
	"postgres-scram": `services:
  db:
    image: postgres:17-alpine
    environment:
      POSTGRES_USER: app
      POSTGRES_PASSWORD: secret
      POSTGRES_DB: appdb
      POSTGRES_INITDB_ARGS: --auth-local=scram-sha-256 --auth-host=scram-sha-256
    volumes:
      - ./seed.sql:/docker-entrypoint-initdb.d/seed.sql:ro
    healthcheck:
      test: ["CMD", "pg_isready", "-h", "127.0.0.1", "-U", "app", "-d", "appdb"]
      interval: 1s
      retries: 120
`,
	"mysql": `services:
  db:
    image: mysql:8.4
    environment:
      MYSQL_ROOT_PASSWORD: secret
      MYSQL_DATABASE: appdb
    volumes:
      - ./seed.sql:/docker-entrypoint-initdb.d/seed.sql:ro
    healthcheck:
      test: ["CMD", "mysqladmin", "ping", "-h", "127.0.0.1", "-psecret"]
      interval: 1s
      retries: 180
`,
	"mariadb": `services:
  db:
    image: mariadb:11
    environment:
      MARIADB_ROOT_PASSWORD: secret
      MARIADB_DATABASE: appdb
    volumes:
      - ./seed.sql:/docker-entrypoint-initdb.d/seed.sql:ro
    healthcheck:
      test: ["CMD", "healthcheck.sh", "--connect", "--innodb_initialized"]
      interval: 1s
      retries: 180
`,
}

// A MySQL whose root password is not in the container's environment cannot be
// dumped. The backup must give up promptly rather than wait on a password
// prompt, so the file archive is still taken and the project lock released.
const mysqlWithoutRootPassword = `services:
  db:
    image: mysql:8.4
    environment:
      MYSQL_RANDOM_ROOT_PASSWORD: "yes"
    healthcheck:
      test: ["CMD", "mysqladmin", "ping", "-h", "127.0.0.1"]
      interval: 1s
      retries: 180
`

func TestDumpDatabaseGivesUpWithoutCredentials(t *testing.T) {
	ag, err := local.New(local.Config{ProjectsDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{agent: ag, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dir := t.TempDir()
	project := "windlass-inttest-dump-nopass"
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(mysqlWithoutRootPassword), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "compose", "-p", project, "down", "-v").Run() })
	up := exec.CommandContext(ctx, "docker", "compose", "-p", project, "up", "-d", "--wait")
	up.Dir = dir
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("compose up: %v: %s", err, out)
	}

	done := make(chan bool, 1)
	go func() {
		_, ok := s.dumpDatabase(ctx, project)
		done <- ok
	}()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("dump reported success without credentials")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("dump still waiting after 30s; it is stuck on a password prompt")
	}
}

func TestDumpDatabaseRealContainers(t *testing.T) {
	ag, err := local.New(local.Config{ProjectsDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{agent: ag, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	for name, compose := range dumpStacks {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			dir := t.TempDir()
			project := "windlass-inttest-dump-" + name
			seed := "CREATE TABLE windlass_marker (note VARCHAR(40));\n" +
				"INSERT INTO windlass_marker VALUES ('backup-proof');\n"
			if !strings.HasPrefix(name, "postgres") {
				seed = "USE appdb;\n" + seed
			}
			for file, body := range map[string]string{"compose.yaml": compose, "seed.sql": seed} {
				if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			compose := func(args ...string) *exec.Cmd {
				return exec.CommandContext(ctx, "docker", append([]string{"compose", "-p", project}, args...)...)
			}
			down := compose("down", "-v")
			down.Dir = dir
			t.Cleanup(func() { _ = exec.Command("docker", "compose", "-p", project, "down", "-v").Run() })
			_ = down.Run()

			up := compose("up", "-d", "--wait")
			up.Dir = dir
			if out, err := up.CombinedOutput(); err != nil {
				t.Fatalf("compose up: %v: %s", err, out)
			}

			dump, ok := s.dumpDatabase(ctx, project)
			if !ok {
				t.Fatal("no dump taken")
			}
			text := string(dump)
			if !strings.Contains(text, "windlass_marker") || !strings.Contains(text, "backup-proof") {
				t.Fatalf("dump is missing the seeded row; first 400 bytes: %q", text[:min(400, len(text))])
			}
			if strings.ContainsRune(text, '\x00') {
				t.Fatal("dump contains NUL bytes, stream headers leaked into it")
			}
			if strings.Contains(text, "Using a password on the command line") {
				t.Fatal("stderr leaked into the dump")
			}
		})
	}
}
