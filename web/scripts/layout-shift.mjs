#!/usr/bin/env node
// Layout shift check for the panel: visits every route, clicks every project
// tab, settings section and sidebar link, and fails on any movement.
//
// What it measures, per view:
//   - every `layout-shift` entry after first paint, including the ones
//     Chrome marks hadRecentInput (a tab click is input, and a tab bar that
//     jumps under the pointer is exactly the shift we care about);
//   - the box of every tab in the tab bars before and after each click;
//   - the content width of the main scroll column, which must be the same
//     on every route (a scrollbar that comes and goes changes it).
//
// Run against a scratch instance only (AGENTS.md), never a real one:
//   WINDLASS_URL=http://127.0.0.1:8822 WINDLASS_EMAIL=... WINDLASS_PASSWORD=... \
//     node scripts/layout-shift.mjs [--project website] [--only 1440-light] [--out dir]
//
// Classic scrollbars are forced with --disable-features=OverlayScrollbar, so
// the check sees what Windows and most Linux desktops see.
import { chromium } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const args = process.argv.slice(2);
const flag = (name, dflt) => {
  const i = args.indexOf(`--${name}`);
  return i >= 0 ? args[i + 1] : dflt;
};

const BASE = process.env.WINDLASS_URL ?? "http://127.0.0.1:8822";
const EMAIL = process.env.WINDLASS_EMAIL;
const PASSWORD = process.env.WINDLASS_PASSWORD;
const OUT = flag("out", "");
const ONLY = flag("only", "");
const SETTLE = Number(flag("settle", "1800"));
const EXECUTABLE = process.env.WINDLASS_CHROMIUM || undefined;

if (!EMAIL || !PASSWORD) {
  console.error("WINDLASS_EMAIL and WINDLASS_PASSWORD are required");
  process.exit(2);
}

const CONFIGS = [
  { name: "1440-light", width: 1440, height: 900, scheme: "light" },
  { name: "1440-dark", width: 1440, height: 900, scheme: "dark" },
  { name: "390-light", width: 390, height: 844, scheme: "light" },
  { name: "390-dark", width: 390, height: 844, scheme: "dark" },
].filter((c) => !ONLY || ONLY.split(",").includes(c.name));

// Records every shift with the nodes that moved, so a failure names its cause.
const OBSERVER = () => {
  window.__shifts = [];
  const describe = (n) => {
    if (n && n.nodeType === 3) return `#text "${n.textContent.trim().slice(0, 30)}" in ${describe(n.parentElement)}`;
    if (!n || n.nodeType !== 1) return n ? n.nodeName : "?";
    const cls = (n.getAttribute("class") || "").split(/\s+/).slice(0, 4).join(".");
    const text = (n.textContent || "").trim().replace(/\s+/g, " ").slice(0, 40);
    return `${n.tagName.toLowerCase()}${cls ? "." + cls : ""} "${text}"`;
  };
  new PerformanceObserver((list) => {
    for (const e of list.getEntries()) {
      window.__shifts.push({
        value: e.value,
        input: e.hadRecentInput,
        t: Math.round(e.startTime),
        sources: (e.sources || []).map((s) => ({
          node: describe(s.node),
          // Exact, unrounded: a box that did not move at all is told apart
          // from one that moved by a fraction of a pixel.
          same: ["x", "y", "width", "height"].every((k) => s.previousRect[k] === s.currentRect[k]),
          from: [s.previousRect.x, s.previousRect.y, s.previousRect.width, s.previousRect.height].map(Math.round),
          to: [s.currentRect.x, s.currentRect.y, s.currentRect.width, s.currentRect.height].map(Math.round),
        })),
      });
    }
  }).observe({ type: "layout-shift", buffered: true });
};

const failures = [];
const fail = (cfg, view, msg) => failures.push(`[${cfg.name}] ${view}: ${msg}`);
// Chrome also logs an entry when a polled value is replaced inside a box that
// keeps its exact position and size (a live memory figure in a right-aligned
// cell): the text starts at a new x, nothing on the page moves. Those are
// listed as notes so they stay visible, but they do not fail the check.
const notes = [];

