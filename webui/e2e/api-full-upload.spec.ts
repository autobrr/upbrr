// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { expect, test } from "@playwright/test";
import { appendFile } from "node:fs/promises";
import type {
  ActiveInputSnapshot,
  ReleaseCorrectionPatch,
} from "../src/api/generated/release-workflow";
import {
  createE2EAPIToken,
  createE2EWorkspace,
  createMultiDVDSourceFixture,
  expectSingleCollectionTorrentUpload,
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
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
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

test("API uploads one collection torrent for two DVD discs", async () => {
  const workspace = await createE2EWorkspace();
  const sourcePath = await createMultiDVDSourceFixture(workspace);
  const app = await startApp(workspace);
  try {
    const apiToken = await createE2EAPIToken(workspace);
    const client = new ReleaseWorkflowV1Client(app.url, apiToken);
    const trackerID = "HDS";
    const body = uploadBody(sourcePath, {
      confirm: false,
      mode: "upload",
      noSeed: true,
      trackerIDs: [trackerID],
    });
    body.media.screenshots.count = 4;
    body.media.dvdMenus.capture = true;
    const started = await client.raw("/uploads", {
      method: "POST",
      idempotencyKey: "multi-dvd-upload",
      body,
    });
    expect(started.status, await started.clone().text()).toBe(202);
    const approvalBlocked = await client.accepted(started);
    const approvalFeedback = await submitTrackerApproval(
      client,
      approvalBlocked,
      "multi-dvd-upload-approval",
      [trackerID],
    );
    const completed = await client.accepted(approvalFeedback);
    expect(completed.uploadResult?.status).toBe("completed");
    expect(workspace.fake.counters.trackerUploads).toBe(1);
    expectSingleCollectionTorrentUpload(workspace, "Example DVD Collection", [
      "Disc 1",
      "Disc 2",
      "VIDEO_TS",
      "VTS_01_1.VOB",
    ]);
  } finally {
    await app.stop();
    await workspace.cleanup();
  }
});

test("confirm composite resolves duplicate and approval feedback after restart", async () => {
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
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
    // Windows force-stops the process; recovery must wait for its coordinator lease to expire.
    workspace.env.UPBRR_E2E_CLOCK_OFFSET = "2m";
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

    // Stale feedback is rejected before admission. Explicitly resume the expired
    // coordinator, then re-read its recovered revision before reviewed feedback.
    const resumed = await client.raw("/continuations", {
      method: "POST",
      idempotencyKey: "confirm-duplicate-recover",
      body: {
        authority: {
          workflowId: duplicateBlocked.workflow.id,
          expectedRevision: duplicateBlocked.workflow.revision,
        },
        goal: "prepared",
        intent: {},
      },
    });
    expect(resumed.status, await resumed.clone().text()).toBe(409);
    const recoveredResponse = await client.get(duplicateBlocked.workflow.id);
    expect(recoveredResponse.status).toBe(200);
    duplicateBlocked = (await recoveredResponse.json()) as WorkflowV1Current;
    duplicateAction = pendingAction(duplicateBlocked, "review_duplicates");

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
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
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
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
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
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
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
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
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
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
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

test("active input API resolves saved corrections before a release snapshot without deleting history", async () => {
  const workspace = await createE2EWorkspace();
  const app = await startApp(workspace);
  try {
    const authResponse = await fetch(new URL("api/auth/status", app.url));
    expect(authResponse.ok).toBe(true);
    const auth = (await authResponse.json()) as { csrfToken: string };
    expect(auth.csrfToken).toBeTruthy();
    const call = (method: string, body?: unknown) =>
      fetch(new URL(`api/app/${method}`, app.url), {
        method: body === undefined ? "GET" : "POST",
        headers: {
          "Content-Type": "application/json",
          "X-Csrf-Token": auth.csrfToken,
          Origin: new URL(app.url).origin,
        },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
    let active: ActiveInputSnapshot = { state: "empty", revision: 0 };
    const readActive = async () => {
      const response = await call("GetActiveInput");
      expect(response.status, await response.clone().text()).toBe(200);
      active = (await response.json()) as ActiveInputSnapshot;
      return active;
    };
    const open = (idempotencyKey: string, correctionPatch?: ReleaseCorrectionPatch) =>
      call("OpenActiveInput", {
        expectedRevision: active.revision,
        request: {
          idempotencyKey,
          goal: "input_ready",
          intent: {
            preparation: { SourcePath: workspace.sourcePath },
            trackerIds: [releaseWorkflowParityFixture.trackerID],
            ...(correctionPatch ? { correctionPatch } : {}),
          },
        },
      });
    const settle = async (response: Response, status: "blocked" | "completed") => {
      expect(response.status, await response.clone().text()).toBe(200);
      // Active-input commands advance one transition; follow the same desired-state API as the UI.
      for (let transition = 0; transition < 32; transition += 1) {
        await expect
          .poll(async () => (await readActive()).current?.operation?.status || "idle")
          .not.toMatch(/^(queued|running)$/);
        const current = active.current!;
        const continued = await call("ContinueReleaseWorkflow", {
          authority: {
            workflowId: current.workflow.id,
            expectedRevision: current.workflow.revision,
          },
          idempotencyKey: `continue-${current.workflow.id}-${current.workflow.revision}`,
          goal: "input_ready",
          intent: {
            interaction: "interactive",
            trackerIds: [releaseWorkflowParityFixture.trackerID],
            ...(!current.release
              ? {
                  preparation: {
                    SourcePath: workspace.sourcePath,
                    Instructions: current.factInstructions?.instructions,
                  },
                }
              : {}),
          },
        });
        expect(continued.status, await continued.clone().text()).toBe(200);
        await expect
          .poll(async () => (await readActive()).current?.operation?.status || "idle")
          .not.toMatch(/^(queued|running)$/);
        if (active.current?.workflow.revision === current.workflow.revision) break;
        if (
          (active.current?.operation?.status === "blocked" &&
            active.current.workflow.requiredActions?.some(
              (action) => action.status === "pending",
            )) ||
          active.current?.inputReadiness?.status === "completed"
        )
          break;
      }
      expect(active.current?.operation?.status).toBe(status);
      if (status === "completed") expect(active.current?.inputReadiness?.status).toBe("completed");
      return active;
    };
    const changeSource = async () => {
      const closed = await call("ReleaseActiveInput", { expectedRevision: active.revision });
      expect(closed.status, await closed.clone().text()).toBe(200);
      active = (await closed.json()) as ActiveInputSnapshot;
      expect(active.state).toBe("empty");
      await appendFile(workspace.sourcePath, `changed synthetic content: ${active.revision}\n`);
    };
    const emptyValues = { Identity: {}, ReleaseName: {}, Metadata: {} };
    await readActive();
    await settle(
      await open("save-content-corrections", {
        values: {
          Identity: {},
          ReleaseName: { Edition: "Collector Edition" },
          Metadata: {
            AlternateTitle: "Example AKA",
            OriginalLanguage: "Spanish",
            Genres: ["Drama", "Mystery"],
          },
        },
        expectedRevision: 0,
      }),
      "completed",
    );
    const preparedWorkflowID = active.current?.workflow.id;
    await changeSource();
    let blocked = await settle(await open("reopen-changed-content"), "blocked");
    expect(blocked.sourcePath).toBe(workspace.sourcePath);
    expect(blocked.current?.workflow.id).not.toBe(preparedWorkflowID);
    expect(blocked.current?.release ?? null).toBeNull();
    let review = blocked.current?.workflow.requiredActions?.find(
      (action) => action.kind === "confirm_corrections" && action.status === "pending",
    );
    expect(review?.correctionConfirmation?.fields).toEqual([
      "metadata.alternate_title",
      "metadata.genres",
      "metadata.original_language",
    ]);
    const reloaded = await readActive();
    expect(reloaded.current?.release ?? null).toBeNull();
    expect(reloaded.sourcePath).toBe(workspace.sourcePath);
    expect(reloaded.current?.workflow.requiredActions).toContainEqual(review);

    await test.step("changed-source mixed correction replay retains the fresh review", async () => {
      const request = {
        expectedRevision: active.revision,
        request: {
          idempotencyKey: "changed-during-saved-value-review",
          goal: "input_ready",
          intent: {
            preparation: { SourcePath: workspace.sourcePath },
            trackerIds: [releaseWorkflowParityFixture.trackerID],
            correctionPatch: {
              values: { ...emptyValues, Metadata: { OriginalLanguage: "French" } },
              confirmFields: [{ field: "metadata.alternate_title" }],
              resetFields: [{ field: "metadata.genres" }],
              expectedRevision: active.current!.corrections!.revision,
            },
          },
        },
      };
      await appendFile(
        workspace.sourcePath,
        "source changed while saved values were under review\n",
      );
      const refreshed = await settle(await call("OpenActiveInput", request), "blocked");
      expect(refreshed.sourceVersion).not.toBe(blocked.sourceVersion);
      expect(refreshed.current?.release ?? null).toBeNull();
      expect(refreshed.current?.corrections?.corrections.metadata).toEqual(
        blocked.current?.corrections?.corrections.metadata,
      );
      expect(refreshed.current?.corrections?.corrections.staleContentFields).toEqual(
        review?.correctionConfirmation?.fields,
      );
      const freshReview = refreshed.current?.workflow.requiredActions?.find(
        (action) => action.kind === "confirm_corrections" && action.status === "pending",
      );
      expect(freshReview?.correctionConfirmation?.currentBinding.sourceFingerprint).not.toBe(
        review?.correctionConfirmation?.currentBinding.sourceFingerprint,
      );
      // Retry the complete original body, including its now-old active-input revision.
      const replayed = await settle(await call("OpenActiveInput", request), "blocked");
      expect(replayed.current?.release ?? null).toBeNull();
      expect(replayed.current?.corrections).toEqual(refreshed.current?.corrections);
      expect(replayed.current?.workflow.requiredActions).toContainEqual(freshReview);
      expect(replayed.current?.corrections?.corrections.releaseName.Edition).toBe(
        "Collector Edition",
      );
      const conflictingRequest = structuredClone(request);
      conflictingRequest.request.intent.correctionPatch.values.Metadata.OriginalLanguage = "German";
      const conflict = await call("OpenActiveInput", conflictingRequest);
      expect(conflict.status, await conflict.clone().text()).toBe(409);
      blocked = await readActive();
      expect(blocked.current?.corrections).toEqual(refreshed.current?.corrections);
      expect(blocked.current?.workflow.requiredActions).toContainEqual(freshReview);
      review = freshReview;
    });

    const outdated = await open("reject-outdated-correction-revision", {
      values: emptyValues,
      confirmFields: [{ field: "metadata.alternate_title" }],
      expectedRevision: blocked.current!.corrections!.revision - 1,
    });
    expect(outdated.status, await outdated.clone().text()).toBe(409);
    await readActive();
    expect(active.current?.corrections).toEqual(blocked.current?.corrections);
    expect(active.current?.workflow.requiredActions).toContainEqual(review);

    await settle(
      await open("keep-one-saved-value", {
        values: emptyValues,
        confirmFields: [{ field: "metadata.alternate_title" }],
        expectedRevision: active.current!.corrections!.revision,
      }),
      "blocked",
    );
    expect(active.current?.release ?? null).toBeNull();
    expect(active.current?.corrections?.corrections.staleContentFields).toEqual([
      "metadata.genres",
      "metadata.original_language",
    ]);
    expect(active.current?.corrections?.corrections.metadata.AlternateTitle).toBe("Example AKA");
    await readActive();
    await settle(
      await open("edit-and-reset-remaining-values", {
        values: { ...emptyValues, Metadata: { OriginalLanguage: "Japanese" } },
        resetFields: [{ field: "metadata.genres" }],
        expectedRevision: active.current!.corrections!.revision,
      }),
      "completed",
    );
    expect(active.current?.workflow.id).toBe(blocked.current?.workflow.id);
    expect(active.current?.release).toBeTruthy();
    expect(active.current?.corrections?.corrections.staleContentFields ?? []).toEqual([]);
    expect(active.current?.corrections?.corrections.metadata).toMatchObject({
      AlternateTitle: "Example AKA",
      OriginalLanguage: "Japanese",
      Genres: null,
    });
    expect(active.current?.corrections?.corrections.releaseName.Edition).toBe("Collector Edition");

    await changeSource();
    await settle(await open("reopen-before-affected-reset"), "blocked");
    expect(active.current?.release ?? null).toBeNull();
    const affected = active.current?.corrections?.corrections.staleContentFields;
    expect(affected).toEqual(["metadata.alternate_title", "metadata.original_language"]);
    await settle(
      await open("reset-only-affected-values", {
        values: emptyValues,
        resetFields: affected!.map((field) => ({ field })),
        expectedRevision: active.current!.corrections!.revision,
      }),
      "completed",
    );
    expect(active.current?.corrections?.corrections.staleContentFields ?? []).toEqual([]);
    expect(active.current?.corrections?.corrections.metadata).toMatchObject({
      AlternateTitle: null,
      OriginalLanguage: null,
      Genres: null,
    });
    expect(active.current?.corrections?.corrections.releaseName.Edition).toBe("Collector Edition");
    expect(active.current?.factInstructions?.instructions.ReleaseName.Edition).toBe(
      "Collector Edition",
    );
    const history = await call("ListHistory", {});
    expect(history.status, await history.clone().text()).toBe(200);
    expect(await history.json()).toContainEqual(
      expect.objectContaining({ SourcePath: workspace.sourcePath }),
    );
    expect(workspace.fake.counters.trackerUploads).toBe(0);
    expect(workspace.fake.counters.imageUploads).toBe(0);
    expect(workspace.fake.counters.clientInjections).toBe(0);
  } finally {
    await app.stop();
    await workspace.cleanup();
  }
});

test("restart interrupts an uncertain client effect without replay", async () => {
  test.setTimeout(90_000);
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
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
    // Startup uses real time and must respect the crashed process's 60-second leases.
    app = await startApp(workspace, { seed: false, startupTimeoutMs: 75_000 });
    client = new ReleaseWorkflowV1Client(app.url, apiToken);
    const recoveredOperation = await waitForTerminalOperation(
      client,
      accepted.workflow.id,
      accepted.operation!.id,
    );
    const currentResponse = await client.get(accepted.workflow.id);
    expect(currentResponse.status).toBe(200);
    const current = (await currentResponse.json()) as WorkflowV1Current;
    expect(recoveredOperation.status).toBe("interrupted");
    expect(recoveredOperation.failures).toEqual([
      expect.objectContaining({
        failure: expect.objectContaining({ Code: "stale_review", Recovery: "retry" }),
      }),
    ]);
    expect(current.workflow.requiredActions ?? []).not.toEqual(
      expect.arrayContaining([expect.objectContaining({ kind: "reconcile_submission" })]),
    );
    expect(workspace.fake.counters.clientInjections).toBe(1);
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
