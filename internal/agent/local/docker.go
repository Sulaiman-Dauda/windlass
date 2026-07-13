package local

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/windlass-dev/windlass/internal/agent"
)

const (
	labelComposeProject = "com.docker.compose.project"
	labelComposeService = "com.docker.compose.service"
)

type dockerLocal struct{ l *Local }

func (d dockerLocal) ListContainers(ctx context.Context, filter agent.ContainerFilter) ([]agent.Container, error) {
	cli, err := d.l.docker()
	if err != nil {
		return nil, err
	}

	opts := container.ListOptions{All: true}
	if filter.ComposeProject != "" {
		opts.Filters = filters.NewArgs(filters.Arg("label", labelComposeProject+"="+filter.ComposeProject))
	}
	list, err := cli.ContainerList(ctx, opts)
	if err != nil {
		return nil, err
	}

	out := make([]agent.Container, 0, len(list))
	for _, c := range list {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		ac := agent.Container{
			ID:             c.ID,
			Name:           name,
			Image:          c.Image,
			State:          c.State,
			ComposeProject: c.Labels[labelComposeProject],
			ComposeService: c.Labels[labelComposeService],
			CreatedAt:      time.Unix(c.Created, 0).UTC(),
		}
		if c.NetworkSettings != nil {
			for _, nw := range c.NetworkSettings.Networks {
				if nw.IPAddress != "" {
					ac.IPAddress = nw.IPAddress
					break
				}
			}
		}
		// Health and restart count need an inspect; keep the list cheap and
		// parse health from the human status when present.
		if i := strings.Index(c.Status, "(healthy)"); i >= 0 {
			ac.Health = "healthy"
		} else if strings.Contains(c.Status, "(unhealthy)") {
			ac.Health = "unhealthy"
		} else if strings.Contains(c.Status, "(health: starting)") {
			ac.Health = "starting"
		}
		out = append(out, ac)
	}
	return out, nil
}

func (d dockerLocal) Logs(ctx context.Context, containerID string, opts agent.LogOpts, out agent.LogSink) error {
	cli, err := d.l.docker()
	if err != nil {
		return err
	}

	inspect, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return err
	}

	tail := "200"
	if opts.Tail > 0 {
		tail = fmt.Sprintf("%d", opts.Tail)
	}
	rc, err := cli.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     opts.Follow,
		Timestamps: false,
		Tail:       tail,
	})
	if err != nil {
		return err
	}
	defer rc.Close()

	emit := func(stream, text string) {
		out(agent.LogLine{Stream: stream, Text: text, Time: time.Now().UTC()})
	}

	if inspect.Config != nil && inspect.Config.Tty {
		// TTY containers produce a raw stream.
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			emit("stdout", sc.Text())
		}
		return sc.Err()
	}

	// Non-TTY streams are multiplexed; demux into two line writers.
	stdout := &lineWriter{stream: "stdout", emit: emit}
	stderr := &lineWriter{stream: "stderr", emit: emit}
	_, err = stdcopy.StdCopy(stdout, stderr, rc)
	stdout.flush()
	stderr.flush()
	if ctx.Err() != nil {
		return nil // follow cancelled by caller
	}
	return err
}

// lineWriter splits a byte stream into lines for the LogSink.
type lineWriter struct {
	stream string
	emit   func(stream, text string)
	buf    []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := strings.IndexByte(string(w.buf), '\n')
		if i < 0 {
			break
		}
		w.emit(w.stream, strings.TrimRight(string(w.buf[:i]), "\r"))
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.emit(w.stream, string(w.buf))
		w.buf = nil
	}
}

func (d dockerLocal) Stats(ctx context.Context, containerIDs []string) ([]agent.ContainerStats, error) {
	cli, err := d.l.docker()
	if err != nil {
		return nil, err
	}

	out := make([]agent.ContainerStats, 0, len(containerIDs))
	for _, id := range containerIDs {
		resp, err := cli.ContainerStatsOneShot(ctx, id)
		if err != nil {
			continue // container may have exited between list and stats
		}
		var s container.StatsResponse
		if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		stat := agent.ContainerStats{
			ContainerID: id,
			MemoryBytes: s.MemoryStats.Usage,
			MemoryLimit: s.MemoryStats.Limit,
		}
		// CPU percent relative to the previous sample the daemon includes.
		cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
		sysDelta := float64(s.CPUStats.SystemUsage) - float64(s.PreCPUStats.SystemUsage)
		if sysDelta > 0 && cpuDelta >= 0 {
			online := float64(s.CPUStats.OnlineCPUs)
			if online == 0 {
				online = float64(len(s.CPUStats.CPUUsage.PercpuUsage))
			}
			stat.CPUPercent = cpuDelta / sysDelta * online * 100
		}
		for _, nw := range s.Networks {
			stat.NetRxBytes += nw.RxBytes
			stat.NetTxBytes += nw.TxBytes
		}
		out = append(out, stat)
	}
	return out, nil
}

func (d dockerLocal) ImageTag(ctx context.Context, source, target string) error {
	cli, err := d.l.docker()
	if err != nil {
		return err
	}
	return cli.ImageTag(ctx, source, target)
}

func (d dockerLocal) ImageDigest(ctx context.Context, ref string) (string, error) {
	cli, err := d.l.docker()
	if err != nil {
		return "", err
	}
	inspect, _, err := cli.ImageInspectWithRaw(ctx, ref)
	if err != nil {
		return "", err
	}
	// Prefer the registry digest; fall back to the local content ID.
	for _, rd := range inspect.RepoDigests {
		if i := strings.Index(rd, "@"); i >= 0 {
			return rd[i+1:], nil
		}
	}
	return inspect.ID, nil
}

func (d dockerLocal) Events(ctx context.Context, out func(agent.DockerEvent)) error {
	cli, err := d.l.docker()
	if err != nil {
		return err
	}
	msgs, errs := cli.Events(ctx, events.ListOptions{
		Filters: filters.NewArgs(filters.Arg("type", "container")),
	})
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errs:
			return err
		case m := <-msgs:
			out(agent.DockerEvent{
				Type:   string(m.Type),
				Action: string(m.Action),
				ID:     m.Actor.ID,
				Name:   m.Actor.Attributes["name"],
				Time:   time.Unix(m.Time, 0).UTC(),
			})
		}
	}
}
