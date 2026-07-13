import { defineConfig } from "@playwright/test";

// The CI job starts the real windlass binary and passes its URL + the
// one-time setup token via environment variables.
export default defineConfig({
  testDir: "./e2e",
  timeout: 180_000,
  retries: 1,
  use: {
    baseURL: process.env.WINDLASS_URL ?? "http://localhost:8080",
    trace: "retain-on-failure",
  },
});
