package api

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/store/db"
)

const (
	auditPageDefault = 100
	auditPageMax     = 500
	// auditTimeFormat is how audit_log.ts is written, so a filter bound in
	// the same form compares correctly as text.
	auditTimeFormat = "2006-01-02T15:04:05.000Z"
)

type auditEntry struct {
	ID           int64           `json:"id"`
	Ts           string          `json:"ts"`
	UserID       *int64          `json:"user_id"`
	UserEmail    string          `json:"user_email"`
	TokenID      *int64          `json:"token_id"`
	TokenName    string          `json:"token_name"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	IP           string          `json:"ip"`
	Detail       json.RawMessage `json:"detail"`
}

func toAuditEntry(r db.SearchAuditRow) auditEntry {
	e := auditEntry{
		ID: r.ID, Ts: r.Ts, UserEmail: r.UserEmail, TokenName: r.TokenName, Action: r.Action,
		ResourceType: r.ResourceType.String, ResourceID: r.ResourceID.String, IP: r.Ip.String,
		Detail: json.RawMessage("null"),
	}
	if r.UserID.Valid {
		e.UserID = &r.UserID.Int64
	}
	if r.TokenID.Valid {
		e.TokenID = &r.TokenID.Int64
	}
	if r.Detail.Valid && json.Valid([]byte(r.Detail.String)) {
		e.Detail = json.RawMessage(r.Detail.String)
	}
	return e
}

// parseAuditTime reads a date (YYYY-MM-DD, a whole UTC day) or an RFC 3339
// time. For an until bound a date means the end of that day.
func parseAuditTime(s string, until bool) (string, bool) {
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		if until {
			t = t.AddDate(0, 0, 1)
		}
		return t.UTC().Format(auditTimeFormat), true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(auditTimeFormat), true
	}
	return "", false
}

// auditFilter turns the query string into search parameters. An empty
// value is no filter.
func auditFilter(r *http.Request) (db.SearchAuditParams, string) {
	q := r.URL.Query()
	var p db.SearchAuditParams
	if v := q.Get("action"); v != "" {
		if prefix, ok := strings.CutSuffix(v, "*"); ok {
			p.ActionPrefix = prefix
		} else {
			p.Action = v
		}
	}
	if v := q.Get("user_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			return p, "user_id must be a positive integer"
		}
		p.UserID = id
	}
	if v := q.Get("resource_type"); v != "" {
		p.ResourceType = v
	}
	if v := q.Get("resource_id"); v != "" {
		p.ResourceID = v
	}
	if v := q.Get("since"); v != "" {
		ts, ok := parseAuditTime(v, false)
		if !ok {
			return p, "since must be YYYY-MM-DD or an RFC 3339 time"
		}
		p.Since = ts
	}
	if v := q.Get("until"); v != "" {
		ts, ok := parseAuditTime(v, true)
		if !ok {
			return p, "until must be YYYY-MM-DD or an RFC 3339 time"
		}
		p.Until = ts
	}
	if v := q.Get("before_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			return p, "before_id must be a positive integer"
		}
		p.BeforeID = id
	}
	p.RowLimit = auditPageDefault
	if v := q.Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 || n > auditPageMax {
			return p, "limit must be between 1 and 500"
		}
		p.RowLimit = n
	}
	return p, ""
}

// handleListAudit serves one page of the audit log, newest first, with
// next_before_id set when an older page exists.
func (a *API) handleListAudit(w http.ResponseWriter, r *http.Request) {
	p, msg := auditFilter(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, "bad_request", msg)
		return
	}
	limit := p.RowLimit
	p.RowLimit = limit + 1
	rows, err := a.Queries.SearchAudit(r.Context(), p)
	if err != nil {
		a.internalError(w, "list audit", err)
		return
	}
	var next *int64
	if int64(len(rows)) > limit {
		rows = rows[:limit]
		next = &rows[limit-1].ID
	}
	entries := make([]auditEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, toAuditEntry(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "next_before_id": next})
}

// handleListAuditActions lists every action the log holds, for the filter.
func (a *API) handleListAuditActions(w http.ResponseWriter, r *http.Request) {
	actions, err := a.Queries.ListAuditActions(r.Context())
	if err != nil {
		a.internalError(w, "list audit actions", err)
		return
	}
	if actions == nil {
		actions = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": actions})
}

// csvCell stops a spreadsheet reading a cell as a formula.
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// handleExportAudit streams every entry the filter matches as CSV, newest
// first. limit and before_id are ignored: the export is the whole match.
// The export is itself audited, since it takes the log off the server.
func (a *API) handleExportAudit(w http.ResponseWriter, r *http.Request) {
	p, msg := auditFilter(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, "bad_request", msg)
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "system.audit_export", "system", "audit", remoteIP(r),
		map[string]string{"query": r.URL.RawQuery})

	p.BeforeID = nil
	p.RowLimit = 1000
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="windlass-audit-`+time.Now().UTC().Format("20060102-150405")+`.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "time", "user_id", "user", "token_id", "token", "action", "resource_type", "resource_id", "ip", "detail"})
	for {
		rows, err := a.Queries.SearchAudit(r.Context(), p)
		if err != nil {
			// Headers are gone; the truncated file is the signal.
			a.Logger.Error("export audit", "error", err)
			break
		}
		for _, row := range rows {
			id := func(v *int64) string {
				if v == nil {
					return ""
				}
				return strconv.FormatInt(*v, 10)
			}
			e := toAuditEntry(row)
			detail := ""
			if row.Detail.Valid {
				detail = row.Detail.String
			}
			_ = cw.Write([]string{
				strconv.FormatInt(e.ID, 10), e.Ts, id(e.UserID), csvCell(e.UserEmail), id(e.TokenID), csvCell(e.TokenName),
				csvCell(e.Action), csvCell(e.ResourceType), csvCell(e.ResourceID), csvCell(e.IP), csvCell(detail),
			})
		}
		cw.Flush()
		if int64(len(rows)) < p.RowLimit || cw.Error() != nil {
			break
		}
		p.BeforeID = rows[len(rows)-1].ID
	}
}
