import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  testMatch: /visual-quality-capture\.spec\.ts/,
  timeout: 180_000,
  workers: 1,
  use: { ...devices["Desktop Chrome"] },
  reporter: "list",
});
