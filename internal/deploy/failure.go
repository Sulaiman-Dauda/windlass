package deploy

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/windlass-dev/windlass/internal/store/db"
)

// errorWords picks out a log line that reports a failure. Compose, BuildKit
// and the Docker daemon each phrase it differently ("Error response from
// daemon", "failed to solve", "pull access denied", "no such image").
var errorWords = regexp.MustCompile(`(?i)\b(error|errors|failed|failure|fatal|denied|refused|not found|no such|cannot|can't|unable|invalid|unauthorized|forbidden|timed out|exceeded|panic)\b|\berr!`)

// warningLine is a line that names a problem without being the failure.
var warningLine = regexp.MustCompile(`(?i)^\W*(warn|warning)\b`)

var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

// maxErrorLine caps the stored line; a BuildKit line can quote a whole
// command.
const maxErrorLine = 500

// errorLines remembers the last line of a step's output that reads as an
// error. The last, not the first: Compose and BuildKit end a failure with
// their summary ("failed to solve: ...", "Error response from daemon: ..."),
// while earlier matches are often a build tool's own noise. Compose writes
// stdout and stderr from two goroutines, hence the lock.
type errorLines struct {
	mu   sync.Mutex
	last string
}

func (e *errorLines) observe(text string) {
	line := strings.TrimSpace(ansiEscape.ReplaceAllString(text, ""))
	if line == "" || warningLine.MatchString(line) || !errorWords.MatchString(line) {
		return
	}
	e.mu.Lock()
	e.last = line
	e.mu.Unlock()
}

func (e *errorLines) line() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.last
}

// exitCoder is met by *exec.ExitError, which Compose and Git commands wrap.
type exitCoder interface{ ExitCode() int }

// failureRecord is what a failed deploy stores beside its error: the step
// it stopped in, the failing command's exit code, and the line to show
// first.
func failureRecord(step string, err error, lines *errorLines) db.RecordDeploymentFailureParams {
	rec := db.RecordDeploymentFailureParams{FailedStep: sql.NullString{String: step, Valid: true}}
	var ec exitCoder
	if errors.As(err, &ec) && ec.ExitCode() > 0 {
		rec.ExitCode = sql.NullInt64{Int64: int64(ec.ExitCode()), Valid: true}
	}
	line := ""
	if lines != nil {
		line = lines.line()
	}
	if line == "" {
		// No output read as an error, so the error itself is the line.
		line, _, _ = strings.Cut(strings.TrimSpace(err.Error()), "\n")
	}
	if line != "" {
		rec.ErrorLine = sql.NullString{String: clip(line, maxErrorLine), Valid: true}
	}
	return rec
}

func (s *Service) recordFailure(ctx context.Context, d db.Deployment, step string, err error, lines *errorLines) {
	rec := failureRecord(step, err, lines)
	rec.ID = d.ID
	if qerr := s.q.RecordDeploymentFailure(context.WithoutCancel(ctx), rec); qerr != nil {
		s.logger.Error("record deployment failure", "deployment", d.ID, "error", qerr)
	}
}

// clip shortens s to at most n bytes without splitting a character.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}
