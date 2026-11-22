// Package notify sends alerts to the channels an admin sets up: a signed
// webhook, ntfy, Discord, Slack or email.
//
// Delivery goes through an outbox table, so an alert raised while a channel
// is down is retried with backoff rather than lost, and an alert survives a
// restart. One sender goroutine drains it, woken on each new row and by a
// one-minute tick for retries.
//
// A failed send never fails the thing that raised it: Enqueue only writes
// rows, and the outbox row is the only record of a send that gave up.
// Payloads carry names, kinds and short messages, never env values or file
// contents.
package notify

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store/db"
)

// Event kinds a channel can filter on. The Needs you kinds use the same names
// as internal/attention, so one name means one thing across the panel.
const (
	KindTest            = "test"
	KindDeployFailed    = "deploy_failed"
	KindDeploySucceeded = "deploy_succeeded"
	KindContainerDied   = "container_died"
	KindRestartLoop     = "restart_loop"
	KindUnhealthy       = "unhealthy"
	KindCertExpiring    = "cert_expiring"
	KindDisk            = "disk"
	KindNoBackup        = "no_backup"
	KindProxy           = "proxy"
	KindUpdate          = "update"
	KindDrift           = "drift"
	KindDNS             = "dns"
	KindCaddyResume     = "caddy_resume"
	KindBinaryOwner     = "binary_owner"
)

// Kinds lists every kind a channel filter may name, in the order the panel
// shows them.
var Kinds = []string{
	KindDeployFailed, KindDeploySucceeded, KindContainerDied, KindRestartLoop,
	KindUnhealthy, KindCertExpiring, KindDisk, KindNoBackup, KindProxy,
	KindUpdate, KindDrift, KindDNS, KindCaddyResume, KindBinaryOwner,
}

// Event is one alert.
type Event struct {
	Kind    string `json:"event"`
	Project string `json:"project,omitempty"`
	Title   string `json:"title"`
	Message string `json:"message"`
	// Recovered marks the message that says an incident has cleared.
	Recovered bool      `json:"recovered,omitempty"`
	Time      time.Time `json:"time"`
}

const (
	maxAttempts = 6
	batchSize   = 50
	keepFor     = 30 * 24 * time.Hour
	timeLayout  = "2006-01-02T15:04:05.000Z"
)

// backoff is the wait after attempt n (1-based) fails.
var backoff = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 3 * time.Hour}

func retryAfter(attempt int) time.Duration {
	if attempt-1 < len(backoff) {
		return backoff[attempt-1]
	}
	return backoff[len(backoff)-1]
}

type Service struct {
	q      *db.Queries
	box    *secrets.Box
	logger *slog.Logger
	wake   chan struct{}
	now    func() time.Time
}

func New(q *db.Queries, box *secrets.Box, logger *slog.Logger) *Service {
	return &Service{q: q, box: box, logger: logger, wake: make(chan struct{}, 1), now: time.Now}
}

