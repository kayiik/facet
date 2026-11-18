import { defineConfig } from "playwright/test";

export default defineConfig({
  testDir: "./tests/app",
  timeout: 90_000,
  expect: { timeout: 15_000 },
  forbidOnly: !!process.env.CI,
  retries: 0,
  outputDir: "test-results/app",
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    browserName: "chromium",
    actionTimeout: 15_000,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
});
