package plugins

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/migrations"
)

func newService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	sqlDB, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := store.Migrate(sqlDB, migrations.FS); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(db.New(sqlDB), dir, logger), dir
}

func writePlugin(t *testing.T, root, name, manifest string) {
	t.Helper()
	dir := filepath.Join(root, "plugins", name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryAndValidation(t *testing.T) {
	s, root := newService(t)
	ctx := context.Background()

	// No plugins dir → empty list, no error.
	list, err := s.List(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("empty = %v, %v", list, err)
	}

	writePlugin(t, root, "good", `{"name":"good","version":"1.0","command":"good"}`)
	writePlugin(t, root, "mismatch", `{"name":"other","version":"1.0","command":"x"}`)
	writePlugin(t, root, "escape", `{"name":"escape","version":"1.0","command":"../../evil"}`)
	writePlugin(t, root, "absolute", `{"name":"absolute","version":"1.0","command":"/bin/sh"}`)

	list, err = s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "good" {
		t.Errorf("list = %+v, want only 'good'", list)
	}
	if list[0].Enabled || list[0].Running {
		t.Errorf("fresh plugin should be disabled: %+v", list[0])
	}
}

func TestEnableMissingPlugin(t *testing.T) {
	s, _ := newService(t)
	if err := s.Enable(context.Background(), "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestProxyUnavailableWhenNotRunning(t *testing.T) {
	s, root := newService(t)
	writePlugin(t, root, "good", `{"name":"good","version":"1.0","command":"good"}`)

	rec := httptest.NewRecorder()
	s.Proxy("good", rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 503 {
		t.Errorf("proxy to stopped plugin = %d, want 503", rec.Code)
	}
}

func TestDisableIsIdempotent(t *testing.T) {
	s, root := newService(t)
	writePlugin(t, root, "good", `{"name":"good","version":"1.0","command":"good"}`)
	ctx := context.Background()

	if err := s.Disable(ctx, "good"); err != nil {
		t.Errorf("disable never-enabled: %v", err)
	}
	list, _ := s.List(ctx)
	if len(list) != 1 || list[0].Enabled {
		t.Errorf("list = %+v", list)
	}
}
