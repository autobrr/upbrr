// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { expect, test } from "@playwright/test";
import {
  createE2EAPIToken,
  createE2EWorkspace,
  readE2EAuthCounters,
  releaseWorkflowParityFixture,
  startApp,
} from "./helpers/e2eHarness";
import {
  type WorkflowV1Current,
  type WorkflowV1Operation,
  ReleaseWorkflowV1Client,
} from "./helpers/releaseWorkflowV1Client";

test("strict composite upload enforces contract authority and idempotency", async () => {
  const workspace = await createE2EWorkspace();
  const app = await startApp(workspace);
  try {
    const apiToken = await createE2EAPIToken(workspace);
    const openAPI = await fetch(new URL("api/v1/openapi.json", app.url));
    expect(openAPI.status).toBe(200);
    const schema = (await openAPI.json()) as { paths?: Record<string, unknown> };
    for (const path of [
      "/uploads",
      "/uploads/{workflowId}/feedback",
      "/continuations",
      "/workflows/{workflowId}",
      "/workflows/{workflowId}/operations/{operationId}",
      "/workflows/{workflowId}/operations/{operationId}/cancel",
    ]) {
      expect(schema.paths?.[path], `OpenAPI path ${path}`).toBeTruthy();
    }
    expect(schema.paths?.["/workflows"], "retired stage workflow creation path").toBeUndefined();

    const body = uploadBody(workspace.sourcePath, {
      confirm: false,
      mode: "upload",
      noSeed: false,
    });
    const unauthenticated = await fetch(new URL("api/v1/uploads", app.url), {
      method: "POST",
      headers: { "Content-Type": "application/json", "Idempotency-Key": "unauthenticated" },
      body: JSON.stringify(body),
    });
    expect(unauthenticated.status).toBe(401);

    const client = new ReleaseWorkflowV1Client(app.url, apiToken);
    const started = await client.raw("/uploads", {
      method: "POST",
      idempotencyKey: "strict-full-upload",
      body,
    });
    expect(started.status, await started.clone().text()).toBe(202);
    const approvalBlocked = await client.accepted(started);
    expect(approvalBlocked.operation?.status).toBe("blocked");
    expect(approvalBlocked.media).toBeUndefined();
    const approvalFeedback = await submitTrackerApproval(
      client,
      approvalBlocked,
      "strict-full-upload-approval",
      [releaseWorkflowParityFixture.trackerID],
    );
    const completed = await client.accepted(approvalFeedback);
    expect(completed.uploadResult?.status).toBe("completed");
    expect(completed.uploadResult?.results[0]).toMatchObject({
      trackerId: releaseWorkflowParityFixture.trackerID,
      status: "completed",
    });
    expect(workspace.fake.counters.trackerUploads).toBe(1);
    expect(workspace.fake.counters.clientInjections).toBe(1);

    const replay = await client.raw("/uploads", {
      method: "POST",
      idempotencyKey: "strict-full-upload",
      body,
    });
    expect(replay.status, await replay.clone().text()).toBe(200);
    const replayed = (await replay.json()) as WorkflowV1Current;
    expect(replayed.workflow.id).toBe(completed.workflow.id);
    expect(workspace.fake.counters.trackerUploads).toBe(1);
    expect(workspace.fake.counters.clientInjections).toBe(1);

    const conflictingReplay = await client.raw("/uploads", {
      method: "POST",
      idempotencyKey: "strict-full-upload",
      body: uploadBody(workspace.sourcePath, { confirm: false, mode: "debug", noSeed: false }),
    });
    expect(conflictingReplay.status).toBe(409);

    const crossOwnerToken = await createE2EAPIToken(workspace, "different-owner");
    const crossOwner = await new ReleaseWorkflowV1Client(app.url, crossOwnerToken).get(
      completed.workflow.id,
    );
    expect(crossOwner.status).toBe(404);
  } finally {
    await app.stop();
    await workspace.cleanup();
  }
});

