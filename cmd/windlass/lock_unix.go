//go:build unix

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockFileName sits beside windlass.db in the data directory.
const lockFileName = "windlass.lock"

// errAlreadyRunning means another process holds the data directory.
var errAlreadyRunning = errors.New("another windlass instance is already running against this data directory")

// lockDataDir takes an exclusive lock on dataDir for the life of the process,
// so a second server (a stray root shell, a duplicate unit) refuses to start
// instead of sharing the database, the schedulers and the Caddy routes with
// the one already running. The kernel drops the lock when the process exits,
// even on a crash, so there is never a stale lock to clean up.
//
// The file is opened read-only and created 0644: a lock file left behind by a
// root-run instance must still be openable by the windlass service user.
func lockDataDir(dataDir string) (release func(), err error) {
	path := filepath.Join(dataDir, lockFileName)
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	// The umask may have narrowed the create mode. This fails harmlessly
	// when another user owns the file, which then already had to be readable.
	_ = f.Chmod(0o644)
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w (%s)", errAlreadyRunning, path)
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return func() { f.Close() }, nil
}
