import { defineConfig, devices } from "@playwright/test";

// Browser scenarios run against the production bundle in
// runtime/internal/webui/static (`pnpm build` first). Each test starts its own
// in-process fake runtime (e2e/fake) or a real runtime (e2e/runtime).
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: [["list"], ["json", { outputFile: process.env.E2E_JSON ?? "test-results/e2e.json" }]],
  use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 }, trace: "retain-on-failure" },
  projects: [
    { name: "ui", testMatch: /(ui|runtime)\.spec\.ts$/ },
    { name: "perf", testMatch: /perf\.spec\.ts$/, timeout: 600_000 },
  ],
});