test("confirm composite resolves duplicate and approval feedback after restart", async () => {
  const workspace = await createE2EWorkspace();
  workspace.env.UPBRR_E2E_DUPLICATE_TRACKERS = releaseWorkflowParityFixture.trackerID;
  let app = await startApp(workspace);
  try {
    const apiToken = await createE2EAPIToken(workspace);
    let client = new ReleaseWorkflowV1Client(app.url, apiToken);
    const started = await client.raw("/uploads", {
      method: "POST",
      idempotencyKey: "confirm-duplicate-start",
      body: uploadBody(workspace.sourcePath, { confirm: true, mode: "upload", noSeed: true }),
    });
    expect(started.status, await started.clone().text()).toBe(202);
    let duplicateBlocked = await client.accepted(started);
    let duplicateAction = pendingAction(duplicateBlocked, "review_duplicates");
    expect(duplicateAction.trackerId).toBe(releaseWorkflowParityFixture.trackerID);
    expect(workspace.fake.counters.trackerUploads).toBe(0);

    await app.stop();
    app = await startApp(workspace, { seed: false });
    client = new ReleaseWorkflowV1Client(app.url, apiToken);
    const restartedResponse = await client.get(duplicateBlocked.workflow.id);
    expect(restartedResponse.status).toBe(200);
    duplicateBlocked = (await restartedResponse.json()) as WorkflowV1Current;
    duplicateAction = pendingAction(duplicateBlocked, "review_duplicates");

    const stale = await client.raw(`/uploads/${duplicateBlocked.workflow.id}/feedback`, {
      method: "POST",
      revision: duplicateBlocked.workflow.revision - 1,
      idempotencyKey: "confirm-duplicate-stale",
      body: {
        action: {
          id: duplicateAction.id,
          workflowRevision: duplicateBlocked.workflow.revision - 1,
        },
        response: {
          kind: "duplicateReview",
          duplicateReview: {
            trackerId: releaseWorkflowParityFixture.trackerID,
            decision: "ignored",
          },
        },
      },
    });
    expect(stale.status).toBe(409);

    const duplicateFeedback = await client.raw(
      `/uploads/${duplicateBlocked.workflow.id}/feedback`,
      {
        method: "POST",
        revision: duplicateBlocked.workflow.revision,
        idempotencyKey: "confirm-duplicate-ignore",
        body: {
          action: {
            id: duplicateAction.id,
            workflowRevision: duplicateBlocked.workflow.revision,
          },
          response: {
            kind: "duplicateReview",
            duplicateReview: {
              trackerId: releaseWorkflowParityFixture.trackerID,
              decision: "ignored",
            },
          },
        },
      },
    );
    expect(duplicateFeedback.status, await duplicateFeedback.clone().text()).toBe(202);
    const approvalBlocked = await client.accepted(duplicateFeedback);
    expect(approvalBlocked.media).toBeUndefined();
    expect(approvalBlocked.dryRun).toBeUndefined();
    const approval = pendingAction(approvalBlocked, "approve_trackers");
    expect(approval.options).toEqual([
      {
        value: releaseWorkflowParityFixture.trackerID,
        label: releaseWorkflowParityFixture.trackerID,
      },
    ]);

    const approvalFeedback = await client.raw(`/uploads/${approvalBlocked.workflow.id}/feedback`, {
      method: "POST",
      revision: approvalBlocked.workflow.revision,
      idempotencyKey: "confirm-tracker-approval",
      body: {
        action: {
          id: approval.id,
          workflowRevision: approvalBlocked.workflow.revision,
        },
        response: {
          kind: "trackerApproval",
          trackerApproval: {
            confirmed: true,
            trackerIds: [releaseWorkflowParityFixture.trackerID],
          },
        },
      },
    });
    expect(approvalFeedback.status, await approvalFeedback.clone().text()).toBe(202);
    const completed = await client.accepted(approvalFeedback);
    expect(completed.uploadResult?.status).toBe("completed");
    expect(workspace.fake.counters.trackerUploads).toBe(1);
    expect(workspace.fake.counters.clientInjections).toBe(0);
  } finally {
    await app.stop();
    await workspace.cleanup();
  }
});

