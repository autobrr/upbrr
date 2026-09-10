// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { expect, test, type Locator, type Page } from "@playwright/test";
import {
  createE2EWorkspace,
  createMultiBluraySourceFixture,
  createMultiDVDSourceFixture,
  expectSingleCollectionTorrentUpload,
  fetchMetadata,
  releaseWorkflowParityFixture,
  startApp,
  type AppServer,
} from "./helpers/e2eHarness";

const expectEnabledState = async (locator: Locator, enabled: boolean) => {
  if (enabled) {
    await expect(locator).toBeEnabled();
    return;
  }
  await expect(locator).toBeDisabled();
};

const runDuplicateCheck = async (page: Page) => {
  const response = page.waitForResponse((candidate) =>
    candidate.url().includes("/api/app/ContinueReleaseWorkflow"),
  );
  await page.getByRole("button", { name: "Run dupe check" }).click();
  await expect((await response).ok()).toBe(true);
};

for (const scenario of [
  {
    name: "none trackers bypass shared content pages",
    trackers: [releaseWorkflowParityFixture.trackerID],
    screenshots: false,
    descriptions: false,
    upload: true,
  },
  {
    name: "screenshot trackers expose image pages without requiring descriptions",
    trackers: ["ANT"],
    screenshots: true,
    descriptions: false,
    upload: true,
  },
  {
    name: "mixed trackers expose the strictest shared content workflow",
    trackers: ["BTN", "ANT", "AITHER"],
    screenshots: true,
    descriptions: false,
    upload: false,
  },
] as const) {
  test(`embedded web ${scenario.name}`, async ({ page }) => {
    const workspace = await createE2EWorkspace();
    let app: AppServer | undefined;
    try {
      app = await startApp(workspace);
      await fetchMetadata(page, app.url, workspace.sourcePath);
      await page.getByRole("button", { name: "Dupe Check" }).click();
      const defaultTracker = page.getByRole("checkbox", {
        name: releaseWorkflowParityFixture.trackerID,
      });
      await expect(defaultTracker).toBeChecked();
      if (!scenario.trackers.includes(releaseWorkflowParityFixture.trackerID)) {
        await defaultTracker.uncheck();
      }
      for (const tracker of scenario.trackers) {
        await page.getByRole("checkbox", { name: tracker }).check();
      }
      await runDuplicateCheck(page);
      await expect(page.getByRole("button", { name: "Run dupe check" })).toBeEnabled();

      await expectEnabledState(
        page.getByRole("button", { name: "Screenshots" }),
        scenario.screenshots,
      );
      await expectEnabledState(
        page.getByRole("button", { name: "Upload Images" }),
        scenario.screenshots,
      );
      await expectEnabledState(
        page.getByRole("button", { name: "Descriptions" }),
        scenario.descriptions,
      );
      await expectEnabledState(
        page.getByRole("button", { name: "Upload", exact: true }),
        scenario.upload,
      );
      await expect.poll(() => workspace.fake.counters.clientSearches).toBe(1);
    } finally {
      await app?.stop();
      await workspace.cleanup();
    }
  });
}

