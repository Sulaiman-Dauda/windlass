package local

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
)

// A rollback layers compose.rollback.yaml over the project's own file,
// which may be compose.yml. Naming compose.yaml regardless failed every
// rollback of such a project.
func TestUpNamesTheProjectsOwnComposeFile(t *testing.T) {
	root := t.TempDir()
	docker := filepath.Join(root, "docker")
	if err := os.WriteFile(docker, []byte("#!/bin/sh\necho \"$*\" > args\n"), 0o755); err != nil { //nolint:gosec // G306: a test shim must be executable
		t.Fatal(err)
	}
	projects := filepath.Join(root, "projects")
	l, err := New(Config{ProjectsDir: projects, DockerBin: docker})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		files []string
		extra []string
		want  string
	}{
		{[]string{"compose.yml"}, []string{"compose.rollback.yaml"}, "compose -p app -f compose.yml -f compose.rollback.yaml up -d --quiet-pull"},
		{[]string{"compose.yaml"}, []string{"compose.rollback.yaml"}, "compose -p app -f compose.yaml -f compose.rollback.yaml up -d --quiet-pull"},
		{[]string{"compose.yml"}, []string{"a.yaml", "b.yaml"}, "compose -p app -f compose.yml -f a.yaml -f b.yaml up -d --quiet-pull"},
		{[]string{"compose.yml"}, nil, "compose -p app up -d --quiet-pull"},
		// Compose loads an override file on its own only when no -f is given.
		{[]string{"compose.yaml", "compose.override.yaml"}, []string{"compose.rollback.yaml"},
			"compose -p app -f compose.yaml -f compose.override.yaml -f compose.rollback.yaml up -d --quiet-pull"},
		{[]string{"compose.yaml", "docker-compose.override.yml", "compose.override.yml"}, []string{"compose.rollback.yaml"},
			"compose -p app -f compose.yaml -f compose.override.yml -f compose.rollback.yaml up -d --quiet-pull"},
	} {
		dir := filepath.Join(projects, "app")
		_ = os.RemoveAll(dir)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		for _, f := range tc.files {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("services: {}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		req := agent.ComposeUpReq{Project: "app", ExtraFiles: tc.extra}
		if err := l.Compose().Up(context.Background(), req, func(agent.LogLine) {}); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dir, "args")) //nolint:gosec // G304: a file under t.TempDir()
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(got)) != tc.want {
			t.Errorf("%v with %v ran docker %s, want %s", tc.files, tc.extra, strings.TrimSpace(string(got)), tc.want)
		}
	}
}

// COMPOSE_FILE in .env picks the files a plain deploy loads, but Compose
// ignores it once any -f is given, so a rollback has to name those files
// itself or it runs without them, and --remove-orphans removes their services.
func TestUpKeepsComposeFileFromEnv(t *testing.T) {
	root := t.TempDir()
	docker := filepath.Join(root, "docker")
	if err := os.WriteFile(docker, []byte("#!/bin/sh\necho \"$*\" > args\n"), 0o755); err != nil { //nolint:gosec // G306: a test shim must be executable
		t.Fatal(err)
	}
	projects := filepath.Join(root, "projects")
	l, err := New(Config{ProjectsDir: projects, DockerBin: docker})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		env, want string
	}{
		{"COMPOSE_FILE=\"compose.yaml:compose.prod.yaml\"\n",
			"compose -p app -f compose.yaml -f compose.prod.yaml -f compose.rollback.yaml up -d --quiet-pull"},
		{"# deploy files\nexport COMPOSE_FILE='compose.yaml;deploy/prod.yaml'\nCOMPOSE_PATH_SEPARATOR=;\n",
			"compose -p app -f compose.yaml -f deploy/prod.yaml -f compose.rollback.yaml up -d --quiet-pull"},
		// Without it, the project file and its override, as before.
		{"APP_ENV=production\n",
			"compose -p app -f compose.yaml -f compose.override.yaml -f compose.rollback.yaml up -d --quiet-pull"},
	} {
		dir := filepath.Join(projects, "app")
		_ = os.RemoveAll(dir)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"compose.yaml", "compose.prod.yaml", "compose.override.yaml"} {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("services: {}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(tc.env), 0o600); err != nil {
			t.Fatal(err)
		}
		req := agent.ComposeUpReq{Project: "app", ExtraFiles: []string{"compose.rollback.yaml"}}
		if err := l.Compose().Up(context.Background(), req, func(agent.LogLine) {}); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dir, "args")) //nolint:gosec // G304: a file under t.TempDir()
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(got)) != tc.want {
			t.Errorf(".env %q ran docker %s, want %s", tc.env, strings.TrimSpace(string(got)), tc.want)
		}
	}
}

// Compose is inconsistent about which resource numbers it quotes: cpus comes
// back as a JSON number and mem_limit as a string. Parsing has to accept both
// shapes for either field, or a valid compose file fails to deploy.
func TestComposeConfigAcceptsQuotedAndBareLimits(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		mem     int64
		cpus    float64
	}{
		{"compose emits mem_limit quoted", `{"services":{"api":{"mem_limit":"268435456","cpus":0.5}}}`, 268435456, 0.5},
		{"both bare", `{"services":{"api":{"mem_limit":268435456,"cpus":0.5}}}`, 268435456, 0.5},
		{"both quoted", `{"services":{"api":{"mem_limit":"268435456","cpus":"0.5"}}}`, 268435456, 0.5},
		{"limits absent", `{"services":{"api":{"image":"nginx"}}}`, 0, 0},
		{"empty string", `{"services":{"api":{"mem_limit":""}}}`, 0, 0},
		// Compose applies deploy.resources.limits without Swarm. This is the
		// shape `docker compose config --format json` printed for it.
		{"deploy limits", `{"services":{"api":{"mem_limit":null,"deploy":{"resources":{"limits":{"cpus":0.5,"memory":"67108864"}},"placement":{}}}}}`, 67108864, 0.5},
		{"deploy null", `{"services":{"api":{"mem_limit":"100663296","cpus":0.25,"deploy":null}}}`, 100663296, 0.25},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg composeConfig
			if err := json.Unmarshal([]byte(tc.payload), &cfg); err != nil {
				t.Fatalf("parse compose config: %v", err)
			}
			mem, cpus := cfg.Services["api"].limits()
			if mem != tc.mem {
				t.Errorf("memory = %d, want %d", mem, tc.mem)
			}
			if cpus != tc.cpus {
				t.Errorf("cpus = %v, want %v", cpus, tc.cpus)
			}
		})
	}
}

func TestSubcommandSkipsFiles(t *testing.T) {
	for args, want := range map[string]string{
		"up -d":                        "up",
		"-f compose.yaml -f b.yaml up": "up",
		"pull --ignore-buildable":      "pull",
	} {
		if got := subcommand(strings.Fields(args)); got != want {
			t.Errorf("subcommand(%q) = %q, want %q", args, got, want)
		}
	}
}
