import { test, expect } from "@playwright/test";

// The one critical-path E2E spec (docs/plan.md): claim the instance, create
// a project, deploy it for real, watch the live log, check services.
// Everything else is covered by Go HTTP tests; this exists because SSE,
// forms, and routing are where API tests can't see.
test("first-run → project → real deployment", async ({ page }) => {
  const token = process.env.WINDLASS_SETUP_TOKEN;
  test.skip(!token, "WINDLASS_SETUP_TOKEN not set");

  // First-run setup.
  await page.goto("/");
  await expect(page.getByText("Welcome to Windlass")).toBeVisible();
  await page.getByLabel("Setup token").fill(token!);
  await page.getByLabel("Email").fill("admin@example.com");
  await page.getByLabel("Password").fill("supersecret123");
  await page.getByRole("button", { name: "Create admin account" }).click();

  // Landed on the dashboard, signed in.
  await expect(page.getByText("Projects", { exact: true }).first()).toBeVisible();

  // Create a project.
  await page.goto("/projects");
  await page.getByRole("button", { name: "New project" }).click();
  await page.getByPlaceholder(/project-name/).fill("smoke");
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await expect(page.getByRole("link", { name: /smoke/ })).toBeVisible();

  // Deploy it (starter nginx compose) and watch the live log.
  await page.goto("/projects/smoke/deployments");
  await page.getByRole("button", { name: "Deploy", exact: true }).click();
  await expect(page.locator("text=#1")).toBeVisible();

  // The pipeline streams steps into the log pane and finishes.
  await expect(page.getByText("starting services")).toBeVisible({ timeout: 120_000 });
  await expect(page.getByText("succeeded").first()).toBeVisible({ timeout: 120_000 });

  // Services table shows the running container.
  await page.goto("/projects/smoke");
  await expect(page.getByText("running").first()).toBeVisible({ timeout: 30_000 });

  // Sign out returns to the login screen.
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByText("Sign in to your server")).toBeVisible();
});
