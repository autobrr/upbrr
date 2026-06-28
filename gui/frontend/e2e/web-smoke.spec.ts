// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { expect, test } from "@playwright/test";
import { createE2EWorkspace, startApp, type AppServer } from "./helpers/e2eHarness";

test("embedded web boots with dev auth, navigates core pages, and reports invalid paths", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await page.goto(app.url);
    await expect(page.getByRole("heading", { name: "Build Release Name" })).toBeVisible();

    await page.getByRole("button", { name: "Settings" }).click();
    await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();
    await page.getByRole("button", { name: "Reload" }).click();
    await expect(page.getByText("Configuration")).toBeVisible();

    await page.getByRole("button", { name: "Logging" }).click();
    await expect(page.getByRole("heading", { name: "Logging" })).toBeVisible();

    await page.getByRole("button", { name: "History" }).click();
    await expect(page.getByRole("heading", { name: "History" })).toBeVisible();

    await page.getByRole("button", { name: "Input" }).click();
    await page.getByLabel("Source path").fill("Z:\\missing\\e2e.mkv");
    await page.getByRole("button", { name: "Fetch metadata" }).click();
    await expect(page.locator(".error")).toContainText(/path|file|stat|missing/i);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});
