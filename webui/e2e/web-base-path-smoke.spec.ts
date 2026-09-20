// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { expect, test } from "@playwright/test";
import {
  createE2EAPIToken,
  createE2EWorkspace,
  fetchMetadata,
  startApp,
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

test("audio analysis survives reload and serves owner-bound PNGs under a base path", async ({
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
    await expect(page.getByRole("heading", { name: "Waveforms & Spectrograms" })).toBeVisible();
    await expect(page.getByRole("radio", { name: "Primary" })).toBeChecked();
    await expect(page.getByRole("checkbox", { name: "Waveform" })).toBeChecked();
    await expect(page.getByRole("checkbox", { name: "Spectrogram" })).toBeChecked();

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

    await page.reload();
    await page.getByRole("button", { name: "Audio Analysis", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Results" })).toBeVisible();
    await expect(page.getByRole("img")).toHaveCount(2);

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
            variants: ["waveform"],
            profileVersion: "audio-analysis-v1",
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
    expect(ownerArtifact.headers.get("content-type")).toContain("image/png");

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
    await cancel.click();

    await expect(page.getByRole("button", { name: "Generate again", exact: true })).toBeEnabled({
      timeout: 10_000,
    });
    await expect(page.getByRole("heading", { name: "Results" })).toBeVisible();
    await expect(page.getByText(/canceled · expires/)).toBeVisible();
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
        artifacts: readonly Readonly<{ id: string; status: string }>[];
      }>[];
    }>;
  }>;
