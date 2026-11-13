package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/attention"
	"github.com/windlass-dev/windlass/internal/update"
)

func TestAttentionAPI(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.agent.Metrics = agent.HostMetrics{DiskUsed: 90, DiskTotal: 100}

	if rec := e.do(t, http.MethodGet, "/api/v1/system/attention", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon = %d", rec.Code)
	}
	rec := e.do(t, http.MethodGet, "/api/v1/system/attention", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("attention = %d: %s", rec.Code, rec.Body.String())
	}
	var list struct {
		CheckedAt time.Time `json:"checked_at"`
		Items     []struct {
			Kind, Tone, Title string
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.CheckedAt.IsZero() || len(list.Items) != 1 || list.Items[0].Kind != "disk" {
		t.Fatalf("list = %s", rec.Body.String())
	}

	// A viewer reads it too.
	hashViewer(t, e, context.Background())
	login := e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "viewer@example.com", "password": "viewerpassword"})
	if login.Code != http.StatusNoContent {
		t.Fatalf("viewer login = %d", login.Code)
	}
	if rec := e.do(t, http.MethodGet, "/api/v1/system/attention", nil, sessionCookie(t, login)); rec.Code != http.StatusOK {
		t.Fatalf("viewer = %d", rec.Code)
	}
}

func TestSystemDiskAPI(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.agent.Metrics = agent.HostMetrics{DiskUsed: 40e9, DiskTotal: 100e9}
	e.agent.Disk = agent.DiskUsage{
		Images:     agent.DiskUsageKind{Count: 12, ActiveCount: 5, Bytes: 8e9, ReclaimableBytes: 3e9},
		BuildCache: agent.DiskUsageKind{Count: 40, Bytes: 13e9, ReclaimableBytes: 13e9},
		Volumes:    agent.DiskUsageKind{Count: 3, ActiveCount: 3, Bytes: 2e9},
	}
	rec := e.do(t, http.MethodGet, "/api/v1/system/disk", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("disk = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"used_bytes":40000000000`, `"build_cache":{"count":40`,
		`"images":{"count":12,"active_count":5,"bytes":8000000000,"reclaimable_bytes":3000000000}`, `"checked_at"`} {
		if !strings.Contains(body, want) {
			t.Errorf("disk missing %s in %s", want, body)
		}
	}
	// Cached: a second read does not ask Docker again.
	calls := strings.Count(strings.Join(e.agent.Calls, " "), "docker.df")
	e.do(t, http.MethodGet, "/api/v1/system/disk", nil, cookie)
	if n := strings.Count(strings.Join(e.agent.Calls, " "), "docker.df"); n != calls {
		t.Errorf("docker.df called %d times, want %d", n, calls)
	}
}

func TestSystemDiskWithDockerDown(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.agent.Metrics = agent.HostMetrics{DiskUsed: 1, DiskTotal: 2}
	e.agent.Fail["docker.df"] = context.DeadlineExceeded
	rec := e.do(t, http.MethodGet, "/api/v1/system/disk", nil, cookie)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"docker"`) ||
		!strings.Contains(rec.Body.String(), `"total_bytes":2`) {
		t.Fatalf("disk = %d %s", rec.Code, rec.Body.String())
	}
}

func TestMetricsReportPanelMemory(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	rec := e.do(t, http.MethodGet, "/api/v1/system/metrics", nil, cookie)
	var m struct {
		Panel struct {
			RSSBytes uint64            `json:"rss_bytes"`
			History  []json.RawMessage `json:"history"`
		} `json:"panel"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Panel.History == nil {
		t.Error("history must be an array, not null")
	}
	if !strings.Contains(rec.Body.String(), `"rss_bytes"`) {
		t.Errorf("metrics = %s", rec.Body.String())
	}
}

func TestDriftAPI(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	if rec := e.do(t, http.MethodGet, "/api/v1/projects/nope/drift", nil, cookie); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown project = %d", rec.Code)
	}
	e.prepareDeployableProject(t, cookie, "app")
	e.agent.Hashes["app"] = map[string]string{"web": "new"}
	e.agent.Containers = []agent.Container{{ID: "c1", Name: "app-web-1", ComposeProject: "app",
		ComposeService: "web", ConfigHash: "old"}}
	rec := e.do(t, http.MethodGet, "/api/v1/projects/app/drift", nil, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"drifted":true`) ||
		!strings.Contains(rec.Body.String(), `"config_changed":true`) {
		t.Fatalf("drift = %d %s", rec.Code, rec.Body.String())
	}
}

// logEvents reads the SSE "log" events of a logs request.
func logEvents(t *testing.T, e *testEnv, cookie *http.Cookie, query string) []map[string]string {
	t.Helper()
	srv := httptest.NewServer(e.handler)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/projects/app/logs?"+query, nil)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out []map[string]string
	sc := bufio.NewScanner(resp.Body)
	event := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: ") && event == "log":
			var m map[string]string
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m); err != nil {
				t.Fatal(err)
			}
			out = append(out, m)
		}
	}
	return out
}

func TestLogsMergeServicesByTime(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")
	e.agent.Containers = []agent.Container{
		{ID: "c1", Name: "app-web-1", ComposeProject: "app", ComposeService: "web"},
		{ID: "c2", Name: "app-db-1", ComposeProject: "app", ComposeService: "db"},
	}
	at := func(s int) time.Time { return time.Date(2026, 11, 13, 10, 0, s, 0, time.UTC) }
	e.agent.LogLines["c1"] = []agent.LogLine{{Stream: "stdout", Text: "web 1", Time: at(1)},
		{Stream: "stdout", Text: "web 3", Time: at(3)}, {Stream: "stdout", Text: "web 5", Time: at(5)}}
	e.agent.LogLines["c2"] = []agent.LogLine{{Stream: "stderr", Text: "db 2", Time: at(2)},
		{Stream: "stdout", Text: "db 4", Time: at(4)}}

	got := logEvents(t, e, cookie, "follow=false")
	var texts []string
	for _, ev := range got {
		texts = append(texts, ev["text"])
	}
	if strings.Join(texts, ",") != "web 1,db 2,web 3,db 4,web 5" {
		t.Fatalf("merged = %v", texts)
	}
	if got[1]["service"] != "db" || got[1]["container"] != "app-db-1" || got[1]["stream"] != "stderr" ||
		got[1]["time"] != "2026-11-13T10:00:02Z" {
		t.Errorf("event = %v", got[1])
	}

	// tail is the history in all, the newest lines kept.
	texts = nil
	for _, ev := range logEvents(t, e, cookie, "follow=false&tail=2") {
		texts = append(texts, ev["text"])
	}
	if strings.Join(texts, ",") != "db 4,web 5" {
		t.Fatalf("tail 2 = %v", texts)
	}

	// A named service still streams only its own container.
	texts = nil
	for _, ev := range logEvents(t, e, cookie, "service=db&follow=false") {
		texts = append(texts, ev["text"])
	}
	if strings.Join(texts, ",") != "db 2,db 4" {
		t.Fatalf("service=db = %v", texts)
	}
}

func TestLogsMergedFollowStartsAfterTheTail(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")
	e.agent.Containers = []agent.Container{
		{ID: "c1", Name: "app-web-1", ComposeProject: "app", ComposeService: "web"},
		{ID: "c2", Name: "app-db-1", ComposeProject: "app", ComposeService: "db"},
	}
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	e.agent.LogLines["c1"] = []agent.LogLine{{Text: "web", Time: at}}
	e.agent.LogLines["c2"] = []agent.LogLine{}

	// The fake's follow returns once its lines are sent, so the stream ends.
	got := logEvents(t, e, cookie, "")
	if len(got) != 1 || got[0]["text"] != "web" {
		t.Fatalf("a line sent in the tail must not be sent again on follow: %v", got)
	}
	opts := e.agent.LogOpts["c1"]
	if len(opts) != 2 || opts[0].Follow || !opts[1].Follow || !opts[1].Since.Equal(at) {
		t.Errorf("c1 opts = %+v", opts)
	}
	if c2 := e.agent.LogOpts["c2"]; len(c2) != 2 || c2[1].Since.IsZero() {
		t.Errorf("an empty container must follow from when the request began: %+v", c2)
	}
}

// Two lines can share a timestamp. With tail=1 only the second was sent,
// and the follow sees both again: neither is sent twice.
func TestLogsMergedFollowAtASharedTimestamp(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")
	e.agent.Containers = []agent.Container{
		{ID: "c1", Name: "app-web-1", ComposeProject: "app", ComposeService: "web"},
		{ID: "c2", Name: "app-db-1", ComposeProject: "app", ComposeService: "db"},
	}
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	e.agent.LogLines["c1"] = []agent.LogLine{{Text: "old", Time: at.Add(-time.Second)},
		{Text: "one", Time: at}, {Text: "two", Time: at}}
	e.agent.LogLines["c2"] = []agent.LogLine{}

	var texts []string
	for _, ev := range logEvents(t, e, cookie, "tail=1") {
		texts = append(texts, ev["text"])
	}
	if strings.Join(texts, ",") != "two" {
		t.Fatalf("got %v, want each line once", texts)
	}
}

type noPanel struct{}

func (noPanel) PanelDomain(context.Context) (string, error) { return "", nil }

type readyUpdate struct{}

func (readyUpdate) Check(context.Context) (update.Release, error) {
	return update.Release{Version: "v9.9", CurrentVer: "v0.62", UpdateReady: true}, nil
}

// The update row is for admins, as /system/update is: a viewer cannot apply
// one, and the rest of the list is the same for both.
func TestAttentionUpdateRowIsAdminOnly(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(t)
	svc := attention.New(e.queries, e.agent, noPanel{}, readyUpdate{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.api.Attention = svc
	e.agent.Metrics = agent.HostMetrics{DiskUsed: 99, DiskTotal: 100}

	kinds := func(c *http.Cookie) string {
		svc.Invalidate()
		rec := e.do(t, http.MethodGet, "/api/v1/system/attention", nil, c)
		var l struct{ Items []struct{ Kind string } }
		if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		var k []string
		for _, i := range l.Items {
			k = append(k, i.Kind)
		}
		return strings.Join(k, " ")
	}
	// The first list starts the update check in the background.
	deadline := time.Now().Add(5 * time.Second)
	for kinds(admin) != "disk update" {
		if time.Now().After(deadline) {
			t.Fatalf("admin = %q", kinds(admin))
		}
		time.Sleep(10 * time.Millisecond)
	}

	hashViewer(t, e, context.Background())
	login := e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "viewer@example.com", "password": "viewerpassword"})
	if got := kinds(sessionCookie(t, login)); got != "disk" {
		t.Fatalf("viewer = %q", got)
	}
}

func TestLogsMergedTailIsCapped(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")
	e.agent.Containers = []agent.Container{
		{ID: "c1", Name: "app-web-1", ComposeProject: "app", ComposeService: "web"},
		{ID: "c2", Name: "app-db-1", ComposeProject: "app", ComposeService: "db"},
	}
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	for i := range 6000 {
		e.agent.LogLines["c1"] = append(e.agent.LogLines["c1"], agent.LogLine{Text: "w", Time: at.Add(time.Duration(2*i) * time.Millisecond)})
		e.agent.LogLines["c2"] = append(e.agent.LogLines["c2"], agent.LogLine{Text: "d", Time: at.Add(time.Duration(2*i+1) * time.Millisecond)})
	}
	got := logEvents(t, e, cookie, "follow=false&tail=1000000")
	if len(got) != 5000 || got[len(got)-1]["text"] != "d" {
		t.Fatalf("got %d lines", len(got))
	}
	if opts := e.agent.LogOpts["c1"]; len(opts) != 1 || opts[0].Tail != 5000 {
		t.Errorf("c1 asked for %+v", opts)
	}
}