test("debug composite performs client injection unless no-seed is explicit", async () => {
  const workspace = await createE2EWorkspace();
  const app = await startApp(workspace);
  try {
    const apiToken = await createE2EAPIToken(workspace);
    const client = new ReleaseWorkflowV1Client(app.url, apiToken);

    const withInjection = await client.raw("/uploads", {
      method: "POST",
      idempotencyKey: "debug-with-injection",
      body: uploadBody(workspace.sourcePath, { confirm: false, mode: "debug", noSeed: false }),
    });
    expect(withInjection.status, await withInjection.clone().text()).toBe(202);
    const firstBlocked = await client.accepted(withInjection);
    const firstApproval = await submitTrackerApproval(
      client,
      firstBlocked,
      "debug-with-injection-approval",
      [releaseWorkflowParityFixture.trackerID],
    );
    const first = await client.accepted(firstApproval);
    expect(first.dryRun?.status).toBe("completed");
    expect(first.uploadResult).toBeUndefined();
    expect(workspace.fake.counters.clientInjections).toBe(1);
    expect(workspace.fake.counters.trackerUploads).toBe(0);

    const withoutInjection = await client.raw("/uploads", {
      method: "POST",
      idempotencyKey: "debug-without-injection",
      body: uploadBody(workspace.sourcePath, { confirm: false, mode: "debug", noSeed: true }),
    });
    expect(withoutInjection.status, await withoutInjection.clone().text()).toBe(202);
    const secondBlocked = await client.accepted(withoutInjection);
    const secondApproval = await submitTrackerApproval(
      client,
      secondBlocked,
      "debug-without-injection-approval",
      [releaseWorkflowParityFixture.trackerID],
    );
    const second = await client.accepted(secondApproval);
    expect(second.dryRun?.status).toBe("completed");
    expect(second.uploadResult).toBeUndefined();
    expect(workspace.fake.counters.clientInjections).toBe(1);
    expect(workspace.fake.counters.trackerUploads).toBe(0);
  } finally {
    await app.stop();
    await workspace.cleanup();
  }
});

test("strict composite skips an auth-blocked tracker while uploading a ready sibling", async () => {
  const workspace = await createE2EWorkspace();
  workspace.env.UPBRR_E2E_AUTH_SCENARIOS = "HDS=validation_only_missing_cookies";
  const app = await startApp(workspace);
  try {
    const apiToken = await createE2EAPIToken(workspace);
    const client = new ReleaseWorkflowV1Client(app.url, apiToken);
    const started = await client.raw("/uploads", {
      method: "POST",
      idempotencyKey: "strict-auth-skip",
      body: uploadBody(workspace.sourcePath, {
        confirm: false,
        mode: "upload",
        noSeed: true,
        trackerIDs: [releaseWorkflowParityFixture.trackerID, "HDS"],
      }),
    });
    expect(started.status, await started.clone().text()).toBe(202);
    const approvalBlocked = await client.accepted(started);
    const approval = pendingAction(approvalBlocked, "approve_trackers");
    expect(approval.options).toEqual([
      {
        value: releaseWorkflowParityFixture.trackerID,
        label: releaseWorkflowParityFixture.trackerID,
      },
    ]);
    const deprecatedAuthFeedback = await client.raw(
      `/uploads/${approvalBlocked.workflow.id}/feedback`,
      {
        method: "POST",
        revision: approvalBlocked.workflow.revision,
        idempotencyKey: "strict-auth-skip-deprecated-feedback",
        body: {
          action: {
            id: approval.id,
            workflowRevision: approvalBlocked.workflow.revision,
          },
          response: {
            kind: "trackerAuthentication",
            trackerAuthentication: { trackerId: "HDS" },
          },
        },
      },
    );
    const deprecatedAuthPayload = (await deprecatedAuthFeedback.json()) as {
      error?: string;
      failure?: { Code?: string };
    };
    expect(deprecatedAuthFeedback.status).toBe(409);
    expect(deprecatedAuthPayload).toMatchObject({
      error:
        "Tracker authentication must be resolved outside the upload workflow. Start a fresh attempt.",
      failure: { Code: "tracker_auth_required" },
    });
    const approvalFeedback = await submitTrackerApproval(
      client,
      approvalBlocked,
      "strict-auth-skip-approval",
      [releaseWorkflowParityFixture.trackerID],
    );
    const completed = await client.accepted(approvalFeedback);
    expect(completed.uploadResult?.results).toEqual([
      expect.objectContaining({
        trackerId: releaseWorkflowParityFixture.trackerID,
        status: "completed",
      }),
    ]);
    const authBlocked = completed.preflight?.results.find((result) => result.trackerId === "HDS");
    expect(authBlocked).toMatchObject({
      state: "retryable",
      authReady: false,
    });
    expect(authBlocked?.requiredActions).toBeUndefined();
    expect(
      authBlocked?.failures?.some((failure) => failure.failure.Code === "tracker_auth_required"),
    ).toBe(true);
    expect(
      completed.projections?.projections.find((projection) => projection.trackerId === "HDS"),
    ).toMatchObject({
      readiness: "blocked",
      dupeReady: false,
      uploadReady: false,
    });
    expect(completed.workflow.requiredActions).toBeUndefined();
    expect(workspace.fake.counters.trackerUploads).toBe(1);
    expect(await readE2EAuthCounters(workspace)).toEqual({
      capabilityCalls: 1,
      validationCalls: 1,
      loginAttempts: 0,
      validations: { BTN: 1, HDS: 1 },
    });
  } finally {
    await app.stop();
    await workspace.cleanup();
  }
});

