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
    const error = page.locator(".error");
    await expect(error).toContainText("The source path is unavailable.");
    await expect(error).toContainText("Recovery: edit input.");
    await expect(error).not.toContainText("Z:\\missing\\e2e.mkv");
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded tracker settings use the catalog for entries, reset, and unsupported config", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await page.goto(app.url);
    await page.getByRole("button", { name: "Settings" }).click();
    await page.getByRole("button", { name: "Trackers", exact: true }).click();

    await expect(
      page.getByText("BTN", { exact: true }).filter({ visible: true }).first(),
    ).toBeVisible();
    await expect(page.getByText("Unsupported tracker entries")).toBeVisible();
    await expect(page.getByText("OLD", { exact: true })).toBeVisible();

    const entryControls = page.locator(".settings-map__header .settings-map__controls");
    const trackerSelector = entryControls.locator("select");
    await expect(trackerSelector.locator('option[value="OLD"]')).toHaveCount(0);
    await trackerSelector.selectOption("BLU");
    await entryControls.getByRole("button", { name: "Add entry" }).click();

    let bluCard = page
      .locator("details.settings-card")
      .filter({ has: page.locator("summary", { hasText: "BLU" }) });
    await expect(bluCard).toBeVisible();
    await expect(
      bluCard.locator("label.settings-field > span, .settings-switch-row > span"),
    ).toHaveText(["API key", "Anonymous", "Image host"]);
    await bluCard.getByLabel("API key").fill("e2e-blu-activation");

    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByText("Settings saved and applied.")).toBeVisible();
    await page.getByRole("button", { name: "Reload", exact: true }).click();

    bluCard = page
      .locator("details.settings-card")
      .filter({ has: page.locator("summary", { hasText: "BLU" }) });
    await expect(bluCard).toBeVisible();
    await bluCard.getByRole("button", { name: "Remove" }).click();
    await expect(bluCard).toHaveCount(0);
    await expect(trackerSelector.locator('option[value="BLU"]')).toHaveCount(1);

    await page.getByRole("button", { name: "Save", exact: true }).click();
    await page.getByRole("button", { name: "Reload", exact: true }).click();
    await expect(
      page.locator("details.settings-card").filter({
        has: page.locator("summary", { hasText: "BLU" }),
      }),
    ).toHaveCount(0);

    const unsupportedCard = page
      .locator(".settings-card")
      .filter({ has: page.locator(".settings-card__summary-name", { hasText: "OLD" }) });
    await unsupportedCard.getByRole("button", { name: "Delete" }).click();
    await expect(page.getByText("OLD", { exact: true })).toHaveCount(0);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});
