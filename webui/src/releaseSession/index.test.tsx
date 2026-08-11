// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { MetadataPreview, PrepareInput } from "../types";
import { emptyExternalIdentity } from "../utils/canonicalIdentity";
import type {
  ContinueReleaseWorkflowRequest,
  DescriptionInstructions,
  DupeDecision,
  MediaCaptureInstructions,
  ReleaseWorkflowCurrent,
  TrackerProjectionInstructions,
  WorkflowContinuation,
} from "../api/generated/release-workflow";
import { ReleaseSessionProvider, routeAccess, useReleaseSession } from ".";
import type { ReleaseSessionPorts } from "./ports";

const preview = (sourcePath: string, generation: number): MetadataPreview => ({
  SourcePath: sourcePath,
  TrackerName: "AITHER",
  ReleaseName: "Example.Release.2026.1080p-GRP",
  ReleaseNameOverrides: {},
  Release: { SourcePath: sourcePath, Generation: generation },
  Identity: { ...emptyExternalIdentity(sourcePath), Generation: generation },
  Display: { ReleaseName: "Example.Release.2026.1080p-GRP", Providers: [] },
  Bluray: null,
  Diagnostics: [],
  TrackerData: [],
});

const createDeferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
};

type PortOverrides = Readonly<{
  workflow?: ReleaseSessionPorts["workflow"];
}>;

const portsFor = (overrides: PortOverrides = {}): ReleaseSessionPorts => {
  return {
    workflow: overrides.workflow ?? workflowPorts(),
    descriptions: {
      render: async (raw) => raw,
    },
  };
};

const workflowCurrent = (workflowID: string, revision: number): ReleaseWorkflowCurrent => ({
  continuation: {
    lifecycle: revision > 1 ? "ready" : "waiting",
    disposition: "none",
    refs: {},
    availableGoals: [
      "prepared",
      "trackers_assessed",
      "duplicates_decided",
      "media_ready",
      "descriptions_ready",
      "upload_reviewed",
      "dry_run",
      "uploaded",
    ].map((goal) => ({ goal, available: revision > 1 })),
  },
  workflow: {
    id: workflowID,
    revision,
    factInstructions: { id: `${workflowID}-facts`, revision: 1 },
    status: revision > 1 ? "active" : "draft",
    createdAt: "2026-07-20T00:00:00Z",
    updatedAt: "2026-07-20T00:00:00Z",
  },
  factInstructions: {
    id: `${workflowID}-facts`,
    workflowId: workflowID,
    revision: 1,
    instructions: {
      Identity: {},
      ReleaseName: {},
      Metadata: {},
      SourceLookup: "",
      BlurayReleaseID: "",
      Playlist: { Set: false, Selected: [], UseAll: false },
      TrackerIDs: {},
    },
    fingerprint: "0".repeat(64),
    createdAt: "2026-07-20T00:00:00Z",
  },
});

