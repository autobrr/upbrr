import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  testMatch: /visual-quality-touch\.spec\.ts/,
  timeout: 120_000,
  workers: 1,
  use: { ...devices["Desktop Chrome"] },
  reporter: "list",
});
