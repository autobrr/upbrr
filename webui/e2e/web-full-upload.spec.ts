// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { expect, test, type Locator, type Page, type Response } from "@playwright/test";
import type {
  ActiveInputSnapshot,
  ReleaseWorkflowCurrent,
} from "../src/api/generated/release-workflow";
import {
  createAlternateSourceFixture,
  createE2EWorkspace,
  createMultiBluraySourceFixture,
  createMultiDVDSourceFixture,
  expectSingleCollectionTorrentUpload,
  fetchMetadata,
  releaseWorkflowParityFixture,
  startApp,
  waitForMetadataReady,
  type AppServer,
} from "./helpers/e2eHarness";

const waitForAppMethod = (page: Page, method: string) =>
  page.waitForResponse((response) => response.url().endsWith(`/api/app/${method}`));

const activeInputFromResponse = async (response: Response): Promise<ActiveInputSnapshot> => {
  expect(response.ok()).toBe(true);
  return (await response.json()) as ActiveInputSnapshot;
};

const activeCurrentFromResponse = async (response: Response): Promise<ReleaseWorkflowCurrent> => {
  const snapshot = await activeInputFromResponse(response);
  if (!snapshot.current) throw new Error("active input response did not include a workflow");
  return snapshot.current;
};

const workflowCommandBody = (
  method: string,
  body: Record<string, unknown> | null,
): Record<string, unknown> => {
  const nested = body?.request;
  return method === "OpenActiveInput" && typeof nested === "object" && nested !== null
    ? (nested as Record<string, unknown>)
    : body || {};
};

const expectEnabledState = async (locator: Locator, enabled: boolean) => {
  if (enabled) {
    await expect(locator).toBeEnabled();
    return;
  }
  await expect(locator).toBeDisabled();
};

const runDuplicateCheck = async (
  page: Page,
  expectedOperationStatus: "blocked" | "completed" | "failed" = "completed",
): Promise<ReleaseWorkflowCurrent> => {
  let workflowID = "";
  let commandID = "";
  let operationID = "";
  const settled = page.waitForResponse(async (candidate) => {
    const isContinue = candidate.url().endsWith("/api/app/ContinueReleaseWorkflow");
    const isWorkflowRead = candidate.url().endsWith("/api/app/GetReleaseWorkflow");
    if (!isContinue && !isWorkflowRead) {
      return false;
    }

    const request = candidate.request().postDataJSON() as {
      authority?: { workflowId?: string };
      goal?: string;
      idempotencyKey?: string;
      workflowId?: string;
    } | null;
    if (isContinue) {
      if (request?.goal !== "duplicates_decided") return false;
      const candidateWorkflowID = request.authority?.workflowId || "";
      const candidateCommandID = request.idempotencyKey || "";
      if (!workflowID) {
        workflowID = candidateWorkflowID;
        commandID = candidateCommandID;
      } else if (candidateWorkflowID !== workflowID || candidateCommandID !== commandID) {
        return false;
      }
      if (!candidate.ok()) return true;
    } else if (!operationID || request?.workflowId !== workflowID || !candidate.ok()) {
      return false;
    }

    const current = (await candidate.json()) as ReleaseWorkflowCurrent;
    if (current.workflow.id !== workflowID) return false;
    const candidateOperationID = current.operation?.id || "";
    if (isContinue) operationID = candidateOperationID;
    if (!operationID || candidateOperationID !== operationID) return false;
    const operationStatus = current.operation?.status;
    if (operationStatus === "queued" || operationStatus === "running") return false;
    return (
      operationStatus !== "completed" ||
      Boolean(current.dupes) ||
      Boolean(current.workflow.submissionExclusions?.length)
    );
  });
  await page.getByRole("button", { name: "Run dupe check" }).click();
  const response = await settled;
  expect(response.ok()).toBe(true);
  const current = (await response.json()) as ReleaseWorkflowCurrent;
  expect(current.operation?.status).toBe(expectedOperationStatus);
  if (expectedOperationStatus === "failed") {
    expect(current.operation?.failures?.length).toBeGreaterThan(0);
  } else {
    expect(Boolean(current.dupes) || Boolean(current.workflow.submissionExclusions?.length)).toBe(
      true,
    );
  }
  await expectEnabledState(
    page.getByRole("button", { name: "Run dupe check" }),
    current.workflow.status !== "completed",
  );
  return current;
};

