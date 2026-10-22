//go:build unix

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// flock locks belong to the open file, so a second open in the same process
// conflicts exactly as a second windlass process would.
func TestLockDataDirRefusesSecondInstance(t *testing.T) {
	dir := t.TempDir()
	release, err := lockDataDir(dir)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if _, err := lockDataDir(dir); !errors.Is(err, errAlreadyRunning) {
		t.Fatalf("second lock = %v, want errAlreadyRunning", err)
	}

	// Once the first instance exits, the next start succeeds.
	release()
	release2, err := lockDataDir(dir)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	release2()
}

// A lock file created by a root-run instance must not stop the service user
// from starting, so it is world-readable and opened read-only.
func TestLockFileIsReadableByOthers(t *testing.T) {
	dir := t.TempDir()
	release, err := lockDataDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	info, err := os.Stat(filepath.Join(dir, lockFileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o044 != 0o044 {
		t.Fatalf("lock file mode = %v, want group and others to read", info.Mode().Perm())
	}
}
