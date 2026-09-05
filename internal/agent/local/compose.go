package local

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/windlass-dev/windlass/internal/agent"
)

// composeLocal executes Docker Compose through the CLI so behavior is
// identical to what a user gets running the same commands by hand
// (principles 1 and 6). Only --format json outputs are machine-parsed;
// everything else is opaque log text for the UI.
type composeLocal struct{ l *Local }

func (c composeLocal) cmd(ctx context.Context, project string, args ...string) (*exec.Cmd, error) {
	if !agent.ValidProjectName(project) {
		return nil, fmt.Errorf("invalid project name %q", project)
	}
	dir := filepath.Join(c.l.cfg.ProjectsDir, project)
	full := append([]string{"compose", "-p", project}, args...)
	cmd := exec.CommandContext(ctx, c.l.cfg.DockerBin, full...)
	cmd.Dir = dir
	return cmd, nil
}

func (c composeLocal) run(ctx context.Context, project string, out agent.LogSink, args ...string) error {
	cmd, err := c.cmd(ctx, project, args...)
	if err != nil {
		return err
	}
	if err := streamCmd(cmd, out); err != nil {
		return fmt.Errorf("docker compose %s: %w", args[0], err)
	}
	return nil
}

func (c composeLocal) Up(ctx context.Context, req agent.ComposeUpReq, out agent.LogSink) error {
	args := []string{}
	for _, f := range req.ExtraFiles {
		// Compose resolves -f relative to the project directory; reject
		// anything that could escape it.
		if strings.ContainsAny(f, "/\\") || strings.HasPrefix(f, ".") {
			return fmt.Errorf("invalid extra compose file %q", f)
		}
		args = append(args, "-f", "compose.yaml", "-f", f)
	}
	args = append(args, "up", "-d", "--quiet-pull")
	if req.RemoveOrphans {
		args = append(args, "--remove-orphans")
	}
	return c.run(ctx, req.Project, out, args...)
}

func (c composeLocal) Down(ctx context.Context, project string, removeVolumes bool, out agent.LogSink) error {
	args := []string{"down"}
	if removeVolumes {
		args = append(args, "--volumes")
	}
	return c.run(ctx, project, out, args...)
}

func (c composeLocal) Stop(ctx context.Context, project string, out agent.LogSink) error {
	return c.run(ctx, project, out, "stop")
}

func (c composeLocal) Restart(ctx context.Context, project string, out agent.LogSink) error {
	return c.run(ctx, project, out, "restart")
}

func (c composeLocal) Pull(ctx context.Context, project string, out agent.LogSink) error {
	// --ignore-buildable: services built locally have nothing to pull.
	return c.run(ctx, project, out, "pull", "--ignore-buildable")
}

func (c composeLocal) Build(ctx context.Context, project string, out agent.LogSink) error {
	return c.run(ctx, project, out, "build")
}

// composePS matches `docker compose ps --format json` (one JSON object per
// line since compose v2.21).
type composePS struct {
	Service    string `json:"Service"`
	Name       string `json:"Name"`
	State      string `json:"State"`
	Health     string `json:"Health"`
	ExitCode   int    `json:"ExitCode"`
	Image      string `json:"Image"`
	Publishers []struct {
		PublishedPort int    `json:"PublishedPort"`
		TargetPort    int    `json:"TargetPort"`
		Protocol      string `json:"Protocol"`
	} `json:"Publishers"`
}

