package store

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/windlass-dev/windlass/migrations"
)

func TestOpenAndMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "windlass.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("database permissions = %o, want 600", got)
		}
	}

	if err := Migrate(db, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Idempotent: applying again is a no-op.
	if err := Migrate(db, migrations.FS); err != nil {
		t.Fatalf("Migrate (second run): %v", err)
	}

	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}

	for _, table := range []string{"users", "sessions", "audit_log", "settings"} {
		var n int
		err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n)
		if err != nil || n != 1 {
			t.Errorf("table %s missing (n=%d, err=%v)", table, n, err)
		}
	}
}
