package server

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestAuditAPI(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	viewer := e.viewerCookie(t, cookie) // writes user.create

	// Seed rows with known times and actions. The formula row checks the
	// CSV guard.
	for i, row := range [][3]string{
		{"2026-11-01T10:00:00.000Z", "project.deploy", "app"},
		{"2026-11-02T10:00:00.000Z", "project.delete", "old"},
		{"2026-11-03T10:00:00.000Z", "domain.add", "=HYPERLINK(\"x\")"},
	} {
		if _, err := e.sqlDB.Exec(`INSERT INTO audit_log (ts, user_id, action, resource_type, resource_id, ip, detail)
			VALUES (?, 1, ?, 'project', ?, '127.0.0.1', ?)`, row[0], row[1], row[2], fmt.Sprintf(`{"n":%d}`, i)); err != nil {
			t.Fatal(err)
		}
	}

	type page struct {
		Entries []struct {
			ID         int64           `json:"id"`
			Action     string          `json:"action"`
			UserEmail  string          `json:"user_email"`
			ResourceID string          `json:"resource_id"`
			Detail     json.RawMessage `json:"detail"`
		} `json:"entries"`
		NextBeforeID *int64 `json:"next_before_id"`
	}
	get := func(query string) page {
		t.Helper()
		rec := e.do(t, http.MethodGet, "/api/v1/system/audit?"+query, nil, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d: %s", query, rec.Code, rec.Body.String())
		}
		var p page
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	if p := get("action=project.*"); len(p.Entries) != 2 || p.Entries[0].Action != "project.delete" ||
		p.Entries[0].UserEmail == "" || string(p.Entries[0].Detail) != `{"n":1}` {
		t.Fatalf("prefix = %+v", p)
	}
	if p := get("action=project.deploy"); len(p.Entries) != 1 || p.Entries[0].ResourceID != "app" {
		t.Fatalf("exact = %+v", p)
	}
	// until as a date takes in the whole day.
	if p := get("since=2026-11-02&until=2026-11-02"); len(p.Entries) != 1 || p.Entries[0].Action != "project.delete" {
		t.Fatalf("dates = %+v", p)
	}
	if p := get("resource_type=project&resource_id=old"); len(p.Entries) != 1 {
		t.Fatalf("resource = %+v", p)
	}

	// Keyset paging walks every row once.
	seen := map[int64]bool{}
	q := "limit=2"
	for range 10 {
		p := get(q)
		for _, en := range p.Entries {
			if seen[en.ID] {
				t.Fatalf("row %d twice", en.ID)
			}
			seen[en.ID] = true
		}
		if p.NextBeforeID == nil {
			break
		}
		q = fmt.Sprintf("limit=2&before_id=%d", *p.NextBeforeID)
	}
	var total int
	_ = e.sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_log`).Scan(&total)
	if len(seen) != total {
		t.Fatalf("paged %d of %d", len(seen), total)
	}

	for _, bad := range []string{"limit=0", "limit=501", "since=yesterday", "user_id=x", "before_id=-1"} {
		if rec := e.do(t, http.MethodGet, "/api/v1/system/audit?"+bad, nil, cookie); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", bad, rec.Code)
		}
	}

	rec := e.do(t, http.MethodGet, "/api/v1/system/audit/actions", nil, cookie)
	if !strings.Contains(rec.Body.String(), `"domain.add","project.delete","project.deploy"`) {
		t.Fatalf("actions = %s", rec.Body.String())
	}

	rec = e.do(t, http.MethodGet, "/api/v1/system/audit/export?resource_type=project", nil, cookie)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("export = %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	records, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil || len(records) != 4 || records[0][6] != "action" {
		t.Fatalf("csv = %v, %v", records, err)
	}
	if records[1][8] != `'=HYPERLINK("x")` || records[3][10] != `{"n":0}` {
		t.Fatalf("csv rows = %v", records[1:])
	}
	if p := get("action=system.audit_export"); len(p.Entries) != 1 {
		t.Fatalf("export not audited: %+v", p)
	}

	for _, path := range []string{"/api/v1/system/audit", "/api/v1/system/audit/actions", "/api/v1/system/audit/export"} {
		if rec := e.do(t, http.MethodGet, path, nil, viewer); rec.Code != http.StatusForbidden {
			t.Errorf("viewer %s = %d", path, rec.Code)
		}
	}
}
