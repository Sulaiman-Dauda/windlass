package local

import (
	"context"
	"strings"
	"testing"
)

// Real tar.gz round trip on the actual filesystem — pure Go, runs anywhere.
func TestArchiveRestoreRoundTrip(t *testing.T) {
	f, _ := newFS(t)
	ctx := context.Background()

	if _, err := f.EnsureProject(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"compose.yaml":    "services:\n  web:\n    image: nginx\n",
		".env":            "KEY=value\n",
		"config/app.conf": "setting = 1\n",
	}
	for name, content := range files {
		if err := f.WriteFile(ctx, "app", name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	info, err := f.ArchiveProject(ctx, "app")
	if err != nil {
		t.Fatalf("ArchiveProject: %v", err)
	}
	if info.Size == 0 || !strings.HasSuffix(info.Path, ".tar.gz") {
		t.Errorf("info = %+v", info)
	}

	// Wreck the project, then restore.
	if err := f.WriteFile(ctx, "app", "compose.yaml", []byte("broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteFile(ctx, "app", "junk.txt", []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := f.RestoreProject(ctx, "app", info.Path); err != nil {
		t.Fatalf("RestoreProject: %v", err)
	}

	for name, want := range files {
		got, err := f.ReadFile(ctx, "app", name)
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v (want %q)", name, got, err, want)
		}
	}
	// Files created after the backup are gone (full restore semantics).
	if _, err := f.ReadFile(ctx, "app", "junk.txt"); err == nil {
		t.Error("junk.txt survived restore")
	}
}

func TestRestoreRejectsOutsideArchive(t *testing.T) {
	f, root := newFS(t)
	ctx := context.Background()
	if _, err := f.EnsureProject(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	if err := f.RestoreProject(ctx, "app", root+"/evil.tar.gz"); err == nil {
		t.Error("archive outside backups dir accepted")
	}
}

func TestRemoveArchiveScoped(t *testing.T) {
	f, _ := newFS(t)
	ctx := context.Background()
	if _, err := f.EnsureProject(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteFile(ctx, "app", "compose.yaml", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := f.ArchiveProject(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.RemoveArchive(ctx, info.Path); err != nil {
		t.Errorf("RemoveArchive: %v", err)
	}
	if err := f.RemoveArchive(ctx, "/etc/passwd"); err == nil {
		t.Error("RemoveArchive accepted a path outside the backups dir")
	}
}
