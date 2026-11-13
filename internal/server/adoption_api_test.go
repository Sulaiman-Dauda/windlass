package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
)

func TestScanAndDeployReportAnAdoptionConflict(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")
	e.agent.Files["legacy"] = map[string][]byte{"docker-compose.yml": []byte("services: {}\n")}
	e.agent.Containers = []agent.Container{{Name: "old-web-1", ComposeProject: "old",
		ComposeWorkingDir: "/opt/windlass/projects/app"}}

	rec := e.do(t, http.MethodPost, "/api/v1/projects/scan", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("scan = %d: %s", rec.Code, rec.Body.String())
	}
	var scan struct {
		Count   int `json:"count"`
		Skipped []struct {
			Name   string `json:"name"`
			Reason string `json:"reason"`
		} `json:"skipped"`
		Conflicts []struct {
			Project   string `json:"project"`
			RunningAs string `json:"running_as"`
			Message   string `json:"message"`
		} `json:"conflicts"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&scan); err != nil {
		t.Fatal(err)
	}
	if scan.Count != 1 || len(scan.Skipped) != 1 || scan.Skipped[0].Name != "legacy" {
		t.Fatalf("scan = %+v", scan)
	}
	if len(scan.Conflicts) != 1 || scan.Conflicts[0].RunningAs != "old" || scan.Conflicts[0].Message == "" {
		t.Fatalf("conflicts = %+v", scan.Conflicts)
	}

	rec = e.do(t, http.MethodPost, "/api/v1/projects/app/deployments", nil, cookie)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "adoption_conflict") {
		t.Fatalf("deploy = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestScanWithNothingToReportAnswersEmptyLists(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	rec := e.do(t, http.MethodPost, "/api/v1/projects/scan", nil, cookie)
	if body := rec.Body.String(); !strings.Contains(body, `"skipped":[]`) || !strings.Contains(body, `"conflicts":[]`) {
		t.Fatalf("scan = %d: %s", rec.Code, body)
	}
}
