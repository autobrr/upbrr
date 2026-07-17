// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { expect, test } from "@playwright/test";
import {
  createBluraySourceFixture,
  createE2EWorkspace,
  fetchMetadata,
  startApp,
  type AppServer,
} from "./helpers/e2eHarness";

test("embedded web runs image upload, tracker dry run, tracker upload, and history", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await expect.poll(() => workspace.fake.counters.clientSearches).toBe(1);
    await page.getByRole("button", { name: "Dupe Check" }).click();
    await expect(
      page.getByText("Select at least one tracker to run duplicate checking."),
    ).toBeVisible();
    await page.getByRole("checkbox", { name: "BTN" }).check();
    await page.getByRole("button", { name: "Run dupe check" }).click();
    await expect(page.getByText("BTN").first()).toBeVisible();
    await expect(page.getByRole("button", { name: "Run dupe check" })).toBeEnabled();
    await expect.poll(() => workspace.fake.counters.clientSearches).toBe(2);

    await page.getByRole("button", { name: "Screenshots" }).click();
    const saveFinalResponse = page.waitForResponse((response) =>
      response.url().includes("/api/app/SaveFinalScreenshotSelections"),
    );
    await page.getByRole("button", { name: "Generate screenshots" }).click();
    await expect((await saveFinalResponse).ok()).toBe(true);
    await expect(page.getByRole("button", { name: "Screenshot 1" })).toBeVisible();
    await page.getByRole("button", { name: "Upload Images" }).click();
    await expect(page.getByText("1 found")).toBeVisible();
    await page.getByRole("combobox", { name: "Image host" }).selectOption("imgbb");
    await page.getByRole("button", { name: "Upload selected (1)" }).click();
    await expect.poll(() => workspace.fake.counters.imageUploads).toBe(1);
    await expect(page.getByRole("link", { name: /\/image\/1$/ }).first()).toHaveAttribute(
      "href",
      /\/image\/1$/,
    );

    await page.getByRole("button", { name: "Descriptions" }).click();
    await page.getByRole("button", { name: "Refresh descriptions" }).click();
    await page.getByRole("button", { name: "Expand" }).click();
    await expect(page.locator("textarea").first()).toHaveValue(/E2E description fixture\./);

    await page.getByRole("button", { name: "Upload", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Review & Upload" })).toBeVisible();
    await page.getByLabel("Log level").selectOption("debug");
    await page.getByRole("button", { name: "Run dry run" }).click();
    await expect.poll(() => workspace.fake.counters.clientSearches).toBe(2);
    const reviewButton = page.getByRole("button", { name: "Review upload" });
    await expect(reviewButton).toBeEnabled();
    await expect(page.getByRole("heading", { name: "BTN" })).toBeVisible();
    await expect(page.getByText("E2E.Movie.2026.1080p.WEB-DL").first()).toBeVisible();

    await reviewButton.click();
    await expect.poll(() => workspace.fake.counters.clientSearches).toBe(3);
    const startButton = page.getByRole("button", { name: "Start upload" });
    await expect(startButton).toBeEnabled();
    await startButton.click();
    await expect.poll(() => workspace.fake.counters.trackerUploads).toBe(1);
    await expect(page.getByText(/Uploaded 1/)).toBeVisible();

    await page.getByRole("button", { name: "History" }).click();
    await expect(page.getByText("E2E.Movie.2026.1080p.WEB-DL").first()).toBeVisible();
    await expect(page.getByText("BTN").first()).toBeVisible();
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded web tracks BDMV playlist preparation and opens duplicate checking", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    const sourcePath = await createBluraySourceFixture(workspace);
    app = await startApp(workspace);
    await page.goto(app.url);
    await page.getByLabel("Source path").fill(sourcePath);
    await page.getByRole("button", { name: "Fetch metadata" }).click();

    await expect(page.getByRole("heading", { name: "Select BDMV Playlists" })).toBeVisible();
    await expect(page.getByText("Discover Blu-ray playlists")).toBeVisible();
    await page.getByRole("checkbox", { name: "00001.mpls" }).check();
    await page.getByRole("button", { name: "Confirm Selection" }).click();

    await expect(page.getByText("E2E.Movie.2026.1080p.WEB-DL")).toBeVisible();
    await expect(page.getByText("Blu-ray analysis complete.")).toHaveCount(0);
    await page.getByRole("button", { name: "Dupe Check" }).click();
    await page.getByRole("checkbox", { name: "BTN" }).check();
    await page.getByRole("button", { name: "Run dupe check" }).click();
    await expect.poll(() => workspace.fake.counters.clientSearches).toBe(2);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});
