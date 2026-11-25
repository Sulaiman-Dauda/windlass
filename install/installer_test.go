package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// busybox stubs: adduser -S only attaches to an existing group (-G) and never
// creates one; addgroup refuses a group that already exists; chown fails on an
// unknown group. These mirror the Alpine behaviour behind issue #173.
// addgroup fails with EACCES when WL_ADDGROUP_FAIL is set, to test that real
// failures are not swallowed.
var busyboxStubs = map[string]string{
	"id": `[ -e "$WL_STATE/users/$1" ]`,
	// getent group NAME exits 0 only when the group exists.
	"getent": `[ "$1" = group ] && [ -e "$WL_STATE/groups/$2" ]`,
	// The installer's gpasswd call must never touch the real system groups.
	"gpasswd": `exit 0`,
	"addgroup": `for name do :; done
if [ -n "${WL_ADDGROUP_FAIL:-}" ]; then echo "addgroup: permission denied" >&2; exit 1; fi
if [ -e "$WL_STATE/groups/$name" ]; then echo "addgroup: group '$name' in use" >&2; exit 1; fi
: > "$WL_STATE/groups/$name"`,
	"adduser": `group=nogroup
while [ $# -gt 1 ]; do
  case "$1" in
    -G) group=$2; shift ;;
    -h|-s) shift ;;
    -S) ;;
    *) echo "adduser: unsupported argument $1" >&2; exit 1 ;;
  esac
  shift
done
name=$1
if [ "$group" != nogroup ] && [ ! -e "$WL_STATE/groups/$group" ]; then echo "adduser: unknown group $group" >&2; exit 1; fi
echo "$group" > "$WL_STATE/users/$name"`,
	"chown": `for arg do
  case "$arg" in
    *:*) g=${arg#*:}
      [ -e "$WL_STATE/groups/$g" ] || { echo "chown: unknown user/group $arg" >&2; exit 1; } ;;
  esac
done`,
}

// TestUserSetupOnBusybox runs the installer's user and directory block with
// busybox-style tools and no useradd, as on Alpine, and checks it can be rerun.
func TestUserSetupOnBusybox(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("installer shell test runs on Linux")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	src, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	var block []string
	in := false
	for _, line := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(line, "if ! id windlass") {
			in = true
		}
		if in {
			block = append(block, line)
		}
		if in && strings.HasPrefix(line, `chmod 750 "$DATA_DIR/projects"`) {
			break
		}
	}
	if len(block) == 0 {
		t.Fatal("user setup block not found in install.sh")
	}

	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	state := filepath.Join(root, "state")
	for _, dir := range []string{bin, filepath.Join(state, "users"), filepath.Join(state, "groups")} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range busyboxStubs {
		script := "#!/bin/sh\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil { // #nosec G306 -- stubs must be executable to be found on PATH; they live in t.TempDir
			t.Fatal(err)
		}
	}

	script := "set -eu\nDATA_DIR=" + filepath.Join(root, "data") + "\n" + strings.Join(block, "\n") + "\n"
	runWith := func(extra ...string) ([]byte, error) {
		cmd := exec.Command("sh", "-c", script) // #nosec G204 -- fixed interpreter; script is the installer block under test
		// /usr/sbin is left out so the real useradd is absent, as on Alpine.
		cmd.Env = append([]string{"PATH=" + bin + ":/usr/bin:/bin", "WL_STATE=" + state}, extra...)
		return cmd.CombinedOutput()
	}
	run := func() {
		if out, err := runWith(); err != nil {
			t.Fatalf("user setup failed: %v\n%s", err, out)
		}
	}

	run()
	if _, err := os.Stat(filepath.Join(state, "groups", "windlass")); err != nil {
		t.Fatalf("windlass group was not created: %v", err)
	}
	group, err := os.ReadFile(filepath.Join(state, "users", "windlass")) // #nosec G304 -- path is inside the test's t.TempDir
	if err != nil {
		t.Fatalf("windlass user was not created: %v", err)
	}
	if strings.TrimSpace(string(group)) != "windlass" {
		t.Fatalf("windlass user primary group = %q, want windlass", strings.TrimSpace(string(group)))
	}

	// A second run must succeed too: the user and group already exist.
	run()

	// A rerun after the user was lost (group still there) must reach addgroup,
	// find the group in use, and carry on without it.
	if err := os.Remove(filepath.Join(state, "users", "windlass")); err != nil {
		t.Fatal(err)
	}
	run()
	if _, err := os.Stat(filepath.Join(state, "users", "windlass")); err != nil {
		t.Fatalf("windlass user was not recreated on rerun: %v", err)
	}

	// A real addgroup failure on a fresh system must stop the installer, not
	// be reported later as adduser's unknown-group error.
	if err := os.RemoveAll(filepath.Join(state, "groups")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(state, "groups"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(state, "users", "windlass")); err != nil {
		t.Fatal(err)
	}
	out, err := runWith("WL_ADDGROUP_FAIL=1")
	if err == nil {
		t.Fatalf("setup succeeded despite addgroup failure:\n%s", out)
	}
	if !strings.Contains(string(out), "permission denied") {
		t.Fatalf("addgroup error not reported:\n%s", out)
	}
}
