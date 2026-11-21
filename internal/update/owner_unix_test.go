//go:build unix

package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOwnedBy(t *testing.T) {
	f := filepath.Join(t.TempDir(), "windlass")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !ownedBy(f, os.Getuid()) {
		t.Error("file the test wrote not reported as its own")
	}
	if ownedBy(f, os.Getuid()+1) {
		t.Error("file reported as another user's")
	}
	if ownedBy(filepath.Join(t.TempDir(), "missing"), os.Getuid()) {
		t.Error("missing file reported as owned")
	}
}

func TestIsInstalled(t *testing.T) {
	if !isInstalled(InstalledBinary) {
		t.Error("installer path not matched")
	}
	if isInstalled(filepath.Join(t.TempDir(), "windlass")) {
		t.Error("a dev build path matched")
	}
}
