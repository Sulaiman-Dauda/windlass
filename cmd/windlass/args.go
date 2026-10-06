package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/windlass-dev/windlass/internal/version"
)

const usage = `Usage: windlass [--version | --help]

With no arguments, windlass starts the server. It is configured entirely through
WINDLASS_* environment variables; see docs/configuration.md.

  --version, version   print the version and exit
  --help, help         print this message and exit
`

// handleArgs answers the command-line arguments before anything else runs.
// Windlass takes no arguments to start the server, so anything it is given is
// either a query it can answer and exit, or a mistake. Neither may fall
// through to run(): that would open the SQLite database, reconcile projects
// and start the schedulers alongside the instance that is already running.
//
// It reports whether the process should exit, and with which status.
func handleArgs(args []string, stdout, stderr io.Writer) (code int, exit bool) {
	if len(args) == 0 {
		return 0, false
	}
	if len(args) == 1 {
		switch args[0] {
		case "--version", "-version", "-v", "version":
			fmt.Fprintf(stdout, "windlass %s (commit %s)\n", version.Version, version.Commit)
			return 0, true
		case "--help", "-help", "-h", "help":
			fmt.Fprint(stdout, usage)
			return 0, true
		}
	}
	what := fmt.Sprintf("argument %q", args[0])
	if len(args) > 1 {
		what = fmt.Sprintf("arguments %q", strings.Join(args, " "))
	}
	fmt.Fprintf(stderr, "windlass: unexpected %s; the server takes no arguments\n\n", what)
	fmt.Fprint(stderr, usage)
	return 2, true
}
