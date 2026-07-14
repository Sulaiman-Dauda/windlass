package local

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

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

	opts := client.ContainerListOptions{All: true}
	if filter.ComposeProject != "" {
		opts.Filters = make(client.Filters).Add("label", labelComposeProject+"="+filter.ComposeProject)
	}
	result, err := cli.ContainerList(ctx, opts)
	if err != nil {
		return nil, err
	}

	out := make([]agent.Container, 0, len(result.Items))
	for _, c := range result.Items {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		ac := agent.Container{
			ID:             c.ID,
			Name:           name,
			Image:          c.Image,
			State:          string(c.State),
			ComposeProject: c.Labels[labelComposeProject],
			ComposeService: c.Labels[labelComposeService],
			CreatedAt:      time.Unix(c.Created, 0).UTC(),
		}
		if c.NetworkSettings != nil {
			for _, nw := range c.NetworkSettings.Networks {
				if nw.IPAddress.IsValid() {
					ac.IPAddress = nw.IPAddress.String()
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

	inspectResult, err := cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return err
	}

	tail := "200"
	if opts.Tail > 0 {
		tail = fmt.Sprintf("%d", opts.Tail)
	}
	rc, err := cli.ContainerLogs(ctx, containerID, client.ContainerLogsOptions{
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

	if inspectResult.Container.Config != nil && inspectResult.Container.Config.Tty {
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
		resp, err := cli.ContainerStats(ctx, id, client.ContainerStatsOptions{
			Stream: false, IncludePreviousSample: true,
		})
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
	_, err = cli.ImageTag(ctx, client.ImageTagOptions{Source: source, Target: target})
	return err
}

func (d dockerLocal) ImageDigest(ctx context.Context, ref string) (string, error) {
	cli, err := d.l.docker()
	if err != nil {
		return "", err
	}
	inspect, err := cli.ImageInspect(ctx, ref)
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

func (d dockerLocal) ImageDiskUsage(ctx context.Context) (agent.ImageDiskUsage, error) {
	cli, err := d.l.docker()
	if err != nil {
		return agent.ImageDiskUsage{}, err
	}
	usage, err := cli.DiskUsage(ctx, client.DiskUsageOptions{})
	if err != nil {
		return agent.ImageDiskUsage{}, err
	}
	return agent.ImageDiskUsage{TotalCount: usage.Images.TotalCount,
		ActiveCount: usage.Images.ActiveCount, TotalBytes: usage.Images.TotalSize,
		ReclaimableBytes: usage.Images.Reclaimable}, nil
}

func (d dockerLocal) PruneImages(ctx context.Context, req agent.ImagePruneReq) (agent.ImagePruneResult, error) {
	cli, err := d.l.docker()
	if err != nil {
		return agent.ImagePruneResult{}, err
	}
	usage, err := cli.DiskUsage(ctx, client.DiskUsageOptions{})
	if err != nil {
		return agent.ImagePruneResult{}, err
	}
	protected := make(map[string]bool, len(req.ProtectedDigests))
	for _, digest := range req.ProtectedDigests {
		protected[digest] = true
	}
	cutoff := time.Now().Add(-time.Duration(req.OlderThanSeconds) * time.Second).Unix()
	var result agent.ImagePruneResult
	for _, image := range usage.Images.Items {
		if image.Containers != 0 || (req.OlderThanSeconds > 0 && image.Created > cutoff) {
			continue
		}
		keep := protected[image.ID]
		for _, digest := range image.RepoDigests {
			if protected[digest] || protected[strings.TrimPrefix(digest[strings.LastIndex(digest, "@")+1:], "@")] {
				keep = true
			}
		}
		if keep {
			continue
		}
		if _, err := cli.ImageRemove(ctx, image.ID, client.ImageRemoveOptions{PruneChildren: true}); err != nil {
			continue // raced with a deployment or another tag; leave it safely
		}
		result.Deleted++
		result.ReclaimedBytes += image.Size
	}
	return result, nil
}

func (d dockerLocal) Events(ctx context.Context, out func(agent.DockerEvent)) error {
	cli, err := d.l.docker()
	if err != nil {
		return err
	}
	result := cli.Events(ctx, client.EventsListOptions{
		Filters: make(client.Filters).Add("type", "container"),
	})
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-result.Err:
			return err
		case m := <-result.Messages:
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
