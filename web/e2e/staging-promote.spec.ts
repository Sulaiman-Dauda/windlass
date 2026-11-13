import { test, expect, type Page } from "@playwright/test";
import { panelData } from "./staging-fixture";

const h = (c: string) => c.repeat(40);

async function panel(page: Page, options: { role?: string; behind?: boolean } = {}) {
  const role = options.role ?? "member";
  const at = new Date(Date.now() - 3 * 3600_000).toISOString();
  const staged = { number: 7, status: "succeeded", commit: h("d") };
  const commits = ["Fix the basket total", "Add the VAT line to invoices", "Tidy the header", "Bump images",
    "Retry webhooks", "Log slow queries", "Rename a column"].map((subject, i) => ({
    hash: h("0123456"[i]), subject, author: "Ada Lovelace", time: at }));
  let deployed = false;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace("/api/v1", "");
    if (route.request().method() === "POST" && path === "/projects/app-staging/deployments") {
      deployed = true;
      return route.fulfill({ status: 201, json: { id: 9, number: 8, status: "queued", triggered_by: "manual", created_at: at } });
    }
    const data = panelData(path, role)
      ?? (path.endsWith("/staging") ? { live: "app", staging: "app-staging", branch: "preview", live_branch: "main",
        auto_promote: false, staging_latest: staged, promotable: staged,
        live_current: { number: 4, status: "succeeded", commit: h("a") }, live_latest: { number: 4, status: "succeeded", commit: h("a") },
        can_recreate: false, rewrites: [], drift: [], commits, commits_more: false,
        branch_head: options.behind ? h("e") : h("d"), env_missing: ["SMTP_HOST", "STRIPE_WEBHOOK_SECRET"] }
      : path.endsWith("/deployments") ? [{ id: 7, ...staged, triggered_by: "webhook", created_at: at }]
      : {});
    return route.fulfill({ json: data });
  });
  return { deployed: () => deployed };
}

test("the card lists the commits Promote brings and the keys live lacks", async ({ page }) => {
  await panel(page);
  await page.goto("/projects/app");
  await expect(page.getByText("7 commits to promote")).toHaveCount(1);
  await expect(page.getByText("Fix the basket total")).toBeVisible();
  // Five on the card until asked for the rest.
  await expect(page.getByText("Rename a column")).toHaveCount(0);
  await page.getByRole("button", { name: "Show all 7" }).click();
  await expect(page.getByText("Rename a column")).toBeVisible();
  await expect(page.getByText("SMTP_HOST, STRIPE_WEBHOOK_SECRET")).toBeVisible();
  await expect(page.getByText(/Staging is behind/)).toHaveCount(0);
  // The reason is on the card, so the button carries no tooltip.
  await expect(page.getByRole("button", { name: "Promote #7" })).not.toHaveAttribute("title");

  await page.getByRole("button", { name: "Promote #7" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("7 commits to promote")).toBeVisible();
  await expect(dialog.getByText("Rename a column")).toBeAttached();
  await expect(dialog.getByText("SMTP_HOST, STRIPE_WEBHOOK_SECRET")).toBeVisible();
});

test("a staging behind its branch offers a deploy", async ({ page }) => {
  const fixture = await panel(page, { behind: true });
  await page.goto("/projects/app-staging");
  await expect(page.getByText(/Staging is behind/)).toBeVisible();
  await page.getByRole("button", { name: "Deploy staging", exact: true }).click();
  await expect.poll(fixture.deployed).toBe(true);
});

test("a viewer sees the notice without the button", async ({ page }) => {
  await panel(page, { role: "viewer", behind: true });
  await page.goto("/projects/app");
  await expect(page.getByText(/Staging is behind/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Deploy staging", exact: true })).toHaveCount(0);
});
