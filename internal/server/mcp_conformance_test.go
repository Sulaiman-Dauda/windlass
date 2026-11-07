//go:build mcpconformance

package server

import (
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The official MCP conformance suite against the router, with the fake
// agent: no Docker, but Node and the network to fetch the suite. CI runs it;
// locally:
//
//	go test -tags mcpconformance ./internal/server -run TestMCPConformance -count=1 -v
//
// Most of 0.1.16's server scenarios call tools from the suite's own fixture
// server (test_simple_text and the like), which no real server has. These
// four test the protocol itself. All run on the 2025-11-25 wire: 0.1.16 has
// no server scenario for 2026-07-28, which the unit tests cover instead.
var conformanceScenarios = []string{"server-initialize", "ping", "tools-list", "dns-rebinding-protection"}

// failsByDesign lists checks Windlass fails on purpose. It refuses any
// request carrying an Origin header, a localhost one included, because no
// browser page has business at the endpoint. The rebinding check itself, a
// foreign Origin refused, must pass.
var failsByDesign = map[string]bool{"localhost-host-valid-accepted": true}

var conformanceCmd = flag.String("conformance.cmd", "npx --yes @modelcontextprotocol/conformance@0.1.16",
	"command that runs the conformance suite, split on spaces")

type conformanceCheck struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	ErrorMessage string `json:"errorMessage"`
}

func TestMCPConformance(t *testing.T) {
	e := newTestEnv(t)
	withMCP(t, e)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")
	read := e.createToken(t, cookie, map[string]any{"name": "conformance", "scope": "read"})

	// The suite cannot send headers, so this listener adds the token. It is
	// on loopback, which the rebinding scenario requires.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+read.Token)
		e.handler.ServeHTTP(w, r)
	}))
	defer srv.Close()
	url := srv.URL + "/api/v1/mcp"

	base := strings.Fields(*conformanceCmd)
	if len(base) == 0 {
		t.Fatal("-conformance.cmd is empty")
	}
	for _, scenario := range conformanceScenarios {
		t.Run(scenario, func(t *testing.T) {
			out := t.TempDir()
			args := append(append([]string{}, base[1:]...),
				"server", "--url", url, "--scenario", scenario, "--output-dir", out)
			cmd := exec.Command(base[0], args...)
			cmd.Dir = out
			output, runErr := cmd.CombinedOutput()
			t.Logf("%s", output)

			// The suite exits 1 when any check fails, by-design ones too, so
			// the checks are judged one by one. None at all means it never ran.
			checks := readConformanceChecks(t, out, scenario)
			if len(checks) == 0 {
				t.Fatalf("the suite recorded no checks for %s (exit: %v)", scenario, runErr)
			}
			for _, c := range checks {
				switch {
				case failsByDesign[c.ID] && c.Status == "SUCCESS":
					t.Errorf("%s now passes: take it out of failsByDesign", c.ID)
				case failsByDesign[c.ID]:
					t.Logf("%s: %s, by design", c.ID, c.Status)
				case c.Status == "FAILURE":
					t.Errorf("%s failed: %s", c.ID, c.ErrorMessage)
				case c.Status != "SUCCESS":
					t.Logf("%s: %s %s", c.ID, c.Status, c.ErrorMessage)
				}
			}
		})
	}
}

// readConformanceChecks reads the checks.json the suite writes for one
// scenario, in <out>/server-<scenario>-<timestamp>/.
func readConformanceChecks(t *testing.T, out, scenario string) []conformanceCheck {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(out, "server-"+scenario+"-*", "checks.json"))
	if len(paths) != 1 {
		t.Fatalf("want one checks.json for %s, found %d", scenario, len(paths))
	}
	raw, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var checks []conformanceCheck
	if err := json.Unmarshal(raw, &checks); err != nil {
		t.Fatalf("%s: %v", paths[0], err)
	}
	return checks
}