// Channel is the safe shape: secret fields are blanked, with a flag saying
// whether one is stored.
type Channel struct {
	ID        int64          `json:"id"`
	Name      string         `json:"name"`
	Kind      string         `json:"kind"`
	Enabled   bool           `json:"enabled"`
	Events    []string       `json:"events"`
	Project   string         `json:"project,omitempty"`
	Config    map[string]any `json:"config"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
}

// ChannelInput is what the API accepts. A secret field left empty on update
// keeps the stored value, so editing a channel never needs the secret again.
type ChannelInput struct {
	Name    string            `json:"name"`
	Kind    string            `json:"kind"`
	Enabled *bool             `json:"enabled"`
	Events  []string          `json:"events"`
	Project string            `json:"project"`
	Config  map[string]string `json:"config"`
}

func (s *Service) decode(row db.NotificationChannel) (map[string]string, error) {
	plain, err := s.box.Decrypt(row.ConfigEnc)
	if err != nil {
		return nil, fmt.Errorf("channel %d: config cannot be decrypted", row.ID)
	}
	var cfg map[string]string
	if err := json.Unmarshal(plain, &cfg); err != nil {
		return nil, fmt.Errorf("channel %d: config is not valid JSON", row.ID)
	}
	return cfg, nil
}

func (s *Service) toChannel(row db.NotificationChannel) Channel {
	ch := Channel{
		ID: row.ID, Name: row.Name, Kind: row.Kind, Enabled: row.Enabled == 1,
		Project: row.Project.String, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		Events: []string{},
	}
	_ = json.Unmarshal([]byte(row.Events), &ch.Events)
	cfg, err := s.decode(row)
	if err != nil {
		ch.Config = map[string]any{"error": err.Error()}
		return ch
	}
	ch.Config = redact(row.Kind, cfg)
	return ch
}

func (s *Service) List(ctx context.Context) ([]Channel, error) {
	rows, err := s.q.ListNotificationChannels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Channel, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.toChannel(r))
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, id int64) (Channel, error) {
	row, err := s.q.GetNotificationChannel(ctx, id)
	if err != nil {
		return Channel{}, err
	}
	return s.toChannel(row), nil
}

func validateCommon(in *ChannelInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Project = strings.TrimSpace(in.Project)
	if in.Name == "" {
		return errors.New("a channel needs a name")
	}
	if len(in.Name) > 100 {
		return errors.New("the name is longer than 100 characters")
	}
	if in.Events == nil {
		in.Events = []string{}
	}
	for _, e := range in.Events {
		if !slices.Contains(Kinds, e) {
			return fmt.Errorf("unknown event %q", e)
		}
	}
	return nil
}

func nullable(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func (s *Service) seal(kind string, cfg map[string]string) ([]byte, error) {
	if err := validateConfig(kind, cfg); err != nil {
		return nil, err
	}
	plain, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return s.box.Encrypt(plain)
}

func (s *Service) Create(ctx context.Context, in ChannelInput) (Channel, error) {
	if err := validateCommon(&in); err != nil {
		return Channel{}, err
	}
	if _, ok := senders[in.Kind]; !ok {
		return Channel{}, fmt.Errorf("unknown channel kind %q", in.Kind)
	}
	cfg := cleanConfig(in.Kind, in.Config)
	enc, err := s.seal(in.Kind, cfg)
	if err != nil {
		return Channel{}, err
	}
	events, _ := json.Marshal(in.Events)
	enabled := in.Enabled == nil || *in.Enabled
	row, err := s.q.CreateNotificationChannel(ctx, db.CreateNotificationChannelParams{
		Name: in.Name, Kind: in.Kind, ConfigEnc: enc, Enabled: boolInt(enabled),
		Events: string(events), Project: nullable(in.Project),
	})
	if err != nil {
		return Channel{}, err
	}
	return s.toChannel(row), nil
}

// Update changes a channel. Its kind is fixed once created: the fields mean
// different things per kind, so a change of kind is a new channel.
func (s *Service) Update(ctx context.Context, id int64, in ChannelInput) (Channel, error) {
	row, err := s.q.GetNotificationChannel(ctx, id)
	if err != nil {
		return Channel{}, err
	}
	if in.Kind != "" && in.Kind != row.Kind {
		return Channel{}, errors.New("a channel's kind cannot change; add a new channel")
	}
	if err := validateCommon(&in); err != nil {
		return Channel{}, err
	}
	old, err := s.decode(row)
	if err != nil {
		return Channel{}, err
	}
	cfg := cleanConfig(row.Kind, in.Config)
	moved := false
	for _, f := range destinationFields[row.Kind] {
		if cfg[f] != old[f] {
			moved = true
		}
	}
	for _, f := range secretFields[row.Kind] {
		if cfg[f] == "" && old[f] != "" && !moved {
			cfg[f] = old[f]
		}
	}
	enc, err := s.seal(row.Kind, cfg)
	if err != nil {
		return Channel{}, err
	}
	events, _ := json.Marshal(in.Events)
	enabled := row.Enabled == 1
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	updated, err := s.q.UpdateNotificationChannel(ctx, db.UpdateNotificationChannelParams{
		Name: in.Name, ConfigEnc: enc, Enabled: boolInt(enabled),
		Events: string(events), Project: nullable(in.Project), ID: id,
	})
	if err != nil {
		return Channel{}, err
	}
	return s.toChannel(updated), nil
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.q.DeleteNotificationChannel(ctx, id)
}

// Test sends a test message to one channel straight away, outside the
// outbox, so the admin sees the real error on the screen.
func (s *Service) Test(ctx context.Context, id int64) error {
	row, err := s.q.GetNotificationChannel(ctx, id)
	if err != nil {
		return err
	}
	cfg, err := s.decode(row)
	if err != nil {
		return err
	}
	return send(ctx, row.Kind, cfg, Event{
		Kind:    KindTest,
		Title:   "Windlass test alert",
		Message: fmt.Sprintf("This is a test from Windlass. The %q channel works.", row.Name),
		Time:    s.now().UTC(),
	})
}

// optIn kinds reach a channel only when it names them: an empty filter means
// every problem, and a successful deploy is not one.
var optIn = []string{KindDeploySucceeded}

func matches(row db.NotificationChannel, e Event) bool {
	if row.Project.Valid && row.Project.String != "" && row.Project.String != e.Project {
		return false
	}
	var events []string
	_ = json.Unmarshal([]byte(row.Events), &events)
	if len(events) == 0 {
		return !slices.Contains(optIn, e.Kind)
	}
	return slices.Contains(events, e.Kind)
}

// Enqueue writes one outbox row per enabled channel that takes e, and wakes
// the sender. Callers log a returned error and carry on: an alert that could
// not be queued is never the caller's failure.
func (s *Service) Enqueue(ctx context.Context, e Event) error {
	if e.Time.IsZero() {
		e.Time = s.now().UTC()
	}
	rows, err := s.q.ListEnabledNotificationChannels(ctx)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(e)
	if err != nil {
		return err
	}
	queued := 0
	for _, row := range rows {
		if !matches(row, e) {
			continue
		}
		if _, err := s.q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
			ChannelID: row.ID, Event: e.Kind, Project: nullable(e.Project), Payload: string(payload),
			NextAttemptAt: e.Time.UTC().Format(timeLayout),
		}); err != nil {
			return err
		}
		queued++
	}
	if queued > 0 {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return nil
}

// Delivery is one outbox row as the panel shows it.
type Delivery struct {
	ID        int64  `json:"id"`
	ChannelID int64  `json:"channel_id"`
	Channel   string `json:"channel"`
	Event     string `json:"event"`
	Project   string `json:"project,omitempty"`
	Status    string `json:"status"`
	Attempts  int64  `json:"attempts"`
	LastError string `json:"last_error,omitempty"`
	CreatedAt string `json:"created_at"`
	SentAt    string `json:"sent_at,omitempty"`
}

func (s *Service) Recent(ctx context.Context, limit int64) ([]Delivery, error) {
	rows, err := s.q.ListRecentNotifications(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Delivery, 0, len(rows))
	for _, r := range rows {
		out = append(out, Delivery{
			ID: r.ID, ChannelID: r.ChannelID, Channel: r.ChannelName, Event: r.Event,
			Project: r.Project.String, Status: r.Status, Attempts: r.Attempts,
			LastError: r.LastError.String, CreatedAt: r.CreatedAt, SentAt: r.SentAt.String,
		})
	}
	return out, nil
}

// Run drains the outbox until ctx ends.
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	lastPrune := time.Time{}
	for {
		s.Drain(ctx)
		if s.now().Sub(lastPrune) > time.Hour {
			cutoff := s.now().UTC().Add(-keepFor).Format(timeLayout)
			if err := s.q.PruneNotifications(ctx, cutoff); err != nil && ctx.Err() == nil {
				s.logger.Warn("notify: prune failed", "err", err)
			}
			lastPrune = s.now()
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-tick.C:
		}
	}
}

// Drain sends every due row once. Exported for tests.
//
// A channel that fails is skipped for the rest of the pass, so one dead
// receiver costs one timeout rather than one per queued row and the other
// channels still get their alerts. A failed outbox write ends the pass: the
// row is still due, and carrying on would send it again at once.
func (s *Service) Drain(ctx context.Context) {
	failed := map[int64]bool{}
	for ctx.Err() == nil {
		due, err := s.q.ListDueNotifications(ctx, db.ListDueNotificationsParams{
			NextAttemptAt: s.now().UTC().Format(timeLayout), Limit: batchSize,
		})
		if err != nil {
			if ctx.Err() == nil {
				s.logger.Warn("notify: reading the outbox failed", "err", err)
			}
			return
		}
		skipped := false
		for _, n := range due {
			if failed[n.ChannelID] {
				skipped = true
				continue
			}
			sent, err := s.deliver(ctx, n)
			if err != nil {
				s.logger.Warn("notify: recording a delivery failed", "id", n.ID, "err", err)
				return
			}
			if !sent {
				failed[n.ChannelID] = true
			}
		}
		if len(due) < batchSize || skipped {
			return
		}
	}
}

// deliver sends one row and records the outcome. err is a failure to record
// it, which the caller must not carry on past.
func (s *Service) deliver(ctx context.Context, n db.Notification) (sent bool, err error) {
	sendErr := s.attempt(ctx, n)
	if sendErr == nil {
		return true, s.q.MarkNotificationSent(ctx, n.ID)
	}
	msg := nullable(truncate(sendErr.Error(), 500))
	attempt := int(n.Attempts) + 1
	if attempt >= maxAttempts || isPermanent(sendErr) {
		s.logger.Warn("notify: giving up", "id", n.ID, "channel", n.ChannelID, "event", n.Event, "err", sendErr)
		return false, s.q.MarkNotificationFailed(ctx, db.MarkNotificationFailedParams{LastError: msg, ID: n.ID})
	}
	next := s.now().UTC().Add(retryAfter(attempt)).Format(timeLayout)
	return false, s.q.MarkNotificationRetry(ctx, db.MarkNotificationRetryParams{LastError: msg, NextAttemptAt: next, ID: n.ID})
}

// permanentError marks a failure a retry cannot fix. Its text is the cause
// alone, because the panel shows it.
type permanentError struct{ err error }

func (p permanentError) Error() string { return p.err.Error() }
func (p permanentError) Unwrap() error { return p.err }

func permanent(err error) error { return permanentError{err} }

func isPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

func (s *Service) attempt(ctx context.Context, n db.Notification) error {
	row, err := s.q.GetNotificationChannel(ctx, n.ChannelID)
	if err != nil {
		return permanent(fmt.Errorf("channel not found"))
	}
	if row.Enabled != 1 {
		return permanent(fmt.Errorf("channel is turned off"))
	}
	cfg, err := s.decode(row)
	if err != nil {
		return permanent(err)
	}
	var e Event
	if err := json.Unmarshal([]byte(n.Payload), &e); err != nil {
		return permanent(fmt.Errorf("payload is not valid JSON"))
	}
	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return send(sendCtx, row.Kind, cfg, e)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
