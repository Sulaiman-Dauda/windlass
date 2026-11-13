package local

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Real tar.gz round trip on the actual filesystem, pure Go, runs anywhere.
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

	spool, n, err := f.SpoolFile(ctx, strings.NewReader("database dump"))
	if err != nil || n != int64(len("database dump")) {
		t.Fatalf("SpoolFile = %d, %v", n, err)
	}
	info, err := f.ArchiveProject(ctx, "app", map[string]string{
		".windlass/backup/db_dump.sql": spool,
	})
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
	if got, err := f.ReadFile(ctx, "app", ".windlass/backup/db_dump.sql"); err != nil || string(got) != "database dump" {
		t.Errorf("injected dump = %q, %v", got, err)
	}
	// Files created after the backup are gone (full restore semantics).
	if _, err := f.ReadFile(ctx, "app", "junk.txt"); err == nil {
		t.Error("junk.txt survived restore")
	}
}

// A restore leaves the archived dump in .windlass/backup for a manual
// import. The next backup must not carry it again as though it were new.
func TestArchiveSkipsTheDumpARestoreLeft(t *testing.T) {
	f, _ := newFS(t)
	ctx := context.Background()
	if _, err := f.EnsureProject(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteFile(ctx, "app", "compose.yaml", []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteFile(ctx, "app", ".windlass/backup/db_dump.sql", []byte("old dump"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := f.ArchiveProject(ctx, "app", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.RestoreProject(ctx, "app", info.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ReadFile(ctx, "app", "compose.yaml"); err != nil {
		t.Fatalf("restore lost compose.yaml: %v", err)
	}
	if got, err := f.ReadFile(ctx, "app", ".windlass/backup/db_dump.sql"); err == nil {
		t.Fatalf("archive without a dump restored the old one: %q", got)
	}
}

// A crash can leave a spool or half-written archive in the backups dir.
// Old ones are removed; recent ones may belong to a backup still running.
func TestRemoveLeftoversSparesRecentFiles(t *testing.T) {
	f, _ := newFS(t)
	ctx := context.Background()
	dir, err := f.BackupsDir(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-leftoverAge - time.Hour)
	for name, mtime := range map[string]time.Time{
		".windlass-spool-old":   old,
		".windlass-archive-old": old,
		".windlass-spool-new":   time.Now(),
		"app-20260101.tar.gz":   old,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	f.l.removeLeftovers()
	var left []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if strings.Join(left, ",") != ".windlass-spool-new,app-20260101.tar.gz" {
		t.Fatalf("left = %v", left)
	}
}

// An extra file must be a regular file inside the backups dir, so a caller
// cannot copy another file on the node into a backup.
func TestArchiveExtraMustBeASpooledFile(t *testing.T) {
	f, root := newFS(t)
	ctx := context.Background()
	if _, err := f.EnsureProject(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(outside, []byte("not for backups"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir, err := f.BackupsDir(ctx)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ".windlass-spool-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]string{
		"outside": outside,
		"escape":  filepath.Join(dir, "..", "secret.txt"),
		"symlink": link,
		"dir":     dir + "/.",
	} {
		if _, err := f.ArchiveProject(ctx, "app", map[string]string{"dump.sql": src}); err == nil {
			t.Errorf("%s: extra %s was archived", name, src)
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tar.gz") || strings.HasPrefix(e.Name(), ".windlass-") && e.Name() != ".windlass-spool-link" {
			t.Errorf("refused archive left %s behind", e.Name())
		}
	}
}

func TestSpoolFileRemovesPartialFileOnError(t *testing.T) {
	f, _ := newFS(t)
	ctx := context.Background()
	if _, _, err := f.SpoolFile(ctx, io.MultiReader(strings.NewReader("part"), errReader{})); err == nil {
		t.Fatal("spool of a failing reader succeeded")
	}
	dir, _ := f.BackupsDir(ctx)
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("failed spool left %d files", len(entries))
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("dump stream broke") }

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
	info, err := f.ArchiveProject(ctx, "app", nil)
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

func TestArchiveNamesDoNotCollide(t *testing.T) {
	f, _ := newFS(t)
	ctx := context.Background()
	if _, err := f.EnsureProject(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteFile(ctx, "app", "compose.yaml", []byte("services: {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := f.ArchiveProject(ctx, "app", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.ArchiveProject(ctx, "app", nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Path == second.Path {
		t.Fatalf("consecutive backups collided at %s", first.Path)
	}
}
