# Design: Windlass panel

## Identity

For the person who runs a server and the apps on it, often with live customers behind them.
The one impression is exact and calm: every screen says what is running, where, and what a
button will do before it does it. Register: dense and practical, technical and exact.

## Tokens

Source of truth: `web/src/index.css` (the `@theme` block and the light and dark token blocks).
The website's `scripts/sync-brand.mjs` reads that file, so keep its markers (see `AGENTS.md`).

- fonts: Manrope, Windlass Mono
- colours (light): #f3f4f6, #ffffff, #f8f9fa, #f1f2f4, #0c0e12, #0e1116, #4a515c, #858b96, #0d6f97, #0a5c7f, #12804a, #a15e00, #c4382a
- colours (dark): #08090a, #0e0f11, #131416, #18191c, #0a0b0d, #f2f3f5, #a0a5ae, #6b717b, #3aa9d6, #1786b8, #3ccf85, #f0a83a, #ff6b5b
- radii: 8px (controls), 12px (cards), 16px (dialogs), full (pills, dots, switches)
- accent: #0d6f97 light, #3aa9d6 dark. The primary action, the selected row, links and focus.

Type scale (px): 11 / 12.5 / 14 / 15 / 16 / 17 / 20 / 24 / 30. App body 14/20; card titles 15
semibold; dialog titles 17 semibold. Commits, image names, paths and keys are in Windlass Mono.
Spacing: Tailwind's 4px steps; cards pad 16 to 20, rows 10 to 14 vertical.
Elevation: `--shadow-xs` on cards, `--shadow-lg` on dialogs and menus only.
Density: table rows 36 to 48px, side cards in a 340px column beside the main card at xl.

## Colour roles

| Role | Token | Use |
| --- | --- | --- |
| Ink | --fg | names, values, headings |
| Secondary | --fg2 | supporting values |
| Muted | --fg3 | labels, hints, timestamps |
| Canvas | --canvas | page background |
| Surface | --surface, --surface2 | cards; table heads and footers |
| Line | --hairline, --edge | 1px rules; control borders |
| Accent | --accent, --accent-soft | primary action, selection, links, info callouts |
| Status | --ok, --warn, --err and their -soft | state only: running, needs attention, failed |

## Signature

- Status is a pill or a dot in ok, warn, err or idle, never a coloured word alone.
- Hairline cards with a header row, a body, and a surface2 footer for notes.
- A thing that is about to change something live says so in plain words before the button,
  and destructive dialogs title in `--err`.

## Banned here

- No second accent. Staging is marked with the warn tone, not a new colour.
- No gradients, no illustrations, no emoji in the app.
- No `window.confirm`; use `useConfirm` or `Modal`, which render through portals.
- No competing Tailwind classes on kit components (`cn()` does not resolve conflicts).

## Allowlist

## References

The panel predates this file; it was written down from the shipped UI on 27/10/2026.
