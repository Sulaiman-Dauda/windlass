//go:build linux

package local

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/windlass-dev/windlass/internal/agent"
)

// readHostMetrics samples /proc directly — a metrics dependency is not
// justified for five numbers (principle 10).
func readHostMetrics(ctx context.Context, diskPath string) (agent.HostMetrics, error) {
	var m agent.HostMetrics

	// CPU: two /proc/stat samples 200ms apart.
	idle1, total1, err := readCPUSample()
	if err == nil {
		select {
		case <-ctx.Done():
			return m, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
		if idle2, total2, err := readCPUSample(); err == nil && total2 > total1 {
			idleDelta := float64(idle2 - idle1)
			totalDelta := float64(total2 - total1)
			m.CPUPercent = (1 - idleDelta/totalDelta) * 100
		}
	}

	// Memory: MemTotal and MemAvailable from /proc/meminfo.
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		var totalKB, availKB uint64
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			v, _ := strconv.ParseUint(fields[1], 10, 64)
			switch fields[0] {
			case "MemTotal:":
				totalKB = v
			case "MemAvailable:":
				availKB = v
			}
		}
		m.MemoryTotal = totalKB * 1024
		if totalKB >= availKB {
			m.MemoryUsed = (totalKB - availKB) * 1024
		}
	}

	// Load average + uptime.
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		if fields := strings.Fields(string(data)); len(fields) > 0 {
			m.Load1, _ = strconv.ParseFloat(fields[0], 64)
		}
	}
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		if fields := strings.Fields(string(data)); len(fields) > 0 {
			up, _ := strconv.ParseFloat(fields[0], 64)
			m.UptimeSeconds = uint64(up)
		}
	}

	// Disk: statfs on the projects volume.
	var st unix.Statfs_t
	if err := unix.Statfs(diskPath, &st); err == nil {
		bs := uint64(st.Bsize)
		m.DiskTotal = st.Blocks * bs
		m.DiskUsed = (st.Blocks - st.Bavail) * bs
	}

	return m, nil
}

func readCPUSample() (idle, total uint64, err error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, err
	}
	line, _, _ := strings.Cut(string(data), "\n")
	fields := strings.Fields(line) // "cpu user nice system idle iowait irq softirq steal ..."
	for i, f := range fields[1:] {
		v, _ := strconv.ParseUint(f, 10, 64)
		total += v
		if i == 3 || i == 4 { // idle + iowait
			idle += v
		}
	}
	return idle, total, nil
}
