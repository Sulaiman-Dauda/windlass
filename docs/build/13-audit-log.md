# 13 Audit log page

Status: built 21/11/2026 on `feat/v063-u10-audit` (v0.63 U10, F6). Not part of the MCP
scaffold. Ships in v0.63.

## Goal

Admins can read, filter and export the audit log in the panel. Before this, `ListAudit`
existed and nothing called it.

## Fills

- `internal/store/queries/audit.sql`: `SearchAudit` (every filter optional, keyset on id,
  joins the user's email and the token's name) and `ListAuditActions`. Read-only, no
  migration.
- `internal/api/audit.go`: `GET /system/audit`, `/system/audit/actions`,
  `/system/audit/export` (CSV with a formula guard; the export writes its own audit entry).
- `web/src/components/AuditLog.tsx`: the Settings "Audit log" tab.

## Touches

- `internal/api/`, `internal/store/`, `api/openapi.yaml`, `web/src/`

## Leaves alone

How audit entries are written, the audit_log schema, MCP tools.

## Done means

`internal/server/audit_api_test.go` passes (filters, paging, bad input, CSV, viewer 403).
Local instance: the tab lists entries, and the action filter and CSV export return the
matching rows.