test("strict composite stops cleanly when every tracker is auth-blocked", async () => {
  const workspace = await createE2EWorkspace();
  workspace.env.UPBRR_E2E_AUTH_SCENARIOS = "HDS=validation_only_missing_cookies";
  const app = await startApp(workspace);
  try {
    const apiToken = await createE2EAPIToken(workspace);
    const client = new ReleaseWorkflowV1Client(app.url, apiToken);
    const started = await client.raw("/uploads", {
      method: "POST",
      idempotencyKey: "strict-all-auth-blocked",
      body: uploadBody(workspace.sourcePath, {
        confirm: false,
        mode: "upload",
        noSeed: true,
        trackerIDs: ["HDS"],
      }),
    });
    expect(started.status, await started.clone().text()).toBe(202);

    const accepted = (await started.json()) as { operation: WorkflowV1Operation };
    const operation = await waitForTerminalOperation(
      client,
      accepted.operation.workflowId,
      accepted.operation.id,
    );
    expect(operation.status).toBe("failed");
    expect(operation.failures).toEqual([
      expect.objectContaining({
        failure: expect.objectContaining({
          Code: "no_eligible_trackers",
          Recovery: "authenticate_trackers",
        }),
      }),
    ]);
    const current = await client.get(accepted.operation.workflowId);
    expect(current.status, await current.clone().text()).toBe(200);
    const blocked = (await current.json()) as WorkflowV1Current;
    expect(blocked.workflow.status).toBe("failed");
    expect(blocked.workflow.requiredActions).toBeUndefined();
    expect(blocked.preflight?.results).toEqual([
      expect.objectContaining({
        trackerId: "HDS",
        state: "retryable",
        authReady: false,
      }),
    ]);
    expect(blocked.dupes).toBeUndefined();
    expect(blocked.media).toBeUndefined();
    expect(blocked.descriptions).toBeUndefined();
    expect(workspace.fake.counters.trackerUploads).toBe(0);
    expect(workspace.fake.counters.clientSearches).toBe(
      releaseWorkflowParityFixture.expectedClientSearches,
    );
    expect(workspace.fake.counters.clientInjections).toBe(0);
    expect(await readE2EAuthCounters(workspace)).toEqual({
      capabilityCalls: 1,
      validationCalls: 1,
      loginAttempts: 0,
      validations: { HDS: 1 },
    });
  } finally {
    await app.stop();
    await workspace.cleanup();
  }
});

