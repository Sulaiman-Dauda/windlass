# 18 UI stability

Status: built 23/11/2026 on `fix/ui-stability`. Not part of the MCP scaffold. Frontend only.

## Goal

No panel view moves while it loads or when you switch tabs: no layout shift after first
paint, no tab that changes position or width, one main column width on every view.

## Fills

- `web/scripts/layout-shift.mjs`: the Playwright check that defines done.
- `web/src/ui/Stable.tsx`: `StableLabel` and `ReserveWidth` reserve a label's widest form.
- `web/src/ui/Page.tsx`: a `loading` prop that holds the body until its quick reads land.
- `web/src/pages/Settings.tsx`: `useSectionLoading` gates each section on its reads.

## Touches

- `web/src/`
- `web/scripts/`
- `web/knip.json`

## Leaves alone

The API, the Go server and `web/src/index.css`. In `web/src/api/auth.ts` the `/auth/me` read
moves into a shared `meQuery` so Settings can wait on it; its key, URL and handling are the
same, and login, logout and sessions are unchanged.

## Done means

`node web/scripts/layout-shift.mjs` against a scratch instance prints PASS at 1440 and
390 px, light and dark, with classic scrollbars (265 problems before). `make test` and
`npm run build` pass.
