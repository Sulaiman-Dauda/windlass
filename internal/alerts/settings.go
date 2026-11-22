package alerts

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/windlass-dev/windlass/internal/attention"
	"github.com/windlass-dev/windlass/internal/notify"
	"github.com/windlass-dev/windlass/internal/store/db"
)

// Settings are the alert thresholds, stored as rows in the settings table.
type Settings struct {
	// Debounce is how long a condition must hold before it alerts. A failed
	// deploy and an available update alert at once.
	Debounce time.Duration
	// Realert is the least time between two alerts for one open incident.
	Realert time.Duration
	// RestartCount restarts inside RestartWindow make a restart loop.
	RestartCount  int
	RestartWindow time.Duration
	// CertDays is how close to expiry a certificate alerts.
	CertDays int
	// DiskPercent is the Needs you disk threshold, shared with the dashboard.
	DiskPercent int
}

// SettingsJSON is the API shape, in whole units a form can hold.
type SettingsJSON struct {
	DebounceSeconds int `json:"debounce_seconds"`
	RealertHours    int `json:"realert_hours"`
	RestartCount    int `json:"restart_count"`
	RestartMinutes  int `json:"restart_minutes"`
	CertDays        int `json:"cert_days"`
	DiskPercent     int `json:"disk_percent"`
}

type field struct {
	key           string
	def, min, max int
	get           func(*SettingsJSON) *int
	label         string
}

var fields = []field{
	{"alerts_debounce_seconds", 120, 0, 3600, func(s *SettingsJSON) *int { return &s.DebounceSeconds }, "debounce seconds"},
	{"alerts_realert_hours", 24, 1, 168, func(s *SettingsJSON) *int { return &s.RealertHours }, "re-alert hours"},
	{"alerts_restart_count", 3, 2, 50, func(s *SettingsJSON) *int { return &s.RestartCount }, "restart count"},
	{"alerts_restart_minutes", 10, 1, 240, func(s *SettingsJSON) *int { return &s.RestartMinutes }, "restart minutes"},
	// Caddy renews at 30 days left, so 30 or more would alert on every
	// healthy certificate.
	{"alerts_cert_days", 14, 1, 29, func(s *SettingsJSON) *int { return &s.CertDays }, "certificate days"},
	{attention.DiskPercentSetting, attention.DefaultDiskPercent, attention.MinDiskPercent, attention.MaxDiskPercent,
		func(s *SettingsJSON) *int { return &s.DiskPercent }, "disk percent"},
}

// LoadSettingsJSON reads every threshold; a missing or out of range value
// reads as its default.
func LoadSettingsJSON(ctx context.Context, q *db.Queries) SettingsJSON {
	var out SettingsJSON
	for _, f := range fields {
		v := f.def
		if raw, err := q.GetSetting(ctx, f.key); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n >= f.min && n <= f.max {
				v = n
			}
		}
		*f.get(&out) = v
	}
	return out
}

// LoadSettings reads the thresholds for a sweep.
func LoadSettings(ctx context.Context, q *db.Queries) Settings {
	j := LoadSettingsJSON(ctx, q)
	return Settings{
		Debounce:      time.Duration(j.DebounceSeconds) * time.Second,
		Realert:       time.Duration(j.RealertHours) * time.Hour,
		RestartCount:  j.RestartCount,
		RestartWindow: time.Duration(j.RestartMinutes) * time.Minute,
		CertDays:      j.CertDays,
		DiskPercent:   j.DiskPercent,
	}
}

// SaveSettings checks every value against its range, then stores them all.
func SaveSettings(ctx context.Context, q *db.Queries, in SettingsJSON) error {
	for _, f := range fields {
		if v := *f.get(&in); v < f.min || v > f.max {
			return fmt.Errorf("%s must be from %d to %d", f.label, f.min, f.max)
		}
	}
	for _, f := range fields {
		if err := q.SetSetting(ctx, db.SetSettingParams{Key: f.key, Value: strconv.Itoa(*f.get(&in))}); err != nil {
			return err
		}
	}
	return nil
}

func (s Settings) debounce(kind string) time.Duration {
	switch kind {
	case notify.KindDeployFailed, notify.KindUpdate:
		return 0
	}
	return s.Debounce
}
