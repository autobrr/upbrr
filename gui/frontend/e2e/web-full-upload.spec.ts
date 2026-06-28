// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { expect, test } from "@playwright/test";
import { createE2EWorkspace, fetchMetadata, startApp } from "./helpers/e2eHarness";

test("embedded web runs image upload, tracker dry run, tracker upload, and history", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace();
  const app = await startApp(workspace);
  try {
    await fetchMetadata(page, app.url, workspace.sourcePath);

    await page.getByRole("button", { name: "Dupe Checking" }).click();
    await page.getByRole("button", { name: "Run dupe check" }).click();
    await expect(page.getByText("BTN").first()).toBeVisible();
    await expect(page.getByRole("button", { name: "Run dupe check" })).toBeEnabled();

    await page.getByRole("button", { name: "Screenshots" }).click();
    await page.getByRole("button", { name: "Generate screenshots" }).click();
    await expect(
      page
        .getByText(
          /Generated screenshots ready for upload\.|All requested screenshots already exist\./,
        )
        .first(),
    ).toBeVisible();
    await page.getByRole("button", { name: "Upload Images" }).click();
    await expect(page.getByText("Available: 1")).toBeVisible();
    await page.getByRole("button", { name: "Upload 1" }).click();
    await expect.poll(() => workspace.fake.counters.imageUploads).toBe(1);
    await expect(page.getByRole("link", { name: "Web URL" }).first()).toHaveAttribute(
      "href",
      /\/image\/1$/,
    );

    await page.getByRole("button", { name: "Description Builder" }).click();
    await page.getByRole("button", { name: "Refresh descriptions" }).click();
    await page.getByRole("button", { name: "Expand" }).click();
    await expect(page.locator("textarea").first()).toHaveValue(/E2E description fixture\./);

    await page.getByRole("button", { name: "Tracker Upload" }).click();
    await expect(page.getByRole("heading", { name: "Upload Targets" })).toBeVisible();
    await page.getByRole("button", { name: "Run Dry Run" }).click();
    await page.getByText("Dry run data").click();
    const dryRunDetails = page.locator("details[open]").filter({ hasText: "Dry run data" }).first();
    await expect(dryRunDetails.getByText("E2E.Movie.2026.1080p.WEB-DL").first()).toBeVisible();
    await expect(dryRunDetails.getByText("/upload").first()).toBeVisible();

    await page.getByRole("button", { name: "Start Upload" }).click();
    await expect.poll(() => workspace.fake.counters.trackerUploads).toBe(1);
    await expect(page.getByText(/Uploaded:\s*1/)).toBeVisible();

    await page.getByRole("button", { name: "History" }).click();
    await expect(page.getByText("E2E.Movie.2026.1080p.WEB-DL").first()).toBeVisible();
    await expect(page.getByText("BTN").first()).toBeVisible();
  } finally {
    await app.stop();
    await workspace.cleanup();
  }
});