func (c composeLocal) PS(ctx context.Context, project string) ([]agent.ServiceStatus, error) {
	cmd, err := c.cmd(ctx, project, "ps", "-a", "--format", "json")
	if err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker compose ps: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	var out []agent.ServiceStatus
	dec := json.NewDecoder(&stdout)
	for dec.More() {
		var row composePS
		if err := dec.Decode(&row); err != nil {
			return nil, fmt.Errorf("parse compose ps output: %w", err)
		}
		s := agent.ServiceStatus{
			Service:  row.Service,
			Name:     row.Name,
			State:    row.State,
			Health:   row.Health,
			ExitCode: row.ExitCode,
			Image:    row.Image,
		}
		for _, p := range row.Publishers {
			if p.PublishedPort > 0 {
				s.PublishedPorts = append(s.PublishedPorts, agent.PortBinding{
					HostPort:      p.PublishedPort,
					ContainerPort: p.TargetPort,
					Protocol:      p.Protocol,
				})
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// composeConfig matches the subset of `docker compose config --format json`
// Windlass needs.
type composeConfig struct {
	Services map[string]struct {
		Image  string          `json:"image"`
		Build  json.RawMessage `json:"build"`
		Expose []string        `json:"expose"`
		Ports  []struct {
			Target int `json:"target"`
		} `json:"ports"`
		Labels   map[string]string `json:"labels"`
		MemLimit byteValue         `json:"mem_limit"`
		CPUs     numberValue       `json:"cpus"`
	} `json:"services"`
}

// Compose versions have emitted resource numbers as both JSON numbers and
// strings. Accept both so a valid compose file never fails panel parsing.
type numberValue float64

func (n *numberValue) UnmarshalJSON(data []byte) error {
	var value float64
	if len(data) > 0 && data[0] == '"' {
		parsed, err := strconv.ParseFloat(strings.Trim(string(data), `"`), 64)
		if err != nil {
			return err
		}
		value = parsed
	} else if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*n = numberValue(value)
	return nil
}

// Compose emits mem_limit as a quoted byte count rather than a number, so it
// needs the same tolerance as cpus. Without it every compose file that sets a
// memory limit fails to parse and the deployment stops.
type byteValue int64

func (b *byteValue) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		text := strings.Trim(string(data), `"`)
		if text == "" {
			*b = 0
			return nil
		}
		parsed, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return err
		}
		*b = byteValue(parsed)
		return nil
	}
	var value int64
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*b = byteValue(value)
	return nil
}

func (c composeLocal) Config(ctx context.Context, project string) (agent.ResolvedConfig, error) {
	cmd, err := c.cmd(ctx, project, "config", "--format", "json")
	if err != nil {
		return agent.ResolvedConfig{}, err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// compose config failing means the file is invalid — surface its
		// message, which is the useful part.
		return agent.ResolvedConfig{}, fmt.Errorf("compose file invalid: %s", strings.TrimSpace(stderr.String()))
	}

	var cfg composeConfig
	if err := json.Unmarshal(stdout.Bytes(), &cfg); err != nil {
		return agent.ResolvedConfig{}, fmt.Errorf("parse compose config: %w", err)
	}
	resolved := agent.ResolvedConfig{Services: map[string]agent.ResolvedService{}}
	for name, svc := range cfg.Services {
		ports := make([]int, 0, len(svc.Expose)+len(svc.Ports))
		for _, exposed := range svc.Expose {
			if port, err := strconv.Atoi(strings.SplitN(exposed, "/", 2)[0]); err == nil {
				ports = append(ports, port)
			}
		}
		for _, published := range svc.Ports {
			ports = append(ports, published.Target)
		}
		resolved.Services[name] = agent.ResolvedService{
			Image: svc.Image, ContainerPorts: ports, MemoryLimit: int64(svc.MemLimit), CPULimit: float64(svc.CPUs),
			Build: len(svc.Build) > 0 && string(svc.Build) != "null",
		}
		if rawURL := strings.TrimSpace(svc.Labels["windlass.health.url"]); rawURL != "" {
			check := agent.ApplicationHealthCheck{Service: name, URL: rawURL,
				ExpectedStatus: 200, StabilitySeconds: 10}
			if value, err := strconv.Atoi(svc.Labels["windlass.health.status"]); err == nil && value > 0 {
				check.ExpectedStatus = value
			}
			if value, err := strconv.Atoi(svc.Labels["windlass.health.stability_seconds"]); err == nil && value >= 0 {
				check.StabilitySeconds = value
			}
			check.Contains = svc.Labels["windlass.health.contains"]
			resolved.HealthChecks = append(resolved.HealthChecks, check)
		}
	}
	return resolved, nil
}
