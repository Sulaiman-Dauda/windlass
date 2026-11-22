package notify

import (
	"bufio"
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/migrations"
)

func testService(t *testing.T) (*Service, *db.Queries) {
	t.Helper()
	conn, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := store.Migrate(conn, migrations.FS); err != nil {
		t.Fatal(err)
	}
	box, _ := secrets.New(make([]byte, 32))
	q := db.New(conn)
	return New(q, box, slog.New(slog.NewTextHandler(io.Discard, nil))), q
}

// allowLocal lets the senders reach a receiver on 127.0.0.1 for one test.
func allowLocal(t *testing.T) {
	t.Helper()
	orig := dialFunc
	dialFunc = func(ctx context.Context, network, addr string, _ bool) (net.Conn, error) {
		return dialer(true).DialContext(ctx, network, addr)
	}
	t.Cleanup(func() { dialFunc = orig })
}

func onlyRow(t *testing.T, q *db.Queries) db.Notification {
	t.Helper()
	rows, err := q.ListDueNotifications(context.Background(), db.ListDueNotificationsParams{NextAttemptAt: "9999", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	recent, _ := q.ListRecentNotifications(context.Background(), 10)
	if len(recent) != 1 {
		t.Fatalf("want 1 outbox row, got %d", len(recent))
	}
	for _, r := range rows {
		if r.ID == recent[0].ID {
			return r
		}
	}
	return db.Notification{ID: recent[0].ID, Status: recent[0].Status, Attempts: recent[0].Attempts, LastError: recent[0].LastError}
}

// fixtureSigningKey is a made-up test value, not a credential.
var fixtureSigningKey = strings.Repeat("k", 16)

func webhook(t *testing.T, s *Service, url string) Channel {
	t.Helper()
	ch, err := s.Create(context.Background(), ChannelInput{
		Name: "hook", Kind: ChannelWebhook,
		Config: map[string]string{"url": url, "secret": fixtureSigningKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func TestRefused(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1": true, "::1": true, "10.1.2.3": true, "172.16.0.1": true,
		"192.168.1.1": true, "169.254.169.254": true, "fe80::1": true, "fc00::1": true,
		"0.0.0.0": true, "::ffff:127.0.0.1": true, "224.0.0.1": true,
		"1.1.1.1": false, "2606:4700:4700::1111": false,
	}
	for ip, want := range cases {
		if got := refused(netip.MustParseAddr(ip), false); got != want {
			t.Errorf("refused(%s) = %v, want %v", ip, got, want)
		}
	}
	if refused(netip.MustParseAddr("127.0.0.1"), true) {
		t.Error("loopback refused with allowLoopback")
	}
	if !refused(netip.MustParseAddr("10.0.0.1"), true) {
		t.Error("allowLoopback must not open the private ranges")
	}
}

// A name that resolves to loopback is refused at dial time, after DNS, and
// the receiver never sees a request. One attempt, then failed: retrying a
// refused address cannot help.
func TestLoopbackRefusedAfterDNS(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))

	s, q := testService(t)
	webhook(t, s, "http://localhost:"+port+"/hook")
	if err := s.Enqueue(context.Background(), Event{Kind: KindDeployFailed, Title: "x", Message: "y"}); err != nil {
		t.Fatal(err)
	}
	s.Drain(context.Background())
	if hit {
		t.Fatal("the request reached a loopback receiver")
	}
	row := onlyRow(t, q)
	if row.Status != "failed" || row.Attempts != 1 || !strings.Contains(row.LastError.String, "private, loopback") {
		t.Fatalf("got %+v", row)
	}
}

func TestWebhookSignature(t *testing.T) {
	allowLocal(t)
	var got Event
	var sigOK bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		want := sign(fixtureSigningKey, r.Header.Get("X-Windlass-Timestamp"), body)
		sigOK = hmac.Equal([]byte(want), []byte(r.Header.Get("X-Windlass-Signature")))
		_ = json.Unmarshal(body, &got)
	}))
	defer srv.Close()

	s, q := testService(t)
	webhook(t, s, srv.URL)
	_ = s.Enqueue(context.Background(), Event{Kind: KindDeployFailed, Project: "shop", Title: "Deploy failed", Message: "pull failed"})
	s.Drain(context.Background())
	if !sigOK {
		t.Fatal("signature did not verify")
	}
	if got.Kind != KindDeployFailed || got.Project != "shop" {
		t.Fatalf("payload %+v", got)
	}
	if row := onlyRow(t, q); row.Status != "sent" {
		t.Fatalf("status %s", row.Status)
	}
}

