// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { expect, type Locator, type Page, type Response } from "@playwright/test";
import { appendFile, readFile, writeFile } from "node:fs/promises";
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
  readE2EAuthCounters,
  releaseWorkflowParityFixture,
  startApp,
  test,
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

const waitForWorkflowGoal = (
  page: Page,
  goal: "trackers_projected" | "trackers_assessed" | "duplicates_decided",
): Promise<Response> => {
  let workflowID = "";
  let commandID = "";
  let operationID = "";
  return page.waitForResponse(async (candidate) => {
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
      if (request?.goal !== goal) return false;
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
    if (goal === "trackers_projected") {
      return operationStatus !== "completed" || Boolean(current.projections);
    }
    if (goal === "trackers_assessed") {
      return operationStatus !== "completed" || Boolean(current.preflight);
    }
    return (
      operationStatus !== "completed" ||
      Boolean(current.dupes) ||
      Boolean(current.workflow.submissionExclusions?.length)
    );
  });
};

const runDuplicateCheck = async (
  page: Page,
  expectedOperationStatus: "blocked" | "completed" | "failed" = "completed",
): Promise<ReleaseWorkflowCurrent> => {
  const settled = waitForWorkflowGoal(page, "duplicates_decided");
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

const antQuestionPanel = (page: Page) =>
  page
    .getByRole("region", { name: "Tracker questions", exact: true })
    .locator("details")
    .filter({ has: page.locator("summary").filter({ hasText: /^ANT(?: ·|$)/ }) });

const applyTrackerAnswers = async (page: Page): Promise<ReleaseWorkflowCurrent> => {
  const settled = waitForWorkflowGoal(page, "trackers_assessed");
  const apply = page.getByRole("button", { name: "Apply tracker answers" });
  await apply.click();
  const response = await settled;
  expect(response.ok()).toBe(true);
  const current = (await response.json()) as ReleaseWorkflowCurrent;
  expect(current.operation?.status).toBe("completed");
  await expect(apply).toBeEnabled();
  return current;
};

const answerAntTags = async (page: Page) => {
  const questions = antQuestionPanel(page);
  await expect(questions).not.toHaveAttribute("open");
  await questions.locator("summary").click();
  await questions.getByRole("textbox", { name: "Tags *", exact: true }).fill("drama");
  await applyTrackerAnswers(page);
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
      if (
        scenario.trackers.some((tracker) => tracker === "ANT") &&
        scenario.mediaKind === "movie"
      ) {
        await answerAntTags(page);
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
        if (tracker === "ANT") {
          await answerAntTags(page);
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

test("embedded web reviews default tracker questions before dupes and retains compatible evidence", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace({ preparedMediaInfo: true });
  let app: AppServer | undefined;
  try {
    const config = await readFile(workspace.configPath, "utf8");
    await writeFile(
      workspace.configPath,
      config.replace('default_trackers: ["BTN"]', 'default_trackers: ["ANT"]'),
    );
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    const authBefore = await readE2EAuthCounters(workspace);
    const countersBefore = { ...workspace.fake.counters };
    const goals = new Map<string, string>();
    let duplicateRequests = 0;
    page.on("request", (request) => {
      if (request.url().endsWith("/api/app/ContinueReleaseWorkflow")) {
        const body = request.postDataJSON() as { goal: string; idempotencyKey: string };
        goals.set(body.idempotencyKey, body.goal);
        if (body.goal === "duplicates_decided") duplicateRequests += 1;
      }
    });
    const projected = waitForWorkflowGoal(page, "trackers_projected");
    await page.getByRole("button", { name: "Dupe Check" }).click();
    const response = await projected;
    expect(response.ok()).toBe(true);
    const current = (await response.json()) as ReleaseWorkflowCurrent;
    expect(current.operation?.status).toBe("blocked");
    expect(current.projections?.projections.map((projection) => projection.trackerId)).toEqual([
      "ANT",
    ]);
    expect(current.preflight).toBeFalsy();
    expect(current.dupes).toBeFalsy();
    expect([...goals.values()]).toEqual(["trackers_projected"]);
    expect(await readE2EAuthCounters(workspace)).toEqual(authBefore);
    expect(workspace.fake.counters).toEqual(countersBefore);
    await expect(page.getByRole("checkbox", { name: "ANT", exact: true })).toBeChecked();

    const questions = antQuestionPanel(page);
    const summary = questions.locator("summary");
    const tags = questions.getByRole("textbox", { name: "Tags *", exact: true });
    await expect(questions).not.toHaveAttribute("open");
    await expect(summary).toContainText("Required");
    await expect(tags).toBeHidden();
    await summary.focus();
    await summary.press("Enter");
    await expect(questions).toHaveAttribute("open");
    await tags.fill("drama");
    await summary.press("Space");
    await expect(questions).not.toHaveAttribute("open");
    await expect(summary).toContainText("Unapplied changes");
    await expect(tags).toBeHidden();
    await summary.press("Enter");
    await expect(tags).toHaveValue("drama");
    const applied = await applyTrackerAnswers(page);
    expect(applied.dupes).toBeFalsy();
    expect(applied.projections?.projections[0].questionnaireAnswers?.tags).toBe("drama");
    await expect(questions).toHaveAttribute("open");
    await expect(tags).toHaveValue("drama");
    await expect(summary).not.toContainText("Unapplied changes");

    const checked = await runDuplicateCheck(page);
    expect(checked.dupes?.results).toHaveLength(1);
    const original = checked.dupes!.results[0];
    expect(original.trackerId).toBe("ANT");
    expect(original.status).toBe("completed");
    expect(original.checkedAt).toBeTruthy();
    expect(original.searchFingerprint).toBeTruthy();
    const requestsBeforeEdit = duplicateRequests;
    await tags.fill("drama,mystery");
    const reapplied = await applyTrackerAnswers(page);
    expect(reapplied.projections?.projections[0].questionnaireAnswers?.tags).toBe("drama,mystery");
    expect(reapplied.dupes?.results).toHaveLength(1);
    const retained = reapplied.dupes!.results[0];
    expect(retained).toEqual({
      ...original,
      projectionFingerprint: expect.any(String),
    });
    expect(retained.projectionFingerprint).not.toBe(original.projectionFingerprint);
    expect(reapplied.dupes?.projectionSet).toEqual({
      id: reapplied.projections?.id,
      revision: reapplied.projections?.revision,
    });
    expect(reapplied.dupes?.projectionSet).not.toEqual(checked.dupes?.projectionSet);
    expect(duplicateRequests).toBe(requestsBeforeEdit);
    expect([...goals.values()].filter((goal) => goal === "duplicates_decided")).toHaveLength(1);
    expect([...goals.values()].filter((goal) => goal === "trackers_assessed")).toHaveLength(2);
    await expect(tags).toHaveValue("drama,mystery");
    await expect(summary).not.toContainText("Unapplied changes");
    await expect(page.getByRole("button", { name: "Screenshots" })).toBeEnabled();
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded web reload restores the authoritative prepared workflow", async ({ page }) => {
  const workspace = await createE2EWorkspace({ preparedMediaInfo: true });
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    const opened = await fetchMetadata(page, app.url, workspace.sourcePath);
    const mediaInfoPanel = page.getByText("MediaInfo Preview", { exact: true }).locator("..");
    await mediaInfoPanel.locator("summary").first().click();
    await expect(mediaInfoPanel.locator(".mediainfo__video")).toContainText("AVC");
    await page.setViewportSize({ width: 390, height: 844 });
    await page.evaluate(() => {
      const next = JSON.stringify({ version: 1, theme: "swizzin", mode: "dark", accents: {} });
      localStorage.setItem("upbrr:appearance:v1", next);
      dispatchEvent(
        new StorageEvent("storage", {
          key: "upbrr:appearance:v1",
          newValue: next,
          storageArea: localStorage,
        }),
      );
    });
    await expect(page.locator("html")).toHaveAttribute("data-theme", "swizzin");
    await expect(page.locator("html")).toHaveClass(/dark/);
    await mediaInfoPanel.locator(".mediainfo__raw > summary").click();
    await expect(mediaInfoPanel.locator(".mediainfo__raw pre")).toContainText("Unique ID");
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
    ).toBeLessThanOrEqual(0);
    const counters = { ...workspace.fake.counters };
    const restored = waitForAppMethod(page, "GetActiveInput");
    await page.reload();
    const snapshot = await activeInputFromResponse(await restored);
    expect(snapshot.current?.workflow.id).toBe(opened.current?.workflow.id);
    expect(snapshot.inputId).toBe(opened.inputId);
    expect(snapshot.sourceVersion).toBe(opened.sourceVersion);
    await expect(page.getByText("E2E.Movie.2026.1080p.WEB-DL")).toBeVisible();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "swizzin");
    await mediaInfoPanel.locator("summary").first().click();
    await expect(mediaInfoPanel.locator(".mediainfo__video")).toContainText("AVC");
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
    await waitForMetadataReady(secondPage, app.url);

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
      await waitForMetadataReady(page, app.url);
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

type CorrectionValue = string | number | boolean | readonly string[];
type CorrectionCase = {
  field: string;
  label: string;
  group: "ReleaseName" | "Metadata";
  key: string;
  values: readonly CorrectionValue[];
  mediaKind?: "movie" | "tv";
};

// Sample each distinct editor control; production Go tests retain the broad field matrix.
const releaseDetailCorrections: readonly CorrectionCase[] = [
  {
    field: "release_name.type",
    label: "Type",
    group: "ReleaseName",
    key: "Type",
    values: ["WEBDL", "ENCODE"],
  },
  {
    field: "metadata.distributor",
    label: "Distributor",
    group: "Metadata",
    key: "Distributor",
    values: ["ARROW", "VINEGAR SYNDROME", ""],
  },
  {
    field: "release_name.service",
    label: "Service",
    group: "ReleaseName",
    key: "Service",
    values: ["NF", "AMZN", ""],
  },
  {
    field: "release_name.episode",
    label: "Episode",
    group: "ReleaseName",
    key: "Episode",
    values: ["E02", "E03", ""],
    mediaKind: "tv",
  },
  {
    field: "release_name.manual_year",
    label: "Manual year",
    group: "ReleaseName",
    key: "ManualYear",
    values: [2024, 2025, 0],
  },
  {
    field: "release_name.no_episode_title",
    label: "No episode title",
    group: "ReleaseName",
    key: "NoEpisodeTitle",
    values: [true, false],
  },
  {
    field: "metadata.audio_languages",
    label: "Audio languages",
    group: "Metadata",
    key: "AudioLanguages",
    values: [["Japanese", "Spanish"], ["French"], []],
  },
];

test("embedded Input finite choices support keyboard selection without custom or blank entry", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByText("Edit Release Details", { exact: true }).click();
    for (const label of ["Category", "Type", "Source", "Resolution"]) {
      const control = page.getByRole("combobox", { name: label, exact: true });
      const options = await control
        .locator("option:not(:disabled)")
        .evaluateAll((nodes) => nodes.map((node) => (node as HTMLOptionElement).value));
      expect(options.length).toBeGreaterThan(1);
      expect(options).not.toContain("");
      await expect(
        page.getByRole("button", { name: `Enter custom ${label}`, exact: true }),
      ).toHaveCount(0);
      await control.selectOption(options[0]);
      await control.focus();
      await control.press("ArrowDown");
      await control.press("Enter");
      await expect(control).toHaveValue(options[1]);
      await page.getByRole("button", { name: `Auto ${label}`, exact: true }).click();
    }
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

// The fixed evidence collector proves editor transport and durable instructions here.
// Production preparation tests separately verify regenerated names and media facts.
for (const correction of releaseDetailCorrections) {
  test(`embedded web persists Release Details ${correction.label} edits and Auto`, async ({
    page,
  }) => {
    const workspace = await createE2EWorkspace({ mediaKind: correction.mediaKind });
    let app: AppServer | undefined;
    try {
      app = await startApp(workspace);
      let snapshot = await fetchMetadata(
        page,
        app.url,
        workspace.sourcePath,
        correction.mediaKind === "tv" ? "E2E.Show.2026.S01E01.1080p.WEB-DL" : undefined,
      );
      const initialCounters = { ...workspace.fake.counters };
      await page.getByText("Edit Release Details", { exact: true }).click();
      const row = page.locator(`[data-correction-field="${correction.field}"]`);
      const boolean = typeof correction.values[0] === "boolean";
      const control = row.getByRole(
        boolean || ["Type", "Service", "Distributor"].includes(correction.key)
          ? "combobox"
          : correction.key === "ManualYear"
            ? "spinbutton"
            : "textbox",
        { name: correction.label, exact: true },
      );
      const automaticValue = await control.inputValue();
      const storedGroup = correction.group === "ReleaseName" ? "releaseName" : "metadata";
      const expectPersisted = (
        current: ReleaseWorkflowCurrent | null | undefined,
        value: unknown,
      ) => {
        expect(current?.corrections?.corrections[storedGroup]).toHaveProperty(
          correction.key,
          value,
        );
        expect(current?.factInstructions?.instructions[correction.group]).toHaveProperty(
          correction.key,
          value,
        );
      };
      for (const value of correction.values) {
        await test.step(`apply ${JSON.stringify(value)} and reload`, async () => {
          const text = Array.isArray(value)
            ? value.join(", ")
            : typeof value === "boolean"
              ? value
                ? "yes"
                : "no"
              : value === 0
                ? ""
                : String(value);
          if (boolean || correction.key === "Type") await control.selectOption(text);
          else if (["Service", "Distributor"].includes(correction.key) && text) {
            await control.fill(
              correction.key === "Service"
                ? text === "AMZN"
                  ? "aMz"
                  : "netfl"
                : text === "ARROW"
                  ? "aRrOw"
                  : "vInegar",
            );
            const choices = row.getByRole("option");
            await expect(choices).toHaveCount(1);
            await control.press("ArrowDown");
            await control.press("Enter");
            await expect(control).toHaveValue(text);
          } else await control.fill(text);
          await expect(row.getByText("Manual change pending", { exact: true })).toBeVisible();
          const saved = waitForAppMethod(page, "OpenActiveInput");
          await page.getByRole("button", { name: "Refresh metadata" }).click();
          const response = await saved;
          expect(response.ok()).toBe(true);
          const command = workflowCommandBody("OpenActiveInput", response.request().postDataJSON());
          expect(command.goal).toBe("input_ready");
          expect((command.intent as { correctionPatch: unknown }).correctionPatch).toEqual({
            values: {
              Identity: {},
              ReleaseName: {},
              Metadata: {},
              [correction.group]: { [correction.key]: value },
            },
            resetFields: [],
            confirmFields: [],
            expectedRevision: snapshot.current?.corrections?.revision ?? 0,
          });
          snapshot = await waitForMetadataReady(page, app!.url);
          expectPersisted(snapshot.current, value);
          await expect(row.getByText("Manual value · Applied", { exact: true })).toBeVisible();
          await expect(control).toHaveValue(text);
          const restored = waitForAppMethod(page, "GetActiveInput");
          await page.reload();
          snapshot = await activeInputFromResponse(await restored);
          expectPersisted(snapshot.current, value);
          await page.getByText("Edit Release Details", { exact: true }).click();
          await expect(control).toHaveValue(text);
          await expect(row.getByText("Manual value · Applied", { exact: true })).toBeVisible();
        });
      }
      if (boolean) await control.selectOption("auto");
      else await row.getByRole("button", { name: `Auto ${correction.label}`, exact: true }).click();
      await expect(row.getByText("Auto reset pending", { exact: true })).toBeVisible();
      const reset = waitForAppMethod(page, "OpenActiveInput");
      await page.getByRole("button", { name: "Refresh metadata" }).click();
      const resetResponse = await reset;
      expect(resetResponse.ok()).toBe(true);
      const resetCommand = workflowCommandBody(
        "OpenActiveInput",
        resetResponse.request().postDataJSON(),
      );
      expect((resetCommand.intent as { correctionPatch: unknown }).correctionPatch).toEqual({
        values: { Identity: {}, ReleaseName: {}, Metadata: {} },
        resetFields: [{ field: correction.field }],
        confirmFields: [],
        expectedRevision: snapshot.current?.corrections?.revision ?? 0,
      });
      snapshot = await waitForMetadataReady(page, app.url);
      expectPersisted(snapshot.current, null);
      await expect(row.getByText(/^Automatic value/)).toBeVisible();
      await expect(control).toHaveValue(automaticValue);
      const resetReload = waitForAppMethod(page, "GetActiveInput");
      await page.reload();
      snapshot = await activeInputFromResponse(await resetReload);
      expectPersisted(snapshot.current, null);
      await page.getByText("Edit Release Details", { exact: true }).click();
      await expect(row.getByText(/^Automatic value/)).toBeVisible();
      await expect(control).toHaveValue(automaticValue);
      expect(workspace.fake.counters).toEqual(initialCounters);
    } finally {
      await app?.stop();
      await workspace.cleanup();
    }
  });
}

test("embedded web recovers saved corrections before a release snapshot and preserves history", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByText("Edit Release Details", { exact: true }).click();
    await page.getByRole("textbox", { name: "Alternate title", exact: true }).fill("Example AKA");
    await page.getByRole("textbox", { name: "Original language", exact: true }).fill("Spanish");
    await page.getByRole("textbox", { name: "Genres", exact: true }).fill("Drama, Mystery");
    await page.getByRole("textbox", { name: "Edition", exact: true }).fill("Collector Edition");
    const saved = waitForAppMethod(page, "OpenActiveInput");
    await page.getByRole("button", { name: "Refresh metadata" }).click();
    expect((await saved).ok()).toBe(true);
    let snapshot = await waitForMetadataReady(page, app.url);
    expect(snapshot.current?.corrections?.corrections.metadata).toMatchObject({
      AlternateTitle: "Example AKA",
      OriginalLanguage: "Spanish",
      Genres: ["Drama", "Mystery"],
    });

    for (const resolution of ["individual", "bulk"] as const) {
      await test.step(`${resolution} resolution survives reload and resumes preparation`, async () => {
        const previousWorkflowID = snapshot.current?.workflow.id;
        const closed = waitForAppMethod(page, "ReleaseActiveInput");
        await page.getByRole("button", { name: "Close input" }).click();
        expect((await activeInputFromResponse(await closed)).state).toBe("empty");
        // Change the real source fingerprint while retaining its saved correction and history keys.
        await appendFile(workspace.sourcePath, `changed synthetic content: ${resolution}\n`);
        if (resolution === "individual") {
          await page.getByRole("button", { name: "History", exact: true }).click();
          await expect(page.getByText(workspace.sourcePath, { exact: true })).toBeVisible();
          const opened = waitForAppMethod(page, "OpenActiveInput");
          await page.getByRole("button", { name: "Open input", exact: true }).click();
          expect((await opened).ok()).toBe(true);
        } else {
          await page.getByLabel("Source path", { exact: true }).fill(workspace.sourcePath);
          const opened = waitForAppMethod(page, "OpenActiveInput");
          await page.getByRole("button", { name: "Fetch metadata", exact: true }).click();
          expect((await opened).ok()).toBe(true);
        }
        const review = page.getByRole("region", { name: "Saved values need review" });
        await expect(review).toBeVisible();
        await expect(page.getByText(/Workflow release snapshot is unavailable/)).toHaveCount(0);
        const response = await page
          .context()
          .request.get(new URL("api/app/GetActiveInput", app!.url).toString());
        expect(response.ok()).toBe(true);
        const blocked = (await response.json()) as ActiveInputSnapshot;
        expect(blocked.current?.workflow.id).not.toBe(previousWorkflowID);
        expect(blocked.current?.release ?? null).toBeNull();
        expect(blocked.current?.operation?.status).toBe("blocked");
        const action = blocked.current?.workflow.requiredActions?.find(
          (candidate) => candidate.kind === "confirm_corrections" && candidate.status === "pending",
        );
        const affectedFields = ["metadata.alternate_title", "metadata.original_language"];
        if (resolution === "individual") affectedFields.push("metadata.genres");
        expect([...(action?.correctionConfirmation?.fields ?? [])].sort()).toEqual(
          [...affectedFields].sort(),
        );
        expect(blocked.current?.corrections?.corrections.releaseName.Edition).toBe(
          "Collector Edition",
        );
        const restored = waitForAppMethod(page, "GetActiveInput");
        await page.reload();
        const reloaded = await activeInputFromResponse(await restored);
        expect(reloaded.current?.release ?? null).toBeNull();
        expect(reloaded.current?.workflow.requiredActions).toContainEqual(action);
        await expect(review).toBeVisible();
        await expect(
          review.getByText("Source identity changed since this value was saved."),
        ).toHaveCount(affectedFields.length);
        await expect(review.getByRole("group", { name: "Edition", exact: true })).toHaveCount(0);
        await expect(page.getByRole("button", { name: "Dupe Check", exact: true })).toBeDisabled();
        const apply = review.getByRole("button", { name: "Apply and continue", exact: true });
        await expect(apply).toBeDisabled();
        if (resolution === "individual") {
          const alternate = review.getByRole("group", { name: "Alternate title", exact: true });
          await expect(alternate.getByText("Example AKA", { exact: true })).toBeVisible();
          await alternate.getByRole("button", { name: "Keep saved value", exact: true }).click();
          await review
            .getByRole("textbox", { name: "Edit Original language", exact: true })
            .fill("Japanese");
          await review
            .getByRole("group", { name: "Genres", exact: true })
            .getByRole("button", { name: "Use automatic value", exact: true })
            .click();
        } else {
          await review
            .getByRole("button", {
              name: "Use automatic values for all affected fields",
              exact: true,
            })
            .click();
        }
        const synchronized = waitForAppMethod(page, "GetActiveInput");
        await page.evaluate(() => window.dispatchEvent(new Event("focus")));
        expect((await synchronized).ok()).toBe(true);
        await page.evaluate(
          () =>
            new Promise<void>((resolve) =>
              requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
            ),
        );
        await expect(
          review.getByText("Choose how to resolve this saved value.", { exact: true }),
        ).toHaveCount(0);
        await expect(apply).toBeEnabled();
        const continued = waitForAppMethod(page, "OpenActiveInput");
        await apply.click();
        const continuedResponse = await continued;
        expect(continuedResponse.ok(), app!.output()).toBe(true);
        const command = workflowCommandBody(
          "OpenActiveInput",
          continuedResponse.request().postDataJSON(),
        );
        expect((command.intent as { correctionPatch: unknown }).correctionPatch).toEqual({
          values: {
            Identity: {},
            ReleaseName: {},
            Metadata: resolution === "individual" ? { OriginalLanguage: "Japanese" } : {},
          },
          resetFields:
            resolution === "individual"
              ? [{ field: "metadata.genres" }]
              : affectedFields.map((field) => ({ field })),
          confirmFields: resolution === "individual" ? [{ field: "metadata.alternate_title" }] : [],
          expectedRevision: blocked.current?.corrections?.revision,
        });
        snapshot = await waitForMetadataReady(page, app!.url);
        await expect(review).toHaveCount(0);
        expect(snapshot.current?.workflow.id).toBe(blocked.current?.workflow.id);
        expect(snapshot.current?.corrections?.corrections.staleContentFields ?? []).toEqual([]);
        const expectedMetadata = {
          AlternateTitle: resolution === "individual" ? "Example AKA" : null,
          OriginalLanguage: resolution === "individual" ? "Japanese" : null,
          Genres: null,
        };
        expect(snapshot.current?.corrections?.corrections.metadata).toMatchObject(expectedMetadata);
        expect(snapshot.current?.factInstructions?.instructions.Metadata).toMatchObject(
          expectedMetadata,
        );
        expect(snapshot.current?.corrections?.corrections.releaseName.Edition).toBe(
          "Collector Edition",
        );
        expect(snapshot.current?.factInstructions?.instructions.ReleaseName.Edition).toBe(
          "Collector Edition",
        );
      });
    }
    await page.getByRole("button", { name: "History", exact: true }).click();
    await expect(page.getByText(workspace.sourcePath, { exact: true })).toBeVisible();
    await expect(page.getByText("No stored releases found.")).toHaveCount(0);
    expect(workspace.fake.counters.trackerUploads).toBe(0);
    expect(workspace.fake.counters.imageUploads).toBe(0);
    expect(workspace.fake.counters.clientInjections).toBe(0);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded web persists Release Details audio track language edits, clear and Auto", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace({ audioAnalysis: true });
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    let snapshot = await fetchMetadata(page, app.url, workspace.sourcePath);
    const track = snapshot.current?.release?.release.Media.Tracks.find(
      (value) => value.Kind === "audio",
    );
    if (!track) throw new Error("audio fixture did not expose an inspected track");
    const initialCounters = { ...workspace.fake.counters };
    await page.getByText("Edit Release Details", { exact: true }).click();
    const tracks = page.getByTestId("input-track-coverage");
    await tracks.locator("summary").click();
    const row = tracks.locator(`[data-track-id="${track.ID}"]`);
    const languages = row.getByRole("textbox", { name: "Audio track 1 languages", exact: true });
    for (const value of [["Japanese", "Spanish"], ["French"], []]) {
      await languages.fill(value.join(", "));
      await expect(row.getByText("Manual change pending", { exact: true })).toBeVisible();
      const saved = waitForAppMethod(page, "OpenActiveInput");
      await page.getByRole("button", { name: "Refresh metadata" }).click();
      const response = await saved;
      expect(response.ok()).toBe(true);
      const command = workflowCommandBody("OpenActiveInput", response.request().postDataJSON());
      const expected = [
        { trackId: track.ID, manifestFingerprint: track.ManifestFingerprint, languages: value },
      ];
      expect((command.intent as { correctionPatch: unknown }).correctionPatch).toEqual({
        values: { Identity: {}, ReleaseName: {}, Metadata: { TrackLanguages: expected } },
        resetFields: [],
        confirmFields: [],
        expectedRevision: snapshot.current?.corrections?.revision ?? 0,
      });
      snapshot = await waitForMetadataReady(page, app.url);
      expect(snapshot.current?.corrections?.corrections.metadata.TrackLanguages).toEqual(expected);
      expect(snapshot.current?.factInstructions?.instructions.Metadata.TrackLanguages).toEqual(
        expected,
      );
      await expect(row.getByText("Manual value · Applied", { exact: true })).toBeVisible();
      const restored = waitForAppMethod(page, "GetActiveInput");
      await page.reload();
      snapshot = await activeInputFromResponse(await restored);
      expect(snapshot.current?.corrections?.corrections.metadata.TrackLanguages).toEqual(expected);
      expect(snapshot.current?.factInstructions?.instructions.Metadata.TrackLanguages).toEqual(
        expected,
      );
      await page.getByText("Edit Release Details", { exact: true }).click();
      await tracks.locator("summary").click();
      await expect(languages).toHaveValue(value.join(", "));
      await expect(row.getByText("Manual value · Applied", { exact: true })).toBeVisible();
    }
    await row.getByRole("button", { name: "Auto Audio track 1 languages", exact: true }).click();
    await expect(row.getByText("Auto reset pending", { exact: true })).toBeVisible();
    const reset = waitForAppMethod(page, "OpenActiveInput");
    await page.getByRole("button", { name: "Refresh metadata" }).click();
    const response = await reset;
    expect(response.ok()).toBe(true);
    const command = workflowCommandBody("OpenActiveInput", response.request().postDataJSON());
    expect((command.intent as { correctionPatch: unknown }).correctionPatch).toEqual({
      values: { Identity: {}, ReleaseName: {}, Metadata: {} },
      resetFields: [{ field: "metadata.track_languages", trackId: track.ID }],
      confirmFields: [],
      expectedRevision: snapshot.current?.corrections?.revision ?? 0,
    });
    snapshot = await waitForMetadataReady(page, app.url);
    expect(snapshot.current?.corrections?.corrections.metadata.TrackLanguages ?? []).toEqual([]);
    expect(snapshot.current?.factInstructions?.instructions.Metadata.TrackLanguages ?? []).toEqual(
      [],
    );
    const restored = waitForAppMethod(page, "GetActiveInput");
    await page.reload();
    snapshot = await activeInputFromResponse(await restored);
    expect(snapshot.current?.corrections?.corrections.metadata.TrackLanguages ?? []).toEqual([]);
    await page.getByText("Edit Release Details", { exact: true }).click();
    await tracks.locator("summary").click();
    await expect(row.getByText("Automatic value", { exact: true })).toBeVisible();
    await expect(languages).toHaveValue(track.Languages.join(", "));
    expect(workspace.fake.counters).toEqual(initialCounters);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded web applies Release Details source options and persists source IDs", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    const sourceOptions = page.getByTestId("input-source-options");
    const sourceID = sourceOptions.getByRole("textbox", { name: "BTN source ID", exact: true });
    const client = sourceOptions.getByRole("textbox", { name: "Client search name", exact: true });
    const skip = sourceOptions.getByRole("checkbox", { name: "Skip client search", exact: true });
    for (const values of [
      { sourceID: "12345", policy: true, skip: true, client: "example-client" },
      { sourceID: "23456", policy: false, skip: false, client: "second-client" },
      { sourceID: "", policy: true, skip: true, client: "" },
    ]) {
      await page.getByText("Edit Release Details", { exact: true }).click();
      await sourceOptions.locator("summary").click();
      await sourceID.fill(values.sourceID);
      for (const label of ["Keep source folder", "Keep images", "Only resolve IDs"]) {
        await sourceOptions
          .getByRole("checkbox", { name: label, exact: true })
          .setChecked(values.policy);
      }
      await client.fill(values.client);
      await skip.setChecked(values.skip);
      await expectEnabledState(client, !values.skip);
      const previousCounters = { ...workspace.fake.counters };
      const saved = waitForAppMethod(page, "OpenActiveInput");
      await page.getByRole("button", { name: "Refresh metadata" }).click();
      const response = await saved;
      expect(response.ok()).toBe(true);
      const command = workflowCommandBody("OpenActiveInput", response.request().postDataJSON());
      expect(command.goal).toBe("input_ready");
      expect(command.intent).toHaveProperty(
        "preparation.Instructions.TrackerIDs",
        values.sourceID ? { BTN: values.sourceID } : {},
      );
      expect(command.intent).toHaveProperty("preparation.Policy", {
        KeepFolder: values.policy,
        KeepImages: values.policy,
        OnlyID: values.policy,
      });
      expect(command.intent).toHaveProperty(
        "preparation.Search",
        values.client ? { Skip: values.skip, Client: values.client } : { Skip: values.skip },
      );
      const current = await waitForMetadataReady(page, app.url);
      expect(current.current?.factInstructions?.instructions.TrackerIDs?.BTN ?? "").toBe(
        values.sourceID,
      );
      const restored = waitForAppMethod(page, "GetActiveInput");
      await page.reload();
      const snapshot = await activeInputFromResponse(await restored);
      expect(snapshot.current?.factInstructions?.instructions.TrackerIDs?.BTN ?? "").toBe(
        values.sourceID,
      );
      await page.getByText("Edit Release Details", { exact: true }).click();
      await sourceOptions.locator("summary").click();
      await expect(sourceID).toHaveValue(values.sourceID);
      // Preparation/search options are per-command choices; source IDs are durable facts.
      for (const label of [
        "Keep source folder",
        "Keep images",
        "Only resolve IDs",
        "Skip client search",
      ]) {
        await expect(
          sourceOptions.getByRole("checkbox", { name: label, exact: true }),
        ).not.toBeChecked();
      }
      await expect(client).toHaveValue("");
      await sourceOptions.locator("summary").click();
      await page.getByText("Edit Release Details", { exact: true }).click();
      if (values.skip)
        expect(workspace.fake.counters.clientSearches).toBe(previousCounters.clientSearches);
      expect(workspace.fake.counters.trackerUploads).toBe(0);
      expect(workspace.fake.counters.imageUploads).toBe(0);
      expect(workspace.fake.counters.clientInjections).toBe(0);
    }
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded web distinguishes a cleared Release Details provider ID from Auto", async ({
  page,
}) => {
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
    await expect(malRow.getByText("Manual value · Applied", { exact: true })).toBeVisible();

    const restored = waitForAppMethod(page, "GetActiveInput");
    await page.reload();
    await expect((await restored).ok()).toBe(true);
    await page.getByText("Edit Release Details", { exact: true }).click();
    await expect(malInput).toHaveValue("");
    await expect(malRow.getByText("Manual value · Applied", { exact: true })).toBeVisible();

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

test("embedded web distinguishes pending Release Details edits and Auto resets through refresh", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByText("Edit Release Details", { exact: true }).click();
    const row = page.locator('[data-correction-field="metadata.original_language"]');
    const language = page.getByRole("textbox", { name: "Original language", exact: true });
    const automaticLanguage = await language.inputValue();
    const manualLanguage = automaticLanguage === "Spanish" ? "Japanese" : "Spanish";
    await language.fill(manualLanguage);
    await expect(row.getByText("Manual change pending", { exact: true })).toBeVisible();
    const pending = page.getByRole("status").filter({ hasText: "Metadata changes are pending" });
    await expect(pending).toBeVisible();
    await page.getByText("Edit Release Details", { exact: true }).click();
    await expect(pending).toBeVisible();
    await page.getByText("Edit Release Details", { exact: true }).click();
    const apply = waitForAppMethod(page, "OpenActiveInput");
    await page.getByRole("button", { name: "Refresh metadata" }).click();
    await expect((await apply).ok()).toBe(true);
    await waitForMetadataReady(page, app.url);
    await expect(row.getByText("Manual value · Applied", { exact: true })).toBeVisible();
    await expect(language).toHaveValue(manualLanguage);
    await expect(pending).toHaveCount(0);
    await page.getByRole("button", { name: "Auto Original language" }).click();
    await expect(row.getByText("Auto reset pending", { exact: true })).toBeVisible();
    await expect(row.getByText("Automatic value", { exact: true })).toHaveCount(0);
    const tmdb = page.getByRole("textbox", { name: "TMDB ID", exact: true });
    const automaticID = await tmdb.inputValue();
    for (const invalid of ["invalid", "invalid-again"]) {
      await tmdb.fill(invalid);
      await expect(tmdb).toHaveAttribute("aria-invalid", "true");
      await page.getByRole("button", { name: "Auto TMDB ID", exact: true }).click();
      await expect(tmdb).toHaveValue(automaticID);
      await expect(tmdb).toHaveAttribute("aria-invalid", "false");
    }
    const reset = waitForAppMethod(page, "OpenActiveInput");
    await page.getByRole("button", { name: "Refresh metadata" }).click();
    await expect((await reset).ok()).toBe(true);
    await waitForMetadataReady(page, app.url);
    await expect(row.getByText("Automatic value", { exact: true })).toBeVisible();
    await expect(language).toHaveValue(automaticLanguage);
    await expect(pending).toHaveCount(0);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded web retains Release Details corrections without downstream workflow effects", async ({
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
    const categoryInput = page.getByRole("combobox", { name: "Category", exact: true });
    await categoryInput.selectOption("tv");
    await expect(page.getByRole("spinbutton", { name: "Manual year", exact: true })).toBeDisabled();
    await categoryInput.selectOption("movie");
    await expect(page.getByRole("spinbutton", { name: "Manual year", exact: true })).toBeEditable();
    await page.getByRole("button", { name: "Auto Category", exact: true }).click();
    await expect(page.getByRole("combobox", { name: "Category", exact: true })).not.toHaveValue("");
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
    await page
      .getByRole("button", {
        name: /^Select candidate \d+: Example Release 2026 Standard Edition$/,
      })
      .click();
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
    await page.getByRole("button", { name: "Dupe Check", exact: true }).click();
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
    await expect(page.getByRole("button", { name: "Screenshots", exact: true })).toBeEnabled();
    await expect(page.getByRole("progressbar")).toHaveCount(0);
    await expect.poll(() => workspace.fake.counters.clientSearches).toBe(1);
    await page.reload();
    await page.getByRole("button", { name: "Dupe Check", exact: true }).click();
    await expect(page.getByText("HDS").first()).toBeVisible();
    await expect(page.getByRole("button", { name: "Run dupe check" })).toBeEnabled();

    const mediaPlanResponse = page.waitForResponse((response) =>
      response.url().includes("/api/app/GetReleaseWorkflowMediaPlan"),
    );
    await page.getByRole("button", { name: "Screenshots", exact: true }).click();
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
    await generated.getByRole("button", { name: "Delete Screenshot 1" }).click();
    await expect(page.getByAltText("Screenshot 1")).toHaveCount(0);
    await page.getByRole("button", { name: "Generate screenshots" }).click();
    await expect(page.getByAltText("Screenshot 1")).toBeVisible();
    await page.reload();
    await expect(page.getByRole("button", { name: "Screenshots", exact: true })).toBeEnabled();
    await page.getByRole("button", { name: "Screenshots", exact: true }).click();
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

    await page.getByRole("button", { name: "Descriptions", exact: true }).click();
    await page.getByRole("button", { name: "Refresh descriptions" }).click();
    await page.getByRole("button", { name: "Expand" }).click();
    await expect(page.getByRole("textbox")).toHaveValue("E2E description fixture.");
    await expect(page.getByText("E2E description fixture.").first()).toBeVisible();
    await page.reload();
    await page.getByRole("button", { name: "Descriptions", exact: true }).click();
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
      .toBe(effectsAfterUpload.clientSearches);
    expect(workspace.fake.counters.imageUploads).toBe(effectsAfterUpload.imageUploads);
    expect(workspace.fake.counters.trackerUploads).toBe(1);
    expect(workspace.fake.counters.clientInjections).toBe(effectsAfterUpload.clientInjections);

    await page.getByRole("button", { name: "Dupe Check", exact: true }).click();
    // Page-open discovery excludes confirmed submissions without another duplicate search.
    await expect(
      page.getByText("All selected trackers were already uploaded. No upload is needed."),
    ).toBeVisible();
    await expect(page.getByRole("button", { name: "Run dupe check" })).toBeDisabled();
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
    expect(workspace.fake.counters).toEqual(effectsAfterUpload);
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
    const reuseResponse = waitForAppMethod(page, "UploadReleaseWorkflowImages");
    await page.getByRole("button", { name: "Prepare required hosts (3)" }).click();
    const reuseAccepted = await reuseResponse;
    expect(reuseAccepted.ok()).toBe(true);
    const reuseStarted = (await reuseAccepted.json()) as ReleaseWorkflowCurrent;
    expect(reuseStarted.operation?.id).toBeTruthy();
    const activeInputURL = new URL("api/app/GetActiveInput", app.url).toString();
    await expect
      .poll(async () => {
        const response = await page.request.get(activeInputURL);
        const current = await activeCurrentFromResponse(response);
        expect(current.workflow.id).toBe(reuseStarted.workflow.id);
        expect(current.operation?.id).toBe(reuseStarted.operation?.id);
        return current.operation?.status;
      })
      .toBe("completed");
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

test("embedded HDR description opens a full-size lightbox without a saved preview map", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace);
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByRole("button", { name: "Dupe Check", exact: true }).click();
    await page.getByRole("checkbox", { name: releaseWorkflowParityFixture.trackerID }).uncheck();
    await page.getByRole("checkbox", { name: "HDS" }).check();
    await runDuplicateCheck(page);
    await page.getByRole("button", { name: "Screenshots", exact: true }).click();
    await page.getByRole("button", { name: "Generate screenshots" }).click();
    await expect(page.getByText("1 captured screenshot(s)")).toBeVisible();
    await page.getByRole("button", { name: "Upload Images", exact: true }).click();
    await page.getByRole("button", { name: "Prepare required hosts (1)" }).click();
    await expect(page.getByText("1 saved")).toBeVisible();
    await page.getByRole("button", { name: "Descriptions", exact: true }).click();
    await page.getByRole("button", { name: "Refresh descriptions" }).click();
    await page.getByRole("button", { name: "Expand" }).click();
    const url = "https://images.example.invalid/hdr.png";
    await page.route(url, (route) =>
      route.fulfill({
        contentType: "image/svg+xml",
        body: '<svg xmlns="http://www.w3.org/2000/svg" width="3000" height="1200"><rect width="100%" height="100%" fill="#5975a9"/></svg>',
      }),
    );
    await page.getByRole("textbox").fill(`[spoiler=source_hdr][img]${url}[/img][/spoiler]`);
    await page.getByRole("button", { name: "Render HDS", exact: true }).click();
    const preview = page
      .getByRole("heading", { name: "Rendered Raw Preview" })
      .locator("../..")
      .locator(".tracker-description.rendered");
    await preview.locator("summary").click();
    for (const mode of ["light", "dark"] as const) {
      await page.evaluate((mode) => {
        const next = JSON.stringify({ version: 1, theme: "minimal", mode, accents: {} });
        localStorage.setItem("upbrr:appearance:v1", next);
        dispatchEvent(
          new StorageEvent("storage", {
            key: "upbrr:appearance:v1",
            newValue: next,
            storageArea: localStorage,
          }),
        );
      }, mode);
      for (const width of [1280, 390]) {
        await page.setViewportSize({ width, height: 900 });
        await preview.getByRole("img").click();
        const dialog = page.getByRole("dialog", { name: "Description image" });
        const image = dialog.getByRole("img");
        await expect(image).toHaveAttribute("src", url);
        await expect
          .poll(() =>
            image.evaluate((image: HTMLImageElement) => ({
              width: image.naturalWidth,
              height: image.naturalHeight,
            })),
          )
          .toEqual({ width: 3000, height: 1200 });
        const box = await image.boundingBox();
        expect(box?.width).toBe(3000);
        expect(box?.height).toBe(1200);
        await dialog.getByRole("button", { name: "Close image preview" }).click();
        await expect(dialog).toHaveCount(0);
      }
    }
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("embedded web restores edited descriptions after reopening an input", async ({ page }) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    await page.clock.install();
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
    await page.route("https://img.example/**", (route) =>
      route.fulfill({
        contentType: "image/svg+xml",
        body: '<svg xmlns="http://www.w3.org/2000/svg" width="600" height="300"><rect width="600" height="300" fill="#5975a9"/></svg>',
      }),
    );
    const editedDescription = `[center][img=300]https://img.example/cover.svg[/img][/center]
[center][url=https://img.example/shot][img=500]https://img.example/shot.svg[/img][/url][/center]
<blockquote>HTML and [b]custom notes[/b] together.</blockquote>
[right][url=https://github.com/autobrr/upbrr]Uploaded by upbrr[/url][/right]`;
    await page.getByRole("textbox").fill(editedDescription);
    const refreshed = waitForAppMethod(page, "GetActiveInput");
    await page.clock.runFor(15_000);
    expect((await refreshed).ok()).toBe(true);
    await expect(page.getByRole("textbox")).toHaveValue(editedDescription);
    await page.getByRole("button", { name: "Render" }).click();
    const renderedPreview = page
      .getByRole("heading", { name: "Rendered Raw Preview" })
      .locator("../..")
      .locator(".tracker-description.rendered");
    await expect(renderedPreview.locator('[style*="text-align: center"] img')).toHaveCount(2);
    await expect(renderedPreview.locator("blockquote b")).toHaveText("custom notes");
    await expect(renderedPreview.locator('[style*="text-align: right"]')).toContainText(
      "Uploaded by upbrr",
    );
    await expect
      .poll(() =>
        renderedPreview
          .locator("img")
          .evaluateAll((images) =>
            images.every((image) => (image as HTMLImageElement).naturalWidth > 0),
          ),
      )
      .toBe(true);
    for (const [theme, mode] of [
      ["minimal", "light"],
      ["swizzin", "dark"],
    ] as const) {
      await page.evaluate(
        ({ theme, mode }) => {
          const next = JSON.stringify({ version: 1, theme, mode, accents: {} });
          localStorage.setItem("upbrr:appearance:v1", next);
          dispatchEvent(
            new StorageEvent("storage", {
              key: "upbrr:appearance:v1",
              newValue: next,
              storageArea: localStorage,
            }),
          );
        },
        { theme, mode },
      );
      await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
      await expect(page.locator("html")).toHaveClass(new RegExp(mode));
      for (const width of [1280, 390]) {
        await page.setViewportSize({ width, height: 900 });
        await expect(renderedPreview.locator("img")).toHaveCount(2);
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
          `${theme} ${mode} ${width} description overflow`,
        ).toBeLessThanOrEqual(0);
      }
    }
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
    await expect(
      page
        .getByRole("heading", { name: "Rendered Raw Preview" })
        .locator("../..")
        .locator("blockquote b"),
    ).toHaveText("custom notes");
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
    const checked = await runDuplicateCheck(page, "blocked");
    const ptp = checked.projections?.projections.find(
      (projection) => projection.trackerId === "PTP",
    );
    expect(ptp).toMatchObject({ readiness: "ready", dupeReady: true });
    expect(ptp?.questionnaire ?? []).toHaveLength(0);
    await expect(page.getByRole("button", { name: "Apply tracker answers" })).toHaveCount(0);
    expect(checked.dupes?.results.map((result) => result.trackerId).sort()).toEqual(["HDS", "PTP"]);
    expect(checked.dupes?.results.find((result) => result.trackerId === "PTP")).toMatchObject({
      status: "blocked",
      decision: "pending",
      matches: [
        {
          name: "Example.Show.S01E01.1080p.WEB-DL.DV-GRP",
          relation: "manual_review",
        },
      ],
    });

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
    const bdInfoPanel = page.getByText("BDInfo Preview", { exact: true }).locator("..");
    await bdInfoPanel.locator("summary").first().click();
    await expect(bdInfoPanel.locator("pre")).toContainText("Disc 1");
    await expect(bdInfoPanel.locator("pre")).toContainText("Disc 2");
    await expect(bdInfoPanel).not.toContainText("MediaInfo");
    await page.setViewportSize({ width: 390, height: 844 });
    await page.evaluate(() => {
      const next = JSON.stringify({ version: 1, theme: "swizzin", mode: "dark", accents: {} });
      localStorage.setItem("upbrr:appearance:v1", next);
      dispatchEvent(
        new StorageEvent("storage", {
          key: "upbrr:appearance:v1",
          newValue: next,
          storageArea: localStorage,
        }),
      );
    });
    await expect(page.locator("html")).toHaveAttribute("data-theme", "swizzin");
    await expect(page.locator("html")).toHaveClass(/dark/);
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
    ).toBeLessThanOrEqual(0);
    await page.setViewportSize({ width: 1280, height: 720 });
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
    await expect(page.getByRole("button", { name: "Refresh descriptions" })).toBeEnabled();
    await page.getByRole("button", { name: "Expand" }).click();
    await expect(page.getByRole("textbox")).toHaveValue("E2E description fixture.");
    await expect(page.getByText("Action required")).toHaveCount(0);

    await page.getByRole("button", { name: "Menu Images" }).click();
    const captureResponse = page.waitForResponse(
      (response) =>
        response.url().includes("/api/app/ContinueReleaseWorkflow") &&
        response.request().postDataJSON()?.goal === "media_ready",
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
    await expect(page.getByRole("button", { name: "Refresh descriptions" })).toBeEnabled();
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

for (const size of [
  { width: 1800, height: 100 },
  { width: 100, height: 1800 },
  { width: 1800, height: 1400 },
  { width: 64, height: 48 },
]) {
  test(`embedded image lightbox preserves ${size.width}x${size.height} pixels across viewport changes`, async ({
    page,
  }) => {
    const workspace = await createE2EWorkspace();
    let app: AppServer | undefined;
    try {
      app = await startApp(workspace);
      await fetchMetadata(page, app.url, workspace.sourcePath);
      await page.getByRole("button", { name: "Dupe Check", exact: true }).click();
      await page.getByRole("checkbox", { name: releaseWorkflowParityFixture.trackerID }).uncheck();
      await page.getByRole("checkbox", { name: "HDS" }).check();
      await runDuplicateCheck(page);
      await page.getByRole("button", { name: "Screenshots", exact: true }).click();
      await page.route("**/api/app/release-workflow-media?*", (route) =>
        route.fulfill({
          contentType: "image/svg+xml",
          body: `<svg xmlns="http://www.w3.org/2000/svg" width="${size.width}" height="${size.height}"><rect width="100%" height="100%" fill="#537da8"/></svg>`,
        }),
      );

      await page.getByRole("button", { name: "Generate screenshots" }).click();
      const thumbnail = page.getByRole("img", { name: "Screenshot 1", exact: true });
      await expect(thumbnail).toBeVisible();
      await thumbnail.click();
      const dialog = page.getByRole("dialog", { name: "Screenshot 1" });
      const image = dialog.getByRole("img");
      const scrollArea = dialog.getByRole("region", { name: "Image", exact: true });
      const close = dialog.getByRole("button", { name: "Close image preview" });
      await expect
        .poll(() =>
          image.evaluate((img: HTMLImageElement) => ({
            width: img.naturalWidth,
            height: img.naturalHeight,
          })),
        )
        .toEqual(size);

      for (const viewport of [
        { width: 1280, height: 900 },
        { width: 390, height: 640 },
      ]) {
        await page.setViewportSize(viewport);
        await page.emulateMedia({ colorScheme: viewport.width === 390 ? "dark" : "light" });
        await expect
          .poll(async () => {
            const box = await dialog.boundingBox();
            return box && { width: box.width, height: box.height, x: box.x, y: box.y };
          })
          .toEqual({ width: viewport.width - 16, height: viewport.height - 16, x: 8, y: 8 });
        const imageBox = await image.boundingBox();
        expect(imageBox?.width).toBe(size.width);
        expect(imageBox?.height).toBe(size.height);
        await scrollArea.evaluate((element) => element.scrollTo(0, 0));
        const start = await scrollArea.evaluate((element) => {
          const box = element.getBoundingClientRect();
          const img = element.querySelector("img")!.getBoundingClientRect();
          return { left: img.left - box.left, top: img.top - box.top };
        });
        expect(start.left).toBeGreaterThanOrEqual(0);
        expect(start.top).toBe(0);
        const overflow = await scrollArea.evaluate((element) => ({
          x: element.scrollWidth > element.clientWidth,
          y: element.scrollHeight > element.clientHeight,
          width: element.clientWidth,
          height: element.clientHeight,
        }));
        expect(overflow.x).toBe(size.width > overflow.width);
        expect(overflow.y).toBe(size.height > overflow.height);
        if (overflow.y) {
          await scrollArea.focus();
          await scrollArea.press("ArrowDown");
          await expect
            .poll(() => scrollArea.evaluate((element) => element.scrollTop))
            .toBeGreaterThan(0);
        }
        await scrollArea.evaluate((element) =>
          element.scrollTo(element.scrollWidth, element.scrollHeight),
        );
        const end = await scrollArea.evaluate((element) => {
          const box = element.getBoundingClientRect();
          const img = element.querySelector("img")!.getBoundingClientRect();
          return {
            right: img.right - (box.left + element.clientWidth),
            bottom: img.bottom - (box.top + element.clientHeight),
          };
        });
        expect(end.right).toBeLessThanOrEqual(1);
        expect(end.bottom).toBeLessThanOrEqual(1);
        await expect(close).toBeInViewport();
      }
      await close.click();
      await expect(dialog).toHaveCount(0);
      await thumbnail.click();
      await scrollArea.focus();
      await expect
        .poll(() =>
          scrollArea.evaluate((element) => ({ x: element.scrollLeft, y: element.scrollTop })),
        )
        .toEqual({ x: 0, y: 0 });
      await scrollArea.press("Escape");
      await expect(dialog).toHaveCount(0);
    } finally {
      await app?.stop();
      await workspace.cleanup();
    }
  });
}
