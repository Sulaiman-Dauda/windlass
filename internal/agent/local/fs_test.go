package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func newFS(t *testing.T) (fsLocal, string) {
	t.Helper()
	dir := t.TempDir()
	l, err := New(Config{ProjectsDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return fsLocal{l}, dir
}

func TestFSWriteReadList(t *testing.T) {
	f, root := newFS(t)
	ctx := context.Background()

	if _, err := f.EnsureProject(ctx, "crm"); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}
	if err := f.WriteFile(ctx, "crm", "compose.yaml", []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Content is on the real filesystem where a user could edit it by hand.
	onDisk, err := os.ReadFile(filepath.Join(root, "crm", "compose.yaml"))
	if err != nil || string(onDisk) != "services: {}\n" {
		t.Fatalf("on-disk = %q, %v", onDisk, err)
	}

	got, err := f.ReadFile(ctx, "crm", "compose.yaml")
	if err != nil || string(got) != "services: {}\n" {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}

	list, err := f.List(ctx, "crm", ".")
	if err != nil || len(list) != 1 || list[0].Name != "compose.yaml" {
		t.Fatalf("List = %+v, %v", list, err)
	}
}

func TestFSPathTraversalBlocked(t *testing.T) {
	f, root := newFS(t)
	ctx := context.Background()
	if _, err := f.EnsureProject(ctx, "crm"); err != nil {
		t.Fatal(err)
	}

	// Plant a sentinel outside the project to prove it is unreachable.
	sentinel := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(sentinel, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	bad := []string{
		"../outside.txt",
		"..\\outside.txt",
		"a/../../outside.txt",
		"/etc/passwd",
		`C:\Windows\win.ini`,
		"..",
	}
	for _, rel := range bad {
		if _, err := f.ReadFile(ctx, "crm", rel); err == nil {
			t.Errorf("ReadFile(%q) succeeded, traversal not blocked", rel)
		}
		if err := f.WriteFile(ctx, "crm", rel, []byte("x"), 0o644); err == nil {
			t.Errorf("WriteFile(%q) succeeded, traversal not blocked", rel)
		}
	}

	// Bad project names are rejected outright.
	for _, proj := range []string{"../crm", "crm/../x", "CRM", "", "a b", ".hidden"} {
		if _, err := f.EnsureProject(ctx, proj); err == nil {
			t.Errorf("EnsureProject(%q) succeeded, want error", proj)
		}
	}
}

func TestFSSymlinkTraversalBlocked(t *testing.T) {
	f, root := newFS(t)
	ctx := context.Background()
	if _, err := f.EnsureProject(ctx, "crm"); err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	sentinel := filepath.Join(outside, "secret.json")
	if err := os.WriteFile(sentinel, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "crm")
	if err := os.Symlink(sentinel, filepath.Join(project, "leak.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(project, "escape")); err != nil {
		t.Fatal(err)
	}

	if _, err := f.ReadFile(ctx, "crm", "leak.json"); err == nil {
		t.Error("ReadFile followed a symlink outside the project")
	}
	if _, err := f.List(ctx, "crm", "escape"); err == nil {
		t.Error("List followed a symlink outside the project")
	}
	if err := f.WriteFile(ctx, "crm", "escape/config.json", []byte("escaped"), 0o600); err == nil {
		t.Error("WriteFile followed a parent symlink outside the project")
	}
	if _, err := os.Stat(filepath.Join(outside, "config.json")); !os.IsNotExist(err) {
		t.Errorf("outside file was created: %v", err)
	}

	if err := os.Symlink(outside, filepath.Join(root, "linked-project")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.EnsureProject(ctx, "linked-project"); err == nil {
		t.Error("EnsureProject accepted a symlinked project directory")
	}
}

func TestFSAtomicWriteReplaces(t *testing.T) {
	f, _ := newFS(t)
	ctx := context.Background()
	if _, err := f.EnsureProject(ctx, "crm"); err != nil {
		t.Fatal(err)
	}

	if err := f.WriteFile(ctx, "crm", ".env", []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteFile(ctx, "crm", ".env", []byte("A=2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := f.ReadFile(ctx, "crm", ".env")
	if err != nil || string(got) != "A=2\n" {
		t.Fatalf("after replace: %q, %v", got, err)
	}

	// No temp files left behind.
	list, _ := f.List(ctx, "crm", ".")
	for _, fi := range list {
		if fi.Name != ".env" {
			t.Errorf("leftover file %q", fi.Name)
		}
	}
}

func TestRemoveProject(t *testing.T) {
	f, root := newFS(t)
	ctx := context.Background()
	if _, err := f.EnsureProject(ctx, "gone"); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteFile(ctx, "gone", "compose.yaml", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.RemoveProject(ctx, "gone"); err != nil {
		t.Fatalf("RemoveProject: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "gone")); !os.IsNotExist(err) {
		t.Error("project directory still exists")
	}
}