async function takeShifts(page) {
  return page.evaluate(() => {
    const s = window.__shifts || [];
    window.__shifts = [];
    return s;
  });
}

// The main column is the panel's own scroller (Layout.tsx), not the window.
async function mainWidth(page) {
  return page.evaluate(() => {
    const el = document.querySelector("main > div");
    if (!el) return null;
    const cs = getComputedStyle(el);
    const borders = parseFloat(cs.borderLeftWidth) + parseFloat(cs.borderRightWidth);
    return { client: el.clientWidth, bar: el.offsetWidth - el.clientWidth - borders, scrolls: el.scrollHeight > el.clientHeight };
  });
}

async function boxes(page, navLabel) {
  return page.evaluate((label) => {
    const nav = document.querySelector(`nav[aria-label="${label}"]`);
    if (!nav) return null;
    // Positions inside the nav's scrolled content: on a phone the tab strip
    // scrolls the active tab into view, which is a scroll, not a shift.
    const n = nav.getBoundingClientRect();
    return [...nav.querySelectorAll("a")].map((a) => {
      const r = a.getBoundingClientRect();
      const x = r.x - n.x + nav.scrollLeft;
      return { label: a.innerText.trim(), x: Math.round(x * 10) / 10, w: Math.round(r.width * 10) / 10 };
    });
  }, navLabel);
}

function compareBoxes(cfg, view, before, after) {
  if (!before || !after) return;
  for (let i = 0; i < Math.min(before.length, after.length); i++) {
    const a = before[i];
    const b = after[i];
    if (a.x !== b.x || a.w !== b.w) {
      fail(cfg, view, `tab "${a.label}" moved: x ${a.x} -> ${b.x}, width ${a.w} -> ${b.w}`);
      return; // one line per click is enough to name the cause
    }
  }
}

function reportShifts(cfg, view, shifts) {
  for (const s of shifts) {
    const where = s.sources.map((x) => `${x.node} ${x.from.join(",")} -> ${x.to.join(",")}`).join(" | ");
    const msg = `layout shift ${s.value.toExponential(2)}${s.input ? " (after input)" : ""} at ${s.t}ms: ${where}`;
    const textOnly = s.sources.length > 0 && s.sources.every((x) => x.same && x.node.startsWith("#text"));
    if (textOnly) notes.push(`[${cfg.name}] ${view}: ${msg}`);
    else fail(cfg, view, msg);
  }
}

// A view is ready when its page heading is on screen; then late fetches get
// SETTLE ms to land. networkidle never fires here (SSE streams).
async function settle(page) {
  await page.locator("main h1").first().waitFor({ state: "visible", timeout: 15_000 });
  await page.waitForTimeout(SETTLE);
}

async function shoot(page, cfg, view) {
  if (!OUT) return;
  const file = join(OUT, `${cfg.name}-${view.replace(/[^a-z0-9]+/gi, "_")}.png`);
  await page.screenshot({ path: file });
}