for (const scenario of [
  {
    name: "none trackers bypass shared content pages",
    trackers: [releaseWorkflowParityFixture.trackerID],
    mediaKind: "tv",
    preparedMediaInfo: false,
    releaseDisplayName: "E2E.Show.2026.S01E01.1080p.WEB-DL",
    screenshots: false,
    descriptions: false,
    upload: true,
  },
  {
    name: "screenshot trackers expose image pages without requiring descriptions",
    trackers: ["ANT"],
    mediaKind: "movie",
    preparedMediaInfo: true,
    releaseDisplayName: releaseWorkflowParityFixture.releaseDisplayName,
    screenshots: true,
    descriptions: false,
    upload: true,
  },
  {
    name: "mixed trackers expose the strictest shared content workflow",
    trackers: ["BTN", "ANT", "AITHER"],
    mediaKind: "tv",
    preparedMediaInfo: true,
    releaseDisplayName: "E2E.Show.2026.S01E01.1080p.WEB-DL",
    screenshots: true,
    descriptions: false,
    upload: false,
  },
] as const) {
  test(`embedded web ${scenario.name}`, async ({ page }) => {
    const workspace = await createE2EWorkspace({
      mediaKind: scenario.mediaKind,
      preparedMediaInfo: scenario.preparedMediaInfo,
    });
    let app: AppServer | undefined;
    try {
      app = await startApp(workspace);
      await fetchMetadata(page, app.url, workspace.sourcePath, scenario.releaseDisplayName);
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

for (const tracker of ["ANT", "BTN"] as const) {
  for (const action of ["Run dry run", "Start upload"] as const) {
    test(`embedded web ${tracker} ${action} prepares content without the description editor`, async ({
      page,
    }) => {
      const workspace = await createE2EWorkspace({
        mediaKind: tracker === "BTN" ? "tv" : "movie",
        preparedMediaInfo: true,
      });
      let app: AppServer | undefined;
      try {
        app = await startApp(workspace);
        await fetchMetadata(
          page,
          app.url,
          workspace.sourcePath,
          tracker === "BTN" ? "E2E.Show.2026.S01E01.1080p.WEB-DL" : undefined,
        );
        await page.getByRole("button", { name: "Dupe Check" }).click();
        if (tracker !== releaseWorkflowParityFixture.trackerID) {
          await page
            .getByRole("checkbox", { name: releaseWorkflowParityFixture.trackerID })
            .uncheck();
          await page.getByRole("checkbox", { name: tracker }).check();
        }
        await runDuplicateCheck(page);
        if (tracker === "ANT") {
          await page.getByRole("button", { name: "Screenshots" }).click();
          await page.getByRole("button", { name: "Generate screenshots" }).click();
          await expect(page.getByText("1 captured screenshot(s)")).toBeVisible();
        }
        await expect(page.getByRole("button", { name: "Descriptions" })).toBeDisabled();
        await page.getByRole("button", { name: "Upload", exact: true }).click();
        await page.getByRole("button", { name: action, exact: true }).click();
        if (action === "Start upload") {
          await expect(page.getByRole("heading", { name: "Workflow upload result" })).toBeVisible();
          expect(workspace.fake.counters.trackerUploads).toBe(1);
        } else {
          await expect(
            page
              .getByRole("heading", { name: "Tracker uploads" })
              .locator("..")
              .getByText("completed", { exact: true }),
          ).toBeVisible();
          await expect(page.getByRole("button", { name: `Expand ${tracker}` })).toBeVisible();
          expect(workspace.fake.counters.trackerUploads).toBe(0);
          expect(workspace.fake.counters.clientInjections).toBe(0);
        }
        await expect(page.getByRole("button", { name: "Descriptions" })).toBeDisabled();
        await expect(page.getByText("Exact upload dry run is unavailable.")).toHaveCount(0);
      } finally {
        await app?.stop();
        await workspace.cleanup();
      }
    });
  }
}

test("embedded web reload restores the authoritative prepared workflow", async ({ page }) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    const opened = await fetchMetadata(page, app.url, workspace.sourcePath);
    const counters = { ...workspace.fake.counters };
    const restored = waitForAppMethod(page, "GetActiveInput");
    await page.reload();
    const snapshot = await activeInputFromResponse(await restored);
    expect(snapshot.current?.workflow.id).toBe(opened.current?.workflow.id);
    expect(snapshot.inputId).toBe(opened.inputId);
    expect(snapshot.sourceVersion).toBe(opened.sourceVersion);
    await expect(page.getByText("E2E.Movie.2026.1080p.WEB-DL")).toBeVisible();
    await expect(page.getByRole("button", { name: "Dupe Check" })).toBeEnabled();
    expect(workspace.fake.counters).toEqual(counters);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded web tabs converge on active input switches and closes", async ({ page }) => {
  const workspace = await createE2EWorkspace();
  const alternateSourcePath = await createAlternateSourceFixture(workspace);
  let app: AppServer | undefined;
  const secondPage = await page.context().newPage();
  try {
    app = await startApp(workspace);
    const initial = await fetchMetadata(page, app.url, workspace.sourcePath);
    const secondLoaded = waitForAppMethod(secondPage, "GetActiveInput");
    await secondPage.goto(app.url);
    const secondInitial = await activeInputFromResponse(await secondLoaded);
    expect(secondInitial.inputId).toBe(initial.inputId);
    expect(secondInitial.current?.workflow.id).toBe(initial.current?.workflow.id);
    await expect(secondPage.getByLabel("Source path", { exact: true })).toHaveValue(
      workspace.sourcePath,
    );

    const firstTabChanged = waitForAppMethod(page, "GetActiveInput");
    const switchedResponse = waitForAppMethod(secondPage, "OpenActiveInput");
    await secondPage.getByLabel("Source path", { exact: true }).fill(alternateSourcePath);
    await secondPage.getByRole("button", { name: "Fetch metadata" }).click();
    const switched = await activeInputFromResponse(await switchedResponse);
    const firstTabSnapshot = await activeInputFromResponse(await firstTabChanged);
    expect(switched.inputId).toBeTruthy();
    expect(switched.revision).toBeGreaterThan(initial.revision);
    expect(switched.inputId).not.toBe(initial.inputId);
    expect(switched.sourceVersion).not.toBe(initial.sourceVersion);
    expect(switched.current?.workflow.id).not.toBe(initial.current?.workflow.id);
    expect(firstTabSnapshot.revision).toBe(switched.revision);
    await expect(page.getByLabel("Source path", { exact: true })).toHaveValue(alternateSourcePath);
    await expect(secondPage.getByLabel("Source path", { exact: true })).toHaveValue(
      alternateSourcePath,
    );

    await page.route(
      "**/api/app/GetActiveInput",
      async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(initial),
        });
      },
      { times: 1 },
    );
    const staleResponse = waitForAppMethod(page, "GetActiveInput");
    await page.evaluate(() => window.dispatchEvent(new Event("focus")));
    await staleResponse;
    await page.evaluate(
      () =>
        new Promise<void>((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
        ),
    );
    await expect(page.getByLabel("Source path", { exact: true })).toHaveValue(alternateSourcePath);

    const secondTabClosed = waitForAppMethod(secondPage, "GetActiveInput");
    const releasedResponse = waitForAppMethod(page, "ReleaseActiveInput");
    await page.getByRole("button", { name: "Close input" }).click();
    const released = await activeInputFromResponse(await releasedResponse);
    const convergedEmpty = await activeInputFromResponse(await secondTabClosed);
    expect(released.state).toBe("empty");
    expect(released.current ?? null).toBeNull();
    expect(convergedEmpty.revision).toBe(released.revision);
    expect(convergedEmpty.state).toBe("empty");
    await expect(page.getByRole("button", { name: "Close input" })).toBeDisabled();
    await expect(secondPage.getByRole("button", { name: "Close input" })).toBeDisabled();
  } finally {
    await secondPage.close();
    await app?.stop();
    await workspace.cleanup();
  }
});

for (const mode of ["rebuild", "reject"] as const) {
  test(`embedded web ${mode} naming authority survives reload`, async ({ page }) => {
    const workspace = await createE2EWorkspace();
    workspace.env.UPBRR_E2E_NAMING_MODE = mode;
    workspace.env.UPBRR_E2E_MEDIA_KIND = "tv";
    let app: AppServer | undefined;
    let suppliedName = false;
    let supplyName = true;
    let repeatDuplicateCheck = false;
    try {
      app = await startApp(workspace);
      // Supply an opaque user instruction through the real API; responses and
      // policy outcomes remain entirely backend-owned.
      await page.route("**/api/app/ContinueReleaseWorkflow", async (route) => {
        const body = route.request().postDataJSON() as {
          goal: string;
          intent: Record<string, unknown>;
        };
        if (supplyName && body.goal === "duplicates_decided") {
          suppliedName = true;
          body.intent.projectionInstructions = {
            BTN: { uploadReleaseName: "Opaque Uncut Name-GRP" },
          };
          await route.continue({ postData: JSON.stringify(body) });
          return;
        }
        if (repeatDuplicateCheck && body.goal === "duplicates_decided") {
          body.intent.duplicateCheckCount = 2;
          await route.continue({ postData: JSON.stringify(body) });
          return;
        }
        await route.continue();
      });
      await page.goto(app.url);
      await page.getByLabel("Source path").fill(workspace.sourcePath);
      await page.getByRole("button", { name: "Fetch metadata" }).click();
      await expect(page.getByRole("button", { name: "Dupe Check" })).toBeEnabled();
      await page.getByRole("button", { name: "Dupe Check" }).click();
      await runDuplicateCheck(page, mode === "reject" ? "failed" : "completed");
      await expect.poll(() => suppliedName).toBe(true);
      await expect(page.getByRole("button", { name: "Run dupe check" })).toBeEnabled();
      supplyName = false;

      const confirmName = page.getByLabel("Confirm release name for BTN", { exact: true });
      const notices = page.getByLabel("Tracker naming notices for BTN", { exact: true });
      if (mode === "rebuild") {
        await expect(notices.getByText(/opaque name was replaced/)).toBeVisible();
        await expect(notices.getByText(/controls edition/)).toBeVisible();
        const name = page.getByLabel("Release name for BTN", { exact: true });
        const reviewedName = "E2E Show 2026 S01E01 Example Episode 1080p WEB-DL DD 5.1 H264-UPBRR";
        await expect(name).toHaveValue(reviewedName);
        await expect(confirmName).not.toBeChecked();
        await confirmName.click();
        await expect(confirmName).toBeChecked();
        await expect(name).toBeDisabled();
        await expect(name).toHaveValue(reviewedName);

        const confirmedReload = waitForAppMethod(page, "GetActiveInput");
        await page.reload();
        const confirmedCurrent = await activeCurrentFromResponse(await confirmedReload);
        await page.getByRole("button", { name: "Dupe Check" }).click();
        await expect(confirmName).toBeChecked();
        await expect(name).toHaveValue(reviewedName);
        await expect(notices.getByText(/controls edition/)).toBeVisible();
        await expect(notices.getByText(/opaque name was replaced/)).toHaveCount(0);

        repeatDuplicateCheck = true;
        await runDuplicateCheck(page);
        await expect(page.getByRole("button", { name: "Run dupe check" })).toBeEnabled();
        const rerunReload = waitForAppMethod(page, "GetActiveInput");
        await page.reload();
        const current = await activeCurrentFromResponse(await rerunReload);
        expect(current.dupes?.checkOrdinal).toBe(2);
        expect(current.workflow.trackerProjections).toEqual(
          confirmedCurrent.workflow.trackerProjections,
        );
        expect(current.workflow.projectionInstructions).toEqual(
          confirmedCurrent.workflow.projectionInstructions,
        );
        expect(
          current.projectionInstructions?.instructions.BTN.confirmedNameFingerprint,
        ).toBeTruthy();
        await page.getByRole("button", { name: "Dupe Check" }).click();
        await expect(confirmName).toBeChecked();
      } else {
        const tracker = page
          .getByRole("article")
          .filter({ has: page.getByRole("heading", { name: "BTN", exact: true }) });
        await expect(
          tracker.getByText(/opaque name cannot satisfy mandatory component rules/),
        ).toBeVisible();
        await expect(confirmName).toHaveCount(0);
        await expect(tracker.getByText("Blocked", { exact: true })).toBeVisible();
        const restored = waitForAppMethod(page, "GetActiveInput");
        await page.reload();
        const current = await activeCurrentFromResponse(await restored);
        expect(
          current.projections?.projections.find((projection) => projection.trackerId === "BTN"),
        ).toMatchObject({ readiness: "blocked", dupeReady: false, uploadReady: false });
        await page.getByRole("button", { name: "Dupe Check" }).click();
        await expect(
          tracker.getByText(/opaque name cannot satisfy mandatory component rules/),
        ).toBeVisible();
        await expect(confirmName).toHaveCount(0);
      }
      expect(workspace.fake.counters.trackerUploads).toBe(0);
    } finally {
      await app?.stop();
      await workspace.cleanup();
    }
  });
}

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
    const cleared = waitForAppMethod(page, "OpenActiveInput");
    await page.getByRole("button", { name: "Refresh metadata" }).click();
    await expect((await cleared).ok()).toBe(true);
    await waitForMetadataReady(page, app.url);
    await expect(malRow.getByText("Manual value", { exact: true })).toBeVisible();

    const restored = waitForAppMethod(page, "GetActiveInput");
    await page.reload();
    await expect((await restored).ok()).toBe(true);
    await page.getByText("Edit Release Details", { exact: true }).click();
    await expect(malInput).toHaveValue("");
    await expect(malRow.getByText("Manual value", { exact: true })).toBeVisible();

    await page.getByRole("button", { name: "Auto MAL ID" }).click();
    const reset = waitForAppMethod(page, "OpenActiveInput");
    await page.getByRole("button", { name: "Refresh metadata" }).click();
    await expect((await reset).ok()).toBe(true);
    await waitForMetadataReady(page, app.url);
    await expect(malRow.getByText("Automatic value", { exact: true })).toBeVisible();

    await malInput.fill("5114");
    const restoredID = waitForAppMethod(page, "OpenActiveInput");
    await page.getByRole("button", { name: "Refresh metadata" }).click();
    await expect((await restoredID).ok()).toBe(true);
    await waitForMetadataReady(page, app.url);
    await expect(malInput).toHaveValue("5114");
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded web retains Input corrections without downstream workflow effects", async ({
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
      if (
        ![
          "OpenActiveInput",
          "ContinueReleaseWorkflow",
          "GetActiveInput",
          "GetReleaseWorkflowOperation",
        ].includes(method)
      )
        return;
      let body: Record<string, unknown> | null = {};
      try {
        body = request.postDataJSON() as Record<string, unknown> | null;
      } catch {
        // GET-style workflow resource requests do not carry JSON.
      }
      workflowRequests.push({ method, body: workflowCommandBody(method, body) });
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
    const saved = waitForAppMethod(page, "OpenActiveInput");
    await page.getByRole("button", { name: "Refresh metadata" }).click();
    await expect((await saved).ok()).toBe(true);
    await waitForMetadataReady(page, app.url);
    await expect(dupeCheck).toBeEnabled();
    await expect(commentary).toHaveValue("no");
    const setCommand = workflowRequests.findLast(
      (request) =>
        request.method === "OpenActiveInput" &&
        (request.body.intent as { correctionPatch?: unknown } | undefined)?.correctionPatch,
    );
    expect(setCommand?.body.goal).toBe("input_ready");
    expect(
      (setCommand?.body.intent as { correctionPatch?: { expectedRevision?: number } })
        ?.correctionPatch?.expectedRevision ?? -1,
    ).toBeGreaterThanOrEqual(0);
    expect(workspace.fake.counters).toEqual(initialCounters);

    const restored = waitForAppMethod(page, "GetActiveInput");
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
    const reset = waitForAppMethod(page, "OpenActiveInput");
    await page.getByRole("button", { name: "Refresh metadata" }).click();
    await expect((await reset).ok()).toBe(true);
    await waitForMetadataReady(page, app.url);
    await expect(dupeCheck).toBeEnabled();
    const resetCommand = workflowRequests.findLast(
      (request) =>
        request.method === "OpenActiveInput" &&
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
    const retained = waitForAppMethod(page, "OpenActiveInput");
    await page.getByRole("button", { name: "Refresh metadata" }).click();
    await expect((await retained).ok()).toBe(true);
    await waitForMetadataReady(page, app.url);
    await expect(dupeCheck).toBeEnabled();
    for (const request of workflowRequests) {
      if (request.method === "OpenActiveInput" || request.method === "ContinueReleaseWorkflow")
        expect(request.body.goal).toBe("input_ready");
      expect([
        "OpenActiveInput",
        "ContinueReleaseWorkflow",
        "GetActiveInput",
        "GetReleaseWorkflowOperation",
      ]).toContain(request.method);
    }
    expect(workspace.fake.counters).toEqual(initialCounters);

    await app.stop();
    app = await startApp(workspace, { seed: false });
    const restarted = waitForAppMethod(page, "GetActiveInput");
    await page.goto(app.url);
    const restartedSnapshot = await activeInputFromResponse(await restarted);
    expect(restartedSnapshot.state).toBe("empty");
    expect(restartedSnapshot.current ?? null).toBeNull();
    await expect(page.getByLabel("Source path", { exact: true })).toHaveValue("");
    await expect(page.getByRole("button", { name: "Close input" })).toBeDisabled();
    expect(workspace.fake.counters).toEqual(initialCounters);

    const reopened = await fetchMetadata(page, app.url, workspace.sourcePath);
    expect(reopened.revision).toBeGreaterThan(restartedSnapshot.revision);
    await page.getByText("Edit Release Details", { exact: true }).click();
    await expect(page.getByRole("combobox", { name: "Commentary" })).toHaveValue("no");
    const reopenedCounters = {
      ...initialCounters,
      clientSearches: initialCounters.clientSearches + 1,
    };
    expect(workspace.fake.counters).toEqual(reopenedCounters);

    const removeTMDB = page.getByRole("button", { name: "Remove TMDB ID", exact: true });
    await expect(removeTMDB).toBeEnabled();
    await removeTMDB.click();
    await sourceOptions.locator("summary").click();
    await skipClientSearch.check();
    const opensBeforeRemoval = workflowRequests.filter(
      (request) => request.method === "OpenActiveInput",
    ).length;
    await expect(page.getByRole("button", { name: "Refresh metadata" })).toBeEnabled();

    const removed = page.waitForResponse(
      (response) =>
        response.url().endsWith("/api/app/OpenActiveInput") &&
        Boolean(response.request().postDataJSON()?.request?.intent?.preparation),
    );
    await page.getByRole("button", { name: "Refresh metadata" }).click();
    await expect((await removed).ok()).toBe(true);
    expect(workflowRequests.filter((request) => request.method === "OpenActiveInput")).toHaveLength(
      opensBeforeRemoval + 1,
    );
    await waitForMetadataReady(page, app.url);
    await expect(dupeCheck).toBeEnabled();
    const removedReload = waitForAppMethod(page, "GetActiveInput");
    await page.reload();
    await activeInputFromResponse(await removedReload);
    await page.getByText("Edit Release Details", { exact: true }).click();
    await expect(removeTMDB).toBeDisabled();
    await expect(removeTMDB).toHaveText("Removed");
    await expect(page.getByRole("textbox", { name: "TMDB ID", exact: true })).toHaveValue("");
    await expect(page.getByRole("button", { name: /^TMDB \d+ Source:/ })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Remove IMDB ID", exact: true })).toBeEnabled();
    expect(workspace.fake.counters).toEqual(reopenedCounters);
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

    const beforeRefreshResponse = await page
      .context()
      .request.get(new URL("api/app/GetActiveInput", app.url).toString());
    expect(beforeRefreshResponse.ok()).toBe(true);
    const beforeRefresh = (await beforeRefreshResponse.json()) as ActiveInputSnapshot;
    expect(beforeRefresh.current?.media?.artifacts.length).toBeGreaterThan(0);
    const effectsAfterUpload = { ...workspace.fake.counters };
    expect(effectsAfterUpload.imageUploads).toBeGreaterThan(0);

    await page.getByRole("button", { name: "Input", exact: true }).click();
    await page.getByText("Edit Release Details", { exact: true }).click();
    const refreshedResponse = waitForAppMethod(page, "OpenActiveInput");
    await page.getByRole("button", { name: "Refresh metadata" }).click();
    const refreshed = await activeInputFromResponse(await refreshedResponse);
    expect(refreshed.inputId).toBe(beforeRefresh.inputId);
    expect(refreshed.sourceVersion).toBe(beforeRefresh.sourceVersion);
    await expect(page.getByRole("button", { name: "Refresh metadata" })).toBeEnabled();
    await expect
      .poll(() => workspace.fake.counters.clientSearches)
      .toBe(effectsAfterUpload.clientSearches + 1);
    expect(workspace.fake.counters.imageUploads).toBe(effectsAfterUpload.imageUploads);
    expect(workspace.fake.counters.trackerUploads).toBe(1);
    expect(workspace.fake.counters.clientInjections).toBe(effectsAfterUpload.clientInjections);

    await page.getByRole("button", { name: "Dupe Check" }).click();
    await expect(page.getByRole("checkbox", { name: "HDS" })).toBeChecked();
    await runDuplicateCheck(page);
    const excludedResponse = await page
      .context()
      .request.get(new URL("api/app/GetActiveInput", app.url).toString());
    expect(excludedResponse.ok()).toBe(true);
    const excluded = (await excludedResponse.json()) as ActiveInputSnapshot;
    expect(excluded.current?.workflow.status).toBe("completed");
    expect(excluded.current?.operation?.status).toBe("completed");
    expect(excluded.current?.operation?.failures ?? []).toEqual([]);
    expect(excluded.current?.workflow.submissionExclusions).toContainEqual(
      expect.objectContaining({ trackerId: "HDS", reason: "already_uploaded" }),
    );
    expect(excluded.current?.dupes ?? null).toBeNull();
    expect(excluded.current?.media ?? null).toBeNull();

    await expect(page.getByLabel("Submission exclusions")).toContainText("HDS");
    await expect(page.getByLabel("Submission exclusions")).toContainText("Already uploaded");
    await expect(
      page.getByText("All selected trackers were already uploaded. No upload is needed."),
    ).toBeVisible();
    expect(workspace.fake.counters).toEqual({
      ...effectsAfterUpload,
      clientSearches: effectsAfterUpload.clientSearches + 1,
    });
    expect(workspace.fake.trackerUploadBodies).toHaveLength(1);

    const deletedWorkflowID = excluded.current?.workflow.id;
    expect(deletedWorkflowID).toBeTruthy();
    await page.getByRole("button", { name: "History" }).click();
    await expect(
      page.getByText(releaseWorkflowParityFixture.releaseDisplayName).first(),
    ).toBeVisible();
    const deletedResponse = waitForAppMethod(page, "DeleteHistoryRelease");
    page.once("dialog", (dialog) => dialog.accept());
    await page.getByRole("button", { name: "Remove from database" }).click();
    await expect((await deletedResponse).ok()).toBe(true);
    await expect(page.getByText("No stored releases found.")).toBeVisible();

    const afterDelete = await page
      .context()
      .request.get(new URL("api/app/GetActiveInput", app.url).toString());
    expect(afterDelete.ok()).toBe(true);
    expect(await afterDelete.json()).toMatchObject({ state: "empty" });
    await page.getByRole("button", { name: "Input", exact: true }).click();
    await expect(page.getByLabel("Source path", { exact: true })).toHaveValue("");
    await expect(page.getByRole("button", { name: "Close input" })).toBeDisabled();

    const reopened = await fetchMetadata(page, app.url, workspace.sourcePath);
    expect(reopened.current?.workflow.id).toBeTruthy();
    expect(reopened.current?.workflow.id).not.toBe(deletedWorkflowID);
    await page.getByRole("button", { name: "Dupe Check" }).click();
    await page.getByRole("checkbox", { name: releaseWorkflowParityFixture.trackerID }).uncheck();
    await page.getByRole("checkbox", { name: "HDS" }).check();
    const afterDeletion = await runDuplicateCheck(page);
    expect(afterDeletion.dupes).toBeTruthy();
    expect(afterDeletion.workflow.submissionExclusions ?? []).not.toContainEqual(
      expect.objectContaining({ trackerId: "HDS", reason: "already_uploaded" }),
    );
    expect(workspace.fake.counters.trackerUploads).toBe(1);
    expect(workspace.fake.trackerUploadBodies).toHaveLength(1);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded web restores captured screens and hosted URLs after reopening an input", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace({ screenshotCount: 4 });
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByRole("button", { name: "Dupe Check" }).click();
    await page.getByRole("checkbox", { name: releaseWorkflowParityFixture.trackerID }).uncheck();
    await page.getByRole("checkbox", { name: "HDS" }).check();
    await runDuplicateCheck(page);
    await page.getByRole("button", { name: "Screenshots" }).click();
    await page.getByRole("button", { name: "Generate screenshots" }).click();
    await expect(page.getByText("4 captured screenshot(s)")).toBeVisible();
    const reordered = waitForAppMethod(page, "ReorderReleaseWorkflowMedia");
    await page
      .getByAltText("Screenshot 3")
      .locator("../..")
      .dragTo(page.getByAltText("Screenshot 1").locator("../.."));
    expect((await reordered).ok()).toBe(true);
    const deselected = waitForAppMethod(page, "SetReleaseWorkflowMediaSelection");
    await page
      .getByAltText("Screenshot 4")
      .locator("../..")
      .getByRole("button", { name: "Unselect" })
      .click();
    expect((await deselected).ok()).toBe(true);
    await page.getByRole("button", { name: "Upload Images" }).click();
    await page.getByRole("button", { name: "Prepare required hosts (3)" }).click();
    await expect(page.getByText("3 saved")).toBeVisible();
    expect(workspace.fake.counters.imageUploads).toBe(3);
    const savedResponse = await page.request.get(
      new URL("api/app/GetActiveInput", app.url).toString(),
    );
    expect(savedResponse.ok()).toBe(true);
    const beforeRestart = (await savedResponse.json()) as ActiveInputSnapshot;
    const savedScreens = beforeRestart.current?.media?.artifacts
      .filter((artifact) => artifact.kind === "screenshot")
      .map(({ index, order, selected }) => ({ index, order, selected }))
      .sort((left, right) => left.index - right.index);
    const savedURLs = beforeRestart.current?.media?.artifacts
      .filter((artifact) => artifact.kind === "hosted_image")
      .map((artifact) => artifact.url)
      .sort();
    expect(savedScreens?.filter((artifact) => artifact.selected)).toHaveLength(3);
    expect(savedScreens?.some((artifact) => artifact.order !== artifact.index)).toBe(true);
    expect(savedURLs).toHaveLength(3);

    await app.stop();
    app = await startApp(workspace, { seed: false });
    await page.goto(app.url);
    await expect(page.getByLabel("Source path", { exact: true })).toHaveValue("");
    await expect(page.getByRole("button", { name: "Close input" })).toBeDisabled();
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByRole("button", { name: "Dupe Check" }).click();
    await page.getByRole("checkbox", { name: releaseWorkflowParityFixture.trackerID }).uncheck();
    await page.getByRole("checkbox", { name: "HDS" }).check();
    const restored = await runDuplicateCheck(page);
    expect(
      restored.media?.artifacts
        .filter((artifact) => artifact.kind === "screenshot")
        .map(({ index, order, selected }) => ({ index, order, selected }))
        .sort((left, right) => left.index - right.index),
    ).toEqual(savedScreens);
    expect(
      restored.media?.artifacts
        .filter((artifact) => artifact.kind === "hosted_image")
        .map((artifact) => artifact.url)
        .sort(),
    ).toEqual(savedURLs);
    await page.getByRole("button", { name: "Screenshots" }).click();
    await expect(page.getByText("4 captured screenshot(s)")).toBeVisible();
    await expect(page.getByAltText("Screenshot 1")).toBeVisible();
    await page.getByRole("button", { name: "Upload Images" }).click();
    await expect(page.getByText("3 saved")).toBeVisible();
    await page.getByRole("button", { name: "Prepare required hosts (3)" }).click();
    await expect(page.getByRole("button", { name: "Prepare required hosts (3)" })).toBeEnabled();
    await expect(page.getByText("3 saved")).toBeVisible();
    expect(workspace.fake.counters.imageUploads).toBe(3);
    expect(workspace.fake.counters.trackerUploads).toBe(0);
    expect(workspace.fake.counters.clientInjections).toBe(0);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded web restores edited descriptions after reopening an input", async ({ page }) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByRole("button", { name: "Dupe Check" }).click();
    await page.getByRole("checkbox", { name: releaseWorkflowParityFixture.trackerID }).uncheck();
    await page.getByRole("checkbox", { name: "HDS" }).check();
    await runDuplicateCheck(page);
    await page.getByRole("button", { name: "Screenshots" }).click();
    await page.getByRole("button", { name: "Generate screenshots" }).click();
    await expect(page.getByText("1 captured screenshot(s)")).toBeVisible();
    await page.getByRole("button", { name: "Upload Images" }).click();
    await page.getByRole("button", { name: "Prepare required hosts (1)" }).click();
    await expect(page.getByText("1 saved")).toBeVisible();
    await page.getByRole("button", { name: "Descriptions" }).click();
    await page.getByRole("button", { name: "Refresh descriptions" }).click();
    await page.getByRole("button", { name: "Expand" }).click();
    await expect(page.getByRole("textbox")).toHaveValue("E2E description fixture.");
    const editedDescription = "Retained description with [b]custom notes[/b].";
    await page.getByRole("textbox").fill(editedDescription);
    const descriptionSaved = waitForAppMethod(page, "SaveReleaseWorkflowDescriptionOverride");
    await page.getByRole("button", { name: "Save group" }).click();
    expect((await descriptionSaved).ok()).toBe(true);
    await expect(page.getByRole("textbox")).toHaveValue(editedDescription);

    await app.stop();
    app = await startApp(workspace, { seed: false });
    await page.goto(app.url);
    await expect(page.getByLabel("Source path", { exact: true })).toHaveValue("");
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByRole("button", { name: "Dupe Check" }).click();
    await page.getByRole("checkbox", { name: releaseWorkflowParityFixture.trackerID }).uncheck();
    await page.getByRole("checkbox", { name: "HDS" }).check();
    await runDuplicateCheck(page);
    await page.getByRole("button", { name: "Descriptions" }).click();
    await page.getByRole("button", { name: "Refresh descriptions" }).click();
    await page.getByRole("button", { name: "Expand" }).click();
    await expect(page.getByRole("textbox")).toHaveValue(editedDescription);
    await page.getByRole("button", { name: "Upload", exact: true }).click();
    await page.getByLabel("Skip client injection").check();
    await page.getByRole("button", { name: "Run dry run" }).click();
    await expect(page.getByRole("heading", { name: "Tracker uploads" })).toBeVisible();
    expect(workspace.fake.counters.imageUploads).toBe(1);
    expect(workspace.fake.counters.trackerUploads).toBe(0);
    expect(workspace.fake.counters.clientInjections).toBe(0);
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
    await runDuplicateCheck(page, "blocked");

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
      response.url().endsWith("/api/app/OpenActiveInput"),
    );
    await page.getByRole("button", { name: "Fetch metadata" }).click();
    const prepared = await prepareResponse;
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
