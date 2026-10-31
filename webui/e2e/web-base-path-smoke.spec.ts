// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { expect } from "@playwright/test";
import { readFile } from "node:fs/promises";
import path from "node:path";
import {
  createE2EAPIToken,
  createE2EWorkspace,
  fetchMetadata,
  repoRoot,
  startApp,
  test,
  type AppServer,
} from "./helpers/e2eHarness";
import { ReleaseWorkflowV1Client, type WorkflowV1Current } from "./helpers/releaseWorkflowV1Client";

test("embedded web serves UI, API, assets, manifest, and events under a base path", async ({
  page,
  request,
}) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;

  try {
    app = await startApp(workspace, { baseURL: "/upbrr/" });
    const origin = new URL(app.url).origin;
    const requestPaths: string[] = [];
    page.on("request", (req) => requestPaths.push(new URL(req.url()).pathname));

    const authStatus = page.waitForResponse((response) => {
      const url = new URL(response.url());
      return url.pathname === "/upbrr/api/auth/status" && response.ok();
    });
    const eventRequest = page.waitForRequest((req) => {
      const url = new URL(req.url());
      return url.pathname === "/upbrr/api/events";
    });

    await page.goto(app.url);
    await authStatus;
    await expect(page.getByRole("heading", { name: "Build Release Name" })).toBeVisible();

    // Direct release workflows no longer subscribe to legacy job events. Open
    // the logging page, the remaining event-stream consumer, before asserting
    // base-path routing for the stream.
    await page.getByRole("button", { name: "Logging" }).click();
    await eventRequest;

    const apiAuthStatus = await request.get(`${origin}/upbrr/api/auth/status`);
    expect(apiAuthStatus.ok()).toBe(true);
    const { csrfToken } = (await apiAuthStatus.json()) as { csrfToken: string };
    const defaultConfig = await request.post(`${origin}/upbrr/api/app/GetDefaultConfig`, {
      data: {},
      headers: { Origin: origin, "X-CSRF-Token": csrfToken },
    });
    expect(defaultConfig.ok()).toBe(true);
    expect(await defaultConfig.json()).toBeTruthy();

    const manifest = await request.get(`${origin}/upbrr/site.webmanifest`);
    expect(manifest.ok()).toBe(true);
    const manifestBody = await manifest.text();
    expect(manifestBody).toContain("/upbrr/");

    const rootAuth = await request.get(`${origin}/api/auth/status`);
    expect(rootAuth.status()).toBe(404);

    expect(requestPaths).toContain("/upbrr/api/auth/status");
    expect(requestPaths).toContain("/upbrr/appearance-bootstrap.js");
    expect(requestPaths).toContain("/upbrr/api/events");
    expect(requestPaths.some((path) => path.startsWith("/upbrr/assets/"))).toBe(true);
    expect(
      requestPaths.filter(
        (path) =>
          (path.startsWith("/api/") ||
            path.startsWith("/assets/") ||
            path === "/site.webmanifest") &&
          !path.startsWith("/upbrr/"),
      ),
    ).toEqual([]);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("direct UI routes reload and browser history work at root and under a base path", async ({
  page,
  request,
}) => {
  const uiRoutes = JSON.parse(
    await readFile(
      path.join(repoRoot, "internal", "webserver", "testdata", "ui-route-paths.json"),
      "utf8",
    ),
  ) as string[];
  for (const baseURL of ["/", "/upbrr/"]) {
    const workspace = await createE2EWorkspace();
    let app: AppServer | undefined;
    try {
      app = await startApp(workspace, { baseURL });
      for (const route of uiRoutes) {
        await page.goto(`${app.url}${route.slice(1)}`);
        await expect(page).toHaveURL(new RegExp(`${baseURL}${route.slice(1)}$`));
        await expect(page.getByRole("main")).toBeVisible();
      }
      await page.goto(`${app.url}settings`);
      await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();
      await page.reload();
      await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();
      await page.getByRole("button", { name: "History" }).click();
      await expect(page).toHaveURL(new RegExp(`${baseURL}history$`));
      await page.goBack();
      await expect(page).toHaveURL(new RegExp(`${baseURL}settings$`));
      await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();

      await page.goto(`${app.url}upload`);
      await expect(page.getByRole("heading", { name: "View unavailable" })).toBeVisible();
      await expect(page.getByRole("button", { name: "Open Input" })).toBeVisible();
      await page.reload();
      await expect(page.getByRole("heading", { name: "View unavailable" })).toBeVisible();

      const origin = new URL(app.url).origin;
      for (const missing of ["assets/missing.js", "assets/missing.css", "unknown"]) {
        expect((await request.get(`${origin}${baseURL}${missing}`)).status()).toBe(404);
      }
    } finally {
      await app?.stop();
      await workspace.cleanup();
    }
  }
});

test("authenticated deep link and API remain scoped to the base path across session loss", async ({
  page,
  context,
}) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace, { baseURL: "/upbrr/", devNoAuth: false });
    const origin = new URL(app.url).origin;
    await page.goto(`${app.url}settings`);
    await expect(page.getByRole("heading", { name: "Sign In" })).toBeVisible();
    await page.getByLabel("Username").fill("e2e-user");
    await page.getByLabel("Password").fill("synthetic-e2e-password");
    await page.getByRole("button", { name: "Sign In" }).click();
    await expect(page.getByRole("heading", { name: "Set Browse Access" })).toBeVisible();
    await page.getByLabel("Browse root").fill(workspace.root);
    await page.getByRole("button", { name: "Continue" }).click();
    await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();
    await expect(page).toHaveURL(`${origin}/upbrr/settings`);

    const authStatus = await context.request.get(`${origin}/upbrr/api/auth/status`);
    expect(authStatus.ok()).toBe(true);
    const { csrfToken } = (await authStatus.json()) as { csrfToken: string };
    const config = await context.request.post(`${origin}/upbrr/api/app/GetDefaultConfig`, {
      data: {},
      headers: { Origin: origin, "X-CSRF-Token": csrfToken },
    });
    expect(config.ok()).toBe(true);
    expect((await context.request.get(`${origin}/api/auth/status`)).status()).toBe(404);

    const other = await context.newPage();
    await other.goto(app.url);
    await other.getByRole("button", { name: "Logout" }).click();
    await expect(other.getByRole("heading", { name: "Sign In" })).toBeVisible();
    await other.close();

    await page.reload();
    await expect(page.getByRole("heading", { name: "Sign In" })).toBeVisible();
    await expect(page).toHaveURL(`${origin}/upbrr/settings`);
    const afterLogout = await context.request.post(`${origin}/upbrr/api/app/GetDefaultConfig`, {
      data: {},
      headers: { Origin: origin, "X-CSRF-Token": csrfToken },
    });
    expect(afterLogout.status()).toBe(401);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("HDR analysis blocks unconfirmed input after reload under a base path", async ({ page }) => {
  const workspace = await createE2EWorkspace();
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace, { baseURL: "/upbrr/" });
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByRole("button", { name: "HDR Analysis", exact: true }).click();
    await expect(page.getByRole("heading", { name: "HDR metadata analysis" })).toBeVisible();
    await expect(page.getByRole("checkbox", { name: /Video 1/ })).toBeDisabled();
    await expect(page.getByRole("checkbox", { name: /Video 1/ })).not.toBeChecked();
    await expect(page.getByRole("checkbox", { name: /Video 1/ })).toHaveAccessibleName(
      /MediaInfo must identify a unique HEVC video track\./,
    );
    await expect(page.getByRole("button", { name: "Generate", exact: true })).toBeDisabled();
    await expect(page.getByRole("checkbox", { name: "Include in descriptions" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Retry", exact: true })).toBeDisabled();
    await page.reload();
    await page.getByRole("button", { name: "HDR Analysis", exact: true }).click();
    await expect(page.getByRole("checkbox", { name: /Video 1/ })).toBeDisabled();
    await expect(page.getByRole("button", { name: "Generate", exact: true })).toBeDisabled();
    await expect(page.getByRole("button", { name: "Retry", exact: true })).toBeDisabled();
    await expect(
      page.getByRole("button", { name: "Disable HDR inclusion", exact: true }),
    ).toHaveCount(0);
    await expect(
      page.getByText(
        "Native HDR plots, selected audio images, and amplitude statistics are included by default. Select picture-check images below to include them in descriptions.",
      ),
    ).toBeVisible();
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("dual HDR retains native plots and a report when a decoded check is unsupported", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace({ hdrAnalysis: true });
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace, { baseURL: "/upbrr/" });
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByRole("button", { name: "HDR Analysis", exact: true }).click();
    await expect(page.getByRole("checkbox", { name: /Video 1/ })).toBeChecked();
    await page.getByRole("combobox", { name: "Analysis mode", exact: true }).selectOption("manual");
    await page.getByRole("checkbox", { name: "Scene synchronization", exact: true }).check();
    await page.getByRole("button", { name: "Generate", exact: true }).click();
    const hdrImage = page.getByRole("img", { name: "Video 1 HDR10+ plot", exact: true });
    const dvImage = page.getByRole("img", { name: "Video 1 Dolby Vision plot", exact: true });
    await expect(hdrImage).toBeVisible({ timeout: 30_000 });
    await expect(dvImage).toBeVisible();
    await expect(page.getByText(/unsupported · Measured 0 \/ 0/)).toBeVisible();
    const includedDV = page.getByRole("checkbox", {
      name: "Include Video 1 Dolby Vision in descriptions",
      exact: true,
    });
    await expect(includedDV).toBeChecked();
    await includedDV.click();
    await expect(includedDV).not.toBeChecked();
    const reportLink = page.getByRole("link", { name: "Download structural report", exact: true });
    const href = await reportLink.getAttribute("href");
    const response = await page.context().request.get(new URL(href || "", app.url).toString());
    expect(response.ok()).toBe(true);
    expect(response.headers()["content-type"]).toContain("application/json");
    const report = await response.json();
    expect(report.formats.map((item: { state: string }) => item.state)).toEqual([
      "complete",
      "complete",
    ]);
    expect(report.measurements[0].result.state).toBe("unsupported");
    expect(report.status).toBe("partial");
    expect(report.decoded[0].state).toBe("unsupported");
    expect(JSON.stringify(report)).not.toContain(workspace.sourcePath);
    const downloadPromise = page.waitForEvent("download");
    await reportLink.click();
    expect((await downloadPromise).suggestedFilename()).toBe("hdr-analysis.json");
    await page.reload();
    await page.getByRole("button", { name: "HDR Analysis", exact: true }).click();
    await expect(hdrImage).toBeVisible();
    await expect(dvImage).toBeVisible();
    await expect(includedDV).not.toBeChecked();
    expect(workspace.fake.counters.imageUploads).toBe(0);
    expect(workspace.fake.counters.trackerUploads).toBe(0);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("automatic HDR and existing audio artifacts survive reload and input reopening under a base path", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace({ hdrAnalysis: "decoded" });
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace, { baseURL: "/upbrr/" });
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByRole("button", { name: "HDR Analysis", exact: true }).click();
    await page
      .getByRole("combobox", { name: "Analysis mode", exact: true })
      .selectOption("pictures_audio");
    await page.getByRole("button", { name: "Generate", exact: true }).click();
    const picture = page.getByRole("img", {
      name: "Video 1 Measured picture brightness plot",
      exact: true,
    });
    const waveform = page.getByRole("img", { name: "Video 1 Audio waveform plot", exact: true });
    const spectrogram = page.getByRole("img", {
      name: "Video 1 Audio spectrogram plot",
      exact: true,
    });
    await expect(picture).toBeVisible({ timeout: 30_000 });
    await expect(waveform).toBeVisible();
    await expect(spectrogram).toBeVisible();
    await expect(page.getByText(/complete · Measured 5 \/ 5/)).toBeVisible();
    const reportLink = page.getByRole("link", { name: "Download structural report", exact: true });
    const response = await page
      .context()
      .request.get(new URL((await reportLink.getAttribute("href")) || "", app.url).toString());
    expect(response.ok()).toBe(true);
    const report = await response.json();
    expect(report.measurements[0].automatic.frames).toHaveLength(5);
    expect(report.measurements[0].automatic.sampling.frames[0]).toBe(120);
    expect(report.measurements[1].audio.track.sampleFrames).toBe(230400);
    expect(JSON.stringify(report)).not.toContain(workspace.sourcePath);
    const statsLink = page.getByRole("link", { name: "Download audio stats", exact: true });
    const statsURL = new URL((await statsLink.getAttribute("href")) || "", app.url).toString();
    const stats = await page.context().request.get(statsURL);
    expect(stats.ok()).toBe(true);
    expect(stats.headers()["content-type"]).toBe("text/plain");
    expect(stats.headers()["content-disposition"]).toBe('attachment; filename="audio-stats.txt"');
    expect(await stats.text()).toContain("Num samples");
    await page.reload();
    await page.getByRole("button", { name: "HDR Analysis", exact: true }).click();
    await expect(picture).toBeVisible();
    await expect(waveform).toBeVisible();
    await expect(spectrogram).toBeVisible();
    expect((await page.context().request.get(statsURL)).ok()).toBe(true);
    await page.getByRole("button", { name: "Input", exact: true }).click();
    await page.getByRole("button", { name: "Close input", exact: true }).click();
    await expect(page.getByRole("button", { name: "Close input", exact: true })).toBeDisabled();
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByRole("button", { name: "HDR Analysis", exact: true }).click();
    await expect(picture).toBeVisible();
    await expect(waveform).toBeVisible();
    await expect(spectrogram).toBeVisible();
    await expect(page.getByText(/complete · Measured 5 \/ 5/)).toBeVisible();
    const reopenedStats = await page
      .context()
      .request.get(new URL((await statsLink.getAttribute("href")) || "", app.url).toString());
    expect(reopenedStats.ok()).toBe(true);
    expect(await reopenedStats.text()).toBe(await stats.text());
    expect(workspace.fake.counters.imageUploads).toBe(0);
    expect(workspace.fake.counters.trackerUploads).toBe(0);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("metadata-and-audio mode skips picture checks and survives reload and input reopening", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace({ hdrAnalysis: "metadata_audio" });
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace, { baseURL: "/upbrr/" });
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByRole("button", { name: "HDR Analysis", exact: true }).click();
    const mode = page.getByRole("combobox", { name: "Analysis mode", exact: true });
    await expect(mode).toHaveValue("metadata_audio");
    await page.getByRole("button", { name: "Generate", exact: true }).click();
    const waveform = page.getByRole("img", { name: "Video 1 Audio waveform plot", exact: true });
    await expect(waveform).toBeVisible({ timeout: 30_000 });
    await expect(
      page.getByRole("img", { name: "Video 1 Audio spectrogram plot", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("checkbox", { name: "Include Video 1 Audio waveform in descriptions" }),
    ).toBeChecked();
    await expect(
      page.getByRole("checkbox", { name: "Include Video 1 Audio spectrogram in descriptions" }),
    ).toBeChecked();
    await expect(
      page.getByRole("img", { name: "Video 1 Measured picture brightness plot", exact: true }),
    ).toHaveCount(0);
    const link = page.getByRole("link", { name: "Download structural report", exact: true });
    const response = await page
      .context()
      .request.get(new URL((await link.getAttribute("href")) || "", app.url).toString());
    expect(response.ok()).toBe(true);
    const report = await response.json();
    expect(report.measurements).toHaveLength(1);
    expect(report.measurements[0].result.mode).toBe("audio");
    expect(report.measurements[0].audio.track.sampleFrames).toBe(230400);
    const statistics = await page
      .context()
      .request.get(
        new URL(
          (await page
            .getByRole("link", { name: "Download audio stats", exact: true })
            .getAttribute("href")) || "",
          app.url,
        ).toString(),
      );
    expect(statistics.ok()).toBe(true);
    expect(await statistics.text()).toBe(report.measurements[0].audio.stats);
    expect(report.measurements[0].audio.stats).toContain("Pk lev dB");
    expect(report.measurements[0].automatic).toBeUndefined();
    await page.reload();
    await page.getByRole("button", { name: "HDR Analysis", exact: true }).click();
    await expect(mode).toHaveValue("metadata_audio");
    await expect(waveform).toBeVisible();
    await expect(
      page.getByRole("checkbox", { name: "Include Video 1 Audio waveform in descriptions" }),
    ).toBeChecked();
    await page.getByRole("button", { name: "Input", exact: true }).click();
    await page.getByRole("button", { name: "Close input", exact: true }).click();
    await expect(page.getByRole("button", { name: "Close input", exact: true })).toBeDisabled();
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByRole("button", { name: "HDR Analysis", exact: true }).click();
    await expect(mode).toHaveValue("metadata_audio");
    await expect(waveform).toBeVisible();
    await expect(
      page.getByRole("checkbox", { name: "Include Video 1 Audio waveform in descriptions" }),
    ).toBeChecked();
    await expect(
      page.getByRole("img", { name: "Video 1 Measured picture brightness plot", exact: true }),
    ).toHaveCount(0);
    expect(workspace.fake.counters.imageUploads).toBe(0);
    expect(workspace.fake.counters.trackerUploads).toBe(0);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("audio analysis survives reload and serves owner-bound images and statistics under a base path", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace({ audioAnalysis: true });
  let app: AppServer | undefined;

  try {
    app = await startApp(workspace, { baseURL: "/upbrr/" });
    await fetchMetadata(page, app.url, workspace.sourcePath);

    const audioNavigation = page.getByRole("button", { name: "Audio Analysis", exact: true });
    await expect(audioNavigation).toBeEnabled();
    await audioNavigation.click();
    await expect(
      page.getByRole("heading", { name: "Waveforms, Spectrograms & Statistics" }),
    ).toBeVisible();
    await expect(page.getByRole("radio", { name: "Primary" })).toBeChecked();
    await expect(page.getByRole("checkbox", { name: "Waveform" })).toBeChecked();
    await expect(page.getByRole("checkbox", { name: "Spectrogram" })).toBeChecked();
    await expect(page.getByRole("checkbox", { name: "Amplitude statistics" })).toBeChecked();
    await page.getByRole("button", { name: "Generate", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Results" })).toBeVisible({ timeout: 20_000 });
    const images = page.getByRole("img");
    await expect(images).toHaveCount(2);
    const artifactPath = await images.first().getAttribute("src");
    expect(artifactPath).toContain("/upbrr/api/app/release-workflow-audio-analysis?");

    const artifactURL = new URL(artifactPath || "", app.url).toString();
    const authorized = await page.context().request.get(artifactURL);
    expect(authorized.ok()).toBe(true);
    expect(authorized.headers()["content-type"]).toContain("image/png");

    const statsBox = page.locator("pre").filter({ hasText: "DC offset" });
    await expect(statsBox).toBeVisible();
    const statsText = await statsBox.textContent();
    expect(statsText).toContain("RMS lev dB");
    expect(statsText).toContain("Bit-depth");
    expect(statsText).toContain("Window s");
    const statsLink = page.getByRole("link", { name: "Download text file" });
    const statsPath = await statsLink.getAttribute("href");
    const statsURL = new URL(statsPath || "", app.url).toString();
    const statsResponse = await page.context().request.get(statsURL);
    expect(statsResponse.headers()["content-type"]).toContain("text/plain");
    expect(await statsResponse.text()).toBe(statsText);
    const downloadPromise = page.waitForEvent("download");
    await statsLink.click();
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toBe("audio-analysis-stats.txt");
    const downloadedPath = await download.path();
    expect(downloadedPath).toBeTruthy();
    expect(await readFile(downloadedPath!, "utf8")).toBe(statsText);

    await page.setViewportSize({ width: 375, height: 812 });
    await expect(statsBox).toBeVisible();
    const logoutBox = await page.getByRole("button", { name: "Logout" }).boundingBox();
    const navigationBox = await page
      .getByRole("navigation", { name: "Release workflow" })
      .boundingBox();
    expect(logoutBox).not.toBeNull();
    expect(navigationBox).not.toBeNull();
    expect(logoutBox!.y + logoutBox!.height).toBeLessThanOrEqual(navigationBox!.y);
    const box = await statsBox.boundingBox();
    expect(box?.height).toBeLessThanOrEqual(160);
    expect(box?.width).toBeLessThan(375);
    await page.screenshot({
      path: test.info().outputPath("audio-statistics-mobile.png"),
      fullPage: true,
    });
    await page.setViewportSize({ width: 1280, height: 720 });

    await page.reload();
    await page.getByRole("button", { name: "Audio Analysis", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Results" })).toBeVisible();
    await expect(page.getByRole("img")).toHaveCount(2);
    await expect(statsBox).toHaveText(statsText || "");

    await page.getByRole("button", { name: "Disable", exact: true }).click();
    await expect(page.getByText("Disabled", { exact: true })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Results" })).toHaveCount(0);

    await page.getByRole("button", { name: "Input", exact: true }).click();
    await page.getByRole("button", { name: "Close input", exact: true }).click();
    await expect(page.getByRole("button", { name: "Close input", exact: true })).toBeDisabled();

    const ownerToken = await createE2EAPIToken(workspace);
    const ownerClient = new ReleaseWorkflowV1Client(app.url, ownerToken);
    const preparedResponse = await ownerClient.raw("/continuations", {
      method: "POST",
      idempotencyKey: "audio-analysis-api-prepare",
      body: {
        goal: "prepared",
        intent: { preparation: { SourcePath: workspace.sourcePath } },
      },
    });
    expect(preparedResponse.status, await preparedResponse.clone().text()).toBe(200);
    const draft = (await preparedResponse.json()) as WorkflowV1Current;
    const continuePreparation = await ownerClient.raw("/continuations", {
      method: "POST",
      idempotencyKey: "audio-analysis-api-continue-prepare",
      body: {
        authority: {
          workflowId: draft.workflow.id,
          expectedRevision: draft.workflow.revision,
        },
        goal: "prepared",
        intent: { preparation: { SourcePath: workspace.sourcePath } },
      },
    });
    expect(continuePreparation.status, await continuePreparation.clone().text()).toBe(202);
    const prepared = (await ownerClient.accepted(continuePreparation)) as AudioAnalysisAPICurrent;
    const primaryTrack = prepared.release.release.Media.Tracks.find(
      (track) => track.ID === prepared.release.release.Media.PrimaryAudioTrackID,
    );
    expect(primaryTrack).toBeTruthy();
    const analysisResponse = await ownerClient.raw(
      `/workflows/${prepared.workflow.id}/audio-analysis`,
      {
        method: "POST",
        revision: prepared.workflow.revision,
        idempotencyKey: "audio-analysis-api-generate",
        body: {
          instructions: {
            release: {
              SourcePath: prepared.release.release.Source.SourcePath,
              Generation: prepared.release.release.Generation,
            },
            resourceId: primaryTrack!.ResourceID,
            selection: "primary",
            trackIds: [primaryTrack!.ID],
            variants: ["stats"],
            profileVersion: "audio-analysis-v3",
          },
        },
      },
    );
    expect(analysisResponse.status, await analysisResponse.clone().text()).toBe(202);
    const analyzed = (await ownerClient.accepted(analysisResponse)) as AudioAnalysisAPICurrent;
    const retainedArtifact = analyzed.audioAnalysis?.tracks[0]?.artifacts[0];
    expect(retainedArtifact?.status).toBe("completed");
    const apiArtifactPath = `/workflows/${analyzed.workflow.id}/audio-analysis/${analyzed.audioAnalysis!.id}/artifacts/${retainedArtifact!.id}?revision=${analyzed.audioAnalysis!.revision}`;
    const ownerArtifact = await ownerClient.raw(apiArtifactPath);
    expect(ownerArtifact.status, await ownerArtifact.clone().text()).toBe(200);
    expect(ownerArtifact.headers.get("content-type")).toContain("text/plain");
    expect(ownerArtifact.headers.get("content-disposition")).toContain("audio-analysis-stats.txt");
    expect(await ownerArtifact.text()).toBe(retainedArtifact?.text);

    const foreignToken = await createE2EAPIToken(workspace, "foreign-audio-analysis-owner");
    const foreignOwner = await new ReleaseWorkflowV1Client(app.url, foreignToken).raw(
      apiArtifactPath,
    );
    expect(foreignOwner.status).toBe(404);
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test("audio analysis cancellation publishes terminal status without incomplete PNGs", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace({
    audioAnalysis: true,
    audioAnalysisDurationSeconds: 1_200,
  });
  let app: AppServer | undefined;

  try {
    app = await startApp(workspace, { baseURL: "/upbrr/" });
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByRole("button", { name: "Audio Analysis", exact: true }).click();
    await page.getByRole("button", { name: "Generate", exact: true }).click();

    const cancel = page.getByRole("button", { name: "Cancel", exact: true });
    await expect(cancel).toBeVisible({ timeout: 10_000 });
    await expect(
      page.getByRole("listitem").filter({ hasText: /Decoding audio \(\d+%\)\./ }),
    ).toBeVisible({ timeout: 30_000 });
    await cancel.click();

    await expect(page.getByRole("button", { name: "Generate again", exact: true })).toBeEnabled({
      timeout: 10_000,
    });
    await expect(page.getByRole("heading", { name: "Results" })).toBeVisible();
    await expect(page.getByText("canceled", { exact: true })).toBeVisible();
    await expect(page.getByRole("img")).toHaveCount(0);
    await expect(page.getByText("Enabled", { exact: true })).toBeVisible();
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

type AudioAnalysisAPICurrent = WorkflowV1Current &
  Readonly<{
    release: Readonly<{
      release: Readonly<{
        Generation: number;
        Source: Readonly<{ SourcePath: string }>;
        Media: Readonly<{
          PrimaryAudioTrackID: string;
          Tracks: readonly Readonly<{ ID: string; ResourceID: string }>[];
        }>;
      }>;
    }>;
    audioAnalysis?: Readonly<{
      id: string;
      revision: number;
      tracks: readonly Readonly<{
        artifacts: readonly Readonly<{ id: string; status: string; text?: string }>[];
      }>[];
    }>;
  }>;

test("bounded HDR mode retains native evidence and restores explicit audio-off controls", async ({
  page,
}) => {
  const workspace = await createE2EWorkspace({ hdrAnalysis: true });
  let app: AppServer | undefined;
  try {
    app = await startApp(workspace, { baseURL: "/upbrr/" });
    await fetchMetadata(page, app.url, workspace.sourcePath);
    await page.getByRole("button", { name: "HDR Analysis", exact: true }).click();
    await page.getByLabel("Analysis mode", { exact: true }).selectOption("boundary");
    await expect(page.getByLabel("Analyze audio", { exact: true })).toBeChecked();
    await expect(page.getByLabel("Active-area borders", { exact: true })).not.toBeChecked();
    await page.getByLabel("Analyze all audio tracks (up to 64)", { exact: true }).check();
    await page.getByLabel("Analyze audio", { exact: true }).uncheck();
    await page
      .getByLabel("Video decoder threads (0 uses the mode default)", { exact: true })
      .fill("4");
    await page.getByRole("button", { name: "Generate", exact: true }).click();
    await expect(page.getByRole("img", { name: "Video 1 HDR10+ plot", exact: true })).toBeVisible({
      timeout: 30_000,
    });
    const link = page.getByRole("link", { name: "Download structural report", exact: true });
    await expect(link).toBeVisible();
    const href = await link.getAttribute("href");
    const response = await page.request.get(new URL(href || "", app.url).toString());
    expect(response.ok()).toBe(true);
    const report = await response.json();
    expect(report.profile).toBe("hdr-analysis-v3");
    expect(report.decoded).toHaveLength(1);
    expect(report.decoded[0].mode).toBe("boundary_sync");
    expect(report.decoded[0].coverage).toBe("unknown");
    await page.reload();
    await page.getByRole("button", { name: "HDR Analysis", exact: true }).click();
    await expect(page.getByLabel("Analysis mode", { exact: true })).toHaveValue("boundary");
    await expect(page.getByLabel("Analyze audio", { exact: true })).not.toBeChecked();
    await expect(
      page.getByLabel("Video decoder threads (0 uses the mode default)", { exact: true }),
    ).toHaveValue("4");
    await page.getByLabel("Analyze audio", { exact: true }).check();
    await expect(
      page.getByLabel("Analyze all audio tracks (up to 64)", { exact: true }),
    ).not.toBeChecked();
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 });
      for (const mode of ["light", "dark"]) {
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
        await expect
          .poll(() =>
            page.locator("html").evaluate((element) => getComputedStyle(element).colorScheme),
          )
          .toBe(mode);
        const content = page.locator("main.content");
        expect(
          await content.evaluate((element) => element.scrollWidth - element.clientWidth),
        ).toBeLessThanOrEqual(2);
        const style = await page.addStyleTag({
          content:
            ".app-shell { overflow: visible !important; } .content { max-height: none !important; overflow-y: visible !important; }",
        });
        try {
          await page.screenshot({
            path: test.info().outputPath(`hdr-boundary-${width}-${mode}.png`),
            fullPage: true,
            animations: "disabled",
          });
        } finally {
          await style.evaluate((element) => element.remove());
        }
      }
    }
  } finally {
    await app?.stop();
    await workspace.cleanup();
  }
});

test.describe("Contextual help outside Settings", () => {
  test.use({ hasTouch: true });

  test("keeps release, analysis and logging controls stable without starting work", async ({
    page,
  }) => {
    const workspace = await createE2EWorkspace({ hdrAnalysis: true, audioAnalysis: true });
    let app: AppServer | undefined;
    try {
      app = await startApp(workspace, { baseURL: "/upbrr/" });
      await fetchMetadata(page, app.url, workspace.sourcePath);
      await page.setViewportSize({ width: 360, height: 780 });
      const before = { ...workspace.fake.counters };
      const mutations: string[] = [];
      page.on("request", (request) => {
        if (
          request.method() === "POST" &&
          /Generate|ContinueReleaseWorkflow|SaveConfig|OpenActiveInput/.test(request.url())
        )
          mutations.push(
            request.url().endsWith("/ContinueReleaseWorkflow")
              ? `ContinueReleaseWorkflow:${(request.postDataJSON() as { goal: string }).goal}`
              : new URL(request.url()).pathname,
          );
      });
      const tooltip = page.getByTestId("field-help-content");
      const checkFloatingHelp = async (
        label: string,
        control: import("@playwright/test").Locator,
      ) => {
        const help = page.getByRole("button", { name: `Help for ${label}`, exact: true });
        await help.scrollIntoViewIfNeeded();
        const controlPosition = () =>
          control.evaluate((element) => {
            const bounds = element.getBoundingClientRect();
            return {
              x: bounds.x + scrollX,
              y: bounds.y + scrollY,
              width: bounds.width,
              height: bounds.height,
            };
          });
        const position = await controlPosition();
        await help.tap();
        await expect(tooltip).toBeVisible();
        const box = await tooltip.boundingBox();
        expect(box!.x).toBeGreaterThanOrEqual(0);
        expect(box!.x + box!.width).toBeLessThanOrEqual(360);
        expect(box!.y).toBeGreaterThanOrEqual(0);
        expect(box!.y + box!.height).toBeLessThanOrEqual(page.viewportSize()!.height);
        expect(await controlPosition()).toEqual(position);
        await help.tap();
        await expect(tooltip).toBeHidden();
        await help.tap();
        await expect(tooltip).toBeVisible();
        await page.getByRole("heading", { level: 1 }).tap();
        await expect(tooltip).toBeHidden();
      };

      await page.getByText("Edit Release Details", { exact: true }).click();
      const provider = page.getByRole("textbox", { name: "TMDB ID", exact: true });
      const originalID = await provider.inputValue();
      await expect(provider).toHaveAccessibleDescription(/numeric content ID/);
      await checkFloatingHelp("TMDB ID", provider);
      const providerHelp = page.getByRole("button", { name: "Help for TMDB ID", exact: true });
      await provider.focus();
      await provider.press("Shift+Tab");
      await expect(providerHelp).toBeFocused();
      await expect(tooltip).toBeVisible();
      await providerHelp.press("Escape");
      await expect(tooltip).toBeHidden();
      await expect(providerHelp).toBeFocused();
      await page.setViewportSize({ width: 1280, height: 900 });
      await provider.focus();
      await page.locator('label[for="correction-identity.tmdb-value"]').hover();
      await expect(tooltip).toBeHidden();
      await providerHelp.hover();
      await expect(tooltip).toBeVisible();
      await tooltip.hover();
      await expect(tooltip).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(provider).toHaveValue(originalID);

      await page.setViewportSize({ width: 360, height: 780 });
      await page.getByRole("button", { name: "HDR Analysis", exact: true }).click();
      const mode = page.getByRole("combobox", { name: "Analysis mode", exact: true });
      await checkFloatingHelp("Analysis mode", mode);
      await expect(mode).toHaveValue("metadata_audio");
      await mode.selectOption("manual");
      await page
        .getByRole("checkbox", { name: "Brightness grade comparison", exact: true })
        .check();
      const offset = page.getByRole("spinbutton", { name: "Confirmed frame offset (B − A)" });
      await expect(offset).toHaveAccessibleDescription(/B frame = A frame \+ offset/);
      await checkFloatingHelp("Confirmed frame offset (B − A)", offset);
      await expect(
        page.getByText("Select source B and supply an explicit offset and even crop dimensions."),
      ).toBeVisible();
      await expect(page.getByRole("button", { name: "Generate", exact: true })).toBeDisabled();

      await page.getByRole("button", { name: "Audio Analysis", exact: true }).click();
      const waveform = page.getByRole("checkbox", { name: "Waveform", exact: true });
      await expect(waveform).toHaveAccessibleDescription(/amplitude over time/);
      await checkFloatingHelp("Audio outputs", waveform);
      await expect(waveform).toBeChecked();
      await expect(page.getByRole("radio", { name: "Primary", exact: true })).toBeChecked();

      await page.getByRole("button", { name: "Logging", exact: true }).click();
      const level = page.getByRole("combobox", { name: "Runtime log level" });
      const originalLevel = await level.inputValue();
      await expect(level).toHaveAccessibleDescription(/after Save/);
      await checkFloatingHelp("Runtime log level", level);
      await expect(level).toHaveValue(originalLevel);
      await expect(page.getByRole("button", { name: "Save", exact: true })).toBeDisabled();
      expect(workspace.fake.counters).toEqual(before);
      // Entering HDR normally refreshes tracker requirements, without starting analysis.
      expect(mutations).toEqual(["ContinueReleaseWorkflow:trackers_projected"]);
    } finally {
      await app?.stop();
      await workspace.cleanup();
    }
  });
});
