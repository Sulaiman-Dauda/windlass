package store

import (
	"path/filepath"
	"testing"

	"github.com/windlass-dev/windlass/migrations"
)

func TestOpenAndMigrate(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "windlass.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

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
