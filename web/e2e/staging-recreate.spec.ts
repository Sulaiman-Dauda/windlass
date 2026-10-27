import { test, expect, type Page } from "@playwright/test";

async function panel(page: Page, options: { role?: string; eligible?: boolean; hasPassword?: boolean } = {}) {
  const role = options.role ?? "member";
  const project = { id: 2, name: "app-staging", source: "git", git_repo: "https://example.com/app.git",
    git_branch: "preview", auto_deploy: true, staging_of: "app", created_at: "2026-10-27T00:00:00Z" };
  const live = { ...project, id: 1, name: "app", staging_of: undefined, staging: "app-staging" };
  const latest = { id: 1, number: 1, status: "failed", triggered_by: "manual", created_at: project.created_at };
  let inGap = false;
  let gapReads = 0;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace("/api/v1", "");
    if (path.endsWith("/staging/recreate")) return route.fallback();
    if (path.endsWith("/staging") && inGap) {
      gapReads++;
      return route.fulfill({ status: 404, json: { error: { code: "not_found", message: "project has no staging" } } });
    }
    const data = path === "/auth/status" ? { authenticated: true, needs_setup: false, user: { id: 1, email: "member@example.com", role } }
      : path === "/auth/me" ? { id: 1, email: "member@example.com", role, has_password: options.hasPassword ?? true }
      : path === "/system/metrics" ? { node: { hostname: "test-server" }, containers: { running: 0, total: 0 } }
      : path === "/projects" ? [live, project]
      : path === "/projects/app" ? live
      : path === "/projects/app-staging" ? project
      : path.endsWith("/staging") ? { live: "app", staging: "app-staging", branch: "preview", auto_promote: true,
        staging_latest: latest, live_current: null, live_latest: null, promotable: null,
        promote_blocked: "staging has no succeeded deployment yet", can_recreate: options.eligible ?? true, rewrites: [], drift: [] }
      : path.endsWith("/services") ? { services: [] }
      : path.endsWith("/deployments") ? [latest]
      : path.endsWith("/domains") ? [] : {};
    return route.fulfill({ json: data });
  });
  return { project, gap: () => { inGap = true; }, gapReads: () => gapReads };
}

for (const tc of [
  { role: "member", eligible: true, project: "app-staging", visible: true },
  { role: "viewer", eligible: true, project: "app-staging", visible: false },
  { role: "member", eligible: false, project: "app-staging", visible: false },
  { role: "member", eligible: true, project: "app", visible: false },
]) {
  test(`recreate visibility: ${tc.role}, eligible=${tc.eligible}, ${tc.project}`, async ({ page }) => {
    await panel(page, tc);
    await page.goto(`/projects/${tc.project}`);
    await expect(page.getByText("No succeeded deployment yet").first()).toBeVisible();
    await expect(page.getByRole("button", { name: "Recreate staging", exact: true })).toHaveCount(tc.visible ? 1 : 0);
  });
}

test("recreation keeps its confirmation through a missing pair and shows the rotated webhook secret", async ({ page }) => {
  await page.clock.install();
  const fixture = await panel(page);
  let finish!: () => void;
  const wait = new Promise<void>((resolve) => { finish = resolve; });
  let submitted: unknown;
  await page.route("**/staging/recreate", async (route) => {
    submitted = route.request().postDataJSON();
    fixture.gap();
    await wait;
    await route.fulfill({ status: 201, json: { project: fixture.project, result: {
      deployment: 1, rewritten: {}, skipped: [], webhook_secret: "fresh-webhook-secret", webhook_registered: false,
    } } });
  });
  await page.goto("/projects/app-staging");
  await page.getByRole("button", { name: "Recreate staging", exact: true }).click();
  const dialog = page.getByRole("dialog");
  const submit = dialog.getByRole("button", { name: "Recreate staging", exact: true });
  await expect(submit).toBeDisabled();
  await dialog.getByLabel(/Type app-staging to confirm/).fill("app-staging");
  await dialog.getByLabel("Your password").fill("memberpassword123");
  await expect(submit).toBeDisabled();
  await dialog.getByRole("switch", { name: "Reuse staging's current environment values" }).click();
  await submit.click();
  await expect.poll(() => submitted).toEqual({ password: "memberpassword123", acknowledged: true });
  await page.clock.fastForward(16_000);
  await expect.poll(() => fixture.gapReads()).toBeGreaterThan(0);
  await expect(dialog.getByText("Recreating…")).toBeVisible();
  finish();
  await expect(dialog.getByText("Created app-staging", { exact: true })).toBeVisible();
  await expect(dialog.getByText("fresh-webhook-secret", { exact: true })).toBeVisible();
});

test("failed deletion stays visible for retry", async ({ page }) => {
  await panel(page, { hasPassword: false });
  await page.route("**/staging/recreate", (route) => route.fulfill({ status: 500,
    json: { error: { code: "internal", message: "Could not remove staging volumes" } },
  }));
  await page.goto("/projects/app-staging");
  await page.getByRole("button", { name: "Recreate staging", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByLabel("Your password")).toHaveCount(0);
  await dialog.getByLabel(/Type app-staging to confirm/).fill("app-staging");
  await dialog.getByRole("switch", { name: "Reuse staging's current environment values" }).click();
  await dialog.getByRole("button", { name: "Recreate staging", exact: true }).click();
  await expect(dialog.getByText("Could not remove staging volumes")).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Recreate staging", exact: true })).toBeEnabled();
});