func TestRetryThenGiveUp(t *testing.T) {
	allowLocal(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	s, q := testService(t)
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	webhook(t, s, srv.URL)
	_ = s.Enqueue(context.Background(), Event{Kind: KindDisk, Title: "Disk", Message: "90%"})

	s.Drain(context.Background())
	if row := onlyRow(t, q); row.Status != "pending" || row.Attempts != 1 {
		t.Fatalf("after one failure: %+v", row)
	}
	s.Drain(context.Background()) // not due yet
	if calls != 1 {
		t.Fatalf("retried before the backoff: %d calls", calls)
	}
	for range maxAttempts {
		now = now.Add(4 * time.Hour)
		s.Drain(context.Background())
	}
	if calls != maxAttempts {
		t.Fatalf("calls = %d, want %d", calls, maxAttempts)
	}
	if row := onlyRow(t, q); row.Status != "failed" || !strings.Contains(row.LastError.String, "502") {
		t.Fatalf("after giving up: %+v", row)
	}
}

func TestClientErrorIsPermanent(t *testing.T) {
	allowLocal(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	s, q := testService(t)
	webhook(t, s, srv.URL)
	_ = s.Enqueue(context.Background(), Event{Kind: KindDisk, Title: "Disk", Message: "90%"})
	s.Drain(context.Background())
	if row := onlyRow(t, q); row.Status != "failed" || row.Attempts != 1 {
		t.Fatalf("got %+v", row)
	}
}

func TestSecretsRedactedAndKept(t *testing.T) {
	s, _ := testService(t)
	ch, err := s.Create(context.Background(), ChannelInput{
		Name: "discord", Kind: ChannelDiscord,
		Config: map[string]string{"url": "https://discord.com/api/webhooks/1/secret-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(ch)
	if strings.Contains(string(raw), "secret-token") {
		t.Fatalf("secret returned: %s", raw)
	}
	if ch.Config["url_set"] != true {
		t.Fatalf("url_set missing: %s", raw)
	}
	off := false
	if _, err := s.Update(context.Background(), ch.ID, ChannelInput{Name: "renamed", Enabled: &off, Config: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	row, _ := s.q.GetNotificationChannel(context.Background(), ch.ID)
	cfg, _ := s.decode(row)
	if cfg["url"] != "https://discord.com/api/webhooks/1/secret-token" || row.Name != "renamed" || row.Enabled != 0 {
		t.Fatalf("update lost the secret or the fields: %+v %v", row, cfg)
	}
	if _, err := s.Update(context.Background(), ch.ID, ChannelInput{Name: "x", Kind: ChannelSlack}); err == nil {
		t.Fatal("kind change accepted")
	}
}

// A Discord or Slack URL is the credential, so a failure message must not
// repeat it.
func TestErrorDoesNotLeakURL(t *testing.T) {
	err := sendDiscord(context.Background(), map[string]string{"url": "http://127.0.0.1:1/api/webhooks/1/secret-token"}, Event{})
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(err, ErrRefusedAddress) {
		t.Fatalf("want refused, got %v", err)
	}
}

func TestFilters(t *testing.T) {
	s, q := testService(t)
	ctx := context.Background()
	mk := func(name string, events []string, project string) {
		if _, err := s.Create(ctx, ChannelInput{
			Name: name, Kind: ChannelNtfy, Events: events, Project: project,
			Config: map[string]string{"topic": "t"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk("all", nil, "")
	mk("deploys", []string{KindDeployFailed}, "")
	mk("shop", nil, "shop")
	off := false
	if _, err := s.Create(ctx, ChannelInput{Name: "off", Kind: ChannelNtfy, Enabled: &off, Config: map[string]string{"topic": "t"}}); err != nil {
		t.Fatal(err)
	}
	_ = s.Enqueue(ctx, Event{Kind: KindDisk, Title: "d"})
	_ = s.Enqueue(ctx, Event{Kind: KindDeployFailed, Project: "blog", Title: "f"})
	_ = s.Enqueue(ctx, Event{Kind: KindDeployFailed, Project: "shop", Title: "f"})
	rows, _ := q.ListRecentNotifications(ctx, 50)
	count := map[string]int{}
	for _, r := range rows {
		count[r.ChannelName]++
	}
	want := map[string]int{"all": 3, "deploys": 2, "shop": 1}
	for k, v := range want {
		if count[k] != v {
			t.Errorf("%s: %d rows, want %d", k, count[k], v)
		}
	}
	if count["off"] != 0 {
		t.Error("a turned-off channel got a row")
	}
	if _, err := s.Create(ctx, ChannelInput{Name: "bad", Kind: ChannelNtfy, Events: []string{"nope"}, Config: map[string]string{"topic": "t"}}); err == nil {
		t.Error("unknown event accepted")
	}
}

func TestValidation(t *testing.T) {
	bad := []struct {
		kind string
		cfg  map[string]string
	}{
		{ChannelWebhook, map[string]string{"url": "https://x.example", "secret": "short"}},
		{ChannelWebhook, map[string]string{"url": "ftp://x.example", "secret": fixtureSigningKey}},
		{ChannelWebhook, map[string]string{"url": "https://"+"user:pw"+"@x.example", "secret": fixtureSigningKey}},
		{ChannelNtfy, map[string]string{"topic": "a/b"}},
		{ChannelEmail, map[string]string{"host": "smtp.example", "from": "a@example.com", "to": "b@example.com", "security": "none", "username": "u", "password": "p"}},
		{ChannelEmail, map[string]string{"host": "smtp.example", "from": "nope", "to": "b@example.com"}},
		{ChannelEmail, map[string]string{"host": "smtp.example", "from": "a@example.com"}},
	}
	for i, c := range bad {
		if err := validateConfig(c.kind, c.cfg); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

// fakeSMTP is just enough of an SMTP server to take one message.
func fakeSMTP(t *testing.T) (addr string, got func() string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var data strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		write := func(s string) { _, _ = io.WriteString(c, s+"\r\n") }
		write("220 fake")
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					write("250 ok")
					continue
				}
				mu.Lock()
				data.WriteString(line)
				mu.Unlock()
				continue
			}
			switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(cmd, "EHLO"):
				write("250 fake")
			case cmd == "DATA":
				inData = true
				write("354 go")
			case cmd == "QUIT":
				write("221 bye")
				return
			default:
				write("250 ok")
			}
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String(), func() string { <-done; mu.Lock(); defer mu.Unlock(); return data.String() }
}

// SMTP to a relay on the same host is allowed without any test override.
func TestEmailToLocalRelay(t *testing.T) {
	addr, got := fakeSMTP(t)
	host, port, _ := net.SplitHostPort(addr)
	err := sendEmail(context.Background(), map[string]string{
		"host": host, "port": port, "security": "none",
		"from": "Windlass <alerts@example.com>", "to": "ops@example.com",
	}, Event{Kind: KindDeployFailed, Project: "shop", Title: "Deploy failed\r\nBcc: x@evil.example", Message: "pull failed", Time: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	msg := got()
	if !strings.Contains(msg, "Subject: [Windlass] shop: Deploy failed  Bcc: x@evil.example") || strings.Contains(msg, "\r\nBcc:") {
		t.Fatalf("message:\n%s", msg)
	}
	if !strings.Contains(msg, "pull failed") {
		t.Fatalf("body missing:\n%s", msg)
	}
}

// A kept secret must not follow a changed destination.
func TestSecretDroppedWhenDestinationChanges(t *testing.T) {
	s, _ := testService(t)
	ctx := context.Background()
	ch, err := s.Create(ctx, ChannelInput{Name: "n", Kind: ChannelNtfy,
		Config: map[string]string{"server": "https://ntfy.one.example", "topic": "t", "token": "tk_secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, ch.ID, ChannelInput{Name: "n", Config: map[string]string{"server": "https://ntfy.one.example", "topic": "t2"}}); err != nil {
		t.Fatal(err)
	}
	row, _ := s.q.GetNotificationChannel(ctx, ch.ID)
	if cfg, _ := s.decode(row); cfg["token"] != "tk_secret" {
		t.Fatal("same server: token lost")
	}
	if _, err := s.Update(ctx, ch.ID, ChannelInput{Name: "n", Config: map[string]string{"server": "https://evil.example", "topic": "t2"}}); err != nil {
		t.Fatal(err)
	}
	row, _ = s.q.GetNotificationChannel(ctx, ch.ID)
	if cfg, _ := s.decode(row); cfg["token"] != "" {
		t.Fatal("token followed the new server")
	}
}

func TestSharedRangesRefused(t *testing.T) {
	for _, ip := range []string{"100.100.100.200", "100.64.0.1", "198.18.0.1", "192.0.0.8", "fec0::1"} {
		if !refused(netip.MustParseAddr(ip), false) {
			t.Errorf("%s not refused", ip)
		}
	}
}

func TestOptInAndDisabledWait(t *testing.T) {
	allowLocal(t)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	s, q := testService(t)
	ctx := context.Background()
	ch := webhook(t, s, srv.URL)
	_ = s.Enqueue(ctx, Event{Kind: KindDeploySucceeded, Title: "ok"})
	if rows, _ := q.ListRecentNotifications(ctx, 10); len(rows) != 0 {
		t.Fatal("deploy_succeeded reached a channel that did not pick it")
	}
	_ = s.Enqueue(ctx, Event{Kind: KindDisk, Title: "d"})
	off := false
	if _, err := s.Update(ctx, ch.ID, ChannelInput{Name: "hook", Enabled: &off, Config: map[string]string{"url": srv.URL}}); err != nil {
		t.Fatal(err)
	}
	s.Drain(ctx)
	if hits != 0 || onlyRow(t, q).Status != "pending" {
		t.Fatal("a turned-off channel's row was sent or dropped")
	}
	on := true
	_, _ = s.Update(ctx, ch.ID, ChannelInput{Name: "hook", Enabled: &on, Config: map[string]string{"url": srv.URL}})
	s.Drain(ctx)
	if hits != 1 || onlyRow(t, q).Status != "sent" {
		t.Fatalf("after turning on: hits %d", hits)
	}
}

// A failing channel is tried once per pass, not once per queued row.
func TestFailingChannelSkippedForThePass(t *testing.T) {
	allowLocal(t)
	bad := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { bad++; w.WriteHeader(503) }))
	defer srv.Close()
	s, _ := testService(t)
	webhook(t, s, srv.URL)
	for range 5 {
		_ = s.Enqueue(context.Background(), Event{Kind: KindDisk, Title: "d"})
	}
	s.Drain(context.Background())
	if bad != 1 {
		t.Fatalf("dead channel tried %d times in one pass", bad)
	}
}
