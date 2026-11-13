import { test, expect, type Page } from "@playwright/test";

// The Logs tab in a real browser, past its 2000-line cap (#98). The API is
// mocked and the log stream is a stand-in EventSource the test drives, so the
// rendering, the scroll events and their timing are the browser's own.

async function openLogs(page: Page) {
  const project = { id: 1, name: "app", source: "git", created_at: "2026-11-10T00:00:00Z" };
  await page.route("**/api/v1/**", (route) => {
    const path = new URL(route.request().url()).pathname.replace("/api/v1", "");
    const data = path === "/auth/status" ? { authenticated: true, needs_setup: false, user: { id: 1, email: "member@example.com", role: "member" } }
      : path === "/auth/me" ? { id: 1, email: "member@example.com", role: "member", has_password: true }
      : path === "/system/metrics" ? { node: { hostname: "test-server" }, containers: { running: 1, total: 1 } }
      : path === "/projects" ? [project]
      : path === "/projects/app" ? project
      : path.endsWith("/services") ? { services: [{ service: "web", state: "running" }] }
      : path.endsWith("/deployments") || path.endsWith("/domains") ? [] : {};
    return route.fulfill({ json: data });
  });
  await page.addInitScript(() => {
    type Listener = (ev: MessageEvent) => void;
    const streams: FakeEventSource[] = [];
    let sent = 0;
    class FakeEventSource {
      static CLOSED = 2;
      readyState = 1;
      onopen: (() => void) | null = null;
      onerror: (() => void) | null = null;
      listeners: Listener[] = [];
      constructor(public url: string) {
        streams.push(this);
        setTimeout(() => this.onopen?.(), 0);
      }
      addEventListener(type: string, fn: Listener) {
        if (type === "log") this.listeners.push(fn);
      }
      close() {
        this.readyState = 2;
      }
    }
    Object.assign(window, {
      EventSource: FakeEventSource,
      // Sends `count` more lines, numbered from 1 across calls.
      pushLogs: (count: number) => {
        const open = streams.filter((s) => s.readyState === 1 && s.url.includes("/logs"));
        for (let i = 0; i < count; i++) {
          sent++;
          const data = JSON.stringify({ stream: "stdout", text: `line ${sent}` });
          for (const s of open) for (const fn of s.listeners) fn(new MessageEvent("log", { data }));
        }
      },
    });
  });
  await page.goto("/projects/app/logs");
  await expect(page.getByText("Waiting for logs")).toBeVisible();
}

/** Sends lines in bursts with a frame between each, the way a busy service does. */
async function stream(page: Page, bursts: number, perBurst: number) {
  for (let i = 0; i < bursts; i++) {
    await page.evaluate((n) => (window as unknown as { pushLogs: (n: number) => void }).pushLogs(n), perBurst);
    await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
  }
}

/** How far the pane is from its bottom, in px. */
const fromBottom = (page: Page) =>
  page.locator(".wl-term").evaluate((el) => el.scrollHeight - el.clientHeight - el.scrollTop);

/** The first line fully in view and how far it sits below the pane's top edge. */
const topLine = (page: Page) =>
  page.locator(".wl-term").evaluate((el) => {
    const top = el.getBoundingClientRect().top;
    for (const row of Array.from(el.children)) {
      const y = row.getBoundingClientRect().top - top;
      if (y >= 0) return { text: row.textContent, y: Math.round(y) };
    }
    return null;
  });

/** Where a given line sits below the pane's top edge, or null once it is gone. */
const lineY = (page: Page, text: string) =>
  page.locator(".wl-term").evaluate((el, want) => {
    const row = Array.from(el.children).find((r) => r.textContent === want);
    return row ? Math.round(row.getBoundingClientRect().top - el.getBoundingClientRect().top) : null;
  }, text);

test("the logs pane keeps following past the cap", async ({ page }) => {
  await openLogs(page);
  await stream(page, 13, 200);
  await expect(page.getByText("2000 lines")).toBeVisible();
  await expect(page.getByText("line 2600", { exact: true })).toBeVisible();
  expect(await fromBottom(page)).toBeLessThanOrEqual(1);
});

test("a reader scrolled up keeps their place while lines arrive past the cap", async ({ page }) => {
  await openLogs(page);
  await stream(page, 11, 200);
  await expect(page.getByText("2000 lines")).toBeVisible();

  // Scroll up to the middle of the kept lines, as a reader would.
  await page.locator(".wl-term").evaluate((el) => { el.scrollTop = Math.round(el.scrollHeight / 2); });
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
  const before = await topLine(page);
  expect(before).not.toBeNull();

  // 300 more lines: each one drops a line from the top of the pane.
  await stream(page, 15, 20);
  expect(await lineY(page, before!.text!)).toBe(before!.y);

  // Back at the bottom, the pane follows again.
  await page.locator(".wl-term").evaluate((el) => { el.scrollTop = el.scrollHeight; });
  await stream(page, 5, 20);
  expect(await fromBottom(page)).toBeLessThanOrEqual(1);
});

test("a reader's place does not drift over many small bursts past the cap (#116)", async ({ page }) => {
  await openLogs(page);
  await stream(page, 11, 200);
  await page.locator(".wl-term").evaluate((el) => { el.scrollTop = Math.round(el.scrollHeight / 2); });
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
  const before = await topLine(page);

  // Rows are 20.4 px tall, so each correction is a fraction of a pixel off a
  // whole scrollTop. 150 one- and two-line bursts let any lost remainder add up.
  await stream(page, 75, 1);
  await stream(page, 75, 2);
  const exact = await page.locator(".wl-term").evaluate((el, want) => {
    const row = Array.from(el.children).find((r) => r.textContent === want)!;
    return row.getBoundingClientRect().top - el.getBoundingClientRect().top;
  }, before!.text!);
  expect(Math.abs(exact - before!.y)).toBeLessThan(1);
});

test("with following switched off the pane stays put, and resumes when switched back on", async ({ page }) => {
  await openLogs(page);
  await stream(page, 11, 200);
  await page.getByRole("button", { name: "Stop following new lines" }).click();
  const before = await topLine(page);
  await stream(page, 15, 20);
  expect(await lineY(page, before!.text!)).toBe(before!.y);

  await page.getByRole("button", { name: "Follow new lines" }).click();
  await stream(page, 1, 20);
  expect(await fromBottom(page)).toBeLessThanOrEqual(1);
});
