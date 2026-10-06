package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/version"
)

func TestHandleArgs(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantExit   bool
		wantStdout string
		wantStderr string
	}{
		{name: "no arguments starts the server", args: nil},
		{name: "--version", args: []string{"--version"}, wantExit: true, wantStdout: "windlass " + version.Version},
		{name: "-v", args: []string{"-v"}, wantExit: true, wantStdout: "windlass " + version.Version},
		{name: "version", args: []string{"version"}, wantExit: true, wantStdout: "commit " + version.Commit},
		{name: "--help", args: []string{"--help"}, wantExit: true, wantStdout: "Usage: windlass"},
		{name: "-h", args: []string{"-h"}, wantExit: true, wantStdout: "Usage: windlass"},
		{name: "help", args: []string{"help"}, wantExit: true, wantStdout: "Usage: windlass"},
		{name: "unknown flag", args: []string{"--serve"}, wantCode: 2, wantExit: true, wantStderr: `unexpected argument "--serve"`},
		{name: "unknown word", args: []string{"start"}, wantCode: 2, wantExit: true, wantStderr: `unexpected argument "start"`},
		{name: "known flag with extras", args: []string{"--version", "now"}, wantCode: 2, wantExit: true, wantStderr: `unexpected arguments "--version now"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code, exit := handleArgs(tt.args, &stdout, &stderr)
			if code != tt.wantCode || exit != tt.wantExit {
				t.Fatalf("handleArgs(%q) = (%d, %v), want (%d, %v)", tt.args, code, exit, tt.wantCode, tt.wantExit)
			}
			if tt.wantStdout == "" && stdout.Len() != 0 {
				t.Errorf("unexpected stdout: %q", stdout.String())
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if tt.wantStderr == "" && stderr.Len() != 0 {
				t.Errorf("unexpected stderr: %q", stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