async function run(cfg, browser, projects) {
  const context = await browser.newContext({
    viewport: { width: cfg.width, height: cfg.height },
    colorScheme: cfg.scheme,
    baseURL: BASE,
  });
  await context.addInitScript(OBSERVER);
  const login = await context.request.post("/api/v1/auth/login", { data: { email: EMAIL, password: PASSWORD } });
  if (!login.ok()) throw new Error(`login failed: ${login.status()}`);
  const page = await context.newPage();
  const widths = new Map();

  const visit = async (path, view) => {
    await page.goto(path);
    await settle(page);
    reportShifts(cfg, view, await takeShifts(page));
    const w = await mainWidth(page);
    if (w) widths.set(view, w);
    await shoot(page, cfg, view);
  };

  // A click is a soft navigation inside the same document: the shift
  // buffer and the tab boxes before it are the baseline.
  const click = async (locator, view, navLabel) => {
    await page.waitForTimeout(50);
    await takeShifts(page);
    const before = navLabel ? await boxes(page, navLabel) : null;
    await locator.click();
    await settle(page);
    const after = navLabel ? await boxes(page, navLabel) : null;
    compareBoxes(cfg, view, before, after);
    reportShifts(cfg, view, await takeShifts(page));
    const w = await mainWidth(page);
    if (w) widths.set(view, w);
    await shoot(page, cfg, view);
  };

  // Every top-level route, loaded cold.
  const routes = ["/", "/projects", "/templates", "/agents", "/settings/general"];
  for (const r of routes) await visit(r, `load ${r}`);
  for (const p of projects) await visit(`/projects/${p}`, `load /projects/${p}`);

  // Sidebar links (desktop only; on a phone they sit in the drawer).
  if (cfg.width >= 1024) {
    for (const label of ["Projects", "Templates", "AI agents", "Settings", "Overview"]) {
      await click(page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: label, exact: true }), `sidebar ${label}`, "Main");
    }
  }

  // Settings sections, each clicked in turn.
  await visit("/settings/general", "settings start");
  const sections = await boxes(page, "Settings sections");
  for (const s of sections ?? []) {
    await click(
      page.getByRole("navigation", { name: "Settings sections" }).getByRole("link", { name: s.label, exact: true }),
      `settings ${s.label}`,
      "Settings sections",
    );
  }

  // Every tab of every project, in order and then back to the first.
  for (const p of projects) {
    await visit(`/projects/${p}`, `project ${p}`);
    const tabs = (await boxes(page, "Project sections")) ?? [];
    for (const t of [...tabs.slice(1), tabs[0]]) {
      await click(
        page.getByRole("navigation", { name: "Project sections" }).getByRole("link", { name: t.label, exact: true }),
        `${p} tab ${t.label}`,
        "Project sections",
      );
    }
  }

  // One content width for the main column across every view.
  const distinct = new Map();
  for (const [view, w] of widths) {
    const k = w.client;
    if (!distinct.has(k)) distinct.set(k, []);
    distinct.get(k).push(view);
  }
  if (distinct.size > 1) {
    const parts = [...distinct].map(([k, v]) => `${k}px on ${v.length} view(s), e.g. ${v.slice(0, 3).join(", ")}`);
    fail(cfg, "main column", `content width differs: ${parts.join("; ")}`);
  }
  const sample = [...widths.values()].find((w) => w.scrolls);
  const scrolling = [...widths.values()].filter((w) => w.scrolls).length;
  console.log(
    `[${cfg.name}] ${widths.size} views (${scrolling} scroll), main column ${[...distinct.keys()].join("/")}px` +
      (sample ? `, scrollbar ${sample.bar}px wide on a scrolling view` : ""),
  );
  await context.close();
}

const browser = await chromium.launch({
  executablePath: EXECUTABLE,
  args: ["--disable-features=OverlayScrollbar"],
  // Playwright passes --hide-scrollbars to headless Chromium, which would
  // hide the very scrollbar this check is looking for.
  ignoreDefaultArgs: ["--hide-scrollbars"],
});
try {
  if (OUT) mkdirSync(OUT, { recursive: true });
  // Projects come from the API so the script works on any scratch dataset.
  const ctx = await browser.newContext({ baseURL: BASE });
  await ctx.request.post("/api/v1/auth/login", { data: { email: EMAIL, password: PASSWORD } });
  const list = await (await ctx.request.get("/api/v1/projects")).json();
  await ctx.close();
  const only = flag("project", "");
  const projects = list.map((p) => p.name).filter((n) => !only || only.split(",").includes(n));
  if (projects.length === 0) throw new Error("no projects on this instance: seed a scratch dataset first");
  for (const cfg of CONFIGS) await run(cfg, browser, projects);
} finally {
  await browser.close();
}

if (OUT) writeFileSync(join(OUT, "failures.txt"), failures.join("\n") + "\n");
if (notes.length) {
  console.log(`\nNote: ${notes.length} text change(s) inside a box that did not move`);
  for (const n of notes) console.log("  " + n);
}
if (failures.length) {
  console.log(`\nFAIL: ${failures.length} problem(s)`);
  for (const f of failures) console.log("  " + f);
  process.exit(1);
}
console.log("\nPASS: no layout shift, no tab movement, one main column width");
