package mcp

import (
	"os"
	"strings"
	"testing"
)

// TestDeployScopeMatchesReadOnly holds the rule the panel's tool list
// relies on: a tool needs deploy scope exactly when it is not read-only.
func TestDeployScopeMatchesReadOnly(t *testing.T) {
	for _, d := range catalogue() {
		if (d.minScope == scopeDeploy) == d.Annotations.ReadOnlyHint {
			t.Errorf("%s: minScope %q but readOnlyHint %v", d.Name, d.minScope, d.Annotations.ReadOnlyHint)
		}
	}
}

// TestDocsListEveryTool checks the Tools table in docs/mcp.md names
// exactly the catalogue, in order, with the scope each tool needs.
func TestDocsListEveryTool(t *testing.T) {
	data, err := os.ReadFile("../../docs/mcp.md")
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 4 {
			continue
		}
		rows = append(rows, strings.Trim(strings.TrimSpace(cells[1]), "`")+" "+strings.TrimSpace(cells[2]))
	}
	var want []string
	for _, d := range catalogue() {
		needs := "any"
		if d.minScope == scopeDeploy {
			needs = "deploy"
		}
		want = append(want, d.Name+" "+needs)
	}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Errorf("docs/mcp.md Tools table:\n%s\nwant (name, needs):\n%s", strings.Join(rows, "\n"), strings.Join(want, "\n"))
	}
}
