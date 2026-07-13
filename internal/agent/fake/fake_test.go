package fake

import (
	"context"
	"errors"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
)

func TestFSReadWrite(t *testing.T) {
	f := New()
	ctx := context.Background()

	if err := f.FS().WriteFile(ctx, "crm", "compose.yaml", []byte("services: {}"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	data, err := f.FS().ReadFile(ctx, "crm", "compose.yaml")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "services: {}" {
		t.Errorf("content = %q", data)
	}
}

func TestFSRejectsTraversal(t *testing.T) {
	f := New()
	ctx := context.Background()

	for _, bad := range []string{"../etc/passwd", "..\\secrets", "/abs/path", "a/../../b"} {
		if err := f.FS().WriteFile(ctx, "crm", bad, []byte("x"), 0o644); err == nil {
			t.Errorf("WriteFile(%q) succeeded, want error", bad)
		}
	}
}

func TestFailInjection(t *testing.T) {
	f := New()
	boom := errors.New("boom")
	f.Fail["compose.up"] = boom

	err := f.Compose().Up(context.Background(), agent.ComposeUpReq{Project: "crm"}, nil)
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
	// Other ops are unaffected.
	if err := f.Compose().Pull(context.Background(), "crm", nil); err != nil {
		t.Errorf("Pull: %v", err)
	}
}

func TestCallRecording(t *testing.T) {
	f := New()
	ctx := context.Background()

	_ = f.Compose().Up(ctx, agent.ComposeUpReq{Project: "crm"}, nil)
	_, _ = f.Compose().PS(ctx, "crm")

	want := []string{"compose.up(crm)", "compose.ps(crm)"}
	if len(f.Calls) != len(want) {
		t.Fatalf("calls = %v, want %v", f.Calls, want)
	}
	for i := range want {
		if f.Calls[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, f.Calls[i], want[i])
		}
	}
}

func TestProxyRoutes(t *testing.T) {
	f := New()
	ctx := context.Background()

	routes := []agent.Route{{ID: "windlass_crm_0", Hostname: "crm.example.com", Upstream: "crm-web-1:3000", TLS: true}}
	if err := f.Proxy().ApplyRoutes(ctx, routes); err != nil {
		t.Fatalf("ApplyRoutes: %v", err)
	}
	got, err := f.Proxy().CurrentRoutes(ctx)
	if err != nil || len(got) != 1 || got[0].Hostname != "crm.example.com" {
		t.Errorf("CurrentRoutes = %v, err = %v", got, err)
	}
}

func TestExecEcho(t *testing.T) {
	f := New()
	sess, err := f.Exec().Start(context.Background(), agent.ExecReq{ContainerID: "abc", Cmd: []string{"/bin/sh"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := sess.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := <-sess.Output()
	if string(got) != "hello" {
		t.Errorf("echo = %q", got)
	}
	sess.Close()
	if code, err := sess.Wait(); code != 0 || err != nil {
		t.Errorf("Wait = %d, %v", code, err)
	}
}