test("public approval subset excludes unapproved tracker requirements and upload", async () => {
  const workspace = await createE2EWorkspace();
  const app = await startApp(workspace);
  try {
    const apiToken = await createE2EAPIToken(workspace);
    const client = new ReleaseWorkflowV1Client(app.url, apiToken);
    const started = await client.raw("/uploads", {
      method: "POST",
      idempotencyKey: "approval-subset",
      body: uploadBody(workspace.sourcePath, {
        confirm: false,
        mode: "upload",
        noSeed: true,
        trackerIDs: [releaseWorkflowParityFixture.trackerID, "HDS"],
      }),
    });
    expect(started.status, await started.clone().text()).toBe(202);
    const approvalBlocked = await client.accepted(started);
    expect(approvalBlocked.media).toBeUndefined();
    const approvalFeedback = await submitTrackerApproval(
      client,
      approvalBlocked,
      "approval-subset-feedback",
      [releaseWorkflowParityFixture.trackerID],
      [releaseWorkflowParityFixture.trackerID, "HDS"],
    );
    const completed = await client.accepted(approvalFeedback);
    expect(completed.uploadResult?.results).toEqual([
      expect.objectContaining({
        trackerId: releaseWorkflowParityFixture.trackerID,
        status: "completed",
      }),
    ]);
    expect(workspace.fake.counters.imageUploads).toBe(0);
    expect(workspace.fake.counters.trackerUploads).toBe(1);
  } finally {
    await app.stop();
    await workspace.cleanup();
  }
});

test("composite operation cancellation stops the active tracker stage", async () => {
  const workspace = await createE2EWorkspace();
  workspace.fake.delayTrackerUploads(3_000);
  const app = await startApp(workspace);
  try {
    const apiToken = await createE2EAPIToken(workspace);
    const client = new ReleaseWorkflowV1Client(app.url, apiToken);
    const started = await client.raw("/uploads", {
      method: "POST",
      idempotencyKey: "cancel-active-upload",
      body: uploadBody(workspace.sourcePath, { confirm: false, mode: "upload", noSeed: true }),
    });
    expect(started.status, await started.clone().text()).toBe(202);
    const approvalBlocked = await client.accepted(started);
    const approvalFeedback = await submitTrackerApproval(
      client,
      approvalBlocked,
      "cancel-active-upload-approval",
      [releaseWorkflowParityFixture.trackerID],
    );
    const accepted = (await approvalFeedback.json()) as WorkflowV1Current;
    expect(accepted.operation).toBeTruthy();
    await waitForCounter(() => workspace.fake.counters.trackerUploads, 1);

    const canceled = await client.raw(
      `/workflows/${accepted.workflow.id}/operations/${accepted.operation!.id}/cancel`,
      { method: "POST" },
    );
    expect(canceled.status, await canceled.clone().text()).toBe(200);
    const operation = await waitForTerminalOperation(
      client,
      accepted.workflow.id,
      accepted.operation!.id,
    );
    expect(operation.status).toBe("canceled");
    const currentResponse = await client.get(accepted.workflow.id);
    const current = (await currentResponse.json()) as WorkflowV1Current;
    expect(current.uploadResult).toBeUndefined();
  } finally {
    await app.stop();
    await workspace.cleanup();
  }
});