test("embedded web reload restores the authoritative prepared workflow", async ({ page }) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    const restored = page.waitForResponse((response) =>
      response.url().includes("/api/app/GetReleaseWorkflow"),
    );
    await page.reload();
    await expect((await restored).ok()).toBe(true);
    await expect(page.getByText("E2E.Movie.2026.1080p.WEB-DL")).toBeVisible();
    await expect(page.getByRole("button", { name: "Dupe Check" })).toBeEnabled();
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded web distinguishes a cleared metadata provider ID from Auto", async ({ page }) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);

    await page.getByText("Edit Release Details", { exact: true }).click();
    const malRow = page.locator('[data-correction-field="identity.mal"]');
    const malInput = page.getByRole("textbox", { name: "MAL ID", exact: true });
    await expect(malInput).toHaveValue("");
    await expect(malRow.getByText("Automatic value", { exact: true })).toBeVisible();
    await malInput.fill("0");
    const cleared = page.waitForResponse((response) =>
      response.url().includes("/api/app/ContinueReleaseWorkflow"),
    );
    await page.getByRole("button", { name: "Refresh metadata" }).click();
    await expect((await cleared).ok()).toBe(true);
    await expect(page.getByRole("progressbar")).toHaveCount(0);
    await expect(malRow.getByText("Manual value", { exact: true })).toBeVisible();

    const restored = page.waitForResponse((response) =>
      response.url().includes("/api/app/GetReleaseWorkflow"),
    );
    await page.reload();
    await expect((await restored).ok()).toBe(true);
    await page.getByText("Edit Release Details", { exact: true }).click();
    await expect(malInput).toHaveValue("");
    await expect(malRow.getByText("Manual value", { exact: true })).toBeVisible();

    await page.getByRole("button", { name: "Auto MAL ID" }).click();
    const reset = page.waitForResponse((response) =>
      response.url().includes("/api/app/ContinueReleaseWorkflow"),
    );
    await page.getByRole("button", { name: "Refresh metadata" }).click();
    await expect((await reset).ok()).toBe(true);
    await expect(page.getByRole("progressbar")).toHaveCount(0);
    await expect(malRow.getByText("Automatic value", { exact: true })).toBeVisible();

    await malInput.fill("5114");
    const restoredID = page.waitForResponse((response) =>
      response.url().includes("/api/app/ContinueReleaseWorkflow"),
    );
    await page.getByRole("button", { name: "Refresh metadata" }).click();
    await expect((await restoredID).ok()).toBe(true);
    await expect(page.getByRole("progressbar")).toHaveCount(0);
    await expect(malInput).toHaveValue("5114");
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded web retains Input corrections without downstream workflow calls", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await expect.poll(() => workspace.fake.counters.clientSearches).toBe(1);
    const initialCounters = { ...workspace.fake.counters };
    const workflowRequests: Array<{ method: string; body: Record<string, unknown> }> = [];
    page.on("request", (request) => {
      const method = new URL(request.url()).pathname.split("/").pop() || "";
      if (!method.includes("ReleaseWorkflow")) return;
      let body: Record<string, unknown> = {};
      try {
        body = request.postDataJSON() as Record<string, unknown>;
      } catch {
        // GET-style workflow resource requests do not carry JSON.
      }
      workflowRequests.push({ method, body });
    });

    await page.getByText("Edit Release Details", { exact: true }).click();
    const inputEditor = page.getByTestId("input-correction-editor");
    const dupeCheck = page.getByRole("button", { name: "Dupe Check" });
    for (const provider of ["TMDB", "IMDB", "TVDB", "TVmaze", "MAL"]) {
      await expect(
        page.getByRole("button", { name: `Remove ${provider} ID`, exact: true }),
      ).toBeEnabled();
    }
    await expect(page.getByRole("textbox", { name: "TMDB ID", exact: true })).not.toHaveValue("");
    await expect(page.getByRole("textbox", { name: "Title", exact: true })).not.toHaveValue("");
    await expect(page.getByRole("textbox", { name: "Title", exact: true })).toBeDisabled();
    await expect(page.getByRole("textbox", { name: "Original title", exact: true })).toBeDisabled();
    await expect(page.getByRole("spinbutton", { name: "Manual year", exact: true })).toBeEditable();
    const categoryInput = page.getByRole("textbox", { name: "Category", exact: true });
    await categoryInput.fill("TV");
    await expect(page.getByRole("spinbutton", { name: "Manual year", exact: true })).toBeDisabled();
    await categoryInput.fill("movie");
    await expect(page.getByRole("spinbutton", { name: "Manual year", exact: true })).toBeEditable();
    await page.getByRole("button", { name: "Auto Category", exact: true }).click();
    await expect(page.getByRole("textbox", { name: "Category", exact: true })).not.toHaveValue("");
    for (const group of [
      "Provider IDs",
      "Release name",
      "Metadata and languages",
      "Inspected tracks",
      "Source options",
      "Input readiness",
    ]) {
      await expect(inputEditor.getByText(group, { exact: true })).toBeVisible();
    }
    const skipClientSearch = page.getByRole("checkbox", { name: "Skip client search" });
    const sourceOptions = page.getByTestId("input-source-options");
    await expect(sourceOptions).not.toHaveAttribute("open");
    await expect(skipClientSearch).toBeHidden();
    await sourceOptions.locator("summary").click();
    await expect(skipClientSearch).toBeVisible();
    const readiness = page.getByTestId("input-readiness");
    await expect(readiness).not.toHaveAttribute("open");
    await expect(readiness.getByText(/^Status:/)).toBeHidden();
    await readiness.locator("summary").click();
    await expect(readiness.getByText(/^Status:/)).toBeVisible();
    await readiness.locator("summary").click();
    const titleInput = page.getByRole("textbox", { name: "Title", exact: true });
    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 900 });
      const field = await titleInput.boundingBox();
      expect(field).not.toBeNull();
      expect(field?.width).toBeGreaterThan(240);
      expect((field?.x || 0) + (field?.width || 0)).toBeLessThanOrEqual(width);
    }
    await page.setViewportSize({ width: 1280, height: 900 });
    await skipClientSearch.check();
    const commentary = page.getByRole("combobox", { name: "Commentary" });
    await commentary.selectOption({ label: "No" });
    const saved = page.waitForResponse((response) =>
      response.url().includes("/api/app/ContinueReleaseWorkflow"),
    );
    await page.getByRole("button", { name: "Refresh metadata" }).click();
    await expect((await saved).ok()).toBe(true);
    await expect(page.getByRole("button", { name: "Refresh metadata", exact: true })).toBeEnabled();
    await expect(dupeCheck).toBeEnabled();
    await expect(commentary).toHaveValue("no");
    const setCommand = workflowRequests.findLast(
      (request) =>
        request.method === "ContinueReleaseWorkflow" &&
        (request.body.intent as { correctionPatch?: unknown } | undefined)?.correctionPatch,
    );
    expect(setCommand?.body.goal).toBe("input_ready");
    expect(
      (setCommand?.body.intent as { correctionPatch?: { expectedRevision?: number } })
        ?.correctionPatch?.expectedRevision ?? -1,
    ).toBeGreaterThanOrEqual(0);
    expect(workspace.fake.counters).toEqual(initialCounters);

    const restored = page.waitForResponse((response) =>
      response.url().includes("/api/app/GetReleaseWorkflow"),
    );
    await page.reload();
    await expect((await restored).ok()).toBe(true);
    await page.getByText("Edit Release Details", { exact: true }).click();
    await expect(commentary).toHaveValue("no");
    await page.getByLabel("Source path", { exact: true }).click();
    await expect(page.getByRole("listbox", { name: "Source path history" })).toBeVisible();
    await page.keyboard.press("Escape");

    await commentary.selectOption({ label: "Auto" });
    await sourceOptions.locator("summary").click();
    await skipClientSearch.check();
    const reset = page.waitForResponse((response) =>
      response.url().includes("/api/app/ContinueReleaseWorkflow"),
    );
    await page.getByRole("button", { name: "Refresh metadata" }).click();
    await expect((await reset).ok()).toBe(true);
    await expect(page.getByRole("button", { name: "Refresh metadata", exact: true })).toBeEnabled();
    await expect(dupeCheck).toBeEnabled();
    const resetCommand = workflowRequests.findLast(
      (request) =>
        request.method === "ContinueReleaseWorkflow" &&
        Array.isArray(
          (request.body.intent as { correctionPatch?: { resetFields?: unknown } } | undefined)
            ?.correctionPatch?.resetFields,
        ),
    );
    expect(
      (
        resetCommand?.body.intent as {
          correctionPatch?: { resetFields?: Array<{ field: string }> };
        }
      )?.correctionPatch?.resetFields,
    ).toContainEqual({ field: "metadata.commentary" });

    await commentary.selectOption({ label: "No" });
    await skipClientSearch.check();
    const retained = page.waitForResponse((response) =>
      response.url().includes("/api/app/ContinueReleaseWorkflow"),
    );
    await page.getByRole("button", { name: "Refresh metadata" }).click();
    await expect((await retained).ok()).toBe(true);
    await expect(page.getByRole("button", { name: "Refresh metadata", exact: true })).toBeEnabled();
    await expect(dupeCheck).toBeEnabled();
    const lastContinue = workflowRequests.findLast(
      (request) => request.method === "ContinueReleaseWorkflow",
    );
    const workflowID = (lastContinue?.body.authority as { workflowId?: unknown } | undefined)
      ?.workflowId;
    if (typeof workflowID !== "string" || workflowID === "") {
      throw new Error("workflow ID missing from Input correction request");
    }

    for (const request of workflowRequests) {
      if (request.method === "ContinueReleaseWorkflow")
        expect(request.body.goal).toBe("input_ready");
      expect([
        "ContinueReleaseWorkflow",
        "GetReleaseWorkflow",
        "GetReleaseWorkflowOperation",
      ]).toContain(request.method);
    }
    expect(workspace.fake.counters).toEqual(initialCounters);

    await app.stop();
    app = await startApp(workspace, { seed: false });
    await page.goto(app.url);
    await page.evaluate(
      (id) => sessionStorage.setItem("upbrr.activeReleaseWorkflow", id),
      workflowID,
    );
    await page.reload();
    await page.getByText("Edit Release Details", { exact: true }).click();
    await expect(page.getByRole("combobox", { name: "Commentary" })).toHaveValue("no");
    expect(workspace.fake.counters).toEqual(initialCounters);

    const removeTMDB = page.getByRole("button", { name: "Remove TMDB ID", exact: true });
    await expect(removeTMDB).toBeEnabled();
    await removeTMDB.click();
    await sourceOptions.locator("summary").click();
    await skipClientSearch.check();
    const removed = page.waitForResponse(
      (response) =>
        response.url().includes("/api/app/ContinueReleaseWorkflow") &&
        Boolean(response.request().postDataJSON()?.intent?.preparation),
    );
    await page.getByRole("button", { name: "Refresh metadata" }).click();
    await expect((await removed).ok()).toBe(true);
    await expect(page.getByRole("button", { name: "Refresh metadata", exact: true })).toBeEnabled();
    await expect(dupeCheck).toBeEnabled();
    await page.reload();
    await page.getByText("Edit Release Details", { exact: true }).click();
    await expect(removeTMDB).toBeDisabled();
    await expect(removeTMDB).toHaveText("Removed");
    await expect(page.getByRole("textbox", { name: "TMDB ID", exact: true })).toHaveValue("");
    await expect(page.getByRole("button", { name: /^TMDB \d+ Source:/ })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Remove IMDB ID", exact: true })).toBeEnabled();
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded web selects a Blu-ray candidate through the authoritative workflow", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace();
  workspace.env.UPBRR_E2E_BLURAY_CANDIDATES = "1";
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByRole("button", { name: "Blu-ray Candidates" }).click();
    await expect(page.getByText("Example Release 2026 Collector Edition")).toBeVisible();
    await expect(page.getByRole("button", { name: "Selected" })).toBeDisabled();

    const response = page.waitForResponse((candidate) =>
      candidate.url().includes("/api/app/ContinueReleaseWorkflow"),
    );
    await page.getByRole("button", { name: "Select", exact: true }).click();
    await expect((await response).ok()).toBe(true);
    await expect(page.getByText("Example Release 2026 Standard Edition")).toBeVisible();
    await expect(page.getByRole("button", { name: "Selected" })).toBeDisabled();
    await expect(page.getByText("B").first()).toBeVisible();
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded web runs image upload, direct tracker upload, and history", async ({ page }) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await expect.poll(() => workspace.fake.counters.clientSearches).toBe(1);
    await page.getByRole("button", { name: "Dupe Check" }).click();
    await expect(
      page.getByRole("checkbox", { name: releaseWorkflowParityFixture.trackerID }),
    ).toBeChecked();
    await page.getByRole("checkbox", { name: releaseWorkflowParityFixture.trackerID }).uncheck();
    await expect(
      page.getByText("Select at least one tracker to run duplicate checking."),
    ).toBeVisible();
    await page.getByRole("checkbox", { name: "HDS" }).check();
    await runDuplicateCheck(page);
    await expect(page.getByText("HDS").first()).toBeVisible();
    await expect(page.getByRole("button", { name: "Run dupe check" })).toBeEnabled();
    await expect(page.getByRole("button", { name: "Screenshots" })).toBeEnabled();
    await expect(page.getByRole("progressbar")).toHaveCount(0);
    await expect.poll(() => workspace.fake.counters.clientSearches).toBe(1);
    await page.reload();
    await page.getByRole("button", { name: "Dupe Check" }).click();
    await expect(page.getByText("HDS").first()).toBeVisible();
    await expect(page.getByRole("button", { name: "Run dupe check" })).toBeEnabled();

    const mediaPlanResponse = page.waitForResponse((response) =>
      response.url().includes("/api/app/GetReleaseWorkflowMediaPlan"),
    );
    await page.getByRole("button", { name: "Screenshots" }).click();
    const planned = await mediaPlanResponse;
    expect(planned.ok()).toBe(true);
    const captureResponse = page.waitForResponse((response) =>
      response.url().includes("/api/app/ContinueReleaseWorkflow"),
    );
    await page.getByRole("button", { name: "Generate screenshots" }).click();
    await expect((await captureResponse).ok()).toBe(true);
    await expect(page.getByText("1 captured screenshot(s)")).toBeVisible();
    await expect(page.getByAltText("Screenshot 1")).toBeVisible();
    const frameSelection = page.getByText(/^Frame Selection · 1 frame$/);
    await expect(frameSelection).toBeVisible();
    await expect(frameSelection.locator("..")).not.toHaveAttribute("open", "");

    const generated = page
      .getByRole("heading", { name: "Generated Screenshots" })
      .locator("..")
      .locator("..");
    await generated.getByRole("button", { name: "Delete", exact: true }).click();
    await expect(page.getByAltText("Screenshot 1")).toHaveCount(0);
    await page.getByRole("button", { name: "Generate screenshots" }).click();
    await expect(page.getByAltText("Screenshot 1")).toBeVisible();
    await page.reload();
    await expect(page.getByRole("button", { name: "Screenshots" })).toBeEnabled();
    await page.getByRole("button", { name: "Screenshots" }).click();
    await expect(page.getByAltText("Screenshot 1")).toBeVisible();
    page.once("dialog", (dialog) => dialog.accept());
    await page
      .getByRole("heading", { name: "Generated Screenshots" })
      .locator("..")
      .getByRole("button", { name: "Delete all" })
      .click();
    await expect(page.getByAltText("Screenshot 1")).toHaveCount(0);
    await page.getByRole("button", { name: "Generate screenshots" }).click();
    await expect(page.getByAltText("Screenshot 1")).toBeVisible();

    await page.getByRole("button", { name: "Descriptions" }).click();
    await page.getByRole("button", { name: "Refresh descriptions" }).click();
    await page.getByRole("button", { name: "Expand" }).click();
    await expect(page.getByRole("textbox")).toHaveValue("E2E description fixture.");
    await expect(page.getByText("E2E description fixture.").first()).toBeVisible();
    await page.reload();
    await page.getByRole("button", { name: "Descriptions" }).click();
    await page.getByRole("button", { name: "Expand" }).click();
    await expect(page.getByRole("textbox")).toHaveValue("E2E description fixture.");

    await page.getByRole("button", { name: "Upload", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Review & Upload" })).toBeVisible();
    await page.reload();
    await page.getByRole("button", { name: "Upload", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Review & Upload" })).toBeVisible();
    await page.getByLabel("Log level").selectOption("debug");
    expect(workspace.fake.counters.clientInjections).toBe(0);
    expect(workspace.fake.counters.trackerUploads).toBe(0);

    const startButton = page.getByRole("button", { name: "Start upload" });
    await expect(startButton).toBeEnabled();
    await startButton.click();
    await expect.poll(() => workspace.fake.counters.trackerUploads).toBe(1);
    await expect.poll(() => workspace.fake.counters.clientInjections).toBe(1);
    await expect(page.getByRole("heading", { name: "Workflow upload result" })).toBeVisible();

    await page.getByRole("button", { name: "History" }).click();
    await expect(
      page.getByText(releaseWorkflowParityFixture.releaseDisplayName).first(),
    ).toBeVisible();
    await expect(page.getByText("HDS").first()).toBeVisible();
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded web reports an optional tracker dry run after a duplicate override", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace();
  workspace.env.UPBRR_E2E_DUPLICATE_TRACKERS = "HDS";
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByRole("button", { name: "Dupe Check" }).click();
    await page.getByRole("checkbox", { name: releaseWorkflowParityFixture.trackerID }).uncheck();
    await page.getByRole("checkbox", { name: "HDS" }).check();
    await runDuplicateCheck(page);
    await expect(page.getByText("Example.Release.2026.1080p-GRP")).toBeVisible();
    await expect(page.getByRole("button", { name: "Screenshots" })).toBeEnabled();
    await page.getByLabel("Ignore dupes for HDS").click();
    await expect(page.getByText("1 potential dupe · acknowledged")).toBeVisible();

    await page.getByRole("button", { name: "Screenshots" }).click();
    await page.getByRole("button", { name: "Generate screenshots" }).click();
    await expect(page.getByText("1 captured screenshot(s)")).toBeVisible();
    await page.getByRole("button", { name: "Descriptions" }).click();
    await page.getByRole("button", { name: "Refresh descriptions" }).click();
    await page.getByRole("button", { name: "Expand" }).click();
    await expect(page.getByRole("textbox")).toHaveValue("E2E description fixture.");

    await page.getByRole("button", { name: "Upload", exact: true }).click();
    const dryRunButton = page.getByRole("button", { name: "Run dry run" });
    expect(workspace.fake.counters.clientInjections).toBe(0);
    await dryRunButton.click();
    await expect(page.getByRole("heading", { name: "Tracker uploads" })).toBeVisible();
    await expect(page.getByText("HDS").first()).toBeVisible();
    await page.getByRole("button", { name: "Expand HDS" }).click();
    await expect(
      page.getByText(/Client injection: skipped · Client injection deferred/),
    ).toBeVisible();
    expect(workspace.fake.counters.clientInjections).toBe(0);
    expect(workspace.fake.counters.trackerUploads).toBe(0);

    await page.getByLabel("Skip client injection").check();
    await dryRunButton.click();
    await expect(
      page.getByText(/Client injection: skipped · Client injection disabled/),
    ).toBeVisible();
    expect(workspace.fake.counters.clientInjections).toBe(0);
    expect(workspace.fake.counters.trackerUploads).toBe(0);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded web renders mixed, incomplete, and manual duplicate evidence", async ({ page }) => {
  const workspace = await createE2EWorkspace();
  workspace.env.UPBRR_E2E_DUPE_SCENARIOS = "HDS=mixed_incomplete,PTP=manual";
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByRole("button", { name: "Dupe Check" }).click();
    await page.getByRole("checkbox", { name: releaseWorkflowParityFixture.trackerID }).uncheck();
    for (const tracker of ["HDS", "PTP"]) {
      await page.getByRole("checkbox", { name: tracker }).check();
    }
    await runDuplicateCheck(page);

    await expect(page.getByText("Example.Release.2026.1080p.SDR-GRP")).toHaveCount(0);
    await expect(page.getByText("Example.Release.2026.1080p.HDR10-GRP")).toBeVisible();
    await expect(page.getByText("Example.Release.2026.1080p.Unknown-GRP")).toBeVisible();
    await expect(page.getByText("Example.Show.S01E01.1080p.WEB-DL.DV-GRP")).toBeVisible();
    await expect(page.getByText("coexists")).toHaveCount(0);
    await expect(page.getByText("proposed trumps")).toBeVisible();
    await expect(page.getByText("insufficient evidence")).toBeVisible();
    await expect(page.getByText("manual review", { exact: true })).toBeVisible();
    await expect(
      page.getByText("Synthetic search stopped at the configured page bound."),
    ).toBeVisible();

    for (const tracker of ["HDS", "PTP"]) {
      await expect(page.getByLabel(`Acknowledge dupe risk for ${tracker}`)).toBeVisible();
    }
    await expect(
      page.getByRole("link", { name: "Example.Release.2026.1080p.HDR10-GRP" }),
    ).toHaveAttribute("href", /^https:\/\/tracker\.invalid\/torrents\.php\?id=e2e-trump-1$/);
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
    const sourcePath = await createMultiBluraySourceFixture(workspace);
    app = await startApp(workspace);
    await page.goto(app.url);
    await page.getByLabel("Source path").fill(sourcePath);
    const prepareResponse = page.waitForResponse((response) =>
      response.url().includes("/api/app/ContinueReleaseWorkflow"),
    );
    await page.getByRole("button", { name: "Fetch metadata" }).click();
    const prepared = await prepareResponse;
    if (!prepared.ok()) console.log(app.output());
    expect(prepared.ok()).toBe(true);

    await expect(page.getByRole("heading", { name: "Select BDMV Playlists" })).toBeVisible();
    await expect(
      page.getByText("Choose playlists for the selected preparation source."),
    ).toBeVisible();
    await expect(page.getByRole("heading", { name: "Disc 1" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Disc 2" })).toBeVisible();
    const duplicatePlaylists = page.getByRole("checkbox", { name: "00001.mpls" });
    await expect(duplicatePlaylists).toHaveCount(2);
    await duplicatePlaylists.nth(0).check();
    await expect(page.getByRole("button", { name: "Confirm Selection" })).toBeDisabled();
    await duplicatePlaylists.nth(1).check();
    await page.getByRole("button", { name: "Confirm Selection" }).click();

    await expect(page.getByText("E2E.Movie.2026.1080p.WEB-DL")).toBeVisible();
    await expect(page.getByText("Blu-ray analysis complete.")).toHaveCount(0);
    await page.getByRole("button", { name: "Dupe Check" }).click();
    await page.getByRole("checkbox", { name: releaseWorkflowParityFixture.trackerID }).uncheck();
    await page.getByRole("checkbox", { name: "AITHER" }).check();
    await runDuplicateCheck(page);
    await expect.poll(() => workspace.fake.counters.clientSearches).toBe(2);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded DVD media keeps normal screenshots and optional menus independent", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace({ screenshotCount: 4 });
  let app: AppServer | undefined;
  try {
    const sourcePath = await createMultiDVDSourceFixture(workspace);
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, sourcePath);
    await expect(page.getByText(/Prepared source: 2 DVD discs/i)).toBeVisible();
    await page.getByRole("button", { name: "Dupe Check" }).click();
    await page.getByRole("checkbox", { name: releaseWorkflowParityFixture.trackerID }).uncheck();
    await page.getByRole("checkbox", { name: "HDS" }).check();
    await runDuplicateCheck(page);
    await expect(page.getByText("HDS").first()).toBeVisible();

    await page.getByRole("button", { name: "Screenshots" }).click();
    await expect(page.getByText(/^Frame Selection · 4 frames$/)).toBeVisible();
    const screenshotCaptureResponse = page.waitForResponse((response) =>
      response.url().includes("/api/app/ContinueReleaseWorkflow"),
    );
    await page.getByRole("button", { name: "Generate screenshots" }).click();
    await expect((await screenshotCaptureResponse).ok()).toBe(true);
    await expect(page.getByText("4 captured screenshot(s)")).toBeVisible();
    await expect(page.getByAltText(/^Disc [12] screenshot \d$/)).toHaveCount(4);
    await expect(page.getByRole("heading", { name: "Disc 1" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Disc 2" })).toBeVisible();
    await expect(page.getByText("Action required")).toHaveCount(0);

    await page.getByRole("button", { name: "Upload Images" }).click();
    await expect(page.getByRole("button", { name: "Prepare required hosts (4)" })).toBeEnabled();
    await page.getByRole("button", { name: "Prepare required hosts (4)" }).click();
    await expect.poll(() => workspace.fake.counters.imageUploads).toBe(4);
    await expect(page.getByText("4 saved")).toBeVisible();

    await page.getByRole("button", { name: "Menu Images" }).click();
    await expect(page.getByRole("button", { name: "Capture DVD menus" })).toBeVisible();
    await expect(page.getByText("0 captured menu image(s)")).toBeVisible();

    await page.getByRole("button", { name: "Descriptions" }).click();
    await page.getByRole("button", { name: "Refresh descriptions" }).click();
    await page.getByRole("button", { name: "Expand" }).click();
    await expect(page.getByRole("textbox")).toHaveValue("E2E description fixture.");
    await expect(page.getByText("Action required")).toHaveCount(0);

    await page.getByRole("button", { name: "Menu Images" }).click();
    const captureResponse = page.waitForResponse((response) =>
      response.url().includes("/api/app/ContinueReleaseWorkflow"),
    );
    await page.getByRole("button", { name: "Capture DVD menus" }).click();
    await expect((await captureResponse).ok()).toBe(true);
    await expect(page.getByRole("heading", { name: "Authoritative DVD menu set" })).toBeVisible();
    await expect(page.getByText("2 captured menu image(s)")).toBeVisible();
    await expect(page.getByRole("heading", { name: "Disc 1" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Disc 2" })).toBeVisible();

    await page.reload();
    await page.getByRole("button", { name: "Screenshots" }).click();
    await expect(page.getByText("4 captured screenshot(s)")).toBeVisible();
    await expect(page.getByAltText(/^Disc [12] screenshot \d$/)).toHaveCount(4);

    await page.getByRole("button", { name: "Upload Images" }).click();
    await page.getByRole("button", { name: /^Prepare required hosts \(\d+\)$/ }).click();
    await expect.poll(() => workspace.fake.counters.imageUploads).toBe(6);
    await expect(page.getByText("6 saved")).toBeVisible();

    await page.getByRole("button", { name: "Descriptions" }).click();
    await page.getByRole("button", { name: "Refresh descriptions" }).click();
    await page.getByRole("button", { name: "Expand" }).click();
    await expect(page.getByRole("textbox")).toHaveValue("E2E description fixture.");
    await expect(page.getByText("Action required")).toHaveCount(0);

    await page.getByRole("button", { name: "Upload", exact: true }).click();
    const startButton = page.getByRole("button", { name: "Start upload" });
    await expect(startButton).toBeEnabled();
    await startButton.click();
    await expect.poll(() => workspace.fake.counters.trackerUploads).toBe(1);
    expectSingleCollectionTorrentUpload(workspace, "Example DVD Collection", [
      "Disc 1",
      "Disc 2",
      "VIDEO_TS",
      "VTS_01_1.VOB",
    ]);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});
