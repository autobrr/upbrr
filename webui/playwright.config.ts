// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  workers: process.env.CI ? 2 : 8,
  expect: {
    timeout: 10_000,
  },
  fullyParallel: true,
  // Playwright can fetch the PR base for diff metadata in a shallow CI checkout.
  captureGitInfo: { diff: false },
  reporter: [["list"], ["html", { open: "never", outputFolder: "playwright-report" }]],
  use: {
    baseURL: "http://localhost:7480",
    launchOptions: {
      // Keep Chromium's background DNS local. Playwright can still fulfill mocked URLs.
      args: [
        "--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE localhost, EXCLUDE 127.0.0.1, EXCLUDE [::1]",
      ],
    },
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },
  projects: [
    // Start the lease-expiry scenario early so its wait can overlap browser work.
    {
      name: "api-full-upload",
      testMatch: /api-full-upload\.spec\.ts/,
    },
    {
      name: "web-smoke",
      testMatch: /web-smoke\.spec\.ts/,
      use: { ...devices["Desktop Chrome"] },
    },
    {
      name: "web-base-path-smoke",
      testMatch: /web-base-path-smoke\.spec\.ts/,
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
  ],
});