test("restart stops at reconciliation after an uncertain client effect", async () => {
  const workspace = await createE2EWorkspace();
  workspace.fake.delayClientInjections(5_000);
  let app = await startApp(workspace);
  try {
    const apiToken = await createE2EAPIToken(workspace);
    let client = new ReleaseWorkflowV1Client(app.url, apiToken);
    const started = await client.raw("/uploads", {
      method: "POST",
      idempotencyKey: "uncertain-client-effect",
      body: uploadBody(workspace.sourcePath, { confirm: false, mode: "debug", noSeed: false }),
    });
    expect(started.status, await started.clone().text()).toBe(202);
    const approvalBlocked = await client.accepted(started);
    const approvalFeedback = await submitTrackerApproval(
      client,
      approvalBlocked,
      "uncertain-client-effect-approval",
      [releaseWorkflowParityFixture.trackerID],
    );
    const accepted = (await approvalFeedback.json()) as WorkflowV1Current;
    expect(accepted.operation).toBeTruthy();
    await waitForCounter(() => workspace.fake.counters.clientInjections, 1);

    await app.crash();
    workspace.fake.delayClientInjections(0);
    workspace.env.UPBRR_E2E_CLOCK_OFFSET = "2m";
    app = await startApp(workspace, { seed: false });
    client = new ReleaseWorkflowV1Client(app.url, apiToken);
    const recoveredOperation = await waitForTerminalOperation(
      client,
      accepted.workflow.id,
      accepted.operation!.id,
    );
    const currentResponse = await client.get(accepted.workflow.id);
    expect(currentResponse.status).toBe(200);
    const current = (await currentResponse.json()) as WorkflowV1Current;
    if (
      !current.workflow.requiredActions?.some((action) => action.kind === "reconcile_submission")
    ) {
      throw new Error(
        JSON.stringify(
          {
            recoveredOperation: {
              status: recoveredOperation.status,
              message: recoveredOperation.message,
              failures: recoveredOperation.failures,
            },
            workflow: current.workflow,
            dryRun: current.dryRun,
            output: app.output(),
          },
          undefined,
          2,
        ),
      );
    }
    const reconciliation = pendingAction(current, "reconcile_submission");
    expect(reconciliation.trackerId).toBe(releaseWorkflowParityFixture.trackerID);
    expect(current.workflow.status).toBe("blocked");
    expect(workspace.fake.counters.trackerUploads).toBe(0);
  } finally {
    await app.stop();
    await workspace.cleanup();
  }
});

function uploadBody(
  sourcePath: string,
  options: Readonly<{
    confirm: boolean;
    mode: "upload" | "debug";
    noSeed: boolean;
    trackerIDs?: readonly string[];
  }>,
) {
  return {
    source: { path: sourcePath },
    unattended: { confirm: options.confirm },
    execution: { mode: options.mode, preparedRelease: "allow" },
    trackers: { include: options.trackerIDs ?? [releaseWorkflowParityFixture.trackerID] },
    duplicates: { remoteCheck: true, checkCount: 1, onEvidence: "ask" },
    media: {
      screenshots: { count: 0 },
      dvdMenus: { capture: false },
    },
    client: { noSeed: options.noSeed },
  };
}

function pendingAction(current: WorkflowV1Current, kind: string) {
  const action = current.workflow.requiredActions?.find((candidate) => candidate.kind === kind);
  expect(action, `pending ${kind} action`).toBeTruthy();
  return action!;
}

async function submitTrackerApproval(
  client: ReleaseWorkflowV1Client,
  current: WorkflowV1Current,
  idempotencyKey: string,
  trackerIDs: readonly string[],
  candidateTrackerIDs: readonly string[] = trackerIDs,
) {
  const approval = pendingAction(current, "approve_trackers");
  expect(approval.options?.map((option) => option.value)).toEqual(candidateTrackerIDs);
  const response = await client.raw(`/uploads/${current.workflow.id}/feedback`, {
    method: "POST",
    revision: current.workflow.revision,
    idempotencyKey,
    body: {
      action: {
        id: approval.id,
        workflowRevision: current.workflow.revision,
      },
      response: {
        kind: "trackerApproval",
        trackerApproval: {
          confirmed: true,
          trackerIds: trackerIDs,
        },
      },
    },
  });
  expect(response.status, await response.clone().text()).toBe(202);
  return response;
}

async function waitForCounter(read: () => number, minimum: number) {
  const deadline = Date.now() + 10_000;
  while (read() < minimum) {
    if (Date.now() >= deadline) {
      throw new Error(`counter did not reach ${minimum}`);
    }
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
}

async function waitForTerminalOperation(
  client: ReleaseWorkflowV1Client,
  workflowID: string,
  operationID: string,
): Promise<WorkflowV1Operation> {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    const response = await client.raw(`/workflows/${workflowID}/operations/${operationID}`);
    if (response.status === 429) {
      await new Promise((resolve) => setTimeout(resolve, 500));
      continue;
    }
    expect(response.status, await response.clone().text()).toBe(200);
    const operation = (await response.json()) as WorkflowV1Operation;
    if (!["queued", "running"].includes(operation.status)) {
      return operation;
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`workflow operation ${operationID} did not finish`);
}
