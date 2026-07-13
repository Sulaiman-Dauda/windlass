// Package audit writes the append-only audit log (principle 15). Failures
// are logged, never fatal: an audit hiccup must not break the operation it
// describes.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"

	"github.com/windlass-dev/windlass/internal/store/db"
)

type Log struct {
	q      *db.Queries
	logger *slog.Logger
}

func New(q *db.Queries, logger *slog.Logger) *Log {
	return &Log{q: q, logger: logger}
}

// Write records an action. userID may be 0 for unauthenticated actions
// (e.g. failed logins); detail is JSON-serialized when non-nil.
func (l *Log) Write(ctx context.Context, userID int64, action, resourceType, resourceID, ip string, detail any) {
	var detailStr sql.NullString
	if detail != nil {
		if b, err := json.Marshal(detail); err == nil {
			detailStr = sql.NullString{String: string(b), Valid: true}
		}
	}
	err := l.q.InsertAudit(ctx, db.InsertAuditParams{
		UserID:       sql.NullInt64{Int64: userID, Valid: userID != 0},
		Action:       action,
		ResourceType: sql.NullString{String: resourceType, Valid: resourceType != ""},
		ResourceID:   sql.NullString{String: resourceID, Valid: resourceID != ""},
		Ip:           sql.NullString{String: ip, Valid: ip != ""},
		Detail:       detailStr,
	})
	if err != nil {
		l.logger.Error("audit write failed", "action", action, "error", err)
	}
}
