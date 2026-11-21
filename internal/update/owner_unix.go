//go:build unix

package update

import (
	"os"
	"path/filepath"
	"syscall"
)

// InstalledBinary is where install.sh puts the binary.
const InstalledBinary = "/usr/local/bin/windlass"

// ServiceOwnsBinary reports whether the binary this process runs belongs to
// the process's own, non-root user. Then the service user can rewrite the
// file root starts it from, which the installer's swap hook before v0.63
// allowed by moving the service user's staged file into place. It reads the
// file's owner, not the unit text, so a hand-fixed server stops asking.
// Only the installer's path counts: a dev build or a bare process run from
// elsewhere is its owner's business, and the fix is to rerun the installer.
// Root-run and unreadable cases report false: there is nothing to fix.
func ServiceOwnsBinary() bool {
	uid := os.Getuid()
	if uid == 0 || !supported() {
		return false
	}
	self, err := os.Executable()
	if err != nil {
		return false
	}
	if self, err = filepath.EvalSymlinks(self); err != nil || !isInstalled(self) {
		return false
	}
	return ownedBy(self, uid)
}

func ownedBy(path string, uid int) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == uid
}

// isInstalled matches the installer's path, also where /usr/local is itself
// a symlink (Fedora CoreOS and Silverblue link it to /var/usrlocal), which
// os.Executable reports resolved.
func isInstalled(path string) bool {
	if path == InstalledBinary {
		return true
	}
	resolved, err := filepath.EvalSymlinks(InstalledBinary)
	return err == nil && resolved == path
}
