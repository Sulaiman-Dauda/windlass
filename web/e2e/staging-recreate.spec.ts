import { test, expect, type Page } from "@playwright/test";
import { panelData, stagingProject as project } from "./staging-fixture";

async function panel(page: Page, options: { role?: string; eligible?: boolean; hasPassword?: boolean; flaggedKeys?: string[] } = {}) {
  const role = options.role ?? "member";
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
    const data = panelData(path, role, options.hasPassword ?? true)
      ?? (path.endsWith("/staging") ? { live: "app", staging: "app-staging", branch: "preview", auto_promote: true,
        staging_latest: latest, live_current: null, live_latest: null, promotable: null,
        promote_blocked: "staging has no succeeded deployment yet", can_recreate: options.eligible ?? true,
        recreate_flagged_keys: options.flaggedKeys ?? [], rewrites: [], drift: [], env_missing: [] }
      : path.endsWith("/deployments") ? [latest]
      : {});
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

test("recreation confirms flagged keys, survives missing status and shows the new webhook secret", async ({ page }) => {
  await page.clock.install();
  const fixture = await panel(page, { flaggedKeys: ["SMTP_HOST", "STRIPE_SECRET_KEY"] });
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
  await expect(dialog.getByText("SMTP_HOST", { exact: true })).toBeVisible();
  await expect(dialog.getByText("STRIPE_SECRET_KEY", { exact: true })).toBeVisible();
  await expect(dialog.getByText(/Check these in staging's Environment tab/)).toBeVisible();
  const submit = dialog.getByRole("button", { name: "Recreate staging", exact: true });
  await expect(submit).toBeDisabled();
  await dialog.getByLabel(/Type app-staging to confirm/).fill("app-staging");
  await dialog.getByLabel("Your password").fill("memberpassword123");
  await expect(submit).toBeDisabled();
  await dialog.getByRole("switch", { name: "Reuse staging's current environment values" }).click();
  await expect(submit).toBeEnabled();
  await submit.click();
  await expect.poll(() => submitted).toEqual({ password: "memberpassword123", acknowledged: true });
  await page.clock.fastForward(16_000);
  await expect.poll(() => fixture.gapReads()).toBeGreaterThan(0);
  await expect(page.getByText("No succeeded deployment yet")).toHaveCount(0);
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