type WorkflowStageFixtures = Readonly<{
  create(
    instructions: PrepareInput["Instructions"],
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  prepare(
    current: ReleaseWorkflowCurrent,
    input: PrepareInput,
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  selectCandidate(
    current: ReleaseWorkflowCurrent,
    releaseID: string,
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  replaceFacts(
    current: ReleaseWorkflowCurrent,
    instructions: PrepareInput["Instructions"],
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  project(
    current: ReleaseWorkflowCurrent,
    trackers: readonly string[],
    instructions: Readonly<Record<string, TrackerProjectionInstructions>>,
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  preflight(
    current: ReleaseWorkflowCurrent,
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  checkDuplicates(
    current: ReleaseWorkflowCurrent,
    skipRemote: boolean,
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  decideDuplicates(
    current: ReleaseWorkflowCurrent,
    decisions: Readonly<Record<string, DupeDecision>>,
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  captureMedia(
    current: ReleaseWorkflowCurrent,
    command: MediaCaptureInstructions,
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  generateDescriptions(
    current: ReleaseWorkflowCurrent,
    command: DescriptionInstructions,
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  dryRunUploads(
    current: ReleaseWorkflowCurrent,
    noSeed: boolean,
    trackerIDs: readonly string[],
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  executeUploads(
    current: ReleaseWorkflowCurrent,
    noSeed: boolean,
    trackerIDs: readonly string[],
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
}>;

type TestWorkflowPorts = ReleaseSessionPorts["workflow"] & WorkflowStageFixtures;

const workflowPorts = (overrides: Partial<TestWorkflowPorts> = {}): TestWorkflowPorts => {
  const currentByWorkflow = new Map<string, ReleaseWorkflowCurrent>();
  const continuationStep = new Map<string, number>();
  const pendingPreparation = new Set<string>();
  let activePorts!: TestWorkflowPorts;
  const remember = (current: ReleaseWorkflowCurrent) => {
    currentByWorkflow.set(current.workflow.id, current);
    return current;
  };
  const continueWorkflow = async (
    request: ContinueReleaseWorkflowRequest,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent> => {
    const workflowID = request.authority?.workflowId || "workflow-new";
    let current =
      currentByWorkflow.get(workflowID) || (await activePorts.current(workflowID, signal));
    const progressKey = `${workflowID}\u0000${request.idempotencyKey}`;
    const step = continuationStep.get(progressKey) || 0;
    let next = current;

    switch (request.goal) {
      case "prepared":
        if (!request.authority && step === 0) {
          const instructions =
            request.intent.factInstructions || request.intent.preparation?.Instructions;
          if (instructions) {
            next = await activePorts.create(
              instructions as PrepareInput["Instructions"],
              request.idempotencyKey,
              signal,
            );
            pendingPreparation.add(`${next.workflow.id}\u0000${request.idempotencyKey}`);
          }
        } else if (
          request.authority &&
          request.intent.preparation &&
          (step === 0 || pendingPreparation.has(progressKey))
        ) {
          pendingPreparation.delete(progressKey);
          next = await activePorts.prepare(
            current,
            request.intent.preparation as unknown as PrepareInput,
            request.idempotencyKey,
            signal,
          );
        }
        break;
      case "trackers_assessed":
        if (step === 0) {
          next = await activePorts.project(
            current,
            request.intent.trackerIds || [],
            request.intent.projectionInstructions || {},
            request.idempotencyKey,
            signal,
          );
        } else if (step === 1) {
          next = await activePorts.preflight(current, request.idempotencyKey, signal);
        }
        break;
      case "duplicates_decided":
        if (step === 0) {
          next = await activePorts.project(
            current,
            request.intent.trackerIds || [],
            request.intent.projectionInstructions || {},
            request.idempotencyKey,
            signal,
          );
        } else if (step === 1) {
          next = await activePorts.preflight(current, request.idempotencyKey, signal);
        } else if (step === 2) {
          next = await activePorts.checkDuplicates(
            current,
            request.intent.skipRemoteDuplicates || false,
            request.idempotencyKey,
            signal,
          );
        } else if (
          step === 3 &&
          request.intent.duplicateDecisions &&
          Object.keys(request.intent.duplicateDecisions).length > 0
        ) {
          next = await activePorts.decideDuplicates(
            current,
            request.intent.duplicateDecisions,
            request.idempotencyKey,
            signal,
          );
        }
        break;
      case "media_ready":
        if (step === 0 && request.intent.media) {
          next = await activePorts.captureMedia(
            current,
            request.intent.media,
            request.idempotencyKey,
            signal,
          );
        }
        break;
      case "descriptions_ready":
        if (step === 0 && request.intent.descriptions) {
          next = await activePorts.generateDescriptions(
            current,
            request.intent.descriptions,
            request.idempotencyKey,
            signal,
          );
        }
        break;
      case "dry_run":
        if (step === 0) {
          next = await activePorts.dryRunUploads(
            current,
            request.intent.noSeed || false,
            request.intent.uploadTrackerIds || [],
            request.idempotencyKey,
            signal,
          );
          if (!next.dryRun) {
            const revision = Math.max(next.workflow.revision, current.workflow.revision + 1);
            next = {
              ...next,
              workflow: {
                ...next.workflow,
                revision,
                dryRun: { id: `${workflowID}-dry-run`, revision },
              },
              dryRun: {
                id: `${workflowID}-dry-run`,
                workflowId: workflowID,
                revision,
                projectionSet: { id: `${workflowID}-projections`, revision: 1 },
                dupes: { id: `${workflowID}-dupes`, revision: 1 },
                media: { id: `${workflowID}-media`, revision: 1 },
                descriptions: { id: `${workflowID}-descriptions`, revision: 1 },
                inputFingerprint: "2".repeat(64),
                noSeed: request.intent.noSeed || false,
                trackerIds: request.intent.uploadTrackerIds || [],
                reports: [],
                succeededCount: 0,
                failedCount: 0,
                skippedCount: 0,
                status: "skipped",
                createdAt: "2026-07-20T00:00:00Z",
              },
            } as unknown as ReleaseWorkflowCurrent;
          }
        }
        break;
      case "uploaded":
        if (step === 0) {
          next = await activePorts.executeUploads(
            current,
            request.intent.noSeed || false,
            request.intent.uploadTrackerIds || [],
            request.idempotencyKey,
            signal,
          );
        }
        break;
    }

    continuationStep.set(progressKey, step + 1);
    current = remember(next);
    return current;
  };
  const basePorts: TestWorkflowPorts = {
    continue: continueWorkflow,
    create: async () => workflowCurrent("workflow-new", 1),
    current: async (workflowID) => workflowCurrent(workflowID, 1),
    operation: async (workflowID, operationID) => ({
      id: operationID,
      workflowId: workflowID,
      revision: 1,
      sequence: 1,
      command: "fixture",
      operation: "preparation",
      status: "completed",
      progress: 100,
      completed: 1,
      total: 1,
      startedAt: "2026-07-20T00:00:00Z",
      updatedAt: "2026-07-20T00:00:01Z",
    }),
    cancelOperation: async (workflowID, operationID) => ({
      id: operationID,
      workflowId: workflowID,
      revision: 1,
      sequence: 1,
      command: "fixture",
      operation: "preparation",
      status: "canceled",
      progress: 0,
      completed: 0,
      total: 1,
      startedAt: "2026-07-20T00:00:00Z",
      updatedAt: "2026-07-20T00:00:01Z",
    }),
    prepare: async (current, input) =>
      workflowCurrentFromPreview(
        workflowCurrent(current.workflow.id, current.workflow.revision + 1),
        preview(input.SourcePath, current.workflow.revision + 1),
      ),
    selectCandidate: async (current) =>
      workflowCurrent(current.workflow.id, current.workflow.revision + 1),
    replaceFacts: async (current) => current,
    project: async (current, trackers) => ({
      ...current,
      workflow: {
        ...current.workflow,
        revision: current.workflow.revision + 1,
        trackerProjections: {
          id: `${current.workflow.id}-projections`,
          revision: current.workflow.revision + 1,
        },
      },
      projections: {
        status: "ready",
        projections: trackers.map((trackerId) => ({
          trackerId,
          displayName: trackerId,
          canonicalReleaseName: "Example Release 2026 1080p-GRP",
          uploadReleaseName: "Example.Release.2026.1080p-GRP",
          artifacts: {
            screenshotCount: 1,
            dvdMenuCount: 1,
            imageHosting: true,
            description: true,
          },
          policyDecisions: [],
          readiness: "ready",
        })),
      } as unknown as NonNullable<ReleaseWorkflowCurrent["projections"]>,
    }),
    preflight: async (current) => ({
      ...current,
      workflow: {
        ...current.workflow,
        revision: current.workflow.revision + 1,
        trackerPreflight: {
          id: `${current.workflow.id}-preflight`,
          revision: current.workflow.revision + 1,
        },
      },
      preflight: {
        status: "ready",
        results: (current.projections?.projections || []).map((projection) => ({
          trackerId: projection.trackerId,
          state: "ready",
        })),
      } as unknown as NonNullable<ReleaseWorkflowCurrent["preflight"]>,
    }),
    checkDuplicates: async (current) => ({
      ...current,
      workflow: {
        ...current.workflow,
        revision: current.workflow.revision + 1,
        dupes: {
          id: `${current.workflow.id}-dupes`,
          revision: current.workflow.revision + 1,
        },
      },
      dupes: {
        status: "completed",
        results: (current.projections?.projections || []).map((projection) => ({
          trackerId: projection.trackerId,
          uploadReleaseName: projection.uploadReleaseName,
          matches: [],
          decision: "no_match",
          status: "completed",
        })),
      } as unknown as NonNullable<ReleaseWorkflowCurrent["dupes"]>,
    }),
    decideDuplicates: async (current) => current,
    captureMedia: async (current) => current,
    mediaPlan: async (workflowID) =>
      ({
        id: `${workflowID}-media-plan`,
        workflowId: workflowID,
        revision: 1,
        release: { id: `${workflowID}-release`, revision: 1 },
        projectionSet: { id: `${workflowID}-projections`, revision: 1 },
        durationSeconds: 120,
        frameRate: 24,
        suggestedSelections: [],
        createdAt: "2026-07-20T00:00:00Z",
      }) as Awaited<ReturnType<ReleaseSessionPorts["workflow"]["mediaPlan"]>>,
    previewFrame: async (current, timestampSeconds) => ({
      id: "preview-1",
      workflowId: current.workflow.id,
      workflowRevision: current.workflow.revision,
      release: current.workflow.release!,
      timestampSeconds,
      contentUrl: "/preview/1",
      expiresAt: "2026-07-20T01:00:00Z",
    }),
    setMediaSelection: async (current) => current,
    reorderMedia: async (current) => current,
    deleteMedia: async (current) => current,
    stageMedia: async (_current, file) => ({
      id: `resource-${file.name}`,
      contentType: file.type,
      sizeBytes: file.size,
    }),
    attachMedia: async (current) => current,
    uploadImages: async (current) => current,
    retryImageHost: async (current) => current,
    removeHostedImages: async (current) => current,
    mediaURL: (_current, artifactID) => `/media/${artifactID}`,
    generateDescriptions: async (current) => current,
    saveDescriptionOverride: async (current) => current,
    resetDescriptionOverride: async (current) => current,
    dryRunUploads: async (current) => current,
    executeUploads: async (current) => current,
    retryFailedUploads: async (current) => current,
    retryClientInjections: async (current) => current,
    cancel: async (workflowID) => workflowCurrent(workflowID, 2),
    invalidateTrackers: async (current) => current,
  };
  const configured = Object.assign(basePorts, overrides);
  activePorts = {
    ...configured,
    continue: overrides.continue || continueWorkflow,
    create: async (...args) => remember(await configured.create(...args)),
    current: async (...args) => remember(await configured.current(...args)),
    prepare: async (...args) => remember(await configured.prepare(...args)),
    selectCandidate: async (...args) => remember(await configured.selectCandidate(...args)),
    replaceFacts: async (...args) => remember(await configured.replaceFacts(...args)),
    setMediaSelection: async (...args) => remember(await configured.setMediaSelection(...args)),
    reorderMedia: async (...args) => remember(await configured.reorderMedia(...args)),
    deleteMedia: async (...args) => remember(await configured.deleteMedia(...args)),
    attachMedia: async (...args) => remember(await configured.attachMedia(...args)),
    uploadImages: async (...args) => remember(await configured.uploadImages(...args)),
    retryImageHost: async (...args) => remember(await configured.retryImageHost(...args)),
    removeHostedImages: async (...args) => remember(await configured.removeHostedImages(...args)),
    saveDescriptionOverride: async (...args) =>
      remember(await configured.saveDescriptionOverride(...args)),
    resetDescriptionOverride: async (...args) =>
      remember(await configured.resetDescriptionOverride(...args)),
    retryFailedUploads: async (...args) => remember(await configured.retryFailedUploads(...args)),
    retryClientInjections: async (...args) =>
      remember(await configured.retryClientInjections(...args)),
    cancel: async (...args) => remember(await configured.cancel(...args)),
    invalidateTrackers: async (...args) => remember(await configured.invalidateTrackers(...args)),
  };
  return activePorts;
};

const workflowCurrentFromPreview = (
  current: ReleaseWorkflowCurrent,
  value: MetadataPreview,
): ReleaseWorkflowCurrent =>
  ({
    ...current,
    workflow: {
      ...current.workflow,
      release: { id: `${current.workflow.id}-release`, revision: current.workflow.revision },
    },
    release: {
      id: `${current.workflow.id}-release`,
      workflowId: current.workflow.id,
      revision: current.workflow.revision,
      factInstructions: current.workflow.factInstructions,
      release: {
        Generation: value.Release.Generation,
        Source: { SourcePath: value.SourcePath },
        Naming: { ReleaseName: value.ReleaseName },
        Identity: value.Identity,
        ProviderMetadata: { Bluray: value.Bluray },
      },
      display: { ...value.Display, TrackerData: value.TrackerData },
      diagnostics: value.Diagnostics,
      fingerprint: "1".repeat(64),
      createdAt: "2026-07-20T00:00:00Z",
    },
  }) as unknown as ReleaseWorkflowCurrent;

const wrapperFor = (ports: ReleaseSessionPorts) =>
  function Wrapper({ children }: Readonly<{ children: ReactNode }>) {
    return <ReleaseSessionProvider ports={ports}>{children}</ReleaseSessionProvider>;
  };

const selectAndPrepare = async (
  result: { current: ReturnType<typeof useReleaseSession> },
  sourcePath: string,
) => {
  act(() => result.current.input.updateSourceDraft(sourcePath));
  act(() => result.current.input.selectSource(sourcePath));
  act(() => result.current.upload.chooseTrackers(["AITHER"]));
  await act(() => result.current.input.prepare());
  await act(() => result.current.duplicates.run());
};

describe("tracker workflow capabilities", () => {
  it("opens only applicable pages and keeps tracker-scoped content failures local", () => {
    const available = (
      overrides: Readonly<Record<string, boolean>> = {},
    ): WorkflowContinuation => ({
      lifecycle: "ready",
      disposition: "none",
      refs: {},
      availableGoals: [
        "trackers_assessed",
        "media_ready",
        "descriptions_ready",
        "upload_reviewed",
      ].map((goal) => ({
        goal,
        available: overrides[goal] ?? true,
        reason: overrides[goal] === false ? `Backend blocked ${goal}.` : "",
      })),
    });
    const none = routeAccess(available(), false, { needsImages: false, needsDescriptions: false });
    expect(none.screenshots.available).toBe(false);
    expect(none.descriptions.available).toBe(false);
    expect(none.upload.available).toBe(true);

    const screenshots = routeAccess(available({ upload_reviewed: false }), false, {
      needsImages: true,
      needsDescriptions: false,
    });
    expect(screenshots.screenshots.available).toBe(true);
    expect(screenshots.descriptions.available).toBe(false);
    expect(screenshots.upload.available).toBe(false);
    expect(screenshots.upload.reason).toBe("Backend blocked upload_reviewed.");

    const description = routeAccess(available(), false, {
      needsImages: true,
      needsDescriptions: true,
    });
    expect(description.screenshots.available).toBe(true);
    expect(description.descriptions.available).toBe(true);
    expect(description.upload.available).toBe(true);
    expect(description.upload.reason).toBe("");
  });
});

describe("useReleaseSession", () => {
  it("reloads authoritative workflow state from the retained browser workflow id", async () => {
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", "workflow-retained");
    const sourcePath = "C:\\media\\Example.Release.2026.1080p-GRP.mkv";
    const current = vi.fn(async (workflowID: string) => {
      const restored = workflowCurrentFromPreview(
        workflowCurrent(workflowID, 7),
        preview(sourcePath, 7),
      );
      return {
        ...restored,
        selection: {
          id: "selection-1",
          workflowId: workflowID,
          revision: 4,
          catalog: { id: "catalog-1", revision: 2 },
          runtime: { id: "runtime-1", revision: 3 },
          trackerIds: ["AITHER"],
          fingerprint: "1".repeat(64),
          createdAt: "2026-07-20T00:00:00Z",
        },
      } as unknown as ReleaseWorkflowCurrent;
    });
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ workflow: workflowPorts({ current }) })),
    });

    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
    expect(current).toHaveBeenCalledWith("workflow-retained", expect.any(AbortSignal));
    expect(result.current.workflow.view.current?.workflow).toMatchObject({
      id: "workflow-retained",
      revision: 7,
    });
    expect(result.current.upload.view.selectedTrackers).toEqual(["AITHER"]);

    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("passes the current skip-client option to an explicit workflow dry run", async () => {
    const workflowID = "workflow-dry-run-options";
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const retained = workflowCurrent(workflowID, 7);
    const dryRunUploads = vi.fn(async (current: ReleaseWorkflowCurrent) => current);
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({
            current: async () => retained,
            dryRunUploads,
          }),
        }),
      ),
    });

    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
    act(() => result.current.upload.changeOptions({ noSeed: true }));
    await act(() => result.current.upload.runDryRun());

    expect(dryRunUploads).toHaveBeenCalledWith(
      expect.objectContaining({ workflow: expect.objectContaining({ id: workflowID }) }),
      true,
      [],
      expect.any(String),
      expect.any(AbortSignal),
    );

    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("confirms a retained rule authorization before upload", async () => {
    const workflowID = "workflow-authorize-upload";
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const action = {
      createdAt: "2026-07-20T00:00:00Z",
      id: "action-authorize",
      kind: "authorize_rules" as const,
      prompt: "Confirm BTN autofill.",
      status: "pending" as const,
      workflowRevision: 7,
    };
    const base = workflowCurrent(workflowID, 7);
    const retained: ReleaseWorkflowCurrent = {
      ...base,
      workflow: { ...base.workflow, status: "blocked", requiredActions: [action] },
      continuation: { ...base.continuation, requiredActions: [action] },
    };
    const continueWorkflow = vi.fn(async () => retained);
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({
            current: async () => retained,
            continue: continueWorkflow,
          }),
        }),
      ),
    });

    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
    await act(async () => {
      expect(await result.current.workflow.confirmAction(action)).toBe(true);
    });
    expect(continueWorkflow).toHaveBeenCalledWith(
      expect.objectContaining({
        goal: "uploaded",
        answers: [{ actionId: action.id, workflowRevision: 7, confirmed: true }],
      }),
      expect.any(AbortSignal),
    );

    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("executes a direct upload through one workflow command", async () => {
    const workflowID = "workflow-skipped-upload";
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const retained = workflowCurrent(workflowID, 7);
    const executeUploads = vi.fn(async (current: ReleaseWorkflowCurrent) => current);
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({
            current: async () => retained,
            executeUploads,
          }),
        }),
      ),
    });

    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
    await act(async () => {
      expect(await result.current.upload.start()).toBe(true);
    });

    expect(executeUploads).toHaveBeenCalledWith(
      expect.objectContaining({ workflow: expect.objectContaining({ id: workflowID }) }),
      false,
      [],
      expect.any(String),
      expect.any(AbortSignal),
    );

    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("resumes an accepted workflow operation by polling without browser events", async () => {
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", "workflow-active");
    const startedAt = "2026-07-20T00:00:00Z";
    const operation = (status: "queued" | "running" | "completed", sequence: number) =>
      ({
        id: "operation-active",
        workflowId: "workflow-active",
        revision: 2,
        resultRevision: status === "completed" ? 3 : undefined,
        sequence,
        command: "check_duplicates",
        operation: "duplicate_check",
        phase: "duplicate_check",
        status,
        progress: status === "queued" ? 0 : status === "running" ? 50 : 100,
        completed: status === "completed" ? 1 : 0,
        total: 1,
        message: status === "completed" ? "Operation complete." : "Checking tracker.",
        startedAt,
        updatedAt: startedAt,
        completedAt: status === "completed" ? startedAt : undefined,
      }) as const;
    const current = vi
      .fn()
      .mockResolvedValueOnce({
        ...workflowCurrent("workflow-active", 2),
        operation: operation("queued", 1),
      })
      .mockResolvedValueOnce({
        ...workflowCurrent("workflow-active", 3),
        operation: operation("completed", 3),
      });
    const poll = vi
      .fn()
      .mockResolvedValueOnce(operation("running", 2))
      .mockResolvedValueOnce(operation("completed", 3));
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ workflow: workflowPorts({ current, operation: poll }) })),
    });

    await waitFor(() => expect(result.current.workflow.view.current?.workflow.revision).toBe(3), {
      timeout: 3000,
    });
    expect(poll).toHaveBeenCalledTimes(2);
    expect(result.current.workflow.view.current?.operation?.status).toBe("completed");

    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("surfaces the safe failure retained by a terminal workflow operation", async () => {
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", "workflow-failed");
    const startedAt = "2026-07-20T00:00:00Z";
    const queued = {
      id: "operation-failed",
      workflowId: "workflow-failed",
      revision: 1,
      sequence: 1,
      command: "prepare",
      operation: "preparation",
      status: "queued",
      progress: 0,
      completed: 0,
      total: 1,
      startedAt,
      updatedAt: startedAt,
    } as const;
    const failed = {
      ...queued,
      sequence: 2,
      status: "failed",
      progress: 100,
      completed: 1,
      message: "Operation failed.",
      failures: [
        {
          failure: {
            Code: "source_unavailable",
            Operation: "preparation",
            Message: "The source path is unavailable.",
            Recovery: "edit_input",
          },
        },
      ],
      completedAt: startedAt,
    } as const;
    const current = vi
      .fn()
      .mockResolvedValueOnce({ ...workflowCurrent("workflow-failed", 1), operation: queued })
      .mockResolvedValueOnce({ ...workflowCurrent("workflow-failed", 1), operation: failed });
    const operation = vi.fn().mockResolvedValue(failed);
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ workflow: workflowPorts({ current, operation }) })),
    });

    await waitFor(() => expect(result.current.workflow.view.status).toBe("error"));
    expect(result.current.workflow.view.error).toBe(
      "The source path is unavailable. Recovery: edit input.",
    );
    expect(result.current.workflow.view.failure?.Code).toBe("source_unavailable");

    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("saves and resets authoritative descriptions through revisioned workflow commands", async () => {
    const workflowID = "workflow-descriptions";
    const sourcePath = "C:\\media\\Example.Release.2026.1080p-GRP.mkv";
    const withDescription = (
      revision: number,
      unit3dSource: string,
      standaloneSource = "standalone source",
    ): ReleaseWorkflowCurrent => {
      const current = workflowCurrent(workflowID, revision);
      return {
        ...current,
        workflow: {
          ...current.workflow,
          descriptions: { id: "descriptions-1", revision },
        },
        descriptions: {
          id: "descriptions-1",
          workflowId: workflowID,
          revision,
          release: { id: "release-1", revision: 1 },
          releaseRef: { SourcePath: sourcePath, Generation: 1 },
          projectionSet: { id: "projections-1", revision: 2 },
          media: { id: "media-1", revision: 3 },
          inputFingerprint: "1".repeat(64),
          templateFingerprint: "2".repeat(64),
          descriptions: [
            {
              groupKey: "unit3d",
              trackerIds: ["AITHER"],
              source: unit3dSource,
              rendered: `<p>${unit3dSource}</p>`,
              contentFingerprint: "3".repeat(64),
            },
            {
              groupKey: "standalone",
              trackerIds: ["BLU"],
              source: standaloneSource,
              rendered: `<p>${standaloneSource}</p>`,
              contentFingerprint: "4".repeat(64),
            },
          ],
          status: "completed",
          createdAt: "2026-07-21T00:00:00Z",
        },
      };
    };
    const saveDescriptionOverride = vi
      .fn()
      .mockResolvedValueOnce(withDescription(8, "edited source"));
    const resetDescriptionOverride = vi
      .fn()
      .mockResolvedValueOnce(withDescription(9, "regenerated source"));
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({
            current: async () => withDescription(7, "generated source"),
            saveDescriptionOverride,
            resetDescriptionOverride,
          }),
        }),
      ),
    });

    await waitFor(() => expect(result.current.descriptions.view.artifact?.revision).toBe(7));
    act(() => result.current.descriptions.edit("unit3d", "edited source"));
    await act(() => result.current.descriptions.save("unit3d"));

    expect(saveDescriptionOverride).toHaveBeenCalledWith(
      expect.anything(),
      "unit3d",
      "edited source",
      expect.any(String),
      expect.any(AbortSignal),
    );
    expect(result.current.descriptions.view.artifact?.revision).toBe(8);
    expect(result.current.descriptions.view.artifact?.descriptions[1]?.source).toBe(
      "standalone source",
    );
    expect(result.current.descriptions.view.notice).toBe("Description saved.");

    await act(() => result.current.descriptions.reset("unit3d"));

    expect(resetDescriptionOverride).toHaveBeenCalledWith(
      expect.anything(),
      "unit3d",
      expect.any(String),
      expect.any(AbortSignal),
    );
    expect(result.current.descriptions.view.artifact?.revision).toBe(9);
    expect(result.current.descriptions.view.artifact?.descriptions[0]?.source).toBe(
      "regenerated source",
    );
    expect(result.current.descriptions.view.notice).toBe("Description reset.");

    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("prepares through the backend workflow and retains only its compatibility preview", async () => {
    const create = vi.fn(async () => workflowCurrent("workflow-browser", 1));
    const prepareWorkflow = vi.fn(
      async (
        _current: ReleaseWorkflowCurrent,
        _input: PrepareInput,
        _idempotencyKey: string,
        _signal: AbortSignal,
      ) => {
        return workflowCurrentFromPreview(
          workflowCurrent("workflow-browser", 2),
          preview("C:\\media\\Example Release", 2),
        );
      },
    );
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({
            create,
            prepare: prepareWorkflow,
          }),
        }),
      ),
    });

    act(() => result.current.input.selectSource("C:\\media\\Example Release"));
    act(() =>
      result.current.input.changeMetadata({
        Distributor: "Example Distributor",
        OriginalLanguage: "ja",
      }),
    );
    await act(() => result.current.input.prepare());

    expect(create).toHaveBeenCalledOnce();
    expect(create).toHaveBeenCalledWith(
      expect.objectContaining({
        Metadata: {
          Distributor: "Example Distributor",
          OriginalLanguage: "ja",
        },
      }),
      expect.any(String),
      expect.any(AbortSignal),
    );
    expect(prepareWorkflow).toHaveBeenCalledOnce();
    expect(prepareWorkflow).toHaveBeenCalledWith(
      expect.anything(),
      expect.anything(),
      expect.any(String),
      expect.any(AbortSignal),
    );
    expect(result.current.workflow.view.current?.workflow.revision).toBe(2);
    expect(result.current.identity.view.release).toEqual({
      SourcePath: "C:\\media\\Example Release",
      Generation: 2,
    });
    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("renders and resumes the backend playlist required action", async () => {
    const sourcePath = "C:\\media\\Example Disc";
    const withRelease = (
      current: ReleaseWorkflowCurrent,
      required: boolean,
    ): ReleaseWorkflowCurrent =>
      ({
        ...(required
          ? current
          : workflowCurrentFromPreview(current, preview(sourcePath, current.workflow.revision))),
        workflow: {
          ...current.workflow,
          status: required ? "blocked" : "active",
          requiredActions: required
            ? [
                {
                  id: "action-playlist",
                  kind: "select_playlist",
                  status: "pending",
                  workflowRevision: current.workflow.revision,
                  prompt: "Select one or more Blu-ray playlists to analyze.",
                  options: [
                    {
                      value: "00001.mpls",
                      label: "00001.mpls",
                      playlist: {
                        file: "00001.mpls",
                        duration: 7200,
                        items: [{ file: "00001.m2ts", size: 4_000_000_000 }],
                        score: 91.25,
                        edition: "Example Edition",
                      },
                    },
                  ],
                  createdAt: "2026-07-20T00:00:00Z",
                },
              ]
            : [],
        },
      }) as unknown as ReleaseWorkflowCurrent;
    const prepareWorkflow = vi
      .fn()
      .mockResolvedValueOnce(withRelease(workflowCurrent("workflow-playlist-browser", 2), true))
      .mockResolvedValueOnce(withRelease(workflowCurrent("workflow-playlist-browser", 4), false));
    const create = vi.fn(async () => workflowCurrent("workflow-playlist-browser", 1));
    const replaceFacts = vi.fn(async () => workflowCurrent("workflow-playlist-browser", 3));
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({
            create,
            prepare: prepareWorkflow,
            replaceFacts,
          }),
        }),
      ),
    });

    act(() => result.current.input.selectSource(sourcePath));
    await act(() => result.current.input.prepare());

    expect(result.current.input.view.status).toBe("awaiting_input");
    expect(result.current.input.view.playlist).toEqual(
      expect.objectContaining({
        required: true,
        selected: ["00001.mpls"],
        candidates: [
          {
            file: "00001.mpls",
            duration: 7200,
            items: [{ file: "00001.m2ts", size: 4_000_000_000 }],
            score: 91.25,
            edition: "Example Edition",
          },
        ],
      }),
    );

    await act(() => result.current.input.confirmPlaylists());

    expect(create).toHaveBeenCalledOnce();
    expect(replaceFacts).not.toHaveBeenCalled();
    expect(prepareWorkflow).toHaveBeenLastCalledWith(
      expect.anything(),
      expect.objectContaining({
        Instructions: expect.objectContaining({
          Playlist: { Set: true, Selected: ["00001.mpls"], UseAll: false },
        }),
      }),
      expect.any(String),
      expect.any(AbortSignal),
    );
    expect(result.current.input.view.status).toBe("ready");
    expect(result.current.workflow.view.status).toBe("ready");
    expect(result.current.duplicates.view.status).toBe("idle");
    expect(result.current.workflow.view.current?.workflow.revision).toBe(4);

    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("routes the browser release flow through authoritative workflow commands", async () => {
    let revision = 2;
    const advance = (current: ReleaseWorkflowCurrent) =>
      workflowCurrent(current.workflow.id, ++revision);
    const project = vi.fn(async (current: ReleaseWorkflowCurrent) => advance(current));
    const preflight = vi.fn(async (current: ReleaseWorkflowCurrent) => {
      const next = advance(current);
      return {
        ...next,
        projections: {
          status: "ready",
          projections: [
            {
              trackerId: "AITHER",
              artifacts: {
                screenshotCount: 1,
                dvdMenuCount: 0,
                imageHosting: true,
                description: true,
              },
            },
          ],
        } as unknown as NonNullable<ReleaseWorkflowCurrent["projections"]>,
        preflight: { status: "ready" } as NonNullable<ReleaseWorkflowCurrent["preflight"]>,
      };
    });
    const checkDuplicates = vi.fn(async (current: ReleaseWorkflowCurrent) => ({
      ...advance(current),
      projections: current.projections,
      preflight: current.preflight,
      dupes: {
        status: "completed",
        results: [
          {
            trackerId: "AITHER",
            uploadReleaseName: "Example.Release.S01E01.1080p-GRP",
            matches: [
              {
                id: "123",
                name: "Example.Release.S01E01.1080p-GRP",
                reason: "in_client",
              },
            ],
            decision: "accepted",
            status: "completed",
          },
        ],
      } as unknown as NonNullable<ReleaseWorkflowCurrent["dupes"]>,
    }));
    const captureMedia = vi.fn(
      async (current: ReleaseWorkflowCurrent, instructions: MediaCaptureInstructions) => {
        const next = advance(current);
        return {
          ...current,
          workflow: {
            ...next.workflow,
            media: { id: "media-browser", revision: next.workflow.revision },
          },
          media: {
            id: "media-browser",
            workflowId: next.workflow.id,
            revision: next.workflow.revision,
            release: { id: "release-browser", revision: 1 },
            releaseRef: {
              SourcePath: "C:\\media\\Example Release",
              Generation: 2,
            },
            projectionSet: { id: "projections-browser", revision: 1 },
            captureFingerprint: "5".repeat(64),
            requirementsFingerprint: "6".repeat(64),
            imageRequirementsPrepared: false,
            artifacts: Array.from({ length: instructions.screenshotCount }, (_, index) => ({
              id: `screen-${index}`,
              kind: "screenshot" as const,
              purpose: "final" as const,
              selected: true,
              order: index,
            })),
            status: "completed" as const,
            createdAt: "2026-07-21T00:00:00Z",
          },
        };
      },
    );
    const generateDescriptions = vi.fn(
      async (current: ReleaseWorkflowCurrent, _instructions: DescriptionInstructions) =>
        advance(current),
    );
    const uploadImages = vi.fn(async (current: ReleaseWorkflowCurrent) => {
      const next = advance(current);
      return {
        ...next,
        workflow: {
          ...next.workflow,
          media: { id: "media-hosted", revision: next.workflow.revision },
        },
        media: {
          ...current.media!,
          id: "media-hosted",
          revision: next.workflow.revision,
          workflowId: next.workflow.id,
          imageRequirementsPrepared: true,
          failedHosts: ["imgbox"],
          hostAttempts: [
            {
              id: "host-attempt-1",
              media: { id: current.media!.id, revision: current.media!.revision },
              host: "imgbox",
              status: "failed" as const,
              fallback: false,
              artifactIds: ["screen-0"],
              failures: [],
              attemptedAt: "2026-07-21T00:00:00Z",
            },
          ],
        },
      };
    });
    const executeUploads = vi.fn(async (current: ReleaseWorkflowCurrent) => advance(current));
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({
            create: async () => workflowCurrent("workflow-browser-flow", 1),
            prepare: async (current) =>
              workflowCurrentFromPreview(
                workflowCurrent(current.workflow.id, 2),
                preview("C:\\media\\Example Release", 2),
              ),
            project,
            preflight,
            checkDuplicates,
            captureMedia,
            uploadImages,
            generateDescriptions,
            executeUploads,
          }),
        }),
      ),
    });

    act(() => result.current.input.selectSource("C:\\media\\Example Release"));
    act(() => result.current.duplicates.chooseTrackers(["AITHER"]));
    await act(() => result.current.input.prepare());
    await act(() => result.current.duplicates.run());
    expect(result.current.navigation.view.access.screenshots.available).toBe(true);
    await act(() =>
      result.current.screenshots.generate("final", [
        { Index: 0, TimestampSeconds: 10, Frame: 240, Source: "manual" },
        { Index: 1, TimestampSeconds: 20, Frame: 480, Source: "manual" },
        { Index: 2, TimestampSeconds: 30, Frame: 720, Source: "manual" },
        { Index: 3, TimestampSeconds: 40, Frame: 960, Source: "manual" },
      ]),
    );
    expect(uploadImages).not.toHaveBeenCalled();
    await act(() => result.current.uploadedImages.load());
    await act(async () => {
      expect(await result.current.uploadedImages.upload()).toBe(true);
    });
    expect(uploadImages).toHaveBeenCalledOnce();
    await act(() => result.current.descriptions.load());
    await act(() => result.current.upload.start());

    expect(project).toHaveBeenCalledOnce();
    expect(preflight).toHaveBeenCalledOnce();
    expect(checkDuplicates).toHaveBeenCalledOnce();
    expect(captureMedia).toHaveBeenCalled();
    expect(generateDescriptions).toHaveBeenCalledOnce();
    expect(generateDescriptions.mock.calls[0]?.[1].options.Screens).toBe(4);
    expect(generateDescriptions.mock.calls[0]?.[1].imageHost).toEqual({
      FailedHosts: ["imgbox"],
      SkipUpload: true,
    });
    expect(executeUploads).toHaveBeenCalledWith(
      expect.objectContaining({
        workflow: expect.objectContaining({ id: "workflow-browser-flow" }),
      }),
      false,
      [],
      expect.any(String),
      expect.any(AbortSignal),
    );

    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("accepts selective tracker invalidation without retaining downstream plan state", async () => {
    const projection = (trackerId: string, readiness: "ready" | "stale") => ({
      trackerId,
      displayName: trackerId,
      canonicalReleaseName: "Example.Release.2026.1080p-GRP",
      uploadReleaseName: `Example.Release.2026.1080p-${trackerId}`,
      additionalNames: {},
      taxonomy: {},
      descriptionGroup: trackerId.toLowerCase(),
      metadataLocale: "",
      duplicateCriteria: {},
      artifacts: {
        screenshotCount: 0,
        dvdMenuCount: 0,
        mediaInfo: false,
        bdInfo: false,
        nfo: false,
        description: false,
        imageHosting: false,
        torrent: true,
      },
      policy: { decisions: [], failures: [] },
      uploadReady: readiness === "ready",
      dupeReady: readiness === "ready",
      readiness,
      inputFingerprint: "1".repeat(64),
      catalogFingerprint: "2".repeat(64),
      configFingerprint: "3".repeat(64),
      projectorFingerprint: "4".repeat(64),
      semanticFingerprint: "5".repeat(64),
    });
    const initial = workflowCurrentFromPreview(
      workflowCurrent("workflow-selective-browser", 2),
      preview("C:\\media\\Example Release", 2),
    );
    const withDryRun = {
      ...initial,
      workflow: {
        ...initial.workflow,
        trackerProjections: { id: "projections-1", revision: 2 },
        dryRun: { id: "dry-run-1", revision: 2 },
      },
      projections: {
        id: "projections-1",
        workflowId: initial.workflow.id,
        revision: 2,
        release: { id: "release-1", revision: 2 },
        releaseRef: { SourcePath: "C:\\media\\Example Release", Generation: 2 },
        selection: { id: "selection-1", revision: 2 },
        projections: [projection("ALPHA", "ready"), projection("BETA", "ready")],
        inputFingerprint: "6".repeat(64),
        policyFingerprint: "7".repeat(64),
        status: "ready",
        createdAt: "2026-07-20T00:00:00Z",
      },
      dryRun: { id: "dry-run-1" },
    } as unknown as ReleaseWorkflowCurrent;
    const invalidateTrackers = vi.fn(async () => {
      const invalidated = workflowCurrent(initial.workflow.id, 3);
      return {
        ...invalidated,
        workflow: {
          ...invalidated.workflow,
          trackerProjections: { id: "projections-1", revision: 3 },
        },
        projections: {
          ...withDryRun.projections!,
          revision: 3,
          projections: [projection("ALPHA", "stale"), projection("BETA", "ready")],
        },
      } as unknown as ReleaseWorkflowCurrent;
    });
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({
            create: async () => workflowCurrent(initial.workflow.id, 1),
            prepare: async () => withDryRun,
            invalidateTrackers,
          }),
        }),
      ),
    });

    act(() => result.current.input.selectSource("C:\\media\\Example Release"));
    await act(() => result.current.input.prepare());
    await act(() => result.current.workflow.invalidateTrackers(["ALPHA"], "config changed"));

    expect(invalidateTrackers).toHaveBeenCalledWith(
      expect.anything(),
      ["ALPHA"],
      "config changed",
      expect.any(String),
      expect.any(AbortSignal),
    );
    expect(result.current.workflow.view.current?.projections?.projections).toEqual([
      expect.objectContaining({ trackerId: "ALPHA", readiness: "stale" }),
      expect.objectContaining({ trackerId: "BETA", readiness: "ready" }),
    ]);
    expect(result.current.workflow.view.current?.dryRun).toBeUndefined();

    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("keeps manual path typing as an unselected draft", () => {
    const { result } = renderHook(useReleaseSession, { wrapper: wrapperFor(portsFor()) });

    act(() => result.current.input.updateSourceDraft("C:\\media\\Example"));

    expect(result.current.input.view.sourceDraft).toBe("C:\\media\\Example");
    expect(result.current.identity.view.sourcePath).toBe("");
    expect(result.current.identity.view.release).toBeNull();
  });

  it.each([
    "C:\\media\\Example.Release.2026.mkv",
    "C:\\media\\Example Season",
    "C:\\media\\Example DVD\\VIDEO_TS",
    "C:\\media\\Example Blu-ray",
    "C:\\media\\Example Blu-ray\\BDMV",
  ])("allows duplicate navigation for a prepared source shape: %s", async (sourcePath) => {
    const { result } = renderHook(useReleaseSession, { wrapper: wrapperFor(portsFor()) });
    act(() => result.current.input.selectSource(sourcePath));
    await act(() =>
      result.current.input.prepareSource(sourcePath, {
        ...result.current.input.view.intent,
        playlist: /Blu-ray/.test(sourcePath)
          ? { Set: true, Selected: ["00001.mpls"], UseAll: false }
          : result.current.input.view.intent.playlist,
      }),
    );

    expect(result.current.navigation.view.access.duplicates).toEqual({
      available: true,
      reason: "",
    });
  });

  it("delegates disc and playlist decisions to canonical preparation", async () => {
    const prepare = vi.fn(async (current: ReleaseWorkflowCurrent, input: PrepareInput) =>
      workflowCurrentFromPreview(
        workflowCurrent(current.workflow.id, current.workflow.revision + 1),
        preview(input.SourcePath, 1),
      ),
    );
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ workflow: workflowPorts({ prepare }) })),
    });

    act(() => result.current.input.selectSource("C:\\media\\Example Disc"));
    act(() => result.current.input.chooseTrackers(["GRP"]));
    await act(() => result.current.input.prepare());

    expect(prepare.mock.calls[0]?.[1]).toEqual(
      expect.objectContaining({
        SourcePath: "C:\\media\\Example Disc",
        Controls: { Interaction: "interactive", ConfirmBDMVRescan: false },
      }),
    );
    expect(result.current.identity.view.release).toEqual({
      SourcePath: "C:\\media\\Example Disc",
      Generation: 1,
    });
  });

  it("retries the same preparation with explicit BDMV rescan permission", async () => {
    const prepare = vi.fn(async (current: ReleaseWorkflowCurrent, input: PrepareInput) => {
      if (!input.Controls?.ConfirmBDMVRescan) {
        throw Object.assign(new Error("Confirmation required."), {
          failure: {
            Code: "confirmation_required",
            Operation: "preparation",
            Message: "Blu-ray playlist changes require confirmation before rescanning.",
            Recovery: "confirm",
          },
        });
      }
      return workflowCurrentFromPreview(
        workflowCurrent(current.workflow.id, current.workflow.revision + 1),
        preview(input.SourcePath, 1),
      );
    });
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ workflow: workflowPorts({ prepare }) })),
    });
    const sourcePath = "C:\\media\\Example Disc";
    const directIntent = {
      ...result.current.input.view.intent,
      playlist: { Set: true, Selected: ["00001.mpls"], UseAll: false },
    };

    act(() => result.current.input.selectSource(sourcePath));
    await act(() => result.current.input.prepareSource(sourcePath, directIntent));

    expect(result.current.input.view.failure?.Recovery).toBe("confirm");
    await act(() => result.current.input.confirmBDMVRescan());

    expect(prepare).toHaveBeenCalledTimes(2);
    expect(prepare.mock.calls.map((call) => call[1].Controls?.ConfirmBDMVRescan)).toEqual([
      false,
      true,
    ]);
    expect(result.current.identity.view.release).toEqual({ SourcePath: sourcePath, Generation: 1 });
  });

  it("binds exact generations and invalidates dependent facets on N+1", async () => {
    let generation = 0;
    const ports = portsFor({
      workflow: workflowPorts({
        prepare: async (current, input) =>
          workflowCurrentFromPreview(
            workflowCurrent(current.workflow.id, current.workflow.revision + 1),
            preview(input.SourcePath, ++generation),
          ),
      }),
    });
    const { result } = renderHook(useReleaseSession, { wrapper: wrapperFor(ports) });

    await selectAndPrepare(result, "C:\\media\\Example");
    expect(result.current.identity.view.release).toEqual({
      SourcePath: "C:\\media\\Example",
      Generation: 1,
    });
    const firstRevision = result.current.screenshots.view.revision;

    await act(() => result.current.input.prepare());
    expect(result.current.identity.view.release?.Generation).toBe(2);
    expect(result.current.screenshots.view.revision).toBeGreaterThan(firstRevision);
    expect(result.current.screenshots.view.staleReason).toBe("Prepared generation changed.");
  });

  it("aborts and suppresses stale preparation completion after source replacement", async () => {
    const first = createDeferred<ReleaseWorkflowCurrent>();
    let firstSignal: AbortSignal | undefined;
    const prepare = vi.fn(
      async (
        current: ReleaseWorkflowCurrent,
        input: PrepareInput,
        _idempotencyKey: string,
        signal: AbortSignal,
      ) => {
        if (input.SourcePath.endsWith("First")) {
          firstSignal = signal;
          return first.promise;
        }
        return workflowCurrentFromPreview(
          workflowCurrent(current.workflow.id, current.workflow.revision + 1),
          preview(input.SourcePath, 1),
        );
      },
    );
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ workflow: workflowPorts({ prepare }) })),
    });

    act(() => result.current.input.selectSource("C:\\media\\First"));
    act(() => result.current.upload.chooseTrackers(["AITHER"]));
    let firstCommand!: Promise<boolean>;
    act(() => {
      firstCommand = result.current.input.prepare();
    });
    await waitFor(() => expect(prepare).toHaveBeenCalledTimes(1));

    act(() => result.current.input.selectSource("C:\\media\\Second"));
    expect(firstSignal?.aborted).toBe(true);
    act(() => result.current.upload.chooseTrackers(["AITHER"]));
    await act(() => result.current.input.prepare());

    first.resolve(
      workflowCurrentFromPreview(
        workflowCurrent("workflow-new", 2),
        preview("C:\\media\\First", 1),
      ),
    );
    await act(() => firstCommand);
    expect(result.current.identity.view.sourcePath).toBe("C:\\media\\Second");
  });

  it("rejects stale completion from an older same-source attempt", async () => {
    const first = createDeferred<ReleaseWorkflowCurrent>();
    let calls = 0;
    const execute = vi.fn(async (current: ReleaseWorkflowCurrent, input: PrepareInput) => {
      calls += 1;
      if (calls === 1) {
        return first.promise;
      }
      return workflowCurrentFromPreview(
        workflowCurrent(current.workflow.id, current.workflow.revision + 1),
        preview(input.SourcePath, 2),
      );
    });
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ workflow: workflowPorts({ prepare: execute }) })),
    });
    const sourcePath = "C:\\media\\Example";
    act(() => result.current.input.selectSource(sourcePath));

    let firstResult!: Promise<boolean>;
    act(() => {
      firstResult = result.current.input.prepare();
    });
    await waitFor(() => expect(execute).toHaveBeenCalledTimes(1));
    await act(() => result.current.input.prepare());

    first.resolve(
      workflowCurrentFromPreview(workflowCurrent("workflow-new", 2), preview(sourcePath, 1)),
    );
    await act(() => firstResult);

    expect(result.current.identity.view.release).toEqual({ SourcePath: sourcePath, Generation: 2 });
  });

  it("carries workflow drafts across same-source generations and clears them for another source", async () => {
    const { result } = renderHook(useReleaseSession, { wrapper: wrapperFor(portsFor()) });
    await selectAndPrepare(result, "C:\\media\\Example");
    act(() => result.current.upload.chooseTrackers(["AITHER", "BLU"]));
    act(() => result.current.upload.changeOptions({ noSeed: true, runLogLevel: "debug" }));
    act(() => result.current.upload.answerQuestionnaire("AITHER", "season", "1"));

    await act(() => result.current.input.prepare());
    expect(result.current.upload.view.selectedTrackers).toEqual(["AITHER", "BLU"]);
    expect(result.current.upload.view.options.noSeed).toBe(true);
    expect(result.current.upload.view.options.runLogLevel).toBe("debug");
    expect(result.current.upload.view.questionnaireAnswers.AITHER).toEqual({ season: "1" });

    act(() => result.current.input.selectSource("C:\\media\\Other"));
    expect(result.current.upload.view.selectedTrackers).toEqual([]);
    expect(result.current.upload.view.options.noSeed).toBe(false);
    expect(result.current.upload.view.options.runLogLevel).toBe("info");
    expect(result.current.upload.view.questionnaireAnswers).toEqual({});
  });

  it("keeps independent facets concurrent and suppresses stale media completion", async () => {
    const screenshot =
      createDeferred<Awaited<ReturnType<ReleaseSessionPorts["workflow"]["mediaPlan"]>>>();
    const generateDescriptions = vi.fn(async (current: ReleaseWorkflowCurrent) => current);
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({
            mediaPlan: async () => screenshot.promise,
            generateDescriptions,
          }),
        }),
      ),
    });
    await selectAndPrepare(result, "C:\\media\\Example");
    await waitFor(() => expect(result.current.duplicates.view.status).toBe("ready"));

    let screenshotCommand!: Promise<boolean>;
    act(() => {
      screenshotCommand = result.current.screenshots.load();
    });
    await act(() => result.current.descriptions.load());
    expect(generateDescriptions).not.toHaveBeenCalled();

    await act(() => result.current.input.prepare());
    screenshot.resolve({
      id: "media-plan-1",
      workflowId: "workflow-new",
      revision: 4,
      release: { id: "release-1", revision: 1 },
      projectionSet: { id: "projections-1", revision: 1 },
      durationSeconds: 60,
      frameRate: 24,
      suggestedSelections: [],
      createdAt: "2026-07-20T00:00:00Z",
    });
    await act(() => screenshotCommand);
    expect(result.current.screenshots.view.plan).toBeNull();
    expect(result.current.screenshots.view.staleReason).toBe("Prepared generation changed.");
  });

  it("publishes and reorders opaque final screenshots through workflow commands", async () => {
    const sourcePath = "C:\\media\\Example";
    const captureMedia = vi.fn(async (current: ReleaseWorkflowCurrent) => {
      const revision = current.workflow.revision + 1;
      return {
        ...current,
        workflow: {
          ...current.workflow,
          revision,
          media: { id: "media-final", revision },
        },
        media: {
          id: "media-final",
          workflowId: current.workflow.id,
          revision,
          release: current.workflow.release!,
          releaseRef: { SourcePath: sourcePath, Generation: 1 },
          projectionSet: current.workflow.trackerProjections!,
          captureFingerprint: "5".repeat(64),
          requirementsFingerprint: "6".repeat(64),
          imageRequirementsPrepared: false,
          artifacts: [
            {
              id: "screen-existing",
              kind: "screenshot" as const,
              purpose: "final" as const,
              selected: true,
              order: 0,
              index: 0,
            },
            {
              id: "screen-generated",
              kind: "screenshot" as const,
              purpose: "final" as const,
              selected: true,
              order: 1,
              index: 1,
            },
          ],
          status: "completed" as const,
          createdAt: "2026-07-20T00:00:00Z",
        },
      };
    });
    const reorderMedia = vi.fn(async (current: ReleaseWorkflowCurrent) => current);
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({
            mediaPlan: async (workflowID) => ({
              id: "media-plan-1",
              workflowId: workflowID,
              revision: 4,
              release: { id: "release-1", revision: 1 },
              projectionSet: { id: "projections-1", revision: 1 },
              durationSeconds: 60,
              frameRate: 24,
              suggestedSelections: [{ Index: 1, TimestampSeconds: 10, Frame: 240, Source: "auto" }],
              createdAt: "2026-07-20T00:00:00Z",
            }),
            captureMedia,
            reorderMedia,
          }),
        }),
      ),
    });
    await selectAndPrepare(result, sourcePath);
    await waitFor(() =>
      expect(result.current.navigation.view.access.screenshots.available).toBe(true),
    );
    await act(() => result.current.screenshots.load());
    await act(() => result.current.screenshots.generate("final"));

    expect(captureMedia).toHaveBeenCalledWith(
      expect.objectContaining({ workflow: expect.objectContaining({ id: "workflow-new" }) }),
      expect.objectContaining({
        purpose: "final",
        selections: [{ Index: 1, TimestampSeconds: 10, Frame: 240, Source: "auto" }],
      }),
      expect.any(String),
      expect.any(AbortSignal),
    );
    expect(result.current.screenshots.view.finalSelectionArtifactIDs).toEqual([
      "screen-existing",
      "screen-generated",
    ]);

    await act(() => result.current.screenshots.reorderFinal(1, 0));
    expect(reorderMedia).toHaveBeenCalledWith(
      expect.anything(),
      ["screen-generated", "screen-existing"],
      expect.any(String),
      expect.any(AbortSignal),
    );
  });

  it("keeps a multi-transition continuation busy until controller ownership is released", async () => {
    const finalTransition = createDeferred<boolean>();
    const captureMedia = vi.fn(async (current: ReleaseWorkflowCurrent) => current);
    const baseWorkflow = workflowPorts({ captureMedia });
    const baseContinue = baseWorkflow.continue;
    let duplicateTransitions = 0;
    const workflow: ReleaseSessionPorts["workflow"] = {
      ...baseWorkflow,
      continue: async (request, signal) => {
        const current = await baseContinue(request, signal);
        if (request.goal === "duplicates_decided") {
          duplicateTransitions += 1;
          if (duplicateTransitions === 4) await finalTransition.promise;
        }
        return current;
      },
    };
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ workflow })),
    });
    act(() => result.current.input.selectSource("C:\\media\\Example"));
    act(() => result.current.duplicates.chooseTrackers(["AITHER"]));
    await act(() => result.current.input.prepare());

    let duplicateCommand!: Promise<boolean>;
    act(() => {
      duplicateCommand = result.current.duplicates.run();
    });
    await waitFor(() => expect(duplicateTransitions).toBe(4));
    expect(result.current.workflow.view.status).toBe("running");
    expect(await result.current.menuImages.capture()).toBe(false);
    expect(captureMedia).not.toHaveBeenCalled();

    finalTransition.resolve(true);
    await act(() => duplicateCommand);
    expect(result.current.workflow.view.status).toBe("ready");
    await act(async () => {
      expect(await result.current.menuImages.capture()).toBe(true);
    });
    expect(captureMedia).toHaveBeenCalledOnce();
  });

  it("cancels DVD menu capture as an abortable session media operation", async () => {
    const capture = createDeferred<ReleaseWorkflowCurrent>();
    let captureSignal: AbortSignal | undefined;
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({
            captureMedia: async (_current, _instructions, _key, signal) => {
              captureSignal = signal;
              return capture.promise;
            },
          }),
        }),
      ),
    });
    await selectAndPrepare(result, "C:\\media\\Example");
    await waitFor(() =>
      expect(result.current.navigation.view.access.menuImages.available).toBe(true),
    );

    let command!: Promise<boolean>;
    act(() => {
      command = result.current.menuImages.capture();
    });
    await waitFor(() => expect(captureSignal).toBeDefined());
    act(() => result.current.menuImages.cancelCapture());

    expect(captureSignal?.aborted).toBe(true);
    expect(result.current.menuImages.view.status).toBe("idle");
    expect(result.current.menuImages.view.staleReason).toBe("");
    capture.resolve(workflowCurrent("workflow-new", 6));
    await act(() => command);
  });

  it("lets the backend resolve dry-run trackers from current downstream authority", async () => {
    const dryRunUploads = vi.fn(async (current: ReleaseWorkflowCurrent, noSeed: boolean) => {
      const revision = current.workflow.revision + 1;
      return {
        ...current,
        workflow: {
          ...current.workflow,
          revision,
          dryRun: { id: "dry-run-1", revision },
        },
        dryRun: {
          id: "dry-run-1",
          workflowId: current.workflow.id,
          revision,
          projectionSet: current.workflow.trackerProjections!,
          dupes: current.workflow.dupes!,
          media: { id: "media-1", revision: 1 },
          descriptions: { id: "descriptions-1", revision: 1 },
          inputFingerprint: "1".repeat(64),
          noSeed,
          trackerIds: ["AITHER"],
          reports: [],
          succeededCount: 0,
          failedCount: 0,
          skippedCount: 0,
          status: "skipped" as const,
          createdAt: "2026-07-22T00:00:00Z",
        },
      } as ReleaseWorkflowCurrent;
    });
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ workflow: workflowPorts({ dryRunUploads }) })),
    });
    await selectAndPrepare(result, "C:\\media\\Example");
    await waitFor(() => expect(result.current.duplicates.view.status).toBe("ready"));
    expect(dryRunUploads).not.toHaveBeenCalled();
    expect(
      result.current.upload.view.projections?.projections.map((projection) => ({
        trackerId: projection.trackerId,
        canonicalReleaseName: projection.canonicalReleaseName,
        uploadReleaseName: projection.uploadReleaseName,
      })),
    ).toEqual([
      {
        trackerId: "AITHER",
        canonicalReleaseName: "Example Release 2026 1080p-GRP",
        uploadReleaseName: "Example.Release.2026.1080p-GRP",
      },
    ]);

    await act(() => result.current.upload.runDryRun());
    expect(result.current.upload.view.dryRunStatus).toBe("ready");
    expect(dryRunUploads).toHaveBeenCalledWith(
      expect.anything(),
      false,
      [],
      expect.any(String),
      expect.any(AbortSignal),
    );
    expect(result.current.upload.view.dryRunResult?.id).toBe("dry-run-1");
  });

  it("hydrates hosted-image outcomes from the workflow media snapshot", async () => {
    const captureMedia = vi.fn(async (current: ReleaseWorkflowCurrent) => {
      const revision = current.workflow.revision + 1;
      return {
        ...current,
        workflow: { ...current.workflow, revision, media: { id: "media-1", revision } },
        media: {
          id: "media-1",
          workflowId: current.workflow.id,
          revision,
          release: current.workflow.release!,
          releaseRef: { SourcePath: "C:\\media\\Example", Generation: 1 },
          projectionSet: current.workflow.trackerProjections!,
          captureFingerprint: "5".repeat(64),
          requirementsFingerprint: "6".repeat(64),
          imageRequirementsPrepared: false,
          artifacts: [
            {
              id: "screen-1",
              kind: "screenshot" as const,
              purpose: "final" as const,
              selected: true,
              order: 0,
            },
            {
              id: "screen-2",
              kind: "screenshot" as const,
              purpose: "final" as const,
              selected: true,
              order: 1,
            },
          ],
          status: "completed" as const,
          createdAt: "2026-07-16T00:00:00Z",
        },
      };
    });
    const uploadImages = vi.fn(async (current: ReleaseWorkflowCurrent) => {
      const revision = current.workflow.revision + 1;
      const hosted = {
        id: "hosted-1",
        kind: "hosted_image" as const,
        purpose: "final" as const,
        selected: true,
        order: 2,
        source: "screen-1",
        host: "example",
        url: "https://example.invalid/1.png",
      };
      return {
        ...current,
        workflow: { ...current.workflow, revision, media: { id: "media-2", revision } },
        media: {
          ...current.media!,
          id: "media-2",
          revision,
          imageRequirementsPrepared: true,
          artifacts: [...current.media!.artifacts, hosted],
          hostAttempts: [
            {
              id: "attempt-1",
              media: { id: current.media!.id, revision: current.media!.revision },
              host: "example",
              status: "completed" as const,
              fallback: false,
              artifactIds: ["screen-1", "screen-2"],
              results: [hosted],
              attemptedAt: "2026-07-16T00:00:01Z",
            },
          ],
        },
      };
    });
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({ captureMedia, uploadImages }),
        }),
      ),
    });
    await selectAndPrepare(result, "C:\\media\\Example");
    await waitFor(() =>
      expect(result.current.navigation.view.access.uploadedImages.available).toBe(true),
    );
    await act(() =>
      result.current.screenshots.generate("final", [
        { Index: 0, TimestampSeconds: 1, Frame: 24, Source: "manual" },
        { Index: 1, TimestampSeconds: 2, Frame: 48, Source: "manual" },
      ]),
    );
    expect(result.current.uploadedImages.view.staleReason).not.toBe("");
    await act(() => result.current.uploadedImages.load());
    expect(result.current.uploadedImages.view.selectedArtifactIDs).toEqual([
      "screen-1",
      "screen-2",
    ]);
    await act(() => result.current.uploadedImages.upload());

    expect(uploadImages).toHaveBeenCalledWith(
      expect.anything(),
      ["screen-1", "screen-2"],
      expect.any(String),
      expect.any(AbortSignal),
    );
    expect(result.current.uploadedImages.view.uploaded).toEqual([
      expect.objectContaining({
        artifactID: "hosted-1",
        host: "example",
        url: "https://example.invalid/1.png",
      }),
    ]);
  });

  it("preserves explicit-empty tracker intent and blocks duplicate start", async () => {
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor()),
    });
    act(() => result.current.input.selectSource("C:\\media\\Example"));
    await act(() => result.current.input.prepare());

    expect(result.current.navigation.view.access.duplicates.available).toBe(true);

    let started = true;
    await act(async () => {
      started = await result.current.duplicates.run();
    });
    expect(started).toBe(false);
    expect(result.current.duplicates.view.error).toBe(
      "Select at least one tracker to run duplicate checking.",
    );

    act(() => result.current.duplicates.chooseTrackers(["AITHER"]));
    expect(result.current.input.view.preparationDirty).toBe(false);
    await act(async () => {
      started = await result.current.duplicates.run();
    });
    expect(started).toBe(true);
    expect(result.current.duplicates.view.status).toBe("ready");
  });

  it("acknowledges incomplete zero-candidate duplicate evidence", async () => {
    const fixture = workflowPorts();
    const checkDuplicates = vi.fn(
      async (
        current: ReleaseWorkflowCurrent,
        skipRemote: boolean,
        idempotencyKey: string,
        signal: AbortSignal,
      ) => {
        const checked = await fixture.checkDuplicates(current, skipRemote, idempotencyKey, signal);
        return {
          ...checked,
          dupes: {
            ...checked.dupes!,
            status: "blocked",
            results: [
              {
                trackerId: "AITHER",
                uploadReleaseName: "Example.Release.2026.1080p-GRP",
                matches: [],
                search: {
                  complete: false,
                  pages: 2,
                  candidateCount: 0,
                  scope: "work_identity",
                  warnings: ["Search pagination is incomplete."],
                },
                decision: "pending",
                status: "blocked",
              },
            ],
          } as unknown as NonNullable<ReleaseWorkflowCurrent["dupes"]>,
        };
      },
    );
    const decideDuplicates = vi.fn(
      async (current: ReleaseWorkflowCurrent): Promise<ReleaseWorkflowCurrent> => ({
        ...current,
        dupes: {
          ...current.dupes!,
          status: "completed",
          results: current.dupes!.results.map((result) => ({
            ...result,
            decision: "ignored",
            status: "completed",
          })),
        },
      }),
    );
    const workflow = workflowPorts({ checkDuplicates, decideDuplicates });
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ workflow })),
    });

    act(() => result.current.input.selectSource("C:\\media\\Example Release"));
    act(() => result.current.duplicates.chooseTrackers(["AITHER"]));
    await act(() => result.current.input.prepare());
    await act(() => result.current.duplicates.run());

    act(() => result.current.duplicates.setIgnored("AITHER", true));
    await waitFor(() =>
      expect(decideDuplicates).toHaveBeenCalledWith(
        expect.anything(),
        { AITHER: "ignored" },
        expect.any(String),
        expect.any(AbortSignal),
      ),
    );
    await waitFor(() => {
      expect(result.current.workflow.view.current?.dupes?.status).toBe("completed");
      expect(result.current.workflow.view.current?.dupes?.results[0]).toEqual(
        expect.objectContaining({ decision: "ignored", status: "completed" }),
      );
    });
  });

  it("checks dupes before name review and acknowledges the tracker name without rechecking", async () => {
    const fixture = workflowPorts();
    const action = {
      createdAt: "2026-07-20T00:00:00Z",
      id: "action-review-name",
      kind: "provide_tracker_input" as const,
      prompt: "Confirm the tracker release name.",
      status: "pending" as const,
      trackerId: "AR",
      workflowRevision: 3,
    };
    const project = vi.fn(
      async (
        current: ReleaseWorkflowCurrent,
        trackers: readonly string[],
        instructions: Readonly<Record<string, TrackerProjectionInstructions>>,
        idempotencyKey: string,
        signal: AbortSignal,
      ) => {
        const projected = await fixture.project(
          current,
          trackers,
          instructions,
          idempotencyKey,
          signal,
        );
        return {
          ...projected,
          workflow: {
            ...projected.workflow,
            requiredActions: [action],
            status: "blocked" as const,
          },
          projections: {
            ...projected.projections!,
            projections: projected.projections!.projections.map((projection) => ({
              ...projection,
              dupeReady: true,
              uploadReady: false,
              requiredActions: [action],
              policyDecisions: [
                {
                  code: "release_name_confirmation",
                  decision: "confirmation_required",
                  blocking: false,
                },
              ],
            })),
          },
        };
      },
    );
    const prepare = vi.fn(
      async (
        current: ReleaseWorkflowCurrent,
        input: PrepareInput,
        idempotencyKey: string,
        signal: AbortSignal,
      ) => {
        const prepared = await fixture.prepare(current, input, idempotencyKey, signal);
        return {
          ...prepared,
          workflow: {
            ...prepared.workflow,
            requiredActions: [action],
            status: "blocked" as const,
          },
          projections: {
            status: "ready" as const,
            projections: [
              {
                trackerId: "AR",
                displayName: "AR",
                canonicalReleaseName: "Example Release 2026",
                uploadReleaseName: "Example.Release.2026-GRP",
                artifacts: {},
                dupeReady: true,
                uploadReady: false,
                readiness: "ready" as const,
                requiredActions: [action],
                policyDecisions: [
                  {
                    code: "release_name_confirmation",
                    decision: "confirmation_required",
                    blocking: false,
                  },
                ],
              },
            ],
          } as unknown as NonNullable<ReleaseWorkflowCurrent["projections"]>,
        };
      },
    );
    let checkedCurrent: ReleaseWorkflowCurrent | null = null;
    const checkDuplicates = vi.fn(
      async (
        current: ReleaseWorkflowCurrent,
        skipRemote: boolean,
        idempotencyKey: string,
        signal: AbortSignal,
      ) => {
        checkedCurrent = await fixture.checkDuplicates(current, skipRemote, idempotencyKey, signal);
        return checkedCurrent;
      },
    );
    const workflowBase = workflowPorts({ checkDuplicates, prepare, project });
    const baseContinue = workflowBase.continue;
    let reviewedCurrent: ReleaseWorkflowCurrent | null = null;
    const continueWorkflow = vi.fn(
      async (request: ContinueReleaseWorkflowRequest, signal: AbortSignal) => {
        if (!request.answers?.length) return baseContinue(request, signal);
        if (!checkedCurrent) throw new Error("duplicate fixture is unavailable");
        const active = reviewedCurrent || checkedCurrent;
        const acknowledged = request.answers[0]?.confirmed === true;
        const currentlyAcknowledged =
          active.projections?.projections[0]?.policyDecisions?.[0]?.decision === "confirmed";
        if (acknowledged === currentlyAcknowledged) return active;
        const reviewedName = acknowledged
          ? request.answers[0]?.textValue || ""
          : "Example.Release.2026-GRP";
        const revision = active.workflow.revision + 1;
        const reviewedAction = {
          ...action,
          status: acknowledged ? ("resolved" as const) : ("pending" as const),
          workflowRevision: revision,
        };
        reviewedCurrent = {
          ...active,
          workflow: {
            ...active.workflow,
            revision,
            requiredActions: [reviewedAction],
            status: acknowledged ? "active" : "blocked",
          },
          projections: {
            ...active.projections!,
            projections: active.projections!.projections.map((projection) => ({
              ...projection,
              uploadReleaseName: reviewedName,
              uploadReady: acknowledged,
              requiredActions: [reviewedAction],
              policyDecisions: [
                {
                  code: "release_name_confirmation",
                  decision: acknowledged ? "confirmed" : "confirmation_required",
                  blocking: false,
                },
              ],
            })),
          },
          dupes: {
            ...active.dupes!,
            results: active.dupes!.results.map((dupe) => ({
              ...dupe,
              uploadReleaseName: reviewedName,
            })),
          },
        };
        return reviewedCurrent;
      },
    );
    const workflow: ReleaseSessionPorts["workflow"] = {
      ...workflowBase,
      continue: continueWorkflow,
    };
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ workflow })),
    });
    act(() => result.current.input.selectSource("C:\\media\\Example Release"));
    act(() => result.current.duplicates.chooseTrackers(["AR"]));
    await act(() => result.current.input.prepare());
    act(() =>
      result.current.duplicates.confirmReleaseName("AR", "Example.Release.2026.REVIEWED-GRP"),
    );
    await act(() => result.current.duplicates.run());

    const dupeProjectionInstructions = project.mock.calls[0]?.[2]?.AR;
    expect(dupeProjectionInstructions).not.toHaveProperty("uploadReleaseName");
    expect(checkDuplicates).toHaveBeenCalledOnce();

    await act(async () => {
      expect(await result.current.duplicates.acknowledgeReleaseName("AR", true)).toBe(true);
    });
    const answerRequest = continueWorkflow.mock.calls.find((call) => call[0].answers?.length)?.[0];
    expect(answerRequest?.answers?.[0]).toEqual(
      expect.objectContaining({
        actionId: "action-review-name",
        confirmed: true,
        textValue: "Example.Release.2026.REVIEWED-GRP",
      }),
    );
    expect(checkDuplicates).toHaveBeenCalledOnce();
    expect(result.current.duplicates.view.projections?.projections[0]).toEqual(
      expect.objectContaining({
        uploadReleaseName: "Example.Release.2026.REVIEWED-GRP",
        uploadReady: true,
      }),
    );

    await act(async () => {
      expect(await result.current.duplicates.acknowledgeReleaseName("AR", false)).toBe(true);
    });
    const unconfirmRequest = continueWorkflow.mock.calls.find(
      (call) => call[0].answers?.[0]?.confirmed === false,
    )?.[0];
    expect(unconfirmRequest?.answers?.[0]).toEqual(
      expect.objectContaining({
        actionId: "action-review-name",
        confirmed: false,
      }),
    );
    expect(unconfirmRequest?.answers?.[0]).not.toHaveProperty("textValue");
    expect(checkDuplicates).toHaveBeenCalledOnce();
    expect(result.current.duplicates.view.projections?.projections[0]).toEqual(
      expect.objectContaining({
        uploadReleaseName: "Example.Release.2026-GRP",
        uploadReady: false,
      }),
    );

    await act(async () => {
      expect(await result.current.duplicates.acknowledgeReleaseName("AR", true)).toBe(true);
    });
    expect(checkDuplicates).toHaveBeenCalledOnce();
    expect(result.current.duplicates.view.projections?.projections[0]).toEqual(
      expect.objectContaining({
        uploadReleaseName: "Example.Release.2026.REVIEWED-GRP",
        uploadReady: true,
      }),
    );
  });
});
