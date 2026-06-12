// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  workers: 1,
  expect: {
    timeout: 10_000,
  },
  fullyParallel: false,
  reporter: [["list"], ["html", { open: "never", outputFolder: "playwright-report" }]],
  use: {
    baseURL: "http://localhost:7480",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },
  projects: [
    {
      name: "web-smoke",
      testMatch: /web-smoke\.spec\.ts/,
      use: { ...devices["Desktop Chrome"] },
    },
    {
      name: "web-full-upload",
      testMatch: /web-full-upload\.spec\.ts/,
      use: { ...devices["Desktop Chrome"] },
    },
    {
      name: "cli-full-upload",
      testMatch: /cli-full-upload\.spec\.ts/,
    },
    {
      name: "wails-basic",
      testMatch: /wails-basic\.spec\.ts/,
    },
  ],
});
