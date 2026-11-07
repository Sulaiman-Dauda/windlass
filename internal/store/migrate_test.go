package store

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"

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

// TestDeployScopeMigrationKeepsAudit applies 0011 to a database that already
// holds tokens and audit rows. The migration must add can_deploy without
// rebuilding api_tokens, which would null audit_log.token_id.
func TestDeployScopeMigrationKeepsAudit(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "windlass.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	before := fstest.MapFS{}
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name >= "0011" {
			continue
		}
		body, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		before[name] = &fstest.MapFile{Data: body}
	}
	if err := Migrate(db, before); err != nil {
		t.Fatalf("migrate to 0010: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO users (id, email, password_hash, role) VALUES (1, 'a@example.com', 'x', 'admin')`,
		`INSERT INTO api_tokens (id, public_id, user_id, name, secret_hash, scope, expires_at)
		 VALUES (7, 'aaaaaaaaaaaa', 1, 'ci', 'h', 'write', '2099-01-01T00:00:00Z')`,
		`INSERT INTO audit_log (user_id, token_id, action) VALUES (1, 7, 'deployment.create')`,
		`INSERT INTO audit_log (user_id, token_id, action) VALUES (1, 7, 'project.restart')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	if err := Migrate(db, migrations.FS); err != nil {
		t.Fatalf("migrate to latest: %v", err)
	}
	var kept int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE token_id = 7`).Scan(&kept); err != nil || kept != 2 {
		t.Fatalf("audit rows naming the token after 0011 = %d (%v), want 2", kept, err)
	}
	var scope string
	var deploy int
	if err := db.QueryRow(`SELECT scope, can_deploy FROM api_tokens WHERE id = 7`).Scan(&scope, &deploy); err != nil ||
		scope != "write" || deploy != 0 {
		t.Fatalf("existing token after 0011 = %q %d (%v)", scope, deploy, err)
	}
	// The flag is only for read-scoped rows.
	if _, err := db.Exec(`UPDATE api_tokens SET can_deploy = 1 WHERE id = 7`); err == nil {
		t.Fatal("can_deploy accepted on a write token")
	}
	if _, err := db.Exec(`UPDATE api_tokens SET scope = 'read', can_deploy = 1 WHERE id = 7`); err != nil {
		t.Fatalf("deploy token refused: %v", err)
	}
}
