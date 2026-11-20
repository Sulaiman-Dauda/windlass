# Design: Windlass panel

## Identity

For the person who runs a server and the apps on it, often with live customers behind them.
The one impression is exact and calm: every screen says what is running, where, and what a
button will do before it does it. Register: dense and practical, technical and exact.

## Tokens

Source of truth: `web/src/index.css` (the `@theme` block and the light and dark token blocks).
The website's `scripts/sync-brand.mjs` reads that file, so keep its markers (see `AGENTS.md`).

- fonts: Schibsted Grotesk, IBM Plex Mono, Manrope
- colours: #005277, #00648b, #00739d, #050708, #07080a, #080a0d, #082d3d, #0a0c0f, #0b7743, #0e1013, #0f2e1b, #111417, #11171c, #171a1d, #262b31, #363e46, #382409, #421c18, #42484f, #49535d, #50b1dc, #52cd86, #5e646a, #6ec3eb, #945a00, #94999e, #95a1ac, #a52018, #b4bec8, #ba362b, #babec3, #ced5dd, #dff8e6, #e2f3fd, #eceff1, #edf1f3, #f1aa47, #f1f4f7, #f66d62, #f7f9fc, #ff887c, #ffe9e5, #ffeecf, #ffffff, #c2dae3, #649f79, #244352, #408f62
- radii: 0 (rows and tables), 5px (tiles and keys), 7px (controls), 10px (cards), 14px (panel and dialogs), pill
- Colour is written in OKLCH. The list above is each token as the browser resolves it, light and
  dark, so the lint reads them as tokens. #c2dae3 and #244352 are `--accent-edge` on the light and
  dark surface; #649f79 and #408f62 are the settled step rail. Hairlines and edges carry a little of the accent.
  Mix colours in OKLab, or in OKLCH only between two chromatic colours: white written as
  `oklch(100% 0 0)` has a hue of 0, and an OKLCH mix with it swings green through yellow.
- accent: #00648b light, #50b1dc dark. The primary action, the selected row, links and focus.
  A filled button uses `--accent-fill` (#00739d in dark) so white text on it passes 4.5:1.

Type scale (px): 12 / 12.5 / 14 / 15 / 16 / 17 / 20 / 24 / 30. App body 14/20; card titles 15
semibold; dialog titles 17 semibold. Commits, image names, paths and keys are in IBM Plex Mono;
Manrope is the wordmark only. Figures in values use tabular numbers (`tnum`, digits only).
Spacing: Tailwind's 4px steps; cards pad 16 to 20, rows 10 to 14 vertical.
Elevation: `--shadow-xs` on cards, `--shadow-lg` on dialogs and menus only.
Density: table rows 36 to 48px, side cards in a 340px column beside the main card at xl.

## Colour roles

| Role | Token | Use |
| --- | --- | --- |
| Ink | --fg | names, values, headings |
| Secondary | --fg2 | supporting values |
| Muted | --fg3 | labels, hints, timestamps; at least 4.5:1 on every background in both themes |
| Canvas | --canvas | page background |
| Surface | --surface, --surface2 | cards; table heads and footers |
| Line | --hairline, --edge | 1px rules; control borders |
| Accent | --accent, --accent-soft | primary action, selection, links, info callouts |
| Status | --ok, --warn, --err and their -soft | state only: running, needs attention, failed |

## Signature

- Status is a pill or a dot in ok, warn, err or idle, never a coloured word alone.
- Hairline cards with a header row, a body, and a surface2 footer for notes.
- The sounding meter: a ruler with a tick every 10%, a 3px fill and a 1px plumb mark at the
  value. A budget (the Windlass idle figure) is a dashed warn notch on the ruler.
- The step strip: a 4px rail per deploy step. Done is fg2, skipped is dashed and struck
  through, the running step is an accent bar travelling along its rail, failed is err, pending
  is the edge colour. A deploy whose events are not loaded shows passed steps in a muted ok.
- A failing log line is tinted err with a 2px rule on its left edge.
- A thing that is about to change something live says so in plain words before the button,
  and destructive dialogs title in `--err`.

## Banned here

- No second accent. Staging is marked with the warn tone (the offer card has a dashed warn
  border), not a new colour.
- No gradients, no illustrations, no emoji in the app. The meter ruler and a skipped step's
  dashes are repeating-gradient patterns, not shading.
- No `window.confirm`; use `useConfirm` or `Modal`, which render through portals.
- No competing Tailwind classes on kit components (`cn()` does not resolve conflicts).

## Allowlist

- `motion` transition: all: the reduced-motion reset in index.css sets a 0.001ms duration on
  every element, which the lint counts. Component transitions name their properties.
- `off-token-font` Manrope: the wordmark only, until the logo is outlined as SVG.
- `stats`: the deploy history's relative times ("3m ago") read as marketing numbers. The panel
  has no marketing copy; every figure in it comes from the server.

## References

The panel predates this file; it was written down from the shipped UI on 27/10/2026.
