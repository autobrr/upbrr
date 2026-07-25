// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { expect, test } from "@playwright/test";
import {
  createE2EAPIToken,
  createE2EWorkspace,
  releaseWorkflowParityFixture,
  startApp,
} from "./helpers/e2eHarness";
import { ReleaseWorkflowV1Client } from "./helpers/releaseWorkflowV1Client";

test("HTTP-only client completes exact workflow and enforces authority", async () => {
  const workspace = await createE2EWorkspace();
  let app = await startApp(workspace);
  try {
    const apiToken = await createE2EAPIToken(workspace);
    const openAPI = await fetch(new URL("api/v1/openapi.json", app.url));
    expect(openAPI.status).toBe(200);
    const schema = (await openAPI.json()) as { paths?: Record<string, unknown> };
    for (const path of [
      "/workflows",
      "/workflows/{workflowId}/prepare",
      "/workflows/{workflowId}/trackers/project",
      "/workflows/{workflowId}/duplicates/check",
      "/workflows/{workflowId}/media/capture",
      "/workflows/{workflowId}/descriptions/generate",
      "/workflows/{workflowId}/uploads/dry-run",
      "/workflows/{workflowId}/uploads/execute",
      "/workflows/{workflowId}/uploads/{resultId}/retry",
    ]) {
      expect(schema.paths?.[path], `OpenAPI path ${path}`).toBeTruthy();
    }

    const unauthenticated = await fetch(new URL("api/v1/workflows", app.url), {
      method: "POST",
      headers: { "Content-Type": "application/json", "Idempotency-Key": "unauthenticated" },
      body: JSON.stringify({ instructions: {} }),
    });
    expect(unauthenticated.status).toBe(401);

    const client = new ReleaseWorkflowV1Client(app.url, apiToken);
    let current = await client.create();
    const workflowID = current.workflow.id;

    const stale = await client.raw(`/workflows/${workflowID}/prepare`, {
      method: "POST",
      revision: current.workflow.revision + 10,
      idempotencyKey: "stale-revision",
      body: preparationBody(workspace.sourcePath),
    });
    expect(stale.status).toBe(409);

    const invalidAction = await client.raw(`/workflows/${workflowID}/actions/missing-action`, {
      method: "POST",
      revision: current.workflow.revision,
      idempotencyKey: "invalid-action",
      body: { answer: { confirmed: true } },
    });
    expect(invalidAction.status).toBe(409);

    current = await client.command("/prepare", preparationBody(workspace.sourcePath));
    expect(current.release?.release.Generation).toBeGreaterThan(0);
    current = await client.command("/trackers/project", {
      trackerIds: [releaseWorkflowParityFixture.trackerID],
      instructions: {},
    });
    expect(current.projections?.status).toBe("ready");
    current = await client.command("/trackers/preflight");
    expect(current.preflight?.status).toBe("ready");
    current = await client.command("/duplicates/check", { skipRemote: false });
    expect(current.dupes?.status).toBe("completed");
    current = await client.command("/media/capture", {
      instructions: {
        screenshotCount: 0,
        purpose: "final",
        captureDvdMenus: false,
      },
    });
    expect(["completed", "skipped"]).toContain(current.media?.status);
    current = await client.command("/descriptions/generate", {
      instructions: {
        options: { NoSeed: true, InteractionMode: "unattended" },
        imageHost: {},
      },
    });
    expect(["completed", "skipped"]).toContain(current.descriptions?.status);
    current = await client.command("/uploads/dry-run", { noSeed: false });
    expect(current.dryRun?.status).toBe("completed");
    expect(current.dryRun?.reports[0]).toMatchObject({
      trackerId: releaseWorkflowParityFixture.trackerID,
      uploadReleaseName: expect.stringContaining(
        releaseWorkflowParityFixture.releaseDisplayName.split(".1080p")[0],
      ),
      status: "completed",
    });
    expect(workspace.fake.counters.trackerUploads).toBe(0);
    expect(workspace.fake.counters.clientInjections).toBe(1);
    const dryRunRevision = current.workflow.revision;
    const executionKey = "execute-direct-upload";
    const executionPath = `/workflows/${workflowID}/uploads/execute`;
    const executionBody = { noSeed: true };
    const executed = await client.raw(executionPath, {
      method: "POST",
      revision: dryRunRevision,
      idempotencyKey: executionKey,
      body: executionBody,
    });
    expect(executed.status).toBe(202);
    const executedPayload = JSON.stringify(await executed.clone().json());
    expect(executedPayload.includes(apiToken)).toBe(false);
    await client.accepted(executed);
    expect(workspace.fake.counters.trackerUploads).toBe(
      releaseWorkflowParityFixture.expectedTrackerUploads,
    );

    const idempotentReplay = await client.raw(executionPath, {
      method: "POST",
      revision: dryRunRevision,
      idempotencyKey: executionKey,
      body: executionBody,
    });
    expect(idempotentReplay.status).toBe(202);
    const executedCurrent = await client.accepted(idempotentReplay);
    expect(workspace.fake.counters.trackerUploads).toBe(1);

    const unauthorizedReplay = await client.raw(executionPath, {
      method: "POST",
      revision: executedCurrent.workflow.revision,
      idempotencyKey: "execute-after-result",
      body: executionBody,
    });
    expect(unauthorizedReplay.status).toBe(202);
    await expect(client.accepted(unauthorizedReplay)).rejects.toThrow("workflow operation failed");
    expect(workspace.fake.counters.trackerUploads).toBe(1);

    const cancelClient = new ReleaseWorkflowV1Client(app.url, apiToken);
    const cancelDraft = await cancelClient.create();
    const canceled = await cancelClient.command("/cancel", { reason: "test cancellation" });
    expect(cancelDraft.workflow.status).toBe("draft");
    expect(canceled.workflow.status).toBe("canceled");

    const restartClient = new ReleaseWorkflowV1Client(app.url, apiToken);
    await restartClient.create();
    await restartClient.command("/prepare", preparationBody(workspace.sourcePath));
    await restartClient.command("/trackers/project", {
      trackerIds: [releaseWorkflowParityFixture.trackerID],
      instructions: {},
    });
    await restartClient.command("/trackers/preflight");
    await restartClient.command("/duplicates/check", { skipRemote: false });
    await restartClient.command("/media/capture", {
      instructions: { screenshotCount: 0, purpose: "final", captureDvdMenus: false },
    });
    await restartClient.command("/descriptions/generate", {
      instructions: {
        options: { NoSeed: true, InteractionMode: "unattended" },
        imageHost: {},
      },
    });
    const beforeRestart = await restartClient.get();
    expect(beforeRestart.status).toBe(200);
    const beforeRestartCurrent = await beforeRestart.json();
    const restartWorkflowID = beforeRestartCurrent.workflow.id as string;

    await app.stop();
    app = await startApp(workspace, { seed: false });
    const afterRestartClient = new ReleaseWorkflowV1Client(app.url, apiToken);
    const afterRestartResponse = await afterRestartClient.get(restartWorkflowID);
    expect(afterRestartResponse.status).toBe(200);
    const afterRestart = (await afterRestartResponse.json()) as {
      workflow: {
        revision: number;
        status: string;
        dryRun?: { id: string };
        uploadResult?: { id: string };
      };
      release?: unknown;
    };
    expect(afterRestart.workflow.status).toBe("blocked");
    expect(afterRestart.workflow.dryRun).toBeUndefined();
    expect(afterRestart.workflow.uploadResult).toBeUndefined();
    expect(afterRestart.release).toBeUndefined();

    const restartExecution = await afterRestartClient.raw(
      `/workflows/${restartWorkflowID}/uploads/execute`,
      {
        method: "POST",
        revision: afterRestart.workflow.revision,
        idempotencyKey: "execute-after-restart",
        body: { noSeed: true },
      },
    );
    expect(restartExecution.status).toBe(202);
    await expect(afterRestartClient.accepted(restartExecution)).rejects.toThrow(
      "workflow operation failed",
    );

    await app.stop();
    const crossOwnerToken = await createE2EAPIToken(workspace, "different-owner");
    app = await startApp(workspace, { seed: false });
    const crossOwner = await new ReleaseWorkflowV1Client(app.url, crossOwnerToken).get(workflowID);
    expect(crossOwner.status).toBe(404);
  } finally {
    await app.stop();
    await workspace.cleanup();
  }
});

function preparationBody(sourcePath: string) {
  return {
    input: {
      SourcePath: sourcePath,
      Intent: "upload",
      Instructions: {},
      Policy: { KeepFolder: false, KeepImages: false, OnlyID: false },
      Search: { Skip: false },
      Controls: { Interaction: "unattended", ConfirmBDMVRescan: false },
      Force: false,
    },
  };
}
