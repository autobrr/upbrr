// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ReactNode } from "react";
import userEvent from "@testing-library/user-event";
import {
  act,
  cleanup,
  fireEvent,
  renderHook,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ApplicationInfo, MetadataPreview, PrepareInput } from "../types";
import { emptyExternalIdentity } from "../utils/canonicalIdentity";
import type {
  ActiveInputSnapshot,
  ComparisonInstructions,
  ContinueReleaseWorkflowRequest,
  DescriptionInstructions,
  HDRAnalysisInstructions,
  DupeDecision,
  MediaCaptureInstructions,
  Operation as WorkflowOperationStatus,
  ReleaseWorkflowCurrent,
  TrackerProjectionInstructions,
  SaveReleaseWorkflowDescriptionOverrideRequest,
  WorkflowContinuation,
} from "../api/generated/release-workflow";
import { ReleaseSessionProvider, routeAccess, useReleaseSession } from ".";
import { WorkflowCancellation } from "../components/WorkflowCancellation";
import DescriptionBuilderPage from "../pages/description_builder";
import ScreenshotsPage from "../pages/screenshots";
import DupeCheckPage from "../pages/dupe_check";
import TrackerUploadPage from "../pages/tracker_upload";
import HDRAnalysisPage from "../pages/hdr_analysis";
import BlurayCandidatesPage from "../pages/bluray_candidates";
import type { ReleaseSessionPorts } from "./ports";
import { setAppRequestHandlerForTests } from "../api/client";
import { productionReleaseSessionPorts } from "./production";

afterEach(() => window.sessionStorage.removeItem("upbrr.cancellingReleaseOperation"));

const preview = (sourcePath: string, generation: number): MetadataPreview => ({
  SourcePath: sourcePath,
  TrackerName: "AITHER",
  ReleaseName: "Example.Release.2026.1080p-GRP",
  ReleaseNameOverrides: {},
  Release: { SourcePath: sourcePath, Generation: generation },
  Identity: { ...emptyExternalIdentity(sourcePath), Category: "movie", Generation: generation },
  Display: { ReleaseName: "Example.Release.2026.1080p-GRP", Providers: [] },
  Bluray: null,
  Diagnostics: [],
  TrackerData: [],
});

const createDeferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, reject, resolve };
};

type PortOverrides = Readonly<{
  workflow?: ReleaseSessionPorts["workflow"];
  resumeWorkflowID?: string;
  activeInput?: Partial<ReleaseSessionPorts["activeInput"]>;
}>;

const portsFor = (overrides: PortOverrides = {}): ReleaseSessionPorts => {
  const workflow = overrides.workflow ?? workflowPorts();
  return {
    providerSearch: { search: async (Query, Category) => ({ Query, Category, Providers: [] }) },
    activeInput: {
      cancelOpen: async () => {
        throw new Error("Synthetic open already accepted.");
      },
      get: async (signal) => {
        const workflowID = overrides.resumeWorkflowID?.trim();
        if (!workflowID) return { state: "empty", revision: 0 };
        return {
          state: "active",
          revision: 1,
          inputId: `input-${workflowID}`,
          sourceVersion: `source-${workflowID}`,
          current: await workflow.current(workflowID, signal),
        };
      },
      open: async (request, signal) => ({
        state: "active",
        revision: request.expectedRevision + 1,
        inputId: request.request.intent.preparation?.SourcePath || "input-test",
        sourceVersion: request.request.intent.preparation?.SourcePath || "source-test",
        current: await workflow.continue(request.request, signal),
      }),
      release: async (request) => ({ state: "empty", revision: request.expectedRevision + 1 }),
      recover: async (request, signal) => ({
        state: "recovering",
        revision: 1,
        current: await workflow.current(request.workflowId, signal),
      }),
      reconcile: async () => ({ state: "empty", revision: 2 }),
      subscribe: () => () => undefined,
      ...overrides.activeInput,
    },
    workflow,
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
    availableGoals: (
      [
        "input_ready",
        "prepared",
        "trackers_assessed",
        "duplicates_decided",
        "media_ready",
        "descriptions_ready",
        "upload_reviewed",
        "dry_run",
        "uploaded",
      ] as const
    ).map((goal) => ({ goal, available: revision > 1 })),
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
      Bluray: null,
      Playlist: { Set: false, Selected: [], UseAll: false },
      TrackerIDs: {},
    },
    fingerprint: "0".repeat(64),
    createdAt: "2026-07-20T00:00:00Z",
  },
});

const workflowCurrentWithDescriptions = (
  workflowID: string,
  revision: number,
  unit3dSource: string,
  standaloneSource = "standalone source",
  unit3dImagePreviews?: Readonly<Record<string, string>>,
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
      releaseRef: { SourcePath: "C:\\media\\Example.Release.2026.1080p-GRP.mkv", Generation: 1 },
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
          ...(unit3dImagePreviews ? { imagePreviews: unit3dImagePreviews } : {}),
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
      case "input_ready":
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
      case "trackers_projected":
        if (step === 0) {
          next = await activePorts.project(
            current,
            request.intent.trackerIds || [],
            request.intent.projectionInstructions || {},
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
        // Current-evidence decisions do not reproject or repeat duplicate checking.
        if (
          request.intent.duplicateDecisions &&
          Object.keys(request.intent.duplicateDecisions).length > 0
        ) {
          next = await activePorts.decideDuplicates(
            current,
            request.intent.duplicateDecisions,
            request.idempotencyKey,
            signal,
          );
        } else if (step === 0) {
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
      case "upload_reviewed":
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
    analyzeHDR: async (current) => current,
    selectHDRComparison: async (current) => current,
    setHDRAnalysisEnabled: async (current, artifactIDs) => ({
      ...current,
      workflow: {
        ...current.workflow,
        hdrAnalysisEnabled: artifactIDs.length > 0,
        hdrImages: { artifactIds: artifactIDs },
      },
    }),
    hdrAnalysisURL: (_current, analysisID, analysisRevision, artifactID) =>
      `/api/app/release-workflow-hdr-analysis?analysisId=${analysisID}&analysisRevision=${analysisRevision}&artifactId=${artifactID}`,
    analyzeAudio: async (current) => current,
    setAudioAnalysisEnabled: async (current, enabled) => ({
      ...current,
      workflow: {
        ...current.workflow,
        audioAnalysisEnabled: enabled,
        ...(enabled ? {} : { audioAnalysis: null }),
      },
    }),
    audioAnalysisURL: (_current, analysisID, analysisRevision, artifactID) =>
      `/api/app/release-workflow-audio-analysis?analysisId=${analysisID}&analysisRevision=${analysisRevision}&artifactId=${artifactID}`,
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
    previewFrame: async (current, _discID, timestampSeconds) => ({
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
    editComparisons: async (current) => current,
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
    editComparisons: async (...args) => remember(await configured.editComparisons(...args)),
    analyzeHDR: async (...args) => remember(await configured.analyzeHDR(...args)),
    selectHDRComparison: async (...args) => remember(await configured.selectHDRComparison(...args)),
    setHDRAnalysisEnabled: async (...args) =>
      remember(await configured.setHDRAnalysisEnabled(...args)),
    analyzeAudio: async (...args) => remember(await configured.analyzeAudio(...args)),
    setAudioAnalysisEnabled: async (...args) =>
      remember(await configured.setAudioAnalysisEnabled(...args)),
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
        Naming: { ReleaseName: value.ReleaseName, Resolution: "1080p" },
        Media: { Type: "ENCODE", Source: "BluRay" },
        Identity: value.Identity,
        ProviderMetadata: { Bluray: value.Bluray },
      },
      display: { ...value.Display, TrackerData: value.TrackerData },
      diagnostics: value.Diagnostics,
      fingerprint: "1".repeat(64),
      createdAt: "2026-07-20T00:00:00Z",
    },
  }) as unknown as ReleaseWorkflowCurrent;

const workflowCurrentWithAudioAnalysis = (
  workflowID: string,
  attemptID: string,
  status: "completed" | "failed" = "completed",
  failureMessage = "",
): ReleaseWorkflowCurrent => {
  const sourcePath = "C:\\media\\Example.mkv";
  const current = workflowCurrentFromPreview(
    workflowCurrent(workflowID, 8),
    preview(sourcePath, 4),
  );
  return {
    ...current,
    workflow: {
      ...current.workflow,
      audioAnalysisEnabled: true,
      audioAnalysis: { id: "audio-analysis-one", revision: 8 },
    },
    audioAnalysis: {
      id: "audio-analysis-one",
      workflowId: workflowID,
      revision: 8,
      attemptId: attemptID,
      release: { SourcePath: sourcePath, Generation: 4 },
      resourceId: "resource-one",
      manifestFingerprint: "a".repeat(64),
      selection: "primary",
      trackIds: ["audio-main"],
      variants: ["waveform"],
      profileVersion: "audio-analysis-v3",
      resourceLimits: { decoderThreads: 2 },
      tracks: [
        {
          trackId: "audio-main",
          ordinal: 1,
          channels: 2,
          sampleRate: 48000,
          sampleFrames: 480000,
          durationSeconds: 10,
          status,
          ...(failureMessage
            ? { failure: { code: "output_failed", message: failureMessage } }
            : {}),
          artifacts: [
            {
              id: "audio-waveform-one",
              variant: "waveform",
              status: "completed",
              width: 1600,
              height: 240,
            },
          ],
        },
      ],
      status,
      createdAt: "2026-09-21T00:00:00Z",
      completedAt: "2026-09-21T00:00:02Z",
    },
  } as ReleaseWorkflowCurrent;
};

const liveTestRuntime: ApplicationInfo["testRuntime"] = {
  mode: "live_test",
  runId: "test-run",
  trackerSubmissionAllowed: false,
  clientMutationAllowed: false,
  imageUploadsRequireJournal: true,
  imageUploadLimit: 0,
  trackerSubmission: {
    requestsDenied: 0,
    mutationCallsDenied: 0,
    remoteCallsStarted: 0,
    remoteCallsSucceeded: 0,
  },
  clientMutation: {
    requestsDenied: 0,
    mutationCallsDenied: 0,
    remoteCallsStarted: 0,
    remoteCallsSucceeded: 0,
  },
};

const wrapperFor = (
  ports: ReleaseSessionPorts,
  runtime: { testRuntime?: ApplicationInfo["testRuntime"]; runtimeInfoReady?: boolean } = {},
) =>
  function Wrapper({ children }: Readonly<{ children: ReactNode }>) {
    return (
      <ReleaseSessionProvider ports={ports} {...runtime}>
        {children}
      </ReleaseSessionProvider>
    );
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

const preparedQuestionnaireWorkflow = (
  workflowID: string,
  sourcePath = "C:\\media\\Example",
): ReleaseWorkflowCurrent => ({
  ...workflowCurrentFromPreview(workflowCurrent(workflowID, 2), preview(sourcePath, 1)),
  inputReadiness: {
    selectedTrackerIds: ["AITHER"],
  } as unknown as ReleaseWorkflowCurrent["inputReadiness"],
});

const questionnaireOperation = (
  current: ReleaseWorkflowCurrent,
  status: WorkflowOperationStatus["status"],
): WorkflowOperationStatus => ({
  id: `${current.workflow.id}-projection`,
  workflowId: current.workflow.id,
  revision: 1,
  sequence: status === "running" ? 2 : 5,
  command: "project_trackers",
  operation: "duplicate_check",
  status,
  progress: status === "running" ? 0 : 100,
  completed: status === "running" ? 0 : 1,
  total: 1,
  startedAt: "2026-10-05T08:00:00Z",
  updatedAt: "2026-10-05T08:00:01Z",
});

const QuestionnaireSurface = () => {
  const session = useReleaseSession();
  return (
    <DupeCheckPage
      facet={session.duplicates}
      sourcePath={session.identity.view.sourcePath}
      trackerUploadItems={["AITHER", "BLU", "PTP"].map((name) => ({ name, config: {} }))}
      trackerIconSrcByName={{}}
      submissionExclusions={[]}
      workflowComplete={false}
    />
  );
};

const HDRSurface = () => {
  const session = useReleaseSession();
  return (
    <HDRAnalysisPage
      facet={session.hdrAnalysis}
      setLightboxImage={vi.fn()}
      setLightboxAlt={vi.fn()}
    />
  );
};

const questionnaireWrapper = (ports: ReleaseSessionPorts, defaultTrackers: string[] = []) =>
  function Wrapper({ children }: Readonly<{ children: ReactNode }>) {
    return (
      <ReleaseSessionProvider ports={ports} defaultTrackers={defaultTrackers}>
        <QuestionnaireSurface />
        {children}
      </ReleaseSessionProvider>
    );
  };

describe("tracker workflow capabilities", () => {
  it("opens the questionnaire surface without opening unrelated blocked stages", () => {
    const continuation = {
      availableGoals: [
        { goal: "trackers_assessed", available: true },
        { goal: "upload_reviewed", available: false },
      ],
      requiredActions: [{ kind: "answer_questionnaire", status: "pending", trackerId: "PTP" }],
    } as unknown as WorkflowContinuation;
    const access = routeAccess(continuation, false, { needsImages: true, needsDescriptions: true });
    expect(access.duplicates.available).toBe(true);
    expect(access.upload.available).toBe(false);
    expect(access.screenshots.available).toBe(false);
    expect(access.descriptions.available).toBe(false);
    const local = routeAccess(
      {
        ...continuation,
        availableGoals: [
          ...continuation.availableGoals,
          { goal: "trackers_projected", available: true },
        ],
      },
      false,
      { needsImages: true, needsDescriptions: true },
    );
    expect(local.screenshots.available).toBe(true);
    expect(local.menuImages.available).toBe(false);
    expect(local.uploadedImages.available).toBe(false);
    expect(local.descriptions.available).toBe(false);
  });

  it("opens only applicable pages and keeps tracker-scoped content failures local", () => {
    const available = (
      overrides: Readonly<Record<string, boolean>> = {},
    ): WorkflowContinuation => ({
      lifecycle: "ready",
      disposition: "none",
      refs: {},
      availableGoals: (
        ["trackers_assessed", "media_ready", "descriptions_ready", "upload_reviewed"] as const
      ).map((goal) => ({
        goal,
        available: overrides[goal] ?? true,
        reason: overrides[goal] === false ? `Backend blocked ${goal}.` : "",
      })),
    });
    const none = routeAccess(available(), false, { needsImages: false, needsDescriptions: false });
    expect(none.screenshots.available).toBe(true);
    expect(none.uploadedImages.available).toBe(false);
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

    const activeOperation: WorkflowContinuation = {
      ...available(),
      availableGoals: available().availableGoals.map((goal) => ({
        ...goal,
        available: false,
        reasonCode: "operation_active",
        reason: "Wait for the active operation to finish.",
      })),
    };
    const duringOperation = routeAccess(activeOperation, true, {
      needsImages: true,
      needsDescriptions: true,
    });
    expect(duringOperation.duplicates.reasonCode).toBe("operation_active");
    expect(duringOperation.trackerData.reasonCode).toBe("operation_active");
    expect(duringOperation.screenshots.reasonCode).toBe("operation_active");
    expect(duringOperation.descriptions.reasonCode).toBe("operation_active");

    const noLongerApplicable = routeAccess(activeOperation, false, {
      needsImages: false,
      needsDescriptions: false,
    });
    expect(noLongerApplicable.duplicates.reasonCode).toBe("operation_active");
    expect(noLongerApplicable.trackerData.reasonCode).toBeUndefined();
    expect(noLongerApplicable.trackerData.reason).toBe("No tracker data is available.");
    expect(noLongerApplicable.screenshots.reasonCode).toBe("operation_active");
    expect(noLongerApplicable.uploadedImages.reasonCode).toBeUndefined();
    expect(noLongerApplicable.descriptions.reasonCode).toBeUndefined();
  });
});

describe("useReleaseSession", () => {
  it("builds exact-generation audio instructions from prepared stable track IDs", async () => {
    const workflowID = "workflow-audio";
    const sourcePath = "C:\\media\\Example.mkv";
    const prepared = workflowCurrentFromPreview(
      workflowCurrent(workflowID, 7),
      preview(sourcePath, 4),
    );
    const current = {
      ...prepared,
      release: {
        ...prepared.release!,
        release: {
          ...prepared.release!.release,
          Source: {
            ...prepared.release!.release.Source,
            Classification: { Container: "Matroska" },
          },
          Media: {
            Tracks: [
              {
                ID: "audio-main",
                Kind: "audio",
                ResourceID: "resource-one",
                ManifestFingerprint: "a".repeat(64),
                NativeID: "1",
                Ordinal: 1,
                Title: "Main",
                Codec: "FLAC",
                ChannelLayout: "5.1",
                Channels: 6,
                SampleRate: 48000,
                DetectedLanguages: ["English"],
                Languages: ["English"],
                LanguageProvenance: "automatic",
                Default: true,
                Commentary: false,
              },
            ],
            PrimaryAudioTrackID: "audio-main",
          },
        },
      },
    } as unknown as ReleaseWorkflowCurrent;
    const analyzeAudio = vi.fn(async (value: ReleaseWorkflowCurrent) => value);
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({ current: async () => current, analyzeAudio }),
        }),
      ),
    });
    await waitFor(() => expect(result.current.audioAnalysis.view.available).toBe(true));

    await act(() =>
      result.current.audioAnalysis.generate({
        resourceID: "resource-one",
        selection: "primary",
        trackIDs: ["audio-main"],
        variants: ["waveform", "spectrogram"],
      }),
    );

    expect(analyzeAudio).toHaveBeenCalledWith(
      expect.objectContaining({ workflow: expect.objectContaining({ id: workflowID }) }),
      {
        release: { SourcePath: sourcePath, Generation: 4 },
        resourceId: "resource-one",
        selection: "primary",
        trackIds: ["audio-main"],
        variants: ["waveform", "spectrogram"],
        profileVersion: "audio-analysis-v3",
        resourceLimits: { decoderThreads: 2 },
      },
      expect.any(String),
      expect.any(AbortSignal),
    );
    unmount();
  });

  it("exposes other active workflow operations as an audio mutation block", async () => {
    const workflowID = "workflow-audio-blocked";
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const sourcePath = "C:\\media\\Example.mkv";
    const prepared = workflowCurrentFromPreview(
      workflowCurrent(workflowID, 7),
      preview(sourcePath, 4),
    );
    const current = {
      ...prepared,
      operation: {
        id: "operation-upload",
        workflowId: workflowID,
        revision: 7,
        sequence: 1,
        command: "execute_uploads",
        operation: "upload_execute",
        status: "running",
        progress: 25,
        completed: 0,
        total: 1,
        startedAt: "2026-09-21T00:00:00Z",
        updatedAt: "2026-09-21T00:00:01Z",
      },
      release: {
        ...prepared.release!,
        release: {
          ...prepared.release!.release,
          Media: {
            Tracks: [
              {
                ID: "audio-main",
                Kind: "audio",
                ResourceID: "resource-one",
                ManifestFingerprint: "a".repeat(64),
                NativeID: "1",
                Ordinal: 1,
                Title: "Main",
                Codec: "FLAC",
                ChannelLayout: "stereo",
                Channels: 2,
                SampleRate: 48000,
                DetectedLanguages: ["English"],
                Languages: ["English"],
                LanguageProvenance: "automatic",
                Default: true,
                Commentary: false,
              },
            ],
            PrimaryAudioTrackID: "audio-main",
          },
        },
      },
    } as unknown as ReleaseWorkflowCurrent;
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({ current: async () => current }),
        }),
      ),
    });

    await waitFor(() =>
      expect(result.current.audioAnalysis.view.mutationBlockedReason).toContain(
        "Another workflow operation (upload execute) is running",
      ),
    );
    expect(result.current.audioAnalysis.view.status).toBe("idle");
    expect(result.current.audioAnalysis.view.operationItems).toEqual([]);
    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("retains terminal audio operation failures in the audio facet", async () => {
    const workflowID = "workflow-audio-failed";
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const current = {
      ...workflowCurrent(workflowID, 7),
      operation: {
        id: "operation-audio-failed",
        workflowId: workflowID,
        revision: 7,
        sequence: 2,
        command: "analyze_audio",
        operation: "analyze_audio",
        status: "failed",
        progress: 50,
        completed: 1,
        total: 2,
        message: "Audio analysis failed. Retry the failed work.",
        failures: [
          {
            failure: {
              Code: "audio_analysis_failed",
              Operation: "analyze_audio",
              Message: "Audio analysis failed. Retry the failed work.",
              Recovery: "retry",
            },
          },
        ],
        startedAt: "2026-09-21T00:00:00Z",
        updatedAt: "2026-09-21T00:00:02Z",
        completedAt: "2026-09-21T00:00:02Z",
      },
    } as ReleaseWorkflowCurrent;
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({ current: async () => current }),
        }),
      ),
    });

    await waitFor(() => expect(result.current.audioAnalysis.view.status).toBe("error"));
    expect(result.current.audioAnalysis.view.completed).toBe(1);
    expect(result.current.audioAnalysis.view.total).toBe(2);
    expect(result.current.audioAnalysis.view.error).toBe(
      "Audio analysis failed. Retry the failed work.",
    );
    expect(result.current.screenshots.view.status).toBe("idle");
    expect(result.current.screenshots.view.error).toBe("");
    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it.each(["canceled", "completed", "rejected"] as const)(
    "keeps acknowledged HDR cancellation visible until terminal readback (%s)",
    async (outcome) => {
      const workflowID = "workflow-hdr-cancellation";
      const ready = workflowCurrentFromPreview(
        workflowCurrent(workflowID, 8),
        preview("Synthetic.HDR.mkv", 1),
      );
      const operation: WorkflowOperationStatus = {
        id: "hdr-cancel-operation",
        workflowId: workflowID,
        revision: 8,
        sequence: 1,
        command: "analyze_hdr",
        operation: "analyze_hdr",
        status: "running",
        progress: 20,
        completed: 0,
        total: 1,
        message: "Inspecting HDR source.",
        startedAt: "2026-10-10T00:00:00Z",
        updatedAt: "2026-10-10T00:00:00Z",
      };
      let current = ready;
      const acknowledgement = createDeferred<WorkflowOperationStatus>();
      const retryAcknowledgement = createDeferred<WorkflowOperationStatus>();
      const cancelOperation = vi
        .fn(() => retryAcknowledgement.promise)
        .mockImplementationOnce(() => acknowledgement.promise);
      const { result, unmount } = renderHook(useReleaseSession, {
        wrapper: ({ children }) => (
          <ReleaseSessionProvider
            ports={portsFor({
              resumeWorkflowID: workflowID,
              workflow: workflowPorts({
                current: async () => current,
                operation: async () => current.operation!,
                analyzeHDR: async () => current,
                cancelOperation,
              }),
            })}
          >
            {children}
            <WorkflowCancellation />
            <HDRSurface />
          </ReleaseSessionProvider>
        ),
      });
      await waitFor(() => expect(result.current.identity.view.release).not.toBeNull());
      vi.useFakeTimers();
      try {
        current = { ...ready, operation };
        let generation!: Promise<boolean>;
        act(() => {
          generation = result.current.hdrAnalysis.generate({
            targetIDs: ["hdr-target"],
            peakSource: "histogram",
          });
        });
        await act(async () => {});
        let cancellation!: Promise<boolean>;
        act(() => {
          cancellation =
            outcome === "completed"
              ? result.current.workflow.cancel()
              : result.current.hdrAnalysis.cancel();
        });
        expect(result.current.workflow.cancellation.pending).toBe(true);
        expect(screen.getByRole("button", { name: "Cancelling…" })).toBeDisabled();
        expect(screen.getByText("Inspecting HDR source. (20%)")).toBeInTheDocument();
        expect(cancelOperation).toHaveBeenCalledWith(
          workflowID,
          operation.id,
          expect.any(AbortSignal),
        );
        if (outcome === "rejected") {
          await act(async () => {
            acknowledgement.reject(new Error("Cancellation unavailable."));
          });
          expect(result.current.hdrAnalysis.view.error).toContain("could not be canceled");
          expect(screen.queryByText(/Cancellation requested/)).toBeNull();
          expect(result.current.workflow.cancellation.pending).toBe(true);
          expect(result.current.hdrAnalysis.view.status).toBe("running");
          current = {
            ...current,
            operation: { ...operation, sequence: 2, progress: 30, message: "Scanning frames." },
          };
          await act(async () => vi.advanceTimersByTimeAsync(1000));
          expect(result.current.hdrAnalysis.view.error).toContain("could not be canceled");
          expect(screen.getByText("Scanning frames. (30%)")).toBeInTheDocument();
          act(() => {
            fireEvent.click(screen.getByRole("button", { name: "Retry cancellation" }));
          });
          expect(cancelOperation).toHaveBeenCalledTimes(2);
          expect(cancelOperation.mock.calls[1]).toEqual(cancelOperation.mock.calls[0]);
          await act(async () => retryAcknowledgement.resolve(operation));
        } else {
          await act(async () => acknowledgement.resolve(operation));
        }
        expect(result.current.hdrAnalysis.view.error).toBe("");
        expect(
          screen.getByText(`Cancellation requested. (${outcome === "rejected" ? 30 : 20}%)`),
        ).toBeInTheDocument();
        current = {
          ...current,
          operation: { ...operation, sequence: 3, progress: 40, message: "Scanning frames." },
        };
        await act(async () => vi.advanceTimersByTimeAsync(1000));
        expect(screen.getByText("Cancellation requested. (40%)")).toBeInTheDocument();
        current = {
          ...current,
          operation: {
            ...operation,
            status: outcome === "rejected" ? "canceled" : outcome,
            sequence: 4,
            message: "HDR work finished.",
          },
        };
        await act(async () => {
          await vi.advanceTimersByTimeAsync(1000);
          expect(await cancellation).toBe(true);
        });
        expect(result.current.hdrAnalysis.view.status).not.toBe("running");
        expect(result.current.hdrAnalysis.view.message).toBe("HDR work finished.");
        expect(screen.queryByText(/Cancellation requested/)).toBeNull();
        current = { ...ready, operation: { ...operation, id: "hdr-next-operation" } };
        let nextGeneration!: Promise<boolean>;
        act(() => {
          nextGeneration = result.current.hdrAnalysis.generate({
            targetIDs: ["hdr-target"],
            peakSource: "histogram",
          });
        });
        await act(async () => {});
        expect(screen.getByText("Inspecting HDR source. (20%)")).toBeInTheDocument();
        current = { ...current, operation: { ...current.operation!, status: "completed" } };
        await act(async () => {
          await vi.advanceTimersByTimeAsync(1000);
          await nextGeneration;
        });
        await act(() => generation);
      } finally {
        unmount();
        vi.useRealTimers();
      }
    },
  );

  it.each(["retained", "pending"])(
    "retries a %s v1 HDR request without widening formats",
    async (source) => {
      const workflowID = "workflow-hdr-legacy";
      const initial = workflowCurrentFromPreview(
        workflowCurrent(workflowID, 8),
        preview("Synthetic.HDR.mkv", 1),
      );
      const instructions: HDRAnalysisInstructions = {
        release: { SourcePath: "Synthetic.HDR.mkv", Generation: 1 },
        targetIds: ["hdr_11111111111111111111111111111111"],
        peakSource: "histogram",
        profileVersion: "hdr-analysis-v1",
      };
      const current: ReleaseWorkflowCurrent = {
        ...initial,
        workflow: { ...initial.workflow, hdrAnalysis: { id: "legacy", revision: 7 } },
        hdrPendingRequest: source === "pending" ? instructions : undefined,
        hdrAnalysis: {
          ...instructions,
          id: "legacy",
          workflowId: workflowID,
          revision: 7,
          manifestFingerprint: "manifest",
          attemptId: "legacy-attempt",
          status: "failed",
          targets: [],
          createdAt: "2026-10-10T00:00:00Z",
        },
      };
      const analyzeHDR = vi.fn(
        async (_current: ReleaseWorkflowCurrent, _instructions: HDRAnalysisInstructions) => current,
      );
      const { result, unmount } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(
          portsFor({
            resumeWorkflowID: workflowID,
            workflow: workflowPorts({ current: async () => current, analyzeHDR }),
          }),
        ),
      });
      await waitFor(() => expect(result.current.hdrAnalysis.view.result?.id).toBe("legacy"));
      await act(async () => {
        await result.current.hdrAnalysis.retry();
      });
      expect(analyzeHDR).toHaveBeenCalledOnce();
      // JSON serialization is the browser transport boundary: omitted options stay omitted.
      expect(JSON.parse(JSON.stringify(analyzeHDR.mock.calls[0]?.[1]))).toEqual(instructions);
      unmount();
    },
  );

  it.each([undefined, { artifactIds: ["native"] }, { artifactIds: [] }])(
    "defaults combined HDR audio images and retains explicit selections (%s)",
    async (selection) => {
      const workflowID = "workflow-hdr-audio-default";
      const initial = workflowCurrentFromPreview(
        workflowCurrent(workflowID, 8),
        preview("Synthetic.HDR.mkv", 1),
      );
      const current: ReleaseWorkflowCurrent = {
        ...initial,
        workflow: {
          ...initial.workflow,
          hdrAnalysis: { id: "hdr-audio", revision: 8 },
          hdrAnalysisEnabled: true,
          hdrImages: selection,
        },
        hdrAnalysis: {
          id: "hdr-audio",
          workflowId: workflowID,
          revision: 8,
          release: { SourcePath: "Synthetic.HDR.mkv", Generation: 1 },
          manifestFingerprint: "manifest",
          attemptId: "hdr-audio-attempt",
          targetIds: ["hdr_11111111111111111111111111111111"],
          peakSource: "histogram",
          formats: "both",
          profileVersion: "hdr-analysis-v2",
          status: "partial",
          createdAt: "2026-10-10T00:00:00Z",
          targets: [
            {
              targetId: "hdr_11111111111111111111111111111111",
              label: "Video 1",
              status: "partial",
              frames: 0,
              scenes: 0,
              decoded: [
                {
                  mode: "audio",
                  state: "complete",
                  profile: "audio-analysis-v3",
                  measured: 1,
                  selected: 1,
                  artifacts: ["wave", "spectrum", "stats"],
                },
              ],
              artifacts: [
                {
                  id: "native",
                  kind: "hdr10plus_plot",
                  mime: "image/png",
                  width: 3000,
                  height: 1200,
                  profile: "hdr-analysis-v2",
                },
                {
                  id: "wave",
                  kind: "audio_waveform",
                  mime: "image/png",
                  width: 1800,
                  height: 180,
                  profile: "hdr-analysis-v2",
                },
                {
                  id: "spectrum",
                  kind: "audio_spectrogram",
                  mime: "image/png",
                  width: 1800,
                  height: 600,
                  profile: "hdr-analysis-v2",
                },
                {
                  id: "unowned",
                  kind: "audio_waveform",
                  mime: "image/png",
                  width: 1800,
                  height: 180,
                  profile: "hdr-analysis-v2",
                },
                {
                  id: "picture",
                  kind: "picture_brightness_plot",
                  mime: "image/png",
                  width: 3000,
                  height: 1200,
                  profile: "hdr-analysis-v2",
                },
                {
                  id: "stats",
                  kind: "audio_stats",
                  mime: "text/plain",
                  width: 0,
                  height: 0,
                  profile: "hdr-analysis-v2",
                },
              ],
            },
          ],
        },
      };
      const { result, unmount } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(
          portsFor({
            resumeWorkflowID: workflowID,
            workflow: workflowPorts({ current: async () => current }),
          }),
        ),
      });
      await waitFor(() => expect(result.current.hdrAnalysis.view.result?.id).toBe("hdr-audio"));
      expect(result.current.hdrAnalysis.view.includedArtifactIDs).toEqual(
        selection?.artifactIds ?? ["native", "wave", "spectrum"],
      );
      unmount();
    },
  );

  it("preserves the retained profile when retrying decoded HDR checks", async () => {
    const workflowID = "workflow-hdr-decoded-retry";
    const initial = workflowCurrentFromPreview(
      workflowCurrent(workflowID, 8),
      preview("Synthetic.HDR.mkv", 1),
    );
    const instructions: HDRAnalysisInstructions = {
      release: { SourcePath: "Synthetic.HDR.mkv", Generation: 1 },
      targetIds: ["hdr_11111111111111111111111111111111"],
      peakSource: "histogram",
      formats: "both",
      profileVersion: "hdr-analysis-v3",
      decoded: { sceneSync: true, activeArea: false },
    };
    const current: ReleaseWorkflowCurrent = {
      ...initial,
      workflow: { ...initial.workflow, hdrAnalysis: { id: "retained", revision: 8 } },
      hdrAnalysis: {
        ...instructions,
        id: "retained",
        workflowId: workflowID,
        revision: 8,
        manifestFingerprint: "manifest",
        attemptId: "retained-attempt",
        status: "partial",
        targets: [],
        createdAt: "2026-10-10T00:00:00Z",
      },
    };
    const analyzeHDR = vi.fn(
      async (_current: ReleaseWorkflowCurrent, _instructions: HDRAnalysisInstructions) => current,
    );
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({ current: async () => current, analyzeHDR }),
        }),
      ),
    });
    await waitFor(() => expect(result.current.hdrAnalysis.view.result?.id).toBe("retained"));
    await act(async () => {
      await result.current.hdrAnalysis.retryDecoded(["scene_sync"]);
    });
    expect(analyzeHDR).toHaveBeenCalledOnce();
    expect(analyzeHDR.mock.calls[0]?.[1]).toEqual({
      ...instructions,
      decodedRetry: { analysisAttemptId: "retained-attempt", modes: ["scene_sync"] },
    });
    unmount();
  });

  it("resumes the pending HDR request ahead of an older retained result", async () => {
    const workflowID = "workflow-hdr-pending";
    const initial = workflowCurrentFromPreview(
      workflowCurrent(workflowID, 8),
      preview("Synthetic.HDR.mkv", 1),
    );
    const pending: HDRAnalysisInstructions = {
      release: { SourcePath: "Synthetic.HDR.mkv", Generation: 1 },
      targetIds: ["hdr_11111111111111111111111111111111"],
      peakSource: "max-scl-luminance",
      formats: "both",
      profileVersion: "hdr-analysis-v2",
      decoded: { sceneSync: true, activeArea: false },
      decodedRetry: { analysisAttemptId: "old-attempt", modes: ["scene_sync"] },
    };
    const current: ReleaseWorkflowCurrent = {
      ...initial,
      workflow: { ...initial.workflow, hdrAnalysis: { id: "older", revision: 7 } },
      hdrPendingRequest: pending,
      hdrAnalysis: {
        id: "older",
        workflowId: workflowID,
        revision: 7,
        release: pending.release,
        manifestFingerprint: "manifest",
        attemptId: "old-attempt",
        targetIds: [],
        peakSource: "histogram",
        profileVersion: "hdr-analysis-v2",
        formats: "auto",
        status: "failed",
        targets: [],
        createdAt: "2026-10-10T00:00:00Z",
      },
    };
    const analyzeHDR = vi.fn(
      async (_current: ReleaseWorkflowCurrent, _instructions: HDRAnalysisInstructions) => current,
    );
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({ current: async () => current, analyzeHDR }),
        }),
      ),
    });
    await waitFor(() => expect(result.current.hdrAnalysis.view.pendingRequest).toEqual(pending));
    await act(async () => {
      await result.current.hdrAnalysis.retry();
    });
    expect(analyzeHDR).toHaveBeenCalledOnce();
    expect(analyzeHDR.mock.calls[0]?.[1]).toEqual(pending);
    unmount();
  });

  it.each([false, true])(
    "retains the HDR target failure after an accepted run (typed=%s)",
    async (typed) => {
      const workflowID = "workflow-hdr-resource-failure";
      const initial = workflowCurrentFromPreview(
        workflowCurrent(workflowID, 7),
        preview("Synthetic.HDR.mkv", 1),
      );
      const release = initial.release!.release;
      const message = "HDR analysis exceeded its fixed resource limit";
      const terminal = {
        ...initial,
        workflow: {
          ...initial.workflow,
          revision: 8,
          hdrAnalysis: { id: "hdr-result", revision: 8 },
        },
        hdrAnalysis: {
          id: "hdr-result",
          workflowId: workflowID,
          revision: 8,
          release: { SourcePath: release.Source.SourcePath, Generation: release.Generation },
          attemptId: "hdr-operation",
          manifestFingerprint: "manifest",
          targetIds: ["hdr-target"],
          peakSource: "histogram",
          profileVersion: "hdr-analysis-v1",
          status: "failed",
          createdAt: "2026-10-10T00:00:00Z",
          completedAt: "2026-10-10T00:01:00Z",
          targets: [
            {
              targetId: "hdr-target",
              label: "Video 1",
              status: "failed",
              frames: 0,
              scenes: 0,
              failure: { code: "resource_limit", message },
            },
          ],
        },
        operation: {
          id: "hdr-operation",
          workflowId: workflowID,
          revision: 7,
          resultRevision: 8,
          sequence: 2,
          command: "analyze_hdr",
          operation: "analyze_hdr",
          status: "failed",
          progress: 100,
          completed: 1,
          total: 1,
          message: "Operation completed with retained failures.",
          failures: typed
            ? [
                {
                  failure: {
                    Code: "hdr_analysis_failed",
                    Operation: "analyze_hdr",
                    HDRAnalysisCode: "resource_limit",
                    Message: message,
                    Recovery: "retry",
                  },
                },
              ]
            : [],
          startedAt: "2026-10-10T00:00:00Z",
          updatedAt: "2026-10-10T00:01:00Z",
          completedAt: "2026-10-10T00:01:00Z",
        },
      } as ReleaseWorkflowCurrent;
      let current = initial;
      const { result, unmount } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(
          portsFor({
            resumeWorkflowID: workflowID,
            workflow: workflowPorts({
              current: async () => current,
              operation: async () => terminal.operation!,
              analyzeHDR: async () => {
                current = terminal;
                return { ...initial, operation: { ...terminal.operation!, status: "queued" } };
              },
            }),
          }),
        ),
      });
      await waitFor(() =>
        expect(result.current.workflow.view.current?.workflow.id).toBe(workflowID),
      );
      await act(async () => {
        expect(
          await result.current.hdrAnalysis.generate({
            targetIDs: ["hdr-target"],
            peakSource: "histogram",
          }),
        ).toBe(false);
      });
      expect(result.current.hdrAnalysis.view.error).toBe(message);
      expect(result.current.hdrAnalysis.view.result?.targets[0].failure?.code).toBe(
        "resource_limit",
      );
      unmount();
    },
  );

  it("shows a newer audio operation failure instead of a stale retained result failure", async () => {
    const workflowID = "workflow-audio-newer-failure";
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const retained = workflowCurrentWithAudioAnalysis(
      workflowID,
      "operation-audio-old",
      "failed",
      "The previous analysis failed while rendering its waveform.",
    );
    const current = {
      ...retained,
      operation: {
        id: "operation-audio-new",
        workflowId: workflowID,
        revision: 8,
        sequence: 3,
        command: "analyze_audio",
        operation: "analyze_audio",
        status: "failed",
        progress: 0,
        completed: 0,
        total: 1,
        message: "The latest audio analysis could not start.",
        startedAt: "2026-09-21T00:01:00Z",
        updatedAt: "2026-09-21T00:01:01Z",
        completedAt: "2026-09-21T00:01:01Z",
      },
    } as ReleaseWorkflowCurrent;
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({ current: async () => current }),
        }),
      ),
    });

    await waitFor(() => expect(result.current.audioAnalysis.view.status).toBe("error"));
    expect(result.current.audioAnalysis.view.error).toBe(
      "The latest audio analysis could not start.",
    );
    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("keeps detailed retained audio failures for the matching operation attempt", async () => {
    const workflowID = "workflow-audio-matching-failure";
    const operationID = "operation-audio-matching";
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const retained = workflowCurrentWithAudioAnalysis(
      workflowID,
      operationID,
      "failed",
      "Waveform rendering failed for the selected track.",
    );
    const current = {
      ...retained,
      operation: {
        id: operationID,
        workflowId: workflowID,
        revision: 8,
        sequence: 2,
        command: "analyze_audio",
        operation: "analyze_audio",
        status: "failed",
        progress: 100,
        completed: 1,
        total: 1,
        message: "Audio analysis failed.",
        startedAt: "2026-09-21T00:00:00Z",
        updatedAt: "2026-09-21T00:00:02Z",
        completedAt: "2026-09-21T00:00:02Z",
      },
    } as ReleaseWorkflowCurrent;
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({ current: async () => current }),
        }),
      ),
    });

    await waitFor(() => expect(result.current.audioAnalysis.view.status).toBe("error"));
    expect(result.current.audioAnalysis.view.error).toBe(
      "Waveform rendering failed for the selected track.",
    );
    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("does not leak an unrelated workflow failure into completed audio analysis", async () => {
    const workflowID = "workflow-audio-unrelated-failure";
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const current = workflowCurrentWithAudioAnalysis(workflowID, "operation-audio-completed");
    const invalidateTrackers = vi.fn(async () => {
      throw Object.assign(new Error("tracker invalidation rejected"), {
        failure: {
          Code: "tracker_invalidation_failed",
          Operation: "invalidate_trackers",
          Message: "Tracker invalidation failed.",
          Recovery: "retry",
        },
      });
    });
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({ current: async () => current, invalidateTrackers }),
        }),
      ),
    });
    await waitFor(() => expect(result.current.audioAnalysis.view.status).toBe("ready"));

    await act(() => result.current.workflow.invalidateTrackers(["AITHER"], "test"));

    expect(result.current.workflow.view.status).toBe("error");
    expect(result.current.audioAnalysis.view.status).toBe("ready");
    expect(result.current.audioAnalysis.view.error).toBe("");
    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("exposes an audio command rejection without a retained result", async () => {
    const workflowID = "workflow-audio-rejected";
    const current = workflowCurrentFromPreview(
      workflowCurrent(workflowID, 7),
      preview("C:\\media\\Example.mkv", 4),
    );
    const analyzeAudio = vi.fn(async () => {
      throw Object.assign(new Error("audio analysis request rejected"), {
        failure: {
          Code: "audio_analysis_failed",
          Operation: "analyze_audio",
          Message: "Audio analysis could not start. Retry the request.",
          Recovery: "retry",
        },
      });
    });
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({ current: async () => current, analyzeAudio }),
        }),
      ),
    });
    await waitFor(() => expect(result.current.identity.view.release?.Generation).toBe(4));

    await act(() =>
      result.current.audioAnalysis.generate({
        resourceID: "resource-one",
        selection: "primary",
        trackIDs: ["audio-main"],
        variants: ["waveform"],
      }),
    );

    expect(result.current.audioAnalysis.view.status).toBe("error");
    expect(result.current.audioAnalysis.view.error).toBe(
      "Audio analysis could not start. Retry the request.",
    );
    unmount();
  });

  it("uses a stable audio error when a command rejection has no typed failure", async () => {
    const workflowID = "workflow-audio-untyped-rejection";
    let current = workflowCurrentFromPreview(
      workflowCurrent(workflowID, 7),
      preview("C:\\media\\Example.mkv", 4),
    );
    const analyzeAudio = vi.fn(async () => {
      throw new Error("private transport details");
    });
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({ current: async () => current, analyzeAudio }),
        }),
      ),
    });
    await waitFor(() => expect(result.current.identity.view.release?.Generation).toBe(4));

    await act(() =>
      result.current.audioAnalysis.generate({
        resourceID: "resource-one",
        selection: "primary",
        trackIDs: ["audio-main"],
        variants: ["waveform"],
      }),
    );

    expect(result.current.audioAnalysis.view.status).toBe("error");
    expect(result.current.audioAnalysis.view.error).toBe(
      "Audio analysis could not start. Retry the request.",
    );
    expect(result.current.audioAnalysis.view.error).not.toContain("private transport details");

    current = workflowCurrentWithAudioAnalysis(workflowID, "operation-audio-recovered");
    await act(() => result.current.workflow.reload());

    expect(result.current.audioAnalysis.view.status).toBe("ready");
    expect(result.current.audioAnalysis.view.error).toBe("");
    unmount();
  });

  it("loads screenshots after an untyped audio command failure without leaking its error", async () => {
    const analyzeAudio = vi.fn(async () => {
      throw new Error("private audio transport details");
    });
    const mediaPlan = vi.fn(async (workflowID: string) => ({
      id: `${workflowID}-media-plan`,
      workflowId: workflowID,
      revision: 1,
      release: { id: `${workflowID}-release`, revision: 1 },
      projectionSet: { id: `${workflowID}-projections`, revision: 1 },
      durationSeconds: 120,
      frameRate: 24,
      suggestedSelections: [],
      createdAt: "2026-07-20T00:00:00Z",
    }));
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({ analyzeAudio, mediaPlan }),
        }),
      ),
    });
    await selectAndPrepare(result, "C:\\media\\Example");

    await act(() =>
      result.current.audioAnalysis.generate({
        resourceID: "resource-one",
        selection: "primary",
        trackIDs: ["audio-main"],
        variants: ["waveform"],
      }),
    );

    expect(result.current.workflow.view.status).toBe("error");
    expect(result.current.screenshots.view.status).toBe("idle");
    expect(result.current.screenshots.view.error).toBe("");

    await act(() => result.current.screenshots.load());

    expect(mediaPlan).toHaveBeenCalledOnce();
    expect(result.current.screenshots.view.status).toBe("ready");
    expect(result.current.screenshots.view.error).toBe("");
    expect(result.current.screenshots.view.plan?.DurationSeconds).toBe(120);
  });

  it("recognizes analyze_audio operations and cancels them before disabling", async () => {
    const workflowID = "workflow-audio-running";
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const base = workflowCurrent(workflowID, 4);
    const runningOperation = {
      id: "operation-audio",
      workflowId: workflowID,
      revision: 4,
      sequence: 1,
      command: "analyze_audio",
      operation: "analyze_audio",
      status: "running",
      progress: 25,
      completed: 0,
      total: 1,
      startedAt: "2026-09-21T00:00:00Z",
      updatedAt: "2026-09-21T00:00:01Z",
    } as const;
    const canceledOperation = {
      ...runningOperation,
      sequence: 2,
      status: "canceled",
      updatedAt: "2026-09-21T00:00:02Z",
      completedAt: "2026-09-21T00:00:02Z",
    } as const;
    const running = { ...base, operation: runningOperation } as ReleaseWorkflowCurrent;
    const terminal = { ...base, operation: canceledOperation } as ReleaseWorkflowCurrent;
    const current = vi.fn().mockResolvedValueOnce(running).mockResolvedValue(terminal);
    const cancelOperation = vi.fn(async () => canceledOperation);
    const setAudioAnalysisEnabled = vi.fn(async (value: ReleaseWorkflowCurrent) => ({
      ...value,
      workflow: { ...value.workflow, audioAnalysisEnabled: false, audioAnalysis: null },
    }));
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({ current, cancelOperation, setAudioAnalysisEnabled }),
        }),
      ),
    });
    await waitFor(() => expect(result.current.audioAnalysis.view.status).toBe("running"));

    await act(() => result.current.audioAnalysis.disable());

    expect(cancelOperation).toHaveBeenCalledWith(
      workflowID,
      "operation-audio",
      expect.any(AbortSignal),
    );
    expect(setAudioAnalysisEnabled).toHaveBeenCalledWith(
      expect.objectContaining({ operation: expect.objectContaining({ status: "canceled" }) }),
      false,
      expect.any(String),
      expect.any(AbortSignal),
    );
    expect(cancelOperation.mock.invocationCallOrder[0]).toBeLessThan(
      setAudioAnalysisEnabled.mock.invocationCallOrder[0],
    );
    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("ignores a stale terminal audio operation after disabling and refreshing", async () => {
    const workflowID = "workflow-audio-disabled";
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const failedOperation = {
      id: "operation-audio-failed",
      workflowId: workflowID,
      revision: 4,
      sequence: 2,
      command: "analyze_audio",
      operation: "analyze_audio",
      status: "failed",
      progress: 100,
      completed: 1,
      total: 1,
      message: "Audio analysis failed.",
      startedAt: "2026-09-21T00:00:00Z",
      updatedAt: "2026-09-21T00:00:02Z",
      completedAt: "2026-09-21T00:00:02Z",
    } as const;
    const initial = {
      ...workflowCurrent(workflowID, 4),
      operation: failedOperation,
    } as ReleaseWorkflowCurrent;
    const disabledBase = workflowCurrent(workflowID, 5);
    const disabled = {
      ...disabledBase,
      workflow: {
        ...disabledBase.workflow,
        audioAnalysisEnabled: false,
        audioAnalysis: null,
      },
    } as ReleaseWorkflowCurrent;
    const refreshed = { ...disabled, operation: failedOperation } as ReleaseWorkflowCurrent;
    const current = vi.fn().mockResolvedValueOnce(initial).mockResolvedValue(refreshed);
    const setAudioAnalysisEnabled = vi.fn(async () => disabled);
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({ current, setAudioAnalysisEnabled }),
        }),
      ),
    });
    await waitFor(() => expect(result.current.audioAnalysis.view.status).toBe("error"));

    await act(() => result.current.audioAnalysis.disable());

    expect(result.current.audioAnalysis.view.enabled).toBe(false);
    expect(result.current.audioAnalysis.view.status).toBe("idle");
    expect(result.current.audioAnalysis.view.error).toBe("");

    await act(() => result.current.workflow.reload());

    expect(result.current.audioAnalysis.view.enabled).toBe(false);
    expect(result.current.audioAnalysis.view.status).toBe("idle");
    expect(result.current.audioAnalysis.view.error).toBe("");
    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it.each([false, true])(
    "scopes retained retries to each tracker, with plain sibling=%s",
    async (withPlain) => {
      const workflowID = "workflow-pending-receipt";
      const retained = {
        ...workflowCurrent(workflowID, 7),
        uploadResult: {
          id: "receipt-result",
          revision: 1,
          results: [
            {
              trackerId: "EXAMPLE",
              status: "failed",
              submissionStatus: "failed",
              failures: [{ failure: { Code: "receipt_retention" } }],
            },
            {
              trackerId: "OTHER",
              status: "partial",
              submissionStatus: "completed",
              clientInjectionStatus: "failed",
              clientFailureCode: "client_injection_failed",
              failures: [{ failure: { Code: "receipt_retention" } }],
            },
            ...(withPlain
              ? [{ trackerId: "PLAIN", status: "failed", submissionStatus: "failed" }]
              : []),
          ],
        },
      } as unknown as ReleaseWorkflowCurrent;
      const retryFailedUploads = vi.fn(async () => retained);
      const retryClientInjections = vi.fn(async () => retained);
      const { result, unmount } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(
          portsFor({
            resumeWorkflowID: workflowID,
            workflow: workflowPorts({
              current: async () => retained,
              retryFailedUploads,
              retryClientInjections,
            }),
          }),
        ),
      });
      await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
      await act(async () => {
        expect(await result.current.upload.retry()).toBe(withPlain);
        expect(await result.current.workflow.retryFailedUploads()).toBe(withPlain);
        expect(await result.current.upload.retryClientInjection()).toBe(false);
        expect(await result.current.workflow.retryClientInjections()).toBe(false);
      });
      if (withPlain) {
        expect(retryFailedUploads).toHaveBeenCalledTimes(2);
        for (const call of [1, 2]) {
          expect(retryFailedUploads).toHaveBeenNthCalledWith(
            call,
            expect.anything(),
            { id: "receipt-result", revision: 1 },
            ["PLAIN"],
            expect.any(Boolean),
            expect.any(String),
            expect.any(AbortSignal),
          );
        }
      } else {
        expect(retryFailedUploads).not.toHaveBeenCalled();
      }
      expect(retryClientInjections).not.toHaveBeenCalled();
      unmount();
    },
  );

  it.each([true, false])("blocks retained mutation actions with liveTest=%s", async (liveTest) => {
    const workflowID = "workflow-live-test";
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const retained = {
      ...workflowCurrent(workflowID, 7),
      uploadResult: {
        id: "retained-result",
        revision: 1,
        results: [
          { trackerId: "EXAMPLE", submissionStatus: "failed" },
          {
            trackerId: "OTHER",
            submissionStatus: "completed",
            clientInjectionStatus: "failed",
            clientFailureCode: "client_injection",
          },
        ],
      },
    } as unknown as ReleaseWorkflowCurrent;
    const continueWorkflow = vi.fn(async () => retained);
    const retryFailedUploads = vi.fn(async () => retained);
    const retryClientInjections = vi.fn(async () => retained);
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({
            current: async () => retained,
            continue: continueWorkflow,
            retryFailedUploads,
            retryClientInjections,
          }),
        }),
        liveTest ? { testRuntime: liveTestRuntime } : { runtimeInfoReady: false },
      ),
    });
    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
    act(() => result.current.upload.changeOptions({ noSeed: false }));
    expect(result.current.upload.view.mutationsAllowed).toBe(false);
    expect(result.current.upload.view.options.noSeed).toBe(liveTest);
    await act(async () => {
      expect(await result.current.upload.start()).toBe(false);
      expect(await result.current.upload.retry()).toBe(false);
      expect(await result.current.upload.retryClientInjection()).toBe(false);
      expect(await result.current.workflow.executeUploads()).toBe(false);
      expect(await result.current.workflow.retryFailedUploads()).toBe(false);
      expect(await result.current.workflow.retryClientInjections()).toBe(false);
    });
    expect(continueWorkflow).not.toHaveBeenCalled();
    expect(retryFailedUploads).not.toHaveBeenCalled();
    expect(retryClientInjections).not.toHaveBeenCalled();
    await act(() => result.current.upload.runDryRun());
    expect(continueWorkflow).toHaveBeenCalledWith(
      expect.objectContaining({
        goal: "dry_run",
        intent: {
          interaction: "interactive",
          noSeed: liveTest,
          media: { screenshotCount: 0, purpose: "final", captureDvdMenus: false },
          descriptions: {
            questionnaireAnswers: {},
            options: {
              RunLogLevel: "info",
              Screens: 0,
              NoSeed: liveTest,
              AudioAnalysis: false,
              AudioTracks: "primary",
              AudioImages: "both",
              SkipAutoTorrent: false,
              OnlyID: false,
              KeepFolder: false,
              KeepImages: false,
              CaptureDVDMenus: false,
              InteractionMode: "interactive",
            },
            imageHost: { FailedHosts: [], SkipUpload: true },
            templateVersion: "workflow-v1",
          },
        },
      }),
      expect.any(AbortSignal),
    );
    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });
  it("reloads authoritative workflow state from the backend active-input snapshot", async () => {
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", "stale-browser-workflow");
    const sourcePath = "C:\\media\\Example.Release.2026.1080p-GRP.mkv";
    const current = vi.fn(async (workflowID: string) => {
      const restored = workflowCurrentFromPreview(workflowCurrent(workflowID, 7), {
        ...preview(sourcePath, 7),
        TrackerData: [
          {
            Tracker: "AITHER",
            TrackerID: "123",
            TorrentURL: "",
            InfoHash: "",
            TMDBID: 0,
            IMDBID: 0,
            TVDBID: 0,
            MALID: 0,
            Category: "movie",
            Description: "",
            DescriptionHTML: "",
            ImageURLs: [],
            Filename: "",
            Matched: true,
            UpdatedAt: "",
          },
        ],
      });
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
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: "workflow-retained",
          workflow: workflowPorts({ current }),
        }),
      ),
    });

    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
    expect(current).toHaveBeenCalledWith("workflow-retained", expect.any(AbortSignal));
    expect(result.current.workflow.view.current?.workflow).toMatchObject({
      id: "workflow-retained",
      revision: 7,
    });
    await waitFor(() => expect(result.current.upload.view.selectedTrackers).toEqual(["AITHER"]));
    expect(result.current.input.view.trackerData).toEqual([
      expect.objectContaining({ Tracker: "AITHER", TrackerID: "123" }),
    ]);
    expect(result.current.input.view.intent.trackerSourceIDs).toEqual({});

    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("refetches the authoritative snapshot for an active-input event hint", async () => {
    const sourcePath = "C:\\media\\Event.Release.2026.mkv";
    const active = workflowCurrentFromPreview(
      workflowCurrent("workflow-event", 3),
      preview(sourcePath, 1),
    );
    const get = vi
      .fn()
      .mockResolvedValueOnce({ state: "empty", revision: 1 })
      .mockResolvedValueOnce({
        state: "active",
        revision: 2,
        inputId: "input-event",
        sourceVersion: "source-event-v1",
        current: active,
      });
    let inputChanged: () => void = () => undefined;
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          activeInput: {
            get,
            subscribe: (callback) => {
              inputChanged = callback;
              return () => undefined;
            },
          },
          workflow: workflowPorts({ current: async () => active }),
        }),
      ),
    });

    await waitFor(() => expect(get).toHaveBeenCalledOnce());
    act(() => inputChanged());
    await waitFor(() => expect(result.current.identity.view.sourcePath).toBe(sourcePath));
    expect(get).toHaveBeenCalledTimes(2);
    expect(result.current.input.view.activeInput).toEqual({
      state: "active",
      revision: 2,
      inputID: "input-event",
      sourceVersion: "source-event-v1",
      recoveryWorkflowIDs: [],
    });
    unmount();
  });

  it("correlates source verification and cancels its accepted operation before continuing", async () => {
    let onVerification: Parameters<ReleaseSessionPorts["activeInput"]["subscribe"]>[1] = () =>
      undefined;
    const accepted = createDeferred<ActiveInputSnapshot>();
    const verificationCancellation = createDeferred<ActiveInputSnapshot>();
    const operation: WorkflowOperationStatus = {
      id: "source-verification-operation",
      workflowId: "workflow-verification",
      revision: 1,
      sequence: 1,
      command: "prepare",
      operation: "preparation",
      status: "running",
      completed: 0,
      total: 1,
      progress: 0,
      startedAt: "2026-07-20T00:00:00Z",
      updatedAt: "2026-07-20T00:00:00Z",
    };
    const canceled = { ...operation, status: "canceled" as const };
    const current = { ...workflowCurrent(operation.workflowId, 1), operation: canceled };
    const cancelOperation = vi.fn(async () => canceled);
    const open = vi.fn(
      async (
        _request: Parameters<ReleaseSessionPorts["activeInput"]["open"]>[0],
        _signal: AbortSignal,
      ) => accepted.promise,
    );
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({
            cancelOperation,
            current: async () => current,
            operation: async () => canceled,
          }),
          activeInput: {
            open,
            cancelOpen: async () => verificationCancellation.promise,
            subscribe: (_onChange, nextVerification) => {
              onVerification = nextVerification;
              return () => undefined;
            },
          },
        }),
      ),
    });
    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));

    let preparation!: Promise<boolean>;
    act(() => {
      preparation = result.current.input.openSource("C:\\media\\Large.Release.2026.mkv");
    });
    await waitFor(() => expect(open).toHaveBeenCalledOnce());
    const request = open.mock.calls[0][0];
    act(() =>
      onVerification({
        correlationID: request.request.idempotencyKey,
        completedBytes: 500,
        totalBytes: 1000,
        message: "Verifying source content.",
        status: "running",
      }),
    );
    expect(result.current.input.view.sourceVerification).toMatchObject({
      correlationID: request.request.idempotencyKey,
      completedBytes: 500,
      totalBytes: 1000,
    });

    act(() => result.current.input.cancelPreparation());
    expect(result.current.workflow.cancellation.pending).toBe(true);
    expect(open.mock.calls[0][1].aborted).toBe(false);
    expect(cancelOperation).not.toHaveBeenCalled();
    await act(async () => {
      accepted.resolve({
        state: "active",
        revision: 1,
        inputId: "owned-input",
        sourceVersion: "owned-version",
        current: { ...current, operation },
      });
      expect(await preparation).toBe(false);
    });
    expect(cancelOperation).toHaveBeenCalledWith(
      operation.workflowId,
      operation.id,
      expect.any(AbortSignal),
    );
    expect(open.mock.calls[0][1].aborted).toBe(true);
    expect(result.current.input.view.sourceVerification).toBeNull();
    expect(result.current.input.view.status).toBe("cancelled");
    expect(result.current.input.view.activeInput.inputID).toBe("owned-input");
    expect(result.current.workflow.cancellation.pending).toBe(false);
    await act(async () => {
      verificationCancellation.reject(new Error("Synthetic admission already accepted."));
      await Promise.resolve();
    });
    expect(result.current.input.view.activeInput.inputID).toBe("owned-input");
    unmount();
  });

  it("recovers and reconciles only an owner-scoped legacy workflow", async () => {
    const recoveryAction = {
      id: "action-reconcile",
      kind: "reconcile_submission",
      status: "pending" as const,
      workflowRevision: 7,
      trackerId: "AITHER",
      effectKind: "tracker_submission" as const,
      effectScopeId: "AITHER",
      prompt: "Verify whether the interrupted upload completed.",
      options: [
        {
          value: "not_completed",
          label: "Confirmed not completed; allow a fresh exact attempt",
        },
      ],
      createdAt: "2026-09-19T00:00:00Z",
    };
    const baseCurrent = workflowCurrent("workflow-legacy", 7);
    const recoveryCurrent: ReleaseWorkflowCurrent = {
      ...baseCurrent,
      workflow: {
        ...baseCurrent.workflow,
        status: "blocked",
        requiredActions: [recoveryAction],
      },
      continuation: {
        ...baseCurrent.continuation,
        lifecycle: "waiting",
        disposition: "needs_action",
        requiredActions: [recoveryAction],
        availableGoals: baseCurrent.continuation.availableGoals.map((goal) => ({
          ...goal,
          available: false,
        })),
      },
    };
    const recover = vi.fn(async () => ({
      state: "recovering" as const,
      revision: 2,
      current: recoveryCurrent,
    }));
    const reconcile = vi.fn(async () => ({ state: "empty" as const, revision: 3 }));
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          activeInput: {
            get: async () => ({
              state: "empty",
              revision: 1,
              recoveryWorkflowIds: ["workflow-legacy"],
            }),
            recover,
            reconcile,
          },
        }),
      ),
    });
    await waitFor(() =>
      expect(result.current.input.view.activeInput.recoveryWorkflowIDs).toEqual([
        "workflow-legacy",
      ]),
    );

    await act(() => result.current.input.recoverLegacyWorkflow("workflow-legacy"));
    expect(recover).toHaveBeenCalledWith(
      { workflowId: "workflow-legacy" },
      expect.any(AbortSignal),
    );
    expect(result.current.input.view.activeInput.state).toBe("recovering");
    await act(() => expect(result.current.input.prepare()).resolves.toBe(false));

    await act(() => result.current.workflow.confirmAction(recoveryAction));
    expect(reconcile).toHaveBeenCalledWith(
      {
        authority: { workflowId: "workflow-legacy", expectedRevision: 7 },
        answer: {
          actionId: "action-reconcile",
          workflowRevision: 7,
          selectedValues: ["not_completed"],
        },
        idempotencyKey: expect.any(String),
      },
      expect.any(AbortSignal),
    );
    expect(result.current.input.view.activeInput.state).toBe("empty");
    expect(result.current.workflow.view.current).toBeNull();
    unmount();
  });

  it("reclaims a legacy recovery slot from a previous process", async () => {
    const recover = vi.fn(async () => ({
      state: "recovering" as const,
      revision: 3,
      current: workflowCurrent("workflow-legacy", 2),
    }));
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          activeInput: {
            get: async () => ({
              state: "recovering",
              revision: 2,
              recoveryWorkflowIds: ["workflow-legacy"],
            }),
            recover,
          },
        }),
      ),
    });
    await waitFor(() =>
      expect(result.current.input.view.activeInput.recoveryWorkflowIDs).toEqual([
        "workflow-legacy",
      ]),
    );

    await act(() =>
      expect(result.current.input.recoverLegacyWorkflow("unrelated")).resolves.toBe(false),
    );
    expect(recover).not.toHaveBeenCalled();
    await act(() =>
      expect(result.current.input.recoverLegacyWorkflow("workflow-legacy")).resolves.toBe(true),
    );
    expect(recover).toHaveBeenCalledWith(
      { workflowId: "workflow-legacy" },
      expect.any(AbortSignal),
    );
    unmount();
  });

  it("opens a verified source from an opaque previous-process input", async () => {
    const sourcePath = "C:\\media\\Example.Release.2026-GRP.mkv";
    const open = vi.fn(async () => ({
      state: "active" as const,
      revision: 6,
      inputId: "input-new",
      sourceVersion: "verified-source",
      current: workflowCurrent("workflow-new", 2),
    }));
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          activeInput: {
            get: async () => ({ state: "recovering", revision: 2 }),
            open,
          },
        }),
      ),
    });
    await waitFor(() => expect(result.current.input.view.activeInput.state).toBe("recovering"));
    await act(async () => {
      await result.current.input.openSource(sourcePath);
    });
    expect(open).toHaveBeenCalledWith(
      expect.objectContaining({
        expectedRevision: 2,
        request: expect.objectContaining({
          intent: expect.objectContaining({
            preparation: expect.objectContaining({ SourcePath: sourcePath }),
          }),
        }),
      }),
      expect.any(AbortSignal),
    );
    unmount();
  });

  it("accepts a masked same-revision recovery snapshot after server restart", async () => {
    let restarted = false;
    let emitInputChanged: () => void = () => undefined;
    const get = vi.fn(async () =>
      restarted
        ? {
            state: "recovering" as const,
            revision: 3,
            recoveryWorkflowIds: ["workflow-legacy"],
          }
        : {
            state: "recovering" as const,
            revision: 3,
            current: workflowCurrent("workflow-legacy", 2),
          },
    );
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          activeInput: {
            get,
            subscribe: (onChange) => {
              emitInputChanged = onChange;
              return () => undefined;
            },
          },
        }),
      ),
    });
    await waitFor(() => expect(result.current.input.view.activeInput.state).toBe("recovering"));
    await waitFor(() =>
      expect(result.current.workflow.view.current?.workflow.id).toBe("workflow-legacy"),
    );
    restarted = true;
    act(() => emitInputChanged());
    await waitFor(() =>
      expect(result.current.input.view.activeInput.recoveryWorkflowIDs).toEqual([
        "workflow-legacy",
      ]),
    );
    expect(result.current.workflow.view.current).toBeNull();
    unmount();
  });

  it("clears a prior-process input with the same revision before opening a new source", async () => {
    let restarted = false;
    let emitInputChanged: () => void = () => undefined;
    const get = vi.fn(async () =>
      restarted
        ? { state: "recovering" as const, revision: 3 }
        : {
            state: "active" as const,
            revision: 3,
            inputId: "input-old",
            sourceVersion: "source-old",
            current: workflowCurrent("workflow-old", 2),
          },
    );
    const open = vi.fn(async () => ({
      state: "active" as const,
      revision: 7,
      inputId: "input-new",
      sourceVersion: "source-new",
      current: workflowCurrent("workflow-new", 2),
    }));
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          activeInput: {
            get,
            open,
            subscribe: (onChange) => {
              emitInputChanged = onChange;
              return () => undefined;
            },
          },
        }),
      ),
    });
    await waitFor(() => expect(result.current.input.view.activeInput.inputID).toBe("input-old"));
    restarted = true;
    act(() => emitInputChanged());
    await waitFor(() => expect(result.current.input.view.activeInput.state).toBe("recovering"));
    expect(result.current.input.view.activeInput.inputID).toBe("");
    expect(result.current.workflow.view.current).toBeNull();

    await act(async () => {
      await result.current.input.openSource("C:\\media\\Example.Release.2026-GRP.mkv");
    });
    expect(open).toHaveBeenCalledWith(
      expect.objectContaining({ expectedRevision: 3 }),
      expect.any(AbortSignal),
    );
    unmount();
  });

  it("reconciles a current submission action from an active input", async () => {
    const reconciliationAction = {
      id: "action-active-reconcile",
      kind: "reconcile_submission",
      status: "pending" as const,
      workflowRevision: 9,
      trackerId: "AITHER",
      effectKind: "tracker_submission" as const,
      effectScopeId: "AITHER",
      prompt: "Verify whether the interrupted upload completed.",
      options: [
        {
          value: "not_completed",
          label: "Confirmed not completed; allow a fresh exact attempt",
        },
      ],
      createdAt: "2026-09-19T00:00:00Z",
    };
    const baseCurrent = workflowCurrent("workflow-active-reconcile", 9);
    const activeCurrent: ReleaseWorkflowCurrent = {
      ...baseCurrent,
      workflow: {
        ...baseCurrent.workflow,
        status: "blocked",
        requiredActions: [reconciliationAction],
      },
      continuation: {
        ...baseCurrent.continuation,
        lifecycle: "waiting",
        disposition: "needs_action",
        requiredActions: [reconciliationAction],
        availableGoals: baseCurrent.continuation.availableGoals.map((goal) => ({
          ...goal,
          available: false,
        })),
      },
    };
    const reconcile = vi.fn(async () => ({ state: "empty" as const, revision: 5 }));
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          activeInput: {
            get: async () => ({
              state: "active",
              revision: 4,
              inputId: "input-active-reconcile",
              sourceVersion: "source-active-reconcile-v1",
              current: activeCurrent,
            }),
            reconcile,
          },
        }),
      ),
    });
    await waitFor(() => expect(result.current.input.view.activeInput.state).toBe("active"));

    await act(() =>
      expect(result.current.workflow.confirmAction(reconciliationAction)).resolves.toBe(true),
    );
    expect(reconcile).toHaveBeenCalledWith(
      {
        authority: { workflowId: "workflow-active-reconcile", expectedRevision: 9 },
        answer: {
          actionId: "action-active-reconcile",
          workflowRevision: 9,
          selectedValues: ["not_completed"],
        },
        idempotencyKey: expect.any(String),
      },
      expect.any(AbortSignal),
    );
    expect(result.current.input.view.activeInput.state).toBe("empty");
    expect(result.current.workflow.view.current).toBeNull();
    unmount();
  });

  it("keeps image retry busy until the pre-reconciliation observer finishes", async () => {
    const reconciliationAction = {
      id: "action-active-reconcile",
      kind: "reconcile_submission",
      status: "pending" as const,
      workflowRevision: 9,
      trackerId: "AITHER",
      effectKind: "tracker_submission" as const,
      effectScopeId: "AITHER",
      prompt: "Verify whether the interrupted upload completed.",
      options: [
        {
          value: "not_completed",
          label: "Confirmed not completed; allow a fresh exact attempt",
        },
      ],
      createdAt: "2026-09-19T00:00:00Z",
    };
    const baseCurrent = workflowCurrent("workflow-active-reconcile", 9);
    const activeCurrent: ReleaseWorkflowCurrent = {
      ...baseCurrent,
      workflow: {
        ...baseCurrent.workflow,
        status: "blocked",
        requiredActions: [reconciliationAction],
      },
      continuation: {
        ...baseCurrent.continuation,
        lifecycle: "waiting",
        disposition: "needs_action",
        requiredActions: [reconciliationAction],
        availableGoals: baseCurrent.continuation.availableGoals.map((goal) => ({
          ...goal,
          available: false,
        })),
      },
    };
    const operation = {
      id: "operation-before-reconcile",
      workflowId: activeCurrent.workflow.id,
      revision: 1,
      sequence: 1,
      command: "upload_images",
      operation: "image_upload",
      status: "running",
      progress: 0,
      completed: 0,
      total: 1,
      startedAt: "2026-09-19T00:00:00Z",
      updatedAt: "2026-09-19T00:00:00Z",
    } as const;
    const pendingOperation = createDeferred<NonNullable<ReleaseWorkflowCurrent["operation"]>>();
    const poll = vi.fn(() => pendingOperation.promise);
    const reconciled = workflowCurrent(activeCurrent.workflow.id, 10);
    const snapshot = (current: ReleaseWorkflowCurrent): ActiveInputSnapshot => ({
      state: "active",
      revision: 4,
      inputId: "input-active-reconcile",
      sourceVersion: "source-active-reconcile-v1",
      current,
    });
    const reconcile = vi.fn(async () => snapshot(reconciled));
    const uploadImages = vi
      .fn(async (_current: ReleaseWorkflowCurrent) => reconciled)
      .mockResolvedValueOnce({ ...activeCurrent, operation });
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          activeInput: {
            get: async () => snapshot(activeCurrent),
            reconcile,
          },
          workflow: workflowPorts({
            operation: poll,
            current: async () => reconciled,
            uploadImages,
          }),
        }),
      ),
    });
    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
    let firstUpload!: Promise<boolean>;
    act(() => {
      firstUpload = result.current.uploadedImages.upload();
    });
    await waitFor(() => expect(poll).toHaveBeenCalled(), { timeout: 5000 });
    await act(() =>
      expect(result.current.workflow.confirmAction(reconciliationAction)).resolves.toBe(true),
    );
    expect(result.current.uploadedImages.view.status).toBe("running");
    await act(async () => pendingOperation.resolve({ ...operation, status: "completed" }));
    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
    await act(() => result.current.uploadedImages.upload());
    await firstUpload;
    expect(uploadImages).toHaveBeenCalledTimes(2);
    expect(uploadImages.mock.calls[1]?.[0].workflow.revision).toBe(10);
    unmount();
  });

  it("resyncs on focus and the visible fifteen-second fallback", async () => {
    vi.useFakeTimers();
    const get = vi.fn(async () => ({ state: "empty", revision: 1 }));
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ activeInput: { get } })),
    });
    try {
      await act(async () => Promise.resolve());
      expect(get).toHaveBeenCalledOnce();
      act(() => result.current.input.updateSourceDraft("C:\\media\\Unsaved.mkv"));

      window.dispatchEvent(new Event("focus"));
      await act(async () => Promise.resolve());
      expect(get).toHaveBeenCalledTimes(2);
      expect(result.current.input.view.sourceDraft).toBe("C:\\media\\Unsaved.mkv");

      await act(async () => {
        await vi.advanceTimersByTimeAsync(15_000);
      });
      expect(get).toHaveBeenCalledTimes(3);
      expect(result.current.input.view.sourceDraft).toBe("C:\\media\\Unsaved.mkv");
    } finally {
      unmount();
      vi.useRealTimers();
    }
  });

  it("keeps a locally chosen tracker subset when a later focus resync returns the stale backend selection", async () => {
    const sourcePath = "C:\\media\\Tracker.Choice.2026.mkv";
    const current = workflowCurrentFromPreview(
      workflowCurrent("workflow-tracker-choice", 7),
      preview(sourcePath, 1),
    );
    let revision = 0;
    const get = vi.fn(
      async (): Promise<ActiveInputSnapshot> => ({
        state: "active",
        revision: ++revision,
        inputId: "input-tracker-choice",
        sourceVersion: "source-tracker-choice-v1",
        current,
      }),
    );
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ activeInput: { get } })),
    });
    try {
      await waitFor(() => expect(result.current.input.view.activeInput.revision).toBe(1));
      act(() => result.current.input.chooseTrackers(["AITHER"]));
      expect(result.current.input.view.selectedTrackers).toEqual(["AITHER"]);

      act(() => window.dispatchEvent(new Event("focus")));
      await waitFor(() => expect(result.current.input.view.activeInput.revision).toBe(2));
      expect(get).toHaveBeenCalledTimes(2);
      expect(result.current.input.view.selectedTrackers).toEqual(["AITHER"]);
    } finally {
      unmount();
    }
  });

  it("replaces a dirty draft when a resync first sees an unfinished different input", async () => {
    const oldPath = "C:\\media\\Old.Release.2026.mkv";
    const newPath = "C:\\media\\New.Release.2026.mkv";
    const newWorkflowID = "workflow-new-input";
    const operation = {
      id: "operation-new-input",
      workflowId: newWorkflowID,
      revision: 1,
      sequence: 1,
      command: "prepare_release",
      operation: "preparation",
      status: "running",
      progress: 0,
      completed: 0,
      total: 1,
      startedAt: "2026-09-25T00:00:00Z",
      updatedAt: "2026-09-25T00:00:00Z",
    } as const;
    let snapshot: ActiveInputSnapshot = {
      state: "active",
      revision: 2,
      inputId: "input-old",
      sourceVersion: "source-old",
      current: workflowCurrentFromPreview(
        workflowCurrent("workflow-old-input", 3),
        preview(oldPath, 1),
      ),
    };
    const get = vi.fn(async () => snapshot);
    const completed = workflowCurrentFromPreview(
      workflowCurrent(newWorkflowID, 2),
      preview(newPath, 1),
    );
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          activeInput: { get },
          workflow: workflowPorts({
            operation: vi.fn(async () => ({ ...operation, status: "completed" as const })),
            current: vi.fn(async () => completed),
          }),
        }),
      ),
    });
    try {
      await waitFor(() => expect(result.current.input.view.sourceDraft).toBe(oldPath));
      act(() => result.current.input.chooseTrackers(["AITHER"]));
      expect(result.current.input.view.selectedTrackers).toEqual(["AITHER"]);
      snapshot = {
        state: "active",
        revision: 4,
        inputId: "input-new",
        sourceVersion: "source-new",
        current: { ...workflowCurrent(newWorkflowID, 1), operation },
      };
      act(() => window.dispatchEvent(new Event("focus")));
      await waitFor(() => expect(result.current.input.view.sourceDraft).toBe(newPath), {
        timeout: 5_000,
      });
      expect(result.current.input.view.activeInput.inputID).toBe("input-new");
      expect(result.current.input.view.preparationDirty).toBe(false);
      expect(result.current.input.view.selectedTrackers).toEqual([]);
    } finally {
      unmount();
    }
  });

  it("surfaces an Open failure after its rollback resync advances the empty slot", async () => {
    const sourcePath = "Z:\\missing\\Invalid.Release.2026.mkv";
    const pendingOpen = createDeferred<ActiveInputSnapshot>();
    let slotRevision = 0;
    let emitInputChanged: () => void = () => undefined;
    const get = vi.fn(async () => ({ state: "empty" as const, revision: slotRevision }));
    const open = vi.fn(() => pendingOpen.promise);
    const subscribe: ReleaseSessionPorts["activeInput"]["subscribe"] = (onChange) => {
      emitInputChanged = onChange;
      return () => undefined;
    };
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ activeInput: { get, open, subscribe } })),
    });
    await waitFor(() => expect(get).toHaveBeenCalledOnce());
    act(() => result.current.input.updateSourceDraft(sourcePath));

    let preparation!: Promise<boolean>;
    act(() => {
      preparation = result.current.input.prepareSource(
        sourcePath,
        result.current.input.view.intent,
      );
    });
    await waitFor(() => expect(open).toHaveBeenCalledOnce());
    expect(result.current.input.view.status).toBe("running");

    const failure = Object.assign(
      new Error("The source path is unavailable. Recovery: edit input."),
      {
        failure: {
          Code: "invalid_source",
          Operation: "preparation",
          Message: "The source path is unavailable.",
          Recovery: "edit_input",
        },
      },
    );
    await act(async () => {
      pendingOpen.reject(failure);
      expect(await preparation).toBe(false);
    });
    expect(result.current.input.view.status).toBe("error");

    slotRevision = 2;
    act(() => emitInputChanged());
    await waitFor(() => expect(result.current.input.view.activeInput.revision).toBe(2));

    expect(result.current.input.view.status).toBe("error");
    expect(result.current.input.view.error).toBe(
      "The source path is unavailable. Recovery: edit input.",
    );
    expect(result.current.input.view.failure?.Code).toBe("invalid_source");
    expect(result.current.input.view.sourceDraft).toBe(sourcePath);
    unmount();
  });

  it.each(["error", "stale", "closed"])(
    "does not accept an open with outcome %s",
    async (outcome) => {
      const sourcePath = "C:\\media\\Rejected.Open.2026.mkv";
      const response = createDeferred<ActiveInputSnapshot>();
      const open = vi.fn(() => response.promise);
      const { result, unmount } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(
          portsFor({
            activeInput: {
              get: async (): Promise<ActiveInputSnapshot> =>
                outcome === "closed"
                  ? {
                      state: "active",
                      revision: 4,
                      inputId: "input-current",
                      sourceVersion: "source-previous",
                      current: workflowCurrentFromPreview(
                        workflowCurrent("workflow-current", 2),
                        preview(sourcePath, 1),
                      ),
                    }
                  : { state: "empty", revision: 4 },
              open,
            },
          }),
        ),
      });
      try {
        await waitFor(() => expect(result.current.input.view.activeInput.revision).toBe(4));
        let opening!: Promise<boolean>;
        act(() => {
          opening = result.current.input.openSource(sourcePath);
        });
        await waitFor(() => expect(open).toHaveBeenCalledOnce());
        if (outcome === "closed") {
          await act(async () => expect(await result.current.input.close()).toBe(true));
        }
        await act(async () => {
          if (outcome === "error") response.reject(new Error("Source could not be opened."));
          else
            response.resolve({
              state: "active",
              revision: outcome === "stale" ? 3 : 5,
              inputId: "input-rejected",
              sourceVersion: "source-one",
              sourcePath,
              current: workflowCurrentFromPreview(
                workflowCurrent("workflow-rejected", 2),
                preview(sourcePath, 1),
              ),
            });
          expect(await opening).toBe(false);
        });
        expect(result.current.input.view.activeInput.state).toBe("empty");
        expect(result.current.input.view.selectedSource).toBe("");
        expect(result.current.workflow.view.current).toBeNull();
      } finally {
        unmount();
      }
    },
  );

  it.each(["confirm_corrections", "select_playlist"])(
    "reports an accepted open separately from preparation readiness for %s",
    async (kind) => {
      const sourcePath = "C:\\media\\Pending.Review.2026.mkv";
      const initial = workflowCurrent("workflow-pending-open", 2);
      const current: ReleaseWorkflowCurrent = {
        ...initial,
        workflow: {
          ...initial.workflow,
          status: "blocked",
          requiredActions: [
            {
              id: "action-review",
              kind,
              status: "pending",
              workflowRevision: 2,
              createdAt: initial.workflow.createdAt,
              prompt: "Review the input.",
              ...(kind === "confirm_corrections"
                ? {
                    correctionConfirmation: {
                      revision: 3,
                      fields: ["metadata.title"],
                      previousBindings: {},
                      currentBinding: {
                        category: "movie",
                        sourceFingerprint: "source-one",
                        providerIds: { imdbId: 0, tmdbId: 222, tvdbId: 0, tvmazeId: 0, malId: 0 },
                      },
                    },
                  }
                : { options: [] }),
            },
          ],
        },
      };
      const { result, unmount } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(
          portsFor({ workflow: workflowPorts({ continue: async () => current }) }),
        ),
      });
      try {
        await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
        await act(async () => expect(await result.current.input.openSource(sourcePath)).toBe(true));
        expect(result.current.input.view.status).toBe("awaiting_input");
        expect(result.current.input.view.error).toBe("");
        expect(result.current.input.view.selectedSource).toBe(sourcePath);
        expect(result.current.input.view.release).toBeNull();
        await act(async () =>
          expect(
            await result.current.input.prepareSource(sourcePath, result.current.input.view.intent),
          ).toBe(false),
        );
        expect(result.current.input.view.status).toBe("awaiting_input");
      } finally {
        unmount();
      }
    },
  );

  it.each(["keep", "edit", "reset"])(
    "reviews saved corrections before a first release exists and applies %s with revision authority",
    async (choice) => {
      const sourcePath = "C:\\media\\Correction.Review.2026.mkv";
      const workflowID = "workflow-correction-review";
      const binding = {
        category: "movie" as const,
        providerIds: { imdbId: 0, tmdbId: 222, tvdbId: 0, tvmazeId: 0, malId: 0 },
        sourceFingerprint: "source-current",
      };
      const review = {
        revision: 3,
        fields: ["metadata.title", "metadata.genres"],
        previousBindings: {
          "metadata.title": { ...binding, providerIds: { ...binding.providerIds, tmdbId: 111 } },
        },
        currentBinding: binding,
      };
      const initial = workflowCurrent(workflowID, 7);
      let current: ReleaseWorkflowCurrent = {
        ...initial,
        workflow: {
          ...initial.workflow,
          status: "blocked",
          requiredActions: [
            {
              id: "action-corrections",
              kind: "confirm_corrections",
              status: "pending",
              workflowRevision: 7,
              createdAt: initial.workflow.createdAt,
              prompt: "Review saved corrections.",
              correctionConfirmation: review,
            },
          ],
        },
        corrections: {
          revision: 3,
          corrections: {
            version: 1,
            identity: {},
            releaseName: {},
            metadata: { Title: "Saved title", Genres: ["Drama"] },
            staleContentFields: review.fields,
          },
        },
      };
      let snapshot: ActiveInputSnapshot = { state: "empty", revision: 0 };
      const open = vi.fn(
        async (
          request: Parameters<ReleaseSessionPorts["activeInput"]["open"]>[0],
        ): Promise<ActiveInputSnapshot> => {
          if (request.request.intent.correctionPatch) {
            // Resolve one field first; the remaining review must remain available without a release.
            const remaining = request.request.intent.correctionPatch.confirmFields?.some(
              (field) => field.field === "metadata.genres",
            )
              ? []
              : ["metadata.genres"];
            current = {
              ...current,
              workflow: {
                ...current.workflow,
                revision: current.workflow.revision + 1,
                requiredActions: remaining.length
                  ? [
                      {
                        ...current.workflow.requiredActions![0],
                        correctionConfirmation: { ...review, revision: 4, fields: remaining },
                      },
                    ]
                  : [],
              },
              corrections: { ...current.corrections!, revision: 4 },
            };
            if (!remaining.length)
              current = workflowCurrentFromPreview(current, preview(sourcePath, 1));
          }
          snapshot = {
            state: "active",
            revision: request.expectedRevision + 1,
            inputId: "input-corrections",
            sourceVersion: "source-one",
            sourcePath,
            current,
          };
          return snapshot;
        },
      );
      const get = vi.fn(async () => structuredClone(snapshot));
      const wrapper = wrapperFor(
        portsFor({
          activeInput: { open, get },
          workflow: workflowPorts({ continue: async () => current }),
        }),
      );
      let { result, unmount } = renderHook(useReleaseSession, { wrapper });
      try {
        act(() => result.current.input.updateSourceDraft(sourcePath));
        await act(async () => expect(await result.current.input.prepare()).toBe(false));
        expect(result.current.input.view.status).toBe("awaiting_input");
        expect(result.current.input.view.error).toBe("");
        expect(result.current.input.view.release).toBeNull();
        expect(result.current.input.view.correctionReview).toEqual(review);
        expect(result.current.input.view.intent.metadata).toEqual({
          Title: "Saved title",
          Genres: ["Drama"],
        });
        unmount();
        ({ result, unmount } = renderHook(useReleaseSession, { wrapper }));
        await waitFor(() => expect(result.current.input.view.status).toBe("awaiting_input"));
        expect(result.current.input.view.selectedSource).toBe(sourcePath);
        expect(result.current.input.view.intent.metadata).toEqual({
          Title: "Saved title",
          Genres: ["Drama"],
        });
        expect(result.current.input.view.correctionReview).toEqual(review);
        act(() => {
          if (choice === "keep")
            result.current.input.confirmCorrection({ field: "metadata.title" });
          if (choice === "edit")
            result.current.input.changeMetadata({
              ...result.current.input.view.intent.metadata,
              Title: "Edited title",
            });
          if (choice === "reset") result.current.input.resetCorrection({ field: "metadata.title" });
        });
        const refreshOnFocus = async () => {
          const calls = get.mock.calls.length;
          await act(async () => fireEvent.focus(window));
          await waitFor(() => expect(get).toHaveBeenCalledTimes(calls + 1));
          await waitFor(() => expect(result.current.input.view.status).toBe("awaiting_input"));
        };
        await refreshOnFocus();
        expect(result.current.input.view.correctionDirty).toBe(true);
        expect(result.current.input.view.confirmFields).toEqual(
          choice === "keep" ? [{ field: "metadata.title" }] : [],
        );
        expect(result.current.input.view.resetFields).toEqual(
          choice === "reset" ? [{ field: "metadata.title" }] : [],
        );
        expect(result.current.input.view.valueFields).toEqual(
          choice === "edit" ? [{ field: "metadata.title" }] : [],
        );
        expect(result.current.input.view.intent.metadata.Title).toBe(
          choice === "edit" ? "Edited title" : choice === "reset" ? undefined : "Saved title",
        );
        current = {
          ...current,
          workflow: {
            ...current.workflow,
            revision: current.workflow.revision + 1,
            requiredActions: [
              {
                ...current.workflow.requiredActions![0],
                workflowRevision: current.workflow.revision + 1,
                correctionConfirmation: {
                  ...review,
                  currentBinding: {
                    ...binding,
                    providerIds: { ...binding.providerIds, tmdbId: 333 },
                  },
                },
              },
            ],
          },
        };
        snapshot = { ...snapshot, current };
        await refreshOnFocus();
        expect(result.current.input.view.confirmFields).toEqual([]);
        expect(result.current.input.view.resetFields).toEqual(
          choice === "reset" ? [{ field: "metadata.title" }] : [],
        );
        expect(result.current.input.view.valueFields).toEqual(
          choice === "edit" ? [{ field: "metadata.title" }] : [],
        );
        expect(result.current.input.view.intent.metadata.Title).toBe(
          choice === "edit" ? "Edited title" : choice === "reset" ? undefined : "Saved title",
        );
        if (choice === "keep") {
          act(() => result.current.input.confirmCorrection({ field: "metadata.title" }));
        }
        await act(async () => expect(await result.current.input.prepare()).toBe(false));
        expect(open.mock.calls[1][0]).toMatchObject({
          expectedRevision: 1,
          request: {
            intent: {
              correctionPatch: {
                expectedRevision: 3,
                confirmFields: choice === "keep" ? [{ field: "metadata.title" }] : [],
                resetFields: choice === "reset" ? [{ field: "metadata.title" }] : [],
                values: {
                  Identity: {},
                  ReleaseName: {},
                  Metadata: choice === "edit" ? { Title: "Edited title" } : {},
                },
              },
            },
          },
        });
        expect(result.current.input.view.status).toBe("awaiting_input");
        expect(result.current.input.view.error).toBe("");
        expect(result.current.input.view.correctionReview?.fields).toEqual(["metadata.genres"]);
        expect(result.current.input.view.confirmFields).toEqual([]);
        expect(result.current.input.view.resetFields).toEqual([]);
        expect(result.current.input.view.valueFields).toEqual([]);
        act(() => result.current.input.confirmCorrection({ field: "metadata.genres" }));
        await act(async () => expect(await result.current.input.prepare()).toBe(true));
        expect(result.current.input.view.status).toBe("ready");
        expect(result.current.input.view.correctionReview).toBeNull();
      } finally {
        unmount();
      }
    },
  );

  it("keeps a same-input correction draft for an explicit retry after stale-review resync", async () => {
    const workflowID = "workflow-stale-correction";
    const sourcePath = "C:\\media\\Stale.Correction.2026.mkv";
    const initial = workflowCurrentFromPreview(
      workflowCurrent(workflowID, 7),
      preview(sourcePath, 1),
    );
    let current: ReleaseWorkflowCurrent = {
      ...initial,
      factInstructions: {
        ...initial.factInstructions!,
        correctionRevision: 3,
        instructions: {
          ...initial.factInstructions!.instructions,
          Identity: { TMDBID: 777 },
        },
      },
      corrections: {
        revision: 3,
        corrections: {
          version: 1,
          identity: { TMDBID: 777 },
          releaseName: {},
          metadata: {},
        },
      },
    };
    let getCalls = 0;
    const get = vi.fn(async (): Promise<ActiveInputSnapshot> => {
      getCalls += 1;
      return {
        state: "active",
        revision: getCalls === 1 ? 8 : 10,
        inputId: "input-stale-correction",
        sourceVersion: "source-stale-correction-v1",
        current,
      };
    });
    let openCalls = 0;
    const open = vi.fn(
      async (
        _request: Parameters<ReleaseSessionPorts["activeInput"]["open"]>[0],
      ): Promise<ActiveInputSnapshot> => {
        openCalls += 1;
        if (openCalls === 1) {
          throw Object.assign(new Error("The release changed. Recovery: review again."), {
            failure: {
              Code: "stale_review",
              Operation: "preparation",
              Message: "The release changed.",
              Recovery: "review_again",
            },
          });
        }
        current = {
          ...current,
          workflow: { ...current.workflow, revision: 8 },
          factInstructions: {
            ...current.factInstructions!,
            correctionRevision: 4,
            instructions: {
              ...current.factInstructions!.instructions,
              Identity: { TMDBID: 0 },
            },
          },
          corrections: {
            revision: 4,
            corrections: {
              version: 1,
              identity: { TMDBID: 0 },
              releaseName: {},
              metadata: {},
            },
          },
        };
        return {
          state: "active",
          revision: 11,
          inputId: "input-stale-correction",
          sourceVersion: "source-stale-correction-v1",
          current,
        };
      },
    );
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          activeInput: { get, open },
          workflow: workflowPorts({
            current: async () => current,
            continue: async () => current,
          }),
        }),
      ),
    });
    await waitFor(() => expect(result.current.input.view.selectedSource).toBe(sourcePath));

    act(() => result.current.input.changeIdentity({ TMDBID: 0 }));
    act(() => result.current.input.changeClientSearch({ skip: true, client: "" }));
    expect(result.current.input.view.correctionDirty).toBe(true);

    await act(async () => expect(await result.current.input.prepare()).toBe(false));
    expect(get).toHaveBeenCalledTimes(2);
    expect(open).toHaveBeenCalledOnce();
    expect(result.current.input.view.status).toBe("ready");
    expect(result.current.workflow.view.status).toBe("ready");
    expect(result.current.input.view.activeInput.revision).toBe(10);
    expect(result.current.input.view.intent.identity.TMDBID).toBe(0);
    expect(result.current.input.view.intent.search.skip).toBe(true);
    expect(result.current.input.view.correctionDirty).toBe(true);

    await act(async () => expect(await result.current.input.prepare()).toBe(true));
    expect(open).toHaveBeenCalledTimes(2);
    const firstRequest = open.mock.calls[0][0];
    const retryRequest = open.mock.calls[1][0];
    expect(firstRequest).toMatchObject({
      expectedRevision: 8,
      request: {
        intent: {
          correctionPatch: {
            values: { Identity: { TMDBID: 0 } },
            expectedRevision: 3,
          },
          preparation: { Search: { Skip: true } },
        },
      },
    });
    expect(retryRequest).toMatchObject({
      expectedRevision: 10,
      request: {
        intent: {
          correctionPatch: {
            values: { Identity: { TMDBID: 0 } },
            expectedRevision: 3,
          },
          preparation: { Search: { Skip: true } },
        },
      },
    });
    unmount();
  });

  it("releases the active input with its exact slot revision", async () => {
    const sourcePath = "C:\\media\\Close.Release.2026.mkv";
    const active = workflowCurrentFromPreview(
      workflowCurrent("workflow-close", 3),
      preview(sourcePath, 1),
    );
    const release = vi.fn(async () => ({ state: "empty", revision: 8 }));
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          activeInput: {
            get: async () => ({
              state: "active",
              revision: 7,
              inputId: "input-close",
              sourceVersion: "source-close-v1",
              current: active,
            }),
            release,
          },
          workflow: workflowPorts({ current: async () => active }),
        }),
      ),
    });

    await waitFor(() => expect(result.current.identity.view.sourcePath).toBe(sourcePath));
    await act(async () => expect(await result.current.input.close()).toBe(true));
    expect(release).toHaveBeenCalledWith({ expectedRevision: 7 }, expect.any(AbortSignal));
    expect(result.current.input.view.activeInput).toEqual({
      state: "empty",
      revision: 8,
      inputID: "",
      sourceVersion: "",
      recoveryWorkflowIDs: [],
    });
    expect(result.current.workflow.view.current).toBeNull();
    unmount();
  });

  it.each([true, false])(
    "restores automatic and explicit corrections with stored corrections=%s",
    async (stored) => {
      const workflowID = "workflow-null-corrections";
      window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
      const identity = { TMDBID: null, IMDBID: 1234567, TVDBID: 0, TVmazeID: null, MALID: null };
      const releaseName = { Category: null, Tag: "", NoYear: false, ManualYear: 0 };
      const metadata = {
        Title: null,
        OriginalTitle: "",
        Commentary: false,
        Genres: [],
        AudioLanguages: null,
      };
      const initial = workflowCurrentFromPreview(
        workflowCurrent(workflowID, 7),
        preview("C:\\media\\Example.mkv", 1),
      );
      const current: ReleaseWorkflowCurrent = {
        ...initial,
        factInstructions: {
          ...initial.factInstructions!,
          instructions: {
            ...initial.factInstructions!.instructions,
            Identity: identity,
            ReleaseName: releaseName,
            Metadata: metadata,
          },
        },
        ...(stored
          ? {
              corrections: {
                revision: 1,
                corrections: { version: 1, identity, releaseName, metadata },
              },
            }
          : {}),
      };
      const { result, unmount } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(
          portsFor({
            resumeWorkflowID: workflowID,
            workflow: workflowPorts({ current: async () => current }),
          }),
        ),
      });
      await waitFor(() =>
        expect(result.current.input.view.intent.identity).toEqual({ IMDBID: 1234567, TVDBID: 0 }),
      );
      expect(result.current.input.view.intent.releaseName).toEqual({
        Tag: "",
        NoYear: false,
        ManualYear: 0,
      });
      expect(result.current.input.view.intent.metadata).toEqual({
        OriginalTitle: "",
        Commentary: false,
        Genres: [],
      });
      expect(identity.TMDBID).toBeNull();
      expect(metadata.AudioLanguages).toBeNull();
      unmount();
      window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
    },
  );

  it("sends correction and tracker-answer patches separately while preparing effective facts", async () => {
    const workflowID = "workflow-input-edit";
    const sourcePath = "C:\\media\\Example.mkv";
    let selectedTrackers: readonly string[] = ["PTP"];
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const initialFacts = workflowCurrent(workflowID, 7).factInstructions!;
    let current = {
      ...workflowCurrentFromPreview(workflowCurrent(workflowID, 7), preview(sourcePath, 1)),
      selection: {
        id: "selection-input",
        workflowId: workflowID,
        revision: 3,
        catalog: { id: "catalog-input", revision: 1 },
        runtime: { id: "runtime-input", revision: 1 },
        trackerIds: ["STALE"],
        fingerprint: "3".repeat(64),
        createdAt: "2026-09-09T00:00:00Z",
      },
      factInstructions: {
        ...initialFacts,
        correctionRevision: 3,
        instructions: { ...initialFacts.instructions, Metadata: { Title: "Stored title" } },
      },
      corrections: {
        revision: 3,
        corrections: {
          version: 1,
          identity: {},
          releaseName: {},
          metadata: { Title: "Stored title" },
        },
      },
    } as unknown as ReleaseWorkflowCurrent;
    const inputReadiness = (value: string) =>
      ({
        id: `input-readiness-${current.workflow.revision}`,
        workflowId: workflowID,
        revision: current.workflow.revision,
        release: {
          SourcePath: sourcePath,
          Generation: current.release!.release.Generation,
        },
        factInstructions: current.workflow.factInstructions!,
        correctionRevision: current.corrections!.revision,
        selectedTrackerIds: selectedTrackers,
        requirementsFingerprint: "4".repeat(64),
        fields: [],
        schemas: [
          {
            Tracker: "PTP",
            Fields: [
              {
                Key: "no_english_subtitles",
                Label: "No English subtitles",
                Kind: "select",
                Options: ["auto", "yes", "no"],
                Value: value,
                Placeholder: "",
                Help: "",
                Required: true,
              },
            ],
          },
        ],
        status: "completed",
        createdAt: "2026-09-09T00:00:00Z",
      }) as NonNullable<ReleaseWorkflowCurrent["inputReadiness"]>;
    current = { ...current, inputReadiness: inputReadiness("auto") };

    const continueWorkflow = vi.fn(
      async (request: ContinueReleaseWorkflowRequest): Promise<ReleaseWorkflowCurrent> => {
        if (request.intent.correctionPatch) {
          const factRevision = current.factInstructions!.revision + 1;
          current = {
            ...current,
            workflow: {
              ...current.workflow,
              revision: current.workflow.revision + 1,
              factInstructions: { id: `${workflowID}-facts`, revision: factRevision },
              inputReadiness: undefined,
            },
            factInstructions: {
              ...current.factInstructions!,
              revision: factRevision,
              correctionRevision: 4,
              instructions: {
                ...current.factInstructions!.instructions,
                Metadata: { Title: "Edited title" },
              },
            },
            corrections: {
              revision: 4,
              corrections: {
                version: 1,
                identity: {},
                releaseName: {},
                metadata: { Title: "Edited title" },
              },
            },
            inputReadiness: undefined,
          } as ReleaseWorkflowCurrent;
          return current;
        }
        if (request.intent.preparation) {
          selectedTrackers = request.intent.trackerIds ?? selectedTrackers;
          current = workflowCurrentFromPreview(
            {
              ...current,
              workflow: { ...current.workflow, revision: current.workflow.revision + 1 },
            },
            preview(sourcePath, 2),
          );
          current = {
            ...current,
            factInstructions: {
              ...current.factInstructions!,
              instructions: request.intent.preparation.Instructions,
            },
          };
          current = { ...current, inputReadiness: inputReadiness("auto") };
          return current;
        }
        if (request.intent.trackerInputAnswers) {
          expect(
            Object.keys(request.intent.trackerInputAnswers).every((tracker) =>
              selectedTrackers.includes(tracker),
            ),
          ).toBe(true);
          const value =
            request.intent.trackerInputAnswers.PTP?.no_english_subtitles === "yes" ? "yes" : "auto";
          if (current.inputReadiness?.schemas?.[0]?.Fields[0]?.Value === value) return current;
          current = {
            ...current,
            workflow: { ...current.workflow, revision: current.workflow.revision + 1 },
          };
          current = { ...current, inputReadiness: inputReadiness(value) };
          return current;
        }
        if (!current.inputReadiness) {
          current = {
            ...current,
            workflow: { ...current.workflow, revision: current.workflow.revision + 1 },
          };
          current = { ...current, inputReadiness: inputReadiness("auto") };
          return current;
        }
        return current;
      },
    );
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({
            current: async () => current,
            continue: continueWorkflow,
          }),
        }),
      ),
    });
    await waitFor(() => expect(result.current.input.view.status).toBe("ready"));
    expect(result.current.input.view.selectedTrackers).toEqual(["PTP"]);

    act(() => result.current.input.changeMetadata({ Title: "Edited title" }));
    expect(result.current.input.view.valueFields).toEqual([{ field: "metadata.title" }]);
    act(() => result.current.input.changeSourceLookupURL("https://example.invalid/source"));
    act(() => result.current.input.changeTrackerSourceID("PTP", "123"));
    act(() =>
      result.current.input.changePreparationPolicy({
        keepFolder: false,
        keepImages: true,
        onlyID: false,
      }),
    );
    act(() => result.current.input.changeClientSearch({ skip: false, client: "Search name" }));
    act(() => result.current.input.changeTrackerInputAnswer("PTP", "no_english_subtitles", "yes"));
    await act(async () => {
      expect(await result.current.input.prepare()).toBe(true);
    });

    const requests = continueWorkflow.mock.calls.map(([request]) => request);
    const correctionIndex = requests.findIndex((request) =>
      Boolean(request.intent.correctionPatch),
    );
    expect(correctionIndex).toBeGreaterThanOrEqual(0);
    expect(requests[correctionIndex].intent).toMatchObject({
      correctionPatch: {
        values: {
          Identity: {},
          ReleaseName: {},
          Metadata: { Title: "Edited title" },
        },
        resetFields: [],
        confirmFields: [],
        expectedRevision: 3,
      },
      preparation: {
        Instructions: { Identity: {}, ReleaseName: {}, Metadata: {} },
      },
      trackerIds: ["PTP"],
    });
    expect(requests[correctionIndex].intent.factInstructions).toBeUndefined();
    expect(requests[correctionIndex + 1].intent.correctionPatch).toBeUndefined();
    expect(requests[correctionIndex + 1].intent.preparation?.Instructions.Metadata).toEqual({
      Title: "Edited title",
    });
    expect(requests[correctionIndex + 1].intent.preparation).toMatchObject({
      Instructions: {
        SourceLookup: "https://example.invalid/source",
        TrackerIDs: { PTP: "123" },
      },
      Policy: { KeepImages: true },
      Search: { Skip: false, Client: "Search name" },
    });
    const answerRequest = requests.find((request) => request.intent.trackerInputAnswers);
    expect(answerRequest?.intent).toMatchObject({
      trackerInputAnswers: { PTP: { no_english_subtitles: "yes" } },
    });
    expect(answerRequest?.intent.correctionPatch).toBeUndefined();
    expect(answerRequest?.intent.preparation).toBeUndefined();
    expect(
      requests.every(
        (request) =>
          !request.intent.correctionPatch || request.intent.trackerInputAnswers === undefined,
      ),
    ).toBe(true);
    expect(result.current.input.view.intent.metadata.Title).toBe("Edited title");
    expect(result.current.input.view.valueFields).toEqual([]);
    expect(result.current.input.view.correctionDirty).toBe(false);
    expect(result.current.input.view.intent).toMatchObject({
      sourceLookupURL: "https://example.invalid/source",
      trackerSourceIDs: { PTP: "123" },
      policy: { keepImages: true },
      search: { skip: false, client: "Search name" },
    });
    expect(result.current.input.view.readiness?.schemas?.[0]?.Fields[0]?.Value).toBe("yes");

    const acceptedCallCount = continueWorkflow.mock.calls.length;
    act(() => result.current.input.changeTrackerSourceID("PTP", ""));
    expect(result.current.input.view.intent.trackerSourceIDs.PTP).toBe("");
    act(() => result.current.input.changeTrackerInputAnswer("PTP", "no_english_subtitles", null));
    expect(result.current.input.view.trackerInputAnswers).toEqual({
      PTP: { no_english_subtitles: null },
    });
    await act(async () => {
      expect(await result.current.input.prepare()).toBe(true);
    });
    const resetAnswerRequest = continueWorkflow.mock.calls
      .slice(acceptedCallCount)
      .map(([request]) => request)
      .find((request) => request.intent.trackerInputAnswers);
    const clearedSourceRequest = continueWorkflow.mock.calls
      .slice(acceptedCallCount)
      .map(([request]) => request)
      .find((request) => request.intent.preparation);
    expect(clearedSourceRequest?.intent.preparation?.Instructions.TrackerIDs).toEqual({});
    expect(resetAnswerRequest?.intent.trackerInputAnswers).toEqual({
      PTP: { no_english_subtitles: null },
    });
    expect(result.current.input.view.readiness?.schemas?.[0]?.Fields[0]?.Value).toBe("auto");

    act(() => result.current.input.changeTrackerInputAnswer("PTP", "no_english_subtitles", "yes"));
    act(() => result.current.duplicates.chooseTrackers(["AITHER"]));
    const deselectedCallCount = continueWorkflow.mock.calls.length;
    await act(async () => {
      expect(await result.current.input.prepare()).toBe(true);
    });
    expect(
      continueWorkflow.mock.calls
        .slice(deselectedCallCount)
        .some(([request]) => request.intent.trackerInputAnswers),
    ).toBe(false);
    expect(result.current.input.view.trackerInputAnswers).toEqual({
      PTP: { no_english_subtitles: "yes" },
    });

    act(() => result.current.duplicates.chooseTrackers(["PTP"]));
    const reselectedCallCount = continueWorkflow.mock.calls.length;
    await act(async () => {
      expect(await result.current.input.prepare()).toBe(true);
    });
    expect(
      continueWorkflow.mock.calls
        .slice(reselectedCallCount)
        .find(([request]) => request.intent.trackerInputAnswers)?.[0].intent.trackerInputAnswers,
    ).toEqual({ PTP: { no_english_subtitles: "yes" } });
    expect(result.current.input.view.trackerInputAnswers).toEqual({});

    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it.each(["completed", "partial", "failed", "skipped"] as const)(
    "ends one explicit dry-run action on a new %s report, not its stale predecessor",
    async (status) => {
      const workflowID = `workflow-one-dry-run-${status}`;
      const base = workflowCurrent(workflowID, 7);
      const report = (revision: number): NonNullable<ReleaseWorkflowCurrent["dryRun"]> => ({
        id: `attempt-${revision}`,
        workflowId: workflowID,
        revision,
        projectionSet: { id: "projections", revision: 1 },
        dupes: { id: "dupes", revision: 1 },
        media: { id: "media", revision: 1 },
        descriptions: { id: "descriptions", revision: 1 },
        inputFingerprint: "2".repeat(64),
        noSeed: true,
        trackerIds: ["AITHER"],
        reports: [
          {
            trackerId: "AITHER",
            displayName: "AITHER",
            uploadReleaseName: "Example.Release.2026-GRP",
            semanticFingerprint: "3".repeat(64),
            clientInjection: { status: "skipped" },
            status,
          },
        ],
        succeededCount: status === "completed" ? 1 : 0,
        failedCount: status === "failed" || status === "partial" ? 1 : 0,
        skippedCount: status === "skipped" ? 1 : 0,
        status,
        createdAt: "2026-10-28T00:00:00Z",
      });
      let current: ReleaseWorkflowCurrent = { ...base, dryRun: report(6) };
      let builds = 0;
      const continueWorkflow = vi.fn(async (request: ContinueReleaseWorkflowRequest) => {
        expect(request.goal).toBe("dry_run");
        expect(request.intent.noSeed).toBe(true);
        const revision = current.workflow.revision + 1;
        // First advance a prerequisite without replacing the retained old assessment.
        if (revision > 8) builds++;
        current = {
          ...current,
          workflow: { ...current.workflow, revision },
          dryRun: revision === 8 ? current.dryRun : report(revision),
        };
        return current;
      });
      const { result, unmount } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(
          portsFor({
            resumeWorkflowID: workflowID,
            workflow: workflowPorts({ current: async () => current, continue: continueWorkflow }),
          }),
        ),
      });
      await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
      act(() => result.current.upload.changeOptions({ noSeed: true }));
      await act(async () => {
        expect(await result.current.upload.runDryRun()).toBe(true);
      });
      expect(builds).toBe(1);
      expect(continueWorkflow).toHaveBeenCalledTimes(2);
      expect(result.current.upload.view.workflowBusy).toBe(false);
      expect(result.current.upload.view.dryRunResult).toEqual(report(9));
      await act(async () => {
        expect(await result.current.upload.runDryRun()).toBe(true);
      });
      expect(builds).toBe(2);
      expect(continueWorkflow).toHaveBeenCalledTimes(3);
      expect(result.current.upload.view.dryRunResult).toEqual(report(10));
      unmount();
    },
  );

  it("passes the current skip-client option to an explicit workflow dry run", async () => {
    const workflowID = "workflow-dry-run-options";
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const retained = workflowCurrent(workflowID, 7);
    const dryRunUploads = vi.fn(async (current: ReleaseWorkflowCurrent) => current);
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
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

  it("treats an all-uploaded workflow as a successful terminal no-op", async () => {
    const workflowID = "workflow-all-uploaded";
    const base = workflowCurrent(workflowID, 7);
    const retained: ReleaseWorkflowCurrent = {
      ...base,
      workflow: {
        ...base.workflow,
        status: "completed",
        submissionExclusions: [
          {
            trackerId: "AITHER",
            reason: "already_uploaded",
            confirmedAt: "2026-09-18T10:00:00Z",
          },
        ],
      },
    };
    const continueWorkflow = vi.fn(async () => retained);
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({
            current: async () => retained,
            continue: continueWorkflow,
          }),
        }),
      ),
    });

    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
    act(() => result.current.upload.chooseTrackers(["AITHER"]));
    expect(result.current.upload.view.uploadStatus).toBe("ready");
    expect(result.current.upload.view.submissionExclusions).toEqual(
      retained.workflow.submissionExclusions,
    );
    await act(async () => {
      expect(await result.current.upload.runDryRun()).toBe(false);
      expect(await result.current.upload.start()).toBe(false);
    });
    expect(continueWorkflow).not.toHaveBeenCalled();
    unmount();
  });

  it.each([false, true])(
    "confirms a retained rule authorization with liveTest=%s",
    async (liveTest) => {
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
            resumeWorkflowID: workflowID,
            workflow: workflowPorts({
              current: async () => retained,
              continue: continueWorkflow,
            }),
          }),
          { testRuntime: liveTest ? liveTestRuntime : undefined },
        ),
      });

      await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
      await act(async () => {
        expect(await result.current.workflow.confirmAction(action)).toBe(true);
      });
      expect(continueWorkflow).toHaveBeenCalledWith(
        expect.objectContaining({
          goal: liveTest ? "dry_run" : "uploaded",
          answers: [{ actionId: action.id, workflowRevision: 7, confirmed: true }],
        }),
        expect.any(AbortSignal),
      );

      unmount();
      window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
    },
  );

  it("acknowledges, revokes, and reacknowledges tracker warnings from the dupe facet", async () => {
    const workflowID = "workflow-dupe-rule-override";
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const action = {
      createdAt: "2026-08-15T00:00:00Z",
      id: "action-authorize-alpha",
      kind: "authorize_rules" as const,
      prompt: "ALPHA rule warning. Upload to this tracker anyway?",
      status: "pending" as const,
      trackerId: "ALPHA",
      workflowRevision: 7,
    };
    const base = workflowCurrent(workflowID, 7);
    const retained: ReleaseWorkflowCurrent = {
      ...base,
      workflow: { ...base.workflow, status: "blocked", requiredActions: [action] },
      continuation: { ...base.continuation, requiredActions: [action] },
      projections: {
        projections: [
          {
            trackerId: "ALPHA",
            displayName: "ALPHA",
            readiness: "blocked",
            artifacts: {
              screenshotCount: 0,
              dvdMenuCount: 0,
              imageHosting: false,
              description: false,
            },
            requiredActions: [action],
          },
        ],
      } as unknown as NonNullable<ReleaseWorkflowCurrent["projections"]>,
    };
    let current = retained;
    const continueWorkflow = vi.fn(async (request: ContinueReleaseWorkflowRequest) => {
      const acknowledged = request.answers?.[0]?.confirmed === true;
      if (
        Boolean(current.projections?.projections[0]?.ruleAuthorizationFingerprint) === acknowledged
      ) {
        return current;
      }
      const revision = current.workflow.revision + 1;
      const updatedAction = {
        ...action,
        id: `action-authorize-alpha-${revision}`,
        status: acknowledged ? ("resolved" as const) : ("pending" as const),
        workflowRevision: revision,
      };
      current = {
        ...current,
        workflow: {
          ...current.workflow,
          revision,
          status: acknowledged ? "active" : "blocked",
          requiredActions: acknowledged ? [] : [updatedAction],
        },
        continuation: {
          ...current.continuation,
          requiredActions: acknowledged ? [] : [updatedAction],
        },
        projections: {
          ...current.projections!,
          projections: current.projections!.projections.map((projection) => ({
            ...projection,
            waivableRuleFingerprint: "current-warnings",
            ruleAuthorizationFingerprint: acknowledged ? "current-warnings" : "",
            readiness: acknowledged ? ("ready" as const) : ("blocked" as const),
            requiredActions: [updatedAction],
          })),
        },
      };
      return current;
    });
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({
            current: async () => retained,
            continue: continueWorkflow,
          }),
        }),
      ),
    });

    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
    await act(async () => {
      expect(await result.current.duplicates.acknowledgeRules("alpha", true)).toBe(true);
    });
    expect(continueWorkflow).toHaveBeenCalledWith(
      expect.objectContaining({
        goal: "duplicates_decided",
        answers: [{ actionId: action.id, workflowRevision: 7, confirmed: true }],
      }),
      expect.any(AbortSignal),
    );

    await act(async () => {
      expect(await result.current.duplicates.acknowledgeRules("alpha", false)).toBe(true);
    });
    expect(continueWorkflow).toHaveBeenLastCalledWith(
      expect.objectContaining({
        goal: "duplicates_decided",
        answers: [{ actionId: "action-authorize-alpha-8", workflowRevision: 8, confirmed: false }],
      }),
      expect.any(AbortSignal),
    );
    await act(async () => {
      expect(await result.current.duplicates.acknowledgeRules("alpha", true)).toBe(true);
    });
    expect(continueWorkflow).toHaveBeenLastCalledWith(
      expect.objectContaining({
        goal: "duplicates_decided",
        answers: [{ actionId: "action-authorize-alpha-9", workflowRevision: 9, confirmed: true }],
      }),
      expect.any(AbortSignal),
    );

    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("ignores a tracker rule action retained only in a projection", async () => {
    const workflowID = "workflow-stale-dupe-rule-override";
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const action = {
      createdAt: "2026-08-15T00:00:00Z",
      id: "stale-authorize-alpha",
      kind: "authorize_rules" as const,
      prompt: "Upload to ALPHA anyway?",
      status: "pending" as const,
      trackerId: "ALPHA",
      workflowRevision: 7,
    };
    const base = workflowCurrent(workflowID, 7);
    const retained: ReleaseWorkflowCurrent = {
      ...base,
      projections: {
        projections: [
          {
            trackerId: "ALPHA",
            artifacts: {
              screenshotCount: 0,
              dvdMenuCount: 0,
              imageHosting: false,
              description: false,
            },
            requiredActions: [action],
          },
        ],
      } as unknown as NonNullable<ReleaseWorkflowCurrent["projections"]>,
    };
    const continueWorkflow = vi.fn(async () => retained);
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({ current: async () => retained, continue: continueWorkflow }),
        }),
      ),
    });

    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
    await act(async () => {
      expect(await result.current.duplicates.acknowledgeRules("alpha", true)).toBe(false);
    });
    expect(continueWorkflow).not.toHaveBeenCalled();

    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it.each([
    { mode: "live upload", explicit: false, noSeed: false },
    { mode: "explicit dry run", explicit: true, noSeed: false },
    { mode: "skipped injection dry run", explicit: true, noSeed: true },
  ])(
    "resolves a tracker preparation action for $mode after reload",
    async ({ explicit, noSeed }) => {
      const workflowID = "workflow-resolve-tracker-preparation";
      window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
      const action = {
        createdAt: "2026-07-20T00:00:00Z",
        id: "action-btn-autofill",
        kind: "resolve_tracker_preparation" as const,
        prompt: "Continue with BTN's autofill result?",
        status: "pending" as const,
        workflowRevision: 7,
      };
      const base = workflowCurrent(workflowID, 7);
      const retained: ReleaseWorkflowCurrent = {
        ...base,
        workflow: { ...base.workflow, status: "blocked", requiredActions: [action] },
        continuation: { ...base.continuation, requiredActions: [action] },
        dryRun: explicit
          ? {
              id: "dry-run-preparation-action",
              workflowId: workflowID,
              revision: 7,
              createdAt: action.createdAt,
              projectionSet: { id: "projections", revision: 7 },
              dupes: { id: "dupes", revision: 7 },
              media: { id: "media", revision: 7 },
              descriptions: { id: "descriptions", revision: 7 },
              inputFingerprint: "a".repeat(64),
              explicitDryRun: true,
              clientInjectionRequested: !noSeed,
              noSeed,
              trackerIds: ["BTN"],
              reports: [
                {
                  trackerId: "BTN",
                  displayName: "BTN",
                  uploadReleaseName: "Example.Release.2026.S01E01-GRP",
                  semanticFingerprint: "b".repeat(64),
                  status: "blocked",
                  clientInjection: { status: noSeed ? "skipped" : "pending" },
                },
              ],
              succeededCount: 0,
              failedCount: 0,
              skippedCount: 0,
              status: "blocked",
            }
          : undefined,
      };
      const continueWorkflow = vi.fn(async () => retained);
      const { result, unmount } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(
          portsFor({
            resumeWorkflowID: workflowID,
            workflow: workflowPorts({
              current: async () => retained,
              continue: continueWorkflow,
            }),
          }),
        ),
      });

      await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
      await act(async () => {
        expect(await result.current.workflow.confirmAction(action, false)).toBe(true);
      });
      expect(continueWorkflow).toHaveBeenCalledWith(
        expect.objectContaining({
          goal: explicit ? "dry_run" : "uploaded",
          answers: [{ actionId: action.id, workflowRevision: 7, confirmed: false }],
        }),
        expect.any(AbortSignal),
      );

      unmount();
      window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
    },
  );

  it("submits a release-wide modified-source refusal once", async () => {
    const workflowID = "workflow-modified-release";
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const action = {
      createdAt: "2026-07-20T00:00:00Z",
      id: "action-modified-release",
      kind: "authorize_rules" as const,
      releaseRule: "modified_release",
      prompt: "Verify source provenance for all selected trackers?",
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
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({
            current: async () => retained,
            continue: continueWorkflow,
          }),
        }),
      ),
    });

    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
    await act(async () => {
      expect(await result.current.workflow.confirmAction(action, false)).toBe(true);
    });
    expect(continueWorkflow).toHaveBeenCalledTimes(1);
    expect(continueWorkflow).toHaveBeenCalledWith(
      expect.objectContaining({
        goal: "uploaded",
        answers: [{ actionId: action.id, workflowRevision: 7, confirmed: false }],
      }),
      expect.any(AbortSignal),
    );

    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("executes a direct upload through one workflow command", async () => {
    const workflowID = "workflow-skipped-upload";
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const base = workflowCurrent(workflowID, 7);
    const retained: ReleaseWorkflowCurrent = {
      ...base,
      continuation: {
        ...base.continuation,
        trackerOutcomes: [
          {
            trackerId: "AITHER",
            uploadEligibility: "eligible",
          },
        ] as unknown as NonNullable<WorkflowContinuation["trackerOutcomes"]>,
      },
    };
    const executeUploads = vi.fn(async (current: ReleaseWorkflowCurrent) => current);
    const workflow = workflowPorts({ current: async () => retained, executeUploads });
    const continueWorkflow = vi.spyOn(workflow, "continue");
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow,
        }),
      ),
    });

    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
    act(() => result.current.upload.chooseTrackers(["AITHER"]));
    await act(async () => {
      expect(await result.current.upload.start()).toBe(true);
    });

    const goals = continueWorkflow.mock.calls.map(([request]) => request.goal);
    expect(goals).toContain("upload_reviewed");
    expect(goals).not.toContain("dry_run");
    expect(goals[goals.length - 1]).toBe("uploaded");
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

  it("retains an all-failed dry run for retry without executing an empty upload", async () => {
    const workflowID = "workflow-all-upload-preparation-failed";
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const base = workflowCurrent(workflowID, 7);
    const retained: ReleaseWorkflowCurrent = {
      ...base,
      continuation: {
        ...base.continuation,
        trackerOutcomes: [
          {
            trackerId: "AITHER",
            uploadEligibility: "eligible",
          },
        ] as unknown as NonNullable<WorkflowContinuation["trackerOutcomes"]>,
      },
    };
    const failedDryRun = {
      ...retained,
      workflow: {
        ...retained.workflow,
        revision: 8,
        dryRun: { id: "dry-run-failed", revision: 8 },
      },
      continuation: {
        ...retained.continuation,
        trackerOutcomes: [
          {
            trackerId: "AITHER",
            uploadEligibility: "skipped",
            uploadSkipReason: "upload_preparation_failed",
          },
        ],
      },
      dryRun: {
        id: "dry-run-failed",
        workflowId: workflowID,
        revision: 8,
        reports: [],
        status: "failed",
      },
    } as unknown as ReleaseWorkflowCurrent;
    let assessments = 0;
    const continueWorkflow = vi.fn(async (_request: ContinueReleaseWorkflowRequest) => {
      assessments += 1;
      const revision = 7 + assessments;
      const id = `dry-run-failed-${assessments}`;
      return {
        ...failedDryRun,
        workflow: { ...failedDryRun.workflow, revision, dryRun: { id, revision } },
        dryRun: { ...failedDryRun.dryRun!, id, revision },
      };
    });
    const executeUploads = vi.fn(async (current: ReleaseWorkflowCurrent) => current);
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({
            current: async () => retained,
            continue: continueWorkflow,
            executeUploads,
          }),
        }),
      ),
    });

    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
    act(() => result.current.upload.chooseTrackers(["AITHER"]));
    await act(async () => {
      expect(await result.current.upload.start()).toBe(false);
    });
    expect(executeUploads).not.toHaveBeenCalled();
    expect(result.current.upload.view.dryRunResult?.id).toBe("dry-run-failed-1");
    expect(continueWorkflow).toHaveBeenCalledOnce();
    expect(continueWorkflow.mock.calls[0]?.[0]).toMatchObject({ goal: "upload_reviewed" });
    await act(async () => {
      expect(await result.current.upload.runDryRun()).toBe(true);
    });
    expect(continueWorkflow).toHaveBeenCalledTimes(2);
    expect(continueWorkflow.mock.calls[1]?.[0]).toMatchObject({ goal: "dry_run" });
    expect(result.current.upload.view.dryRunResult?.id).toBe("dry-run-failed-2");
    expect(executeUploads).not.toHaveBeenCalled();

    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("executes a fully skipped dry-run plan as a completed no-op", async () => {
    const workflowID = "workflow-all-upload-preparation-skipped";
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const base = workflowCurrent(workflowID, 8);
    const retained = {
      ...base,
      workflow: {
        ...base.workflow,
        dryRun: { id: "dry-run-skipped", revision: 8 },
      },
      continuation: {
        ...base.continuation,
        trackerOutcomes: [
          {
            trackerId: "AITHER",
            uploadEligibility: "skipped",
            uploadSkipReason: "upload_preparation_skipped",
          },
        ],
      },
      dryRun: {
        id: "dry-run-skipped",
        workflowId: workflowID,
        revision: 8,
        reports: [{ trackerId: "AITHER", status: "skipped" }],
        status: "skipped",
      },
    } as unknown as ReleaseWorkflowCurrent;
    const executeUploads = vi.fn(async (current: ReleaseWorkflowCurrent) => current);
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({
            current: async () => retained,
            executeUploads,
          }),
        }),
      ),
    });

    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
    act(() => result.current.upload.chooseTrackers(["AITHER"]));
    await act(async () => {
      expect(await result.current.upload.start()).toBe(true);
    });
    expect(executeUploads).toHaveBeenCalledOnce();

    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("resumes an accepted workflow operation without exceeding one poll per second", async () => {
    vi.useFakeTimers();
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", "workflow-active");
    try {
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
        wrapper: wrapperFor(
          portsFor({
            resumeWorkflowID: "workflow-active",
            workflow: workflowPorts({ current, operation: poll }),
          }),
        ),
      });

      await act(async () => vi.advanceTimersByTimeAsync(999));
      expect(poll).not.toHaveBeenCalled();
      await act(async () => vi.advanceTimersByTimeAsync(1));
      expect(poll).toHaveBeenCalledOnce();
      await act(async () => vi.advanceTimersByTimeAsync(1000));
      expect(poll).toHaveBeenCalledTimes(2);
      expect(result.current.workflow.view.current?.workflow.revision).toBe(3);
      expect(result.current.workflow.view.current?.operation?.status).toBe("completed");

      unmount();
    } finally {
      window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
      vi.useRealTimers();
    }
  });

  it.each([
    [
      "prepare",
      "preparation",
      "source_unavailable",
      "The source path is unavailable.",
      "edit_input",
      "queued",
    ],
    [
      "composite_upload",
      "upload_execute",
      "invalid_input",
      "Required input is missing or invalid: Release source. Edit the input and retry.",
      "edit_input",
      "queued",
    ],
    [
      "composite_upload",
      "upload_execute",
      "invalid_input",
      "Required input is missing or invalid: Release source. Edit the input and retry.",
      "edit_input",
      "failed",
    ],
    [
      "composite_upload",
      "upload_execute",
      "missing_prerequisite",
      "Upload stopped at capture-media without a viable next step.",
      "review_again",
      "failed",
    ],
  ])(
    "retains %s %s %s failure and recovery after polling or reload",
    async (command, kind, code, message, recovery, initialStatus) => {
      window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", "workflow-failed");
      const startedAt = "2026-07-20T00:00:00Z";
      const queued = {
        id: "operation-failed",
        workflowId: "workflow-failed",
        revision: 1,
        sequence: 1,
        command,
        operation: kind,
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
              Code: code,
              Operation: kind,
              Message: message,
              Recovery: recovery,
            },
          },
        ],
        completedAt: startedAt,
      } as const;
      const prepared = workflowCurrentFromPreview(
        workflowCurrent("workflow-failed", 3),
        preview("C:\\media\\Example.Release.2026.mkv", 1),
      );
      const terminalCurrent = {
        ...prepared,
        workflow: { ...prepared.workflow, status: "failed", failures: failed.failures },
        operation: failed,
      };
      const current = vi.fn().mockResolvedValue(terminalCurrent);
      if (initialStatus === "queued") {
        current.mockResolvedValueOnce({ ...terminalCurrent, operation: queued });
      }
      const operation = vi.fn().mockResolvedValue(failed);
      const { result, unmount } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(
          portsFor({
            resumeWorkflowID: "workflow-failed",
            workflow: workflowPorts({ current, operation }),
          }),
        ),
      });

      await waitFor(() => expect(result.current.workflow.view.status).toBe("error"), {
        timeout: 3000,
      });
      expect(result.current.workflow.view.error).toBe(
        `${message} Recovery: ${recovery.replaceAll("_", " ")}.`,
      );
      expect(result.current.workflow.view.failure).toEqual(failed.failures[0].failure);
      expect(result.current.workflow.view.current?.operation?.status).toBe("failed");
      expect(result.current.workflow.view.current?.operation?.result).toBeUndefined();
      expect(operation).toHaveBeenCalledTimes(initialStatus === "queued" ? 1 : 0);
      expect(current).toHaveBeenCalledTimes(initialStatus === "queued" ? 2 : 1);

      if (command === "composite_upload") {
        await act(() => result.current.workflow.reload());
        expect(result.current.workflow.view.status).toBe("error");
        expect(result.current.workflow.view.failure).toEqual(failed.failures[0].failure);
        expect(result.current.workflow.view.current?.operation?.status).toBe("failed");
        expect(result.current.upload.view.error).toBe(message);
        expect(result.current.input.view.status).not.toBe("running");
        expect(operation).toHaveBeenCalledTimes(initialStatus === "queued" ? 1 : 0);
        expect(current).toHaveBeenCalledTimes(initialStatus === "queued" ? 3 : 2);
        act(() => result.current.input.updateSourceDraft("C:\\media\\Corrected.Release.2026.mkv"));
        expect(result.current.input.view.sourceDraft).toBe("C:\\media\\Corrected.Release.2026.mkv");
      }

      unmount();
      window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
    },
  );

  it.each(["completed", "executed", "partial", "blocked", "failed"] as const)(
    "keeps an active workflow ready when reloading its retained %s composite operation",
    async (status) => {
      const current = workflowCurrentFromPreview(
        workflowCurrent("workflow-retained", 3),
        preview("C:\\media\\Example.Release.2026.mkv", 1),
      );
      const operation = vi.fn();
      const retained: WorkflowOperationStatus = {
        id: "operation-retained",
        workflowId: current.workflow.id,
        revision: 1,
        sequence: 2,
        command: "composite_upload",
        operation: "upload_execute",
        status,
        progress: 100,
        completed: 1,
        total: 1,
        startedAt: "2026-07-20T00:00:00Z",
        updatedAt: "2026-07-20T00:00:00Z",
        failures:
          status === "failed"
            ? [
                {
                  failure: {
                    Code: "invalid_input",
                    Operation: "upload_execute",
                    Message: "Previous input failure.",
                    Recovery: "edit_input",
                  },
                },
              ]
            : [],
      };
      const { result, unmount } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(
          portsFor({
            activeInput: {
              get: async () => ({
                state: "active",
                revision: 1,
                current: { ...current, operation: retained },
              }),
            },
            workflow: workflowPorts({ operation }),
          }),
        ),
      });
      await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
      expect(result.current.workflow.view.failure).toBeNull();
      expect(result.current.workflow.view.error).toBe("");
      expect(result.current.input.view.status).not.toBe("running");
      expect(result.current.workflow.view.current?.operation?.status).toBe(status);
      expect(operation).not.toHaveBeenCalled();
      unmount();
    },
  );

  it.each(["C:\\media\\Next.Release.2026.mkv", ""])(
    "preserves an unsubmitted source path %j until the active input changes",
    async (draft) => {
      const workflowID = "workflow-source-draft";
      const current = workflowCurrentFromPreview(
        workflowCurrent(workflowID, 3),
        preview("C:\\media\\Example.Release.2026.mkv", 1),
      );
      let snapshot: ActiveInputSnapshot = {
        state: "active",
        revision: 1,
        inputId: "input-one",
        sourceVersion: "source-one",
        current,
      };
      const { result, unmount } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(
          portsFor({ activeInput: { get: async () => structuredClone(snapshot) } }),
        ),
      });
      try {
        await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
        act(() => result.current.input.updateSourceDraft(draft));
        await act(() => result.current.workflow.reload());
        expect(result.current.input.view.sourceDraft).toBe(draft);
        expect(result.current.input.view.preparationDirty).toBe(false);
        const nextPath = "C:\\media\\Other.Release.2026.mkv";
        snapshot = {
          state: "active",
          revision: 2,
          inputId: "input-two",
          sourceVersion: "source-two",
          current: workflowCurrentFromPreview(
            workflowCurrent("workflow-two", 3),
            preview(nextPath, 1),
          ),
        };
        await act(() => result.current.workflow.reload());
        expect(result.current.input.view.sourceDraft).toBe(nextPath);
      } finally {
        unmount();
      }
    },
  );

  it("preserves description drafts when the active input refreshes unchanged trackers", async () => {
    const workflowID = "workflow-description-refresh";
    const current: ReleaseWorkflowCurrent = {
      ...workflowCurrentFromPreview(
        workflowCurrentWithDescriptions(workflowID, 7, "generated source"),
        preview("C:\\media\\Example.Release.2026.1080p-GRP.mkv", 1),
      ),
      selection: {
        id: "selection-1",
        workflowId: workflowID,
        revision: 1,
        catalog: { id: "catalog-1", revision: 1 },
        runtime: { id: "runtime-1", revision: 1 },
        trackerIds: ["AITHER"],
        fingerprint: "1".repeat(64),
        createdAt: "2026-07-21T00:00:00Z",
      },
    };
    let refresh: () => void = () => undefined;
    vi.useFakeTimers();
    const ports = portsFor({
      resumeWorkflowID: workflowID,
      workflow: workflowPorts({ current: async () => structuredClone(current) }),
      activeInput: {
        subscribe: (callback) => {
          refresh = callback;
          return () => undefined;
        },
      },
    });
    function DescriptionEditor() {
      const session = useReleaseSession();
      return (
        <DescriptionBuilderPage
          facet={session.descriptions}
          sourcePath={current.release!.release.Source.SourcePath}
        />
      );
    }
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: ({ children }) => (
        <ReleaseSessionProvider ports={ports}>
          {children}
          <DescriptionEditor />
        </ReleaseSessionProvider>
      ),
    });
    try {
      await act(async () => vi.advanceTimersByTimeAsync(0));
      expect(result.current.workflow.view.status).toBe("ready");
      fireEvent.click(screen.getByRole("button", { name: "Expand AITHER" }));
      const editor = screen.getByRole("textbox", { name: "Raw description for AITHER" });
      fireEvent.change(editor, { target: { value: "unsaved description" } });
      for (const reload of [
        () => result.current.workflow.reload(),
        refresh,
        () => window.dispatchEvent(new Event("focus")),
        () => window.dispatchEvent(new Event("online")),
        () => document.dispatchEvent(new Event("visibilitychange")),
        () => vi.advanceTimersByTimeAsync(15_000),
        () => vi.advanceTimersByTimeAsync(15_000),
      ]) {
        await act(async () => {
          await reload();
        });
        expect(editor).toHaveValue("unsaved description");
        expect(result.current.descriptions.view.dirtyGroups).toEqual(["unit3d"]);
      }
      fireEvent.change(editor, { target: { value: "" } });
      await act(async () => {
        await result.current.workflow.reload();
      });
      expect(editor).toHaveValue("");
    } finally {
      unmount();
      vi.useRealTimers();
    }
  });

  it("saves and resets authoritative descriptions through revisioned workflow commands", async () => {
    const workflowID = "workflow-descriptions";
    const withDescription = (revision: number, unit3dSource: string): ReleaseWorkflowCurrent =>
      workflowCurrentWithDescriptions(workflowID, revision, unit3dSource);
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
          resumeWorkflowID: workflowID,
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

  it("renders and saves an unedited description group from its generated source", async () => {
    const workflowID = "workflow-descriptions";
    const sourcePath = "C:\\media\\Example.Release.2026.1080p-GRP.mkv";
    const withPreparedDescription = (revision: number): ReleaseWorkflowCurrent => ({
      ...workflowCurrentFromPreview(
        workflowCurrentWithDescriptions(workflowID, revision, "generated source", undefined, {
          "https://images.example.invalid/full.png": "https://images.example.invalid/preview.png",
        }),
        preview(sourcePath, 1),
      ),
      projections: {
        status: "ready",
        projections: [
          {
            trackerId: "AITHER",
            displayName: "AITHER",
            artifacts: {
              screenshotCount: 0,
              dvdMenuCount: 0,
              imageHosting: false,
              description: true,
            },
          },
        ],
      } as unknown as NonNullable<ReleaseWorkflowCurrent["projections"]>,
    });
    const render = vi.fn(async (raw: string) => `<p>${raw}</p>`);
    const saveDescriptionOverride = vi.fn().mockResolvedValueOnce(withPreparedDescription(8));
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor({
        ...portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({
            current: async () => withPreparedDescription(7),
            saveDescriptionOverride,
          }),
        }),
        descriptions: { render },
      }),
    });

    await waitFor(() => expect(result.current.descriptions.view.artifact?.revision).toBe(7));
    await act(() => result.current.descriptions.render("unit3d"));

    expect(render).toHaveBeenCalledWith(
      "generated source",
      { "https://images.example.invalid/full.png": "https://images.example.invalid/preview.png" },
      expect.any(AbortSignal),
    );
    expect(result.current.descriptions.view.renderedByGroup.unit3d).toBe("<p>generated source</p>");

    await act(() => result.current.descriptions.save("unit3d"));

    expect(saveDescriptionOverride).toHaveBeenCalledWith(
      expect.anything(),
      "unit3d",
      "generated source",
      expect.any(String),
      expect.any(AbortSignal),
    );

    unmount();
    window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
  });

  it("preserves edited source through Render then Save group", async () => {
    const workflowID = "workflow-descriptions";
    const sourcePath = "C:\\media\\Example.Release.2026.1080p-GRP.mkv";
    const withPreparedDescription = (
      revision: number,
      source = "generated source",
    ): ReleaseWorkflowCurrent => ({
      ...workflowCurrentFromPreview(
        workflowCurrentWithDescriptions(workflowID, revision, source, undefined, {
          "https://images.example.invalid/full.png": "https://images.example.invalid/preview.png",
        }),
        preview(sourcePath, 1),
      ),
      projections: {
        status: "ready",
        projections: [
          {
            trackerId: "AITHER",
            displayName: "AITHER",
            artifacts: {
              screenshotCount: 0,
              dvdMenuCount: 0,
              imageHosting: false,
              description: true,
            },
          },
        ],
      } as unknown as NonNullable<ReleaseWorkflowCurrent["projections"]>,
    });
    const render = vi.fn(async (raw: string) => `<p>${raw}</p>`);
    const requests: unknown[] = [];
    setAppRequestHandlerForTests(async (method, body) => {
      expect(method).toBe("SaveReleaseWorkflowDescriptionOverride");
      requests.push(body);
      const command = body as SaveReleaseWorkflowDescriptionOverrideRequest;
      return withPreparedDescription(8, command.override.source);
    });
    const saveDescriptionOverride =
      productionReleaseSessionPorts().workflow.saveDescriptionOverride;
    window.sessionStorage.setItem("upbrr.activeReleaseWorkflow", workflowID);
    const ports = {
      ...portsFor({
        resumeWorkflowID: workflowID,
        workflow: workflowPorts({
          current: async () => withPreparedDescription(7),
          saveDescriptionOverride,
        }),
      }),
      descriptions: { render },
    };
    function DescriptionEditor() {
      const session = useReleaseSession();
      return <DescriptionBuilderPage facet={session.descriptions} sourcePath={sourcePath} />;
    }
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: ({ children }) => (
        <ReleaseSessionProvider ports={ports}>
          {children}
          <DescriptionEditor />
        </ReleaseSessionProvider>
      ),
    });
    try {
      await waitFor(() => expect(result.current.descriptions.view.artifact?.revision).toBe(7));
      fireEvent.click(screen.getByRole("button", { name: "Expand AITHER" }));
      const editor = screen.getByRole("textbox", { name: "Raw description for AITHER" });
      fireEvent.change(editor, { target: { value: "edited source" } });
      await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: "Render AITHER" }));
      });

      expect(render).toHaveBeenCalledWith(
        "edited source",
        { "https://images.example.invalid/full.png": "https://images.example.invalid/preview.png" },
        expect.any(AbortSignal),
      );
      expect(result.current.descriptions.view.renderedByGroup.unit3d).toBe("<p>edited source</p>");

      await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: "Save group AITHER" }));
      });

      expect(requests).toEqual([
        expect.objectContaining({
          workflowId: workflowID,
          expectedRevision: 7,
          override: {
            descriptions: { id: withPreparedDescription(7).descriptions!.id, revision: 7 },
            groupKey: "unit3d",
            source: "edited source",
          },
        }),
      ]);
      expect(editor).toHaveValue("edited source");
      expect(result.current.descriptions.view.artifact?.descriptions[0]?.source).toBe(
        "edited source",
      );
      expect(result.current.descriptions.view.dirtyGroups).toEqual([]);
    } finally {
      unmount();
      window.sessionStorage.removeItem("upbrr.activeReleaseWorkflow");
      setAppRequestHandlerForTests(null);
    }
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

  it("reopens and re-verifies the same source for explicit refresh and reset", async () => {
    const sourcePath = "C:\\media\\Refresh.Release.2026.mkv";
    let slotRevision = 1;
    let workflowRevision = 2;
    let serverCurrent = workflowCurrentFromPreview(
      workflowCurrent("workflow-refresh", workflowRevision),
      preview(sourcePath, 1),
    );
    const continueWorkflow = vi.fn(async () => serverCurrent);
    const workflow = workflowPorts({
      current: async () => serverCurrent,
      continue: continueWorkflow,
    });
    const open = vi.fn(async (_request) => {
      slotRevision += 1;
      workflowRevision += 1;
      serverCurrent = workflowCurrentFromPreview(
        workflowCurrent("workflow-refresh", workflowRevision),
        preview(sourcePath, workflowRevision),
      );
      return {
        state: "active" as const,
        revision: slotRevision,
        inputId: "input-refresh",
        sourceVersion: "source-refresh-v1",
        current: serverCurrent,
      };
    });
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow,
          activeInput: {
            get: async () => ({
              state: "active",
              revision: slotRevision,
              inputId: "input-refresh",
              sourceVersion: "source-refresh-v1",
              current: serverCurrent,
            }),
            open,
          },
        }),
      ),
    });
    await waitFor(() => expect(result.current.identity.view.sourcePath).toBe(sourcePath));

    await act(() => result.current.input.prepare());
    expect(open).toHaveBeenCalledTimes(1);
    expect(open.mock.calls[0][0]).toEqual(
      expect.objectContaining({
        expectedRevision: 1,
        request: expect.objectContaining({
          idempotencyKey: expect.stringMatching(/^preparation-/),
          intent: expect.objectContaining({
            preparation: expect.objectContaining({
              SourcePath: sourcePath,
              ExternalFreshness: "",
              Force: false,
            }),
          }),
        }),
      }),
    );

    await act(() => result.current.input.reset());
    expect(open).toHaveBeenCalledTimes(2);
    expect(open.mock.calls[1][0]).toEqual(
      expect.objectContaining({
        expectedRevision: 2,
        request: expect.objectContaining({
          idempotencyKey: expect.stringMatching(/^preparation-/),
          intent: expect.objectContaining({
            preparation: expect.objectContaining({
              SourcePath: sourcePath,
              ExternalFreshness: "",
              Force: true,
            }),
          }),
        }),
      }),
    );
    unmount();
  });

  it.each(["none", "metadata", "clear source"])(
    "restores saved corrections after early input discovery (later edit: %s)",
    async (laterEdit) => {
      const editDuringPreparation = laterEdit === "metadata";
      const sourcePath = "C:\\media\\Saved.Release.2026.mkv";
      const workflowID = "workflow-reopened";
      const restored = workflowCurrentFromPreview(
        workflowCurrent(workflowID, 3),
        preview(sourcePath, 1),
      );
      const saved = {
        ...restored,
        factInstructions: {
          ...restored.factInstructions!,
          instructions: {
            ...restored.factInstructions!.instructions,
            Metadata: { Commentary: false },
          },
        },
      };
      const workflow = workflowPorts({
        create: async () => workflowCurrent(workflowID, 1),
        prepare: async () => saved,
      });
      const response = createDeferred<void>();
      let snapshot: ActiveInputSnapshot = { state: "empty", revision: 0 };
      let refresh: () => void = () => undefined;
      const get = vi.fn(async () => snapshot);
      const open = vi.fn(
        async (
          request: Parameters<ReleaseSessionPorts["activeInput"]["open"]>[0],
          signal: AbortSignal,
        ) => {
          snapshot = {
            state: "active",
            revision: 2,
            inputId: "input-reopened",
            sourceVersion: "source-one",
            current: await workflow.continue(request.request, signal),
          };
          await response.promise;
          return snapshot;
        },
      );
      const { result, unmount } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(
          portsFor({
            workflow,
            activeInput: {
              get,
              open,
              subscribe: (callback) => {
                refresh = callback;
                return () => undefined;
              },
            },
          }),
        ),
      });
      try {
        await waitFor(() => expect(get).toHaveBeenCalledOnce());
        act(() => result.current.input.updateSourceDraft(sourcePath));
        act(() => result.current.input.chooseTrackers(["AITHER"]));
        let preparation!: Promise<boolean>;
        act(() => {
          preparation = result.current.input.prepare();
        });
        await waitFor(() => expect(snapshot.current).toBeDefined());
        await act(async () => {
          refresh();
        });
        await waitFor(() =>
          expect(result.current.workflow.view.current?.workflow.id).toBe(workflowID),
        );
        if (editDuringPreparation)
          act(() => result.current.input.changeMetadata({ Commentary: true }));
        if (laterEdit === "clear source") act(() => result.current.input.updateSourceDraft(""));
        response.resolve();
        await act(() => preparation);
        expect(result.current.input.view.intent.metadata.Commentary).toBe(editDuringPreparation);
        expect(result.current.input.view.preparationDirty).toBe(editDuringPreparation);
        expect(result.current.input.view.sourceDraft).toBe(
          laterEdit === "clear source" ? "" : sourcePath,
        );
      } finally {
        response.resolve();
        unmount();
      }
    },
  );

  it("reopens a restored playlist selection and sends the HDR check only for that preparation", async () => {
    const sourcePath = "C:\\media\\Synthetic Disc";
    const initial = workflowCurrentFromPreview(
      workflowCurrent("workflow-restored-playlists", 2),
      preview(sourcePath, 1),
    );
    const stored: ReleaseWorkflowCurrent = {
      ...initial,
      release: {
        ...initial.release!,
        release: {
          ...initial.release!.release,
          Source: {
            ...initial.release!.release.Source,
            SelectedPlaylists: [
              {
                id: "disc-one:00001.mpls",
                discId: "disc-one",
                discName: "Disc 1",
                file: "00001.mpls",
                duration: 120,
                items: [],
                score: 1,
                edition: "",
              },
            ],
          },
        },
      },
    };
    const prepare = vi.fn(async (current: ReleaseWorkflowCurrent) => ({
      ...stored,
      workflow: { ...current.workflow, revision: current.workflow.revision + 1 },
    }));
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({ prepare }),
          activeInput: {
            get: async () => ({
              state: "active",
              revision: 1,
              inputId: "input-restored",
              sourceVersion: "source-restored",
              current: stored,
            }),
          },
        }),
      ),
    });
    await waitFor(() => expect(result.current.input.view.status).toBe("ready"));
    act(() => result.current.input.reviewPlaylists());
    expect(result.current.input.view.playlist.required).toBe(true);
    expect(result.current.input.view.playlist.selected).toEqual(["disc-one:00001.mpls"]);
    await act(() => result.current.input.confirmPlaylists(true));
    expect(prepare).toHaveBeenLastCalledWith(
      expect.anything(),
      expect.objectContaining({
        SourcePath: sourcePath,
        Controls: expect.objectContaining({ CaptureHDRMetadata: true }),
      }),
      expect.any(String),
      expect.any(AbortSignal),
    );
    await act(() => result.current.input.prepare());
    expect(prepare).toHaveBeenLastCalledWith(
      expect.anything(),
      expect.objectContaining({ Controls: expect.objectContaining({ CaptureHDRMetadata: false }) }),
      expect.any(String),
      expect.any(AbortSignal),
    );
    unmount();
  });

  it("resumes the backend playlist required action with the captured draft source", async () => {
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
                      value: "disc-one:00001.mpls",
                      label: "Disc 1 — 00001.mpls",
                      playlist: {
                        id: "disc-one:00001.mpls",
                        discId: "disc-one",
                        discName: "Disc 1",
                        file: "00001.mpls",
                        duration: 7200,
                        items: [{ file: "00001.m2ts", size: 4_000_000_000 }],
                        score: 91.25,
                        edition: "Example Edition",
                      },
                    },
                    {
                      value: "disc-two:00001.mpls",
                      label: "Disc 2 — 00001.mpls",
                      playlist: {
                        id: "disc-two:00001.mpls",
                        discId: "disc-two",
                        discName: "Disc 2",
                        file: "00001.mpls",
                        duration: 7000,
                        items: [{ file: "00001.m2ts", size: 3_000_000_000 }],
                        score: 90,
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
    const workflow = workflowPorts({
      create,
      prepare: prepareWorkflow,
      replaceFacts,
    });
    const openResponse = createDeferred<void>();
    let activeSnapshot: ActiveInputSnapshot | null = null;
    let emitInputChanged: () => void = () => undefined;
    const get = vi.fn(async (): Promise<ActiveInputSnapshot> => {
      return activeSnapshot || { state: "empty", revision: 0 };
    });
    const open = vi.fn(
      async (
        request: Parameters<ReleaseSessionPorts["activeInput"]["open"]>[0],
        signal: AbortSignal,
      ): Promise<ActiveInputSnapshot> => {
        const current = await workflow.continue(request.request, signal);
        const snapshot: ActiveInputSnapshot = {
          state: "active",
          revision: 1,
          inputId: "input-playlist-browser",
          sourceVersion: "source-playlist-browser-v1",
          current,
        };
        activeSnapshot = snapshot;
        await openResponse.promise;
        return snapshot;
      },
    );
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow,
          activeInput: {
            get,
            open,
            subscribe: (onChange) => {
              emitInputChanged = onChange;
              return () => undefined;
            },
          },
        }),
      ),
    });

    await waitFor(() => expect(get).toHaveBeenCalledOnce());
    act(() => result.current.input.updateSourceDraft(sourcePath));
    let preparation!: Promise<boolean>;
    act(() => {
      preparation = result.current.input.prepare();
    });
    await waitFor(() => expect(open).toHaveBeenCalledOnce());
    await waitFor(() => expect(activeSnapshot).not.toBeNull());

    act(() => emitInputChanged());
    await waitFor(() =>
      expect(result.current.workflow.view.current?.workflow.id).toBe("workflow-playlist-browser"),
    );
    expect(get).toHaveBeenCalledTimes(2);
    expect(result.current.input.view.selectedSource).toBe("");

    openResponse.resolve(undefined);
    await act(() => preparation);

    expect(result.current.input.view.status).toBe("awaiting_input");
    expect(result.current.input.view.playlist).toEqual(
      expect.objectContaining({
        required: true,
        selected: [],
        candidates: [
          {
            id: "disc-one:00001.mpls",
            discId: "disc-one",
            discName: "Disc 1",
            file: "00001.mpls",
            duration: 7200,
            items: [{ file: "00001.m2ts", size: 4_000_000_000 }],
            score: 91.25,
            edition: "Example Edition",
          },
          {
            id: "disc-two:00001.mpls",
            discId: "disc-two",
            discName: "Disc 2",
            file: "00001.mpls",
            duration: 7000,
            items: [{ file: "00001.m2ts", size: 3_000_000_000 }],
            score: 90,
            edition: "Example Edition",
          },
        ],
      }),
    );

    act(() => result.current.input.choosePlaylists(["disc-one:00001.mpls"], false));
    expect(await result.current.input.confirmPlaylists()).toBe(false);
    expect(prepareWorkflow).toHaveBeenCalledOnce();
    act(() =>
      result.current.input.choosePlaylists(["disc-one:00001.mpls", "disc-two:00001.mpls"], false),
    );
    await act(() => result.current.input.confirmPlaylists(true));

    expect(create).toHaveBeenCalledOnce();
    expect(replaceFacts).not.toHaveBeenCalled();
    expect(prepareWorkflow).toHaveBeenLastCalledWith(
      expect.anything(),
      expect.objectContaining({
        SourcePath: sourcePath,
        Controls: expect.objectContaining({ CaptureHDRMetadata: true }),
        Instructions: expect.objectContaining({
          Playlist: {
            Set: true,
            Selected: ["disc-one:00001.mpls", "disc-two:00001.mpls"],
            UseAll: false,
          },
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
    const advance = (current: ReleaseWorkflowCurrent) => {
      const next = workflowCurrent(current.workflow.id, ++revision);
      return {
        ...next,
        continuation: {
          ...next.continuation,
          trackerOutcomes: [
            {
              trackerId: "AITHER",
              uploadEligibility: "eligible",
            },
          ] as unknown as NonNullable<WorkflowContinuation["trackerOutcomes"]>,
        },
      };
    };
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

  it.each(["Category", "Type", "Source", "Resolution"] as const)(
    "keeps duplicate navigation blocked until missing saved %s is corrected",
    async (field) => {
      const complete = workflowCurrentFromPreview(
        workflowCurrent("required-facts", 4),
        preview("C:\\media\\Example.mkv", 1),
      );
      const release = complete.release!;
      let current: ReleaseWorkflowCurrent = {
        ...complete,
        release: {
          ...release,
          release: {
            ...release.release,
            Identity: {
              ...release.release.Identity,
              Category: field === "Category" ? "unknown" : "movie",
            },
            Media: {
              ...release.release.Media,
              Type: field === "Type" ? " " : "ENCODE",
              Source: field === "Source" ? "" : "BluRay",
            },
            Naming: {
              ...release.release.Naming,
              Resolution: field === "Resolution" ? "" : "1080p",
            },
          },
        },
      };
      const { result } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(
          portsFor({
            resumeWorkflowID: "required-facts",
            workflow: workflowPorts({
              current: async () => current,
              continue: async () => current,
            }),
          }),
        ),
      });
      await waitFor(() => expect(result.current.input.view.release).not.toBeNull());
      expect(result.current.navigation.open("duplicates")).toBe(false);
      expect(result.current.navigation.view.access.duplicates.reason).toMatch(
        /required facts.*Input/,
      );
      act(() =>
        result.current.input.changeReleaseName({
          [field]: { Category: "movie", Type: "ENCODE", Source: "BluRay", Resolution: "1080p" }[
            field
          ],
        }),
      );
      expect(result.current.navigation.open("duplicates")).toBe(false);
      await act(() => result.current.workflow.reload());
      expect(result.current.navigation.open("duplicates")).toBe(false);
      current = { ...complete, workflow: { ...complete.workflow, revision: 5 } };
      await act(() => result.current.workflow.reload());
      expect(result.current.navigation.open("duplicates")).toBe(true);
    },
  );

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
        Controls: {
          Interaction: "interactive",
          ConfirmBDMVRescan: false,
          CaptureHDRMetadata: false,
        },
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

  it("preserves compatible media facets across a same-input N+1 refresh", async () => {
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
    await act(() => result.current.screenshots.load());
    expect(result.current.screenshots.view.plan).not.toBeNull();
    const firstRevision = result.current.screenshots.view.revision;
    const firstPlan = result.current.screenshots.view.plan;

    await act(() => result.current.input.prepare());
    expect(result.current.identity.view.release?.Generation).toBe(2);
    expect(result.current.screenshots.view.revision).toBeGreaterThan(firstRevision);
    expect(result.current.screenshots.view.plan).toBeNull();
    expect(result.current.screenshots.view.staleReason).toBe("Screenshot plan changed.");
    expect(result.current.screenshots.view.selections).toEqual(firstPlan?.SuggestedSelections);
    await act(() => result.current.duplicates.run());
    await act(() => result.current.screenshots.load());
    expect(result.current.screenshots.view.plan).not.toBeNull();
    expect(result.current.screenshots.view.selections).toEqual(firstPlan?.SuggestedSelections);
  });

  it("waits for cancellation and suppresses a stale open response after source replacement", async () => {
    const first = createDeferred<ActiveInputSnapshot>();
    const canceledOpening = createDeferred<ActiveInputSnapshot>();
    let firstSignal: AbortSignal | undefined;
    const open = vi.fn(
      async (
        request: Parameters<ReleaseSessionPorts["activeInput"]["open"]>[0],
        signal: AbortSignal,
      ): Promise<ActiveInputSnapshot> => {
        const input = request.request.intent.preparation!;
        if (input.SourcePath.endsWith("First")) {
          firstSignal = signal;
          return first.promise;
        }
        return {
          state: "active",
          revision: 4,
          inputId: "input-second",
          sourceVersion: "source-second",
          current: workflowCurrentFromPreview(
            workflowCurrent("workflow-second", 2),
            preview(input.SourcePath, 1),
          ),
        };
      },
    );
    const ports = portsFor({
      activeInput: { open, cancelOpen: async () => canceledOpening.promise },
    });
    const cancelOpen = vi.spyOn(ports.activeInput, "cancelOpen");
    const { result } = renderHook(useReleaseSession, { wrapper: wrapperFor(ports) });

    act(() => result.current.input.selectSource("C:\\media\\First"));
    act(() => result.current.upload.chooseTrackers(["AITHER"]));
    let firstCommand!: Promise<boolean>;
    act(() => {
      firstCommand = result.current.input.prepare();
    });
    await waitFor(() => expect(open).toHaveBeenCalledTimes(1));

    act(() => result.current.input.selectSource("C:\\media\\Second"));
    expect(result.current.workflow.cancellation.pending).toBe(true);
    expect(firstSignal?.aborted).toBe(false);
    await act(async () => canceledOpening.resolve({ state: "empty", revision: 2 }));
    await waitFor(() => expect(firstSignal?.aborted).toBe(true));
    expect(cancelOpen).toHaveBeenCalledOnce();
    expect(result.current.input.view.status).toBe("cancelled");
    expect(result.current.input.view.sourceDraft).toBe("C:\\media\\Second");
    act(() => result.current.upload.chooseTrackers(["AITHER"]));
    await act(() => result.current.input.prepare());

    first.resolve({
      state: "active",
      revision: 2,
      inputId: "input-first",
      sourceVersion: "source-first",
      current: workflowCurrentFromPreview(
        workflowCurrent("workflow-first", 2),
        preview("C:\\media\\First", 1),
      ),
    });
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

  describe("early questionnaire projection", () => {
    afterEach(cleanup);
    it.each([false, true])(
      "guards HDR generation until guidance settles (fails=%s)",
      async (fails) => {
        const workflowID = "fresh-hdr-guidance";
        const initial = preparedQuestionnaireWorkflow(workflowID);
        const prepared: ReleaseWorkflowCurrent = {
          ...initial,
          inputReadiness: { ...initial.inputReadiness!, selectedTrackerIds: ["LST"] },
          release: {
            ...initial.release!,
            display: {
              ...initial.release!.display,
              hdrTargets: [
                {
                  id: "hdr-target",
                  label: "Video",
                  supported: true,
                  selectionPolicy: "unique_hevc",
                },
              ],
            },
          },
        };
        const active = workflowPorts();
        const projected = await active.project(
          prepared,
          ["LST"],
          {},
          "seed",
          new AbortController().signal,
        );
        const pending = createDeferred<ReleaseWorkflowCurrent>();
        const project = vi.fn(async () => pending.promise);
        const analyzeHDR = vi.fn(async () => projected);
        const preflight = vi.fn(active.preflight);
        const checkDuplicates = vi.fn(active.checkDuplicates);
        const workflow = workflowPorts({
          current: async () => prepared,
          project,
          analyzeHDR,
          preflight,
          checkDuplicates,
        });
        const PreparedHDRSurface = () => {
          const session = useReleaseSession();
          return session.hdrAnalysis.view.available ? <HDRSurface /> : null;
        };
        const { result } = renderHook(useReleaseSession, {
          wrapper: ({ children }) => (
            <ReleaseSessionProvider ports={portsFor({ resumeWorkflowID: workflowID, workflow })}>
              <PreparedHDRSurface />
              {children}
            </ReleaseSessionProvider>
          ),
        });
        await waitFor(() => expect(project).toHaveBeenCalledOnce());
        const generate = screen.getByRole("button", { name: "Generate" });
        fireEvent.click(generate);
        expect(analyzeHDR).not.toHaveBeenCalled();
        expect(generate).toBeDisabled();
        expect(result.current.hdrAnalysis.view.requirementsNeedRefresh).toBe(false);
        expect(screen.queryByRole("region", { name: "Required hybrid HDR evidence" })).toBeNull();
        await act(async () => {
          if (fails) {
            pending.reject(new Error("Tracker guidance unavailable."));
            return;
          }
          pending.resolve({
            ...projected,
            projections: {
              ...projected.projections!,
              projections: projected.projections!.projections.map((projection) => ({
                ...projection,
                questionnaire: [
                  {
                    key: "hybrid_evidence",
                    label: "Evidence",
                    required: false,
                    hdrGradeComparison: true,
                  },
                ],
              })),
            },
          });
        });
        if (fails) {
          expect(screen.getByRole("alert")).toHaveTextContent(
            "Tracker questions could not be loaded. Retry or prepare the selected source again.",
          );
        } else {
          expect(
            screen.getByRole("region", { name: "Required hybrid HDR evidence" }),
          ).toBeVisible();
        }
        expect(result.current.hdrAnalysis.view.requirementsNeedRefresh).toBe(false);
        expect(project).toHaveBeenCalledOnce();
        expect(generate).toBeEnabled();
        await act(async () => fireEvent.click(generate));
        expect(analyzeHDR).toHaveBeenCalledOnce();
        await act(() => result.current.hdrAnalysis.setIncludedArtifacts(["grade", "plot"]));
        expect(project).toHaveBeenCalledOnce();
        expect(preflight).not.toHaveBeenCalled();
        expect(checkDuplicates).not.toHaveBeenCalled();
      },
    );

    it.each([true, false, undefined])(
      "retains current backend HDR guidance without reprojecting descriptions and clears it on deselection (%s)",
      async (hdrGradeComparison) => {
        const workflowID = "hdr-grade-guidance";
        const initial = preparedQuestionnaireWorkflow(workflowID);
        const prepared = {
          ...initial,
          release: {
            ...initial.release!,
            display: {
              ...initial.release!.display,
              hdrTargets: [
                {
                  id: "hdr-target",
                  label: "Video",
                  supported: true,
                  selectionPolicy: "unique_hevc",
                },
              ],
            },
          },
          inputReadiness: {
            ...initial.inputReadiness!,
            selectedTrackerIds: ["LST"],
          },
        };
        const projected = await workflowPorts().project(
          prepared,
          ["LST"],
          {},
          "seed",
          new AbortController().signal,
        );
        const current: ReleaseWorkflowCurrent = {
          ...projected,
          descriptions: workflowCurrentWithDescriptions(workflowID, 3, "Retained description")
            .descriptions,
          media: {
            id: "retained-media",
            revision: 1,
            workflowId: workflowID,
            release: { id: projected.release!.id, revision: projected.release!.revision },
            releaseRef: { SourcePath: initial.release!.release.Source.SourcePath, Generation: 1 },
            projectionSet: {
              id: projected.projections!.id,
              revision: projected.projections!.revision,
            },
            captureFingerprint: "capture",
            requirementsFingerprint: "requirements",
            imageRequirementsPrepared: true,
            artifacts: [],
            status: "completed",
            createdAt: "2026-10-18T00:00:00Z",
          },
          projections: {
            ...projected.projections!,
            projections: projected.projections!.projections.map((projection) => ({
              ...projection,
              displayName: "LST",
              questionnaire: [
                { key: "hybrid_evidence", label: "Evidence", required: true, hdrGradeComparison },
              ],
            })),
          },
        };
        const project = vi.fn(async () => current);
        const workflow = workflowPorts({ current: async () => current, project });
        const { result } = renderHook(useReleaseSession, {
          wrapper: ({ children }) => (
            <ReleaseSessionProvider ports={portsFor({ resumeWorkflowID: workflowID, workflow })}>
              <HDRSurface />
              {children}
            </ReleaseSessionProvider>
          ),
        });
        await waitFor(() =>
          expect(result.current.duplicates.view.selectedTrackers).toEqual(["LST"]),
        );
        expect(result.current.hdrAnalysis.view.gradeComparisonTrackers).toEqual(
          hdrGradeComparison ? ["LST"] : [],
        );
        expect(result.current.hdrAnalysis.view.available).toBe(true);
        expect(result.current.hdrAnalysis.view.requirementsNeedRefresh).toBe(false);
        expect(project).not.toHaveBeenCalled();
        expect(result.current.descriptions.view.artifact).toEqual(current.descriptions);
        expect(result.current.screenshots.view.artifacts).toEqual(current.media);
        act(() => result.current.duplicates.chooseTrackers([]));
        expect(result.current.hdrAnalysis.view.gradeComparisonTrackers).toEqual([]);
      },
    );

    it("projects default tracker questions on opening Dupe Checking without preflight or duplicate calls", async () => {
      const active = workflowPorts();
      const prepared = preparedQuestionnaireWorkflow("early-questions");
      const project = vi.fn(async (...args: Parameters<TestWorkflowPorts["project"]>) => {
        const current = await active.project(...args);
        return {
          ...current,
          projections: {
            ...current.projections!,
            projections: current.projections!.projections.map((projection) => ({
              ...projection,
              questionnaire: [
                {
                  key: "choices",
                  label: "Choices",
                  required: true,
                  value: "First",
                  options: ["First", "Second"],
                },
              ],
            })),
          },
        };
      });
      const preflight = vi.fn(active.preflight);
      const checkDuplicates = vi.fn(active.checkDuplicates);
      const workflow = workflowPorts({
        current: async () => prepared,
        project,
        preflight,
        checkDuplicates,
      });
      const continued = vi.spyOn(workflow, "continue");
      const { result } = renderHook(useReleaseSession, {
        wrapper: questionnaireWrapper(portsFor({ resumeWorkflowID: "early-questions", workflow }), [
          "AITHER",
        ]),
      });
      await waitFor(() => expect(result.current.duplicates.view.questionnaireStatus).toBe("ready"));
      expect(result.current.duplicates.view.questionnaires[0]?.trackerId).toBe("AITHER");
      expect(project).toHaveBeenCalledTimes(1);
      expect(project.mock.calls[0]?.[1]).toEqual(["AITHER"]);
      expect(continued.mock.calls.every(([request]) => request.goal === "trackers_projected")).toBe(
        true,
      );
      expect(preflight).not.toHaveBeenCalled();
      expect(checkDuplicates).not.toHaveBeenCalled();
      expect(result.current.duplicates.view.questionnaireDirty).toBe(false);
      const summary = screen.getByText(/AITHER/, { selector: "summary" });
      expect(summary.parentElement).not.toHaveAttribute("open");
      expect(screen.getByRole("combobox", { name: "Choices *" })).not.toBeVisible();
      fireEvent.click(summary);
      expect(screen.getByRole("combobox", { name: "Choices *" })).toHaveValue("First");
      expect(project.mock.calls[0]?.[2].AITHER?.questionnaire).toEqual({});
    });

    it.each([false, true])(
      "handles a selection round trip during a production-port projection with canonical edits=%s",
      async (canonicalEdits) => {
        const prepared = preparedQuestionnaireWorkflow("roundtrip-questions");
        const projected = await workflowPorts().project(
          prepared,
          ["AITHER"],
          {},
          "seed",
          new AbortController().signal,
        );
        const current: ReleaseWorkflowCurrent = {
          ...projected,
          projections: {
            ...projected.projections!,
            projections: [
              {
                ...projected.projections!.projections[0],
                questionnaire: [{ key: "choice", label: "Choice", required: true }],
              },
            ],
          },
        };
        const pending = createDeferred<ReleaseWorkflowCurrent>();
        const requests: ContinueReleaseWorkflowRequest[] = [];
        setAppRequestHandlerForTests(async (method, body) => {
          if (method === "GetActiveInput")
            return {
              state: "active",
              revision: 1,
              inputId: "roundtrip-input",
              sourceVersion: "roundtrip-source",
              current: prepared,
            };
          if (method === "ContinueReleaseWorkflow") {
            requests.push(body as ContinueReleaseWorkflowRequest);
            return requests.length === 1 ? pending.promise : current;
          }
          throw new Error(`Unexpected questionnaire method: ${method}`);
        });
        const { result } = renderHook(useReleaseSession, {
          wrapper: questionnaireWrapper(productionReleaseSessionPorts(), ["AITHER"]),
        });
        await waitFor(() => expect(requests).toHaveLength(1));
        act(() => result.current.duplicates.chooseTrackers(["AITHER", "BLU"]));
        if (canonicalEdits) {
          act(() => result.current.input.changeMetadata({ Genres: ["Drama"] }));
          act(() => result.current.input.changeMetadata({}));
        }
        act(() => result.current.duplicates.chooseTrackers(["AITHER"]));
        expect(result.current.duplicates.view.questionnaires).toEqual([]);
        await act(async () => pending.resolve(current));
        expect(result.current.duplicates.view.questionnaireStatus).toBe(
          canonicalEdits ? "error" : "ready",
        );
        expect(result.current.duplicates.view.selectedTrackers).toEqual(["AITHER"]);
        expect(result.current.duplicates.view.questionnaires).toHaveLength(canonicalEdits ? 0 : 1);
        expect(result.current.duplicates.view.questionnaireError).toBe(
          canonicalEdits
            ? "Apply Input changes and prepare the selected source before reviewing tracker questions."
            : "",
        );
        expect(requests).toHaveLength(2);
        expect(requests.map((request) => request.authority?.expectedRevision)).toEqual([
          prepared.workflow.revision,
          current.workflow.revision,
        ]);
        expect(
          requests.every(
            (request) =>
              request.goal === "trackers_projected" &&
              request.intent.trackerIds?.join(",") === "AITHER",
          ),
        ).toBe(true);
        expect(result.current.duplicates.view.preflight).toBeNull();
        expect(result.current.duplicates.view.assessment).toBeNull();
      },
    );

    it("types a complete replacement for a rejected SPD answer without losing matching prefixes", async () => {
      const base = preparedQuestionnaireWorkflow("typing-questions");
      const projected = await workflowPorts().project(
        base,
        ["SPD"],
        {},
        "seed",
        new AbortController().signal,
      );
      let current: ReleaseWorkflowCurrent = {
        ...projected,
        inputReadiness: { ...base.inputReadiness!, selectedTrackerIds: ["SPD"] },
        projections: {
          ...projected.projections!,
          projections: [
            {
              ...projected.projections!.projections[0],
              questionnaire: [{ key: "channel", label: "Channel", required: true, value: "" }],
              questionnaireAnswers: { channel: "anim" },
            },
          ],
        },
      };
      const pendingApply = createDeferred<ReleaseWorkflowCurrent>();
      const applied: ContinueReleaseWorkflowRequest[] = [];
      const completed = new Set<string>();
      setAppRequestHandlerForTests(async (method, body) => {
        if (method === "GetActiveInput")
          return {
            state: "active",
            revision: 1,
            inputId: "typing-input",
            sourceVersion: "typing-source",
            current,
          };
        if (method !== "ContinueReleaseWorkflow")
          throw new Error(`Unexpected questionnaire method: ${method}`);
        const request = body as ContinueReleaseWorkflowRequest;
        if (request.goal === "trackers_assessed" && !completed.has(request.idempotencyKey)) {
          completed.add(request.idempotencyKey);
          applied.push(request);
          current = {
            ...current,
            workflow: { ...current.workflow, revision: current.workflow.revision + 1 },
            projections: {
              ...current.projections!,
              projections: [
                {
                  ...current.projections!.projections[0],
                  questionnaireAnswers: {
                    channel:
                      request.intent.projectionInstructions?.SPD?.questionnaire?.channel || "",
                  },
                },
              ],
            },
          };
          if (applied.length === 2) return pendingApply.promise;
        } else expect(["trackers_projected", "trackers_assessed"]).toContain(request.goal);
        return current;
      });
      const { result } = renderHook(useReleaseSession, {
        wrapper: questionnaireWrapper(productionReleaseSessionPorts(), ["SPD"]),
      });
      await waitFor(() => expect(result.current.duplicates.view.questionnaireStatus).toBe("ready"));
      const user = userEvent.setup();
      await user.click(screen.getByText(/SPD/, { selector: "summary" }));
      const channel = screen.getByRole("textbox", { name: "Channel *" });
      expect(channel).toHaveValue("");
      let typed = "";
      for (const character of "animation") {
        await user.type(channel, character);
        typed += character;
        expect(channel).toHaveValue(typed);
      }
      await user.click(screen.getByRole("button", { name: "Apply tracker answers" }));
      await waitFor(() => expect(applied).toHaveLength(1));
      expect(applied[0].intent.projectionInstructions?.SPD?.questionnaire).toEqual({
        channel: "animation",
      });
      // The returned required-empty field remains authoritative after accepting this submission.
      await waitFor(() => expect(channel).toHaveValue(""));
      expect(result.current.duplicates.view.questionnaireAnswers.SPD?.channel).toBeUndefined();
      await user.type(channel, "animation");
      expect(channel).toHaveValue("animation");
      await user.click(screen.getByRole("button", { name: "Apply tracker answers" }));
      await waitFor(() => expect(applied).toHaveLength(2));
      expect(channel).toBeDisabled();
      act(() => result.current.duplicates.answerQuestionnaire("SPD", "channel", "animations"));
      await act(async () => pendingApply.resolve(current));
      expect(channel).toHaveValue("animations");
      expect(result.current.duplicates.view.questionnaireAnswers.SPD?.channel).toBe("animations");
      expect(result.current.duplicates.view.questionnaireDirty).toBe(true);
    });

    it("keeps Apply available for a dirty GPW late draft after selection projection removes its schema", async () => {
      const base = preparedQuestionnaireWorkflow("late-recovery");
      const projected = await workflowPorts().project(
        base,
        ["GPW"],
        {},
        "seed",
        new AbortController().signal,
      );
      let current: ReleaseWorkflowCurrent = {
        ...projected,
        inputReadiness: { ...base.inputReadiness!, selectedTrackerIds: ["GPW"] },
        projections: { ...projected.projections!, id: "late-projections", revision: 3 },
        dryRun: {
          workflowId: base.workflow.id,
          projectionSet: { id: "late-projections", revision: 3 },
          reports: [
            {
              trackerId: "GPW",
              questionnaire: [
                { key: "director_imdb", label: "Director IMDb", required: true, value: "" },
              ],
            },
          ],
        } as unknown as ReleaseWorkflowCurrent["dryRun"],
      };
      const requests: ContinueReleaseWorkflowRequest[] = [];
      const completed = new Set<string>();
      setAppRequestHandlerForTests(async (method, body) => {
        if (method === "GetActiveInput")
          return {
            state: "active",
            revision: 1,
            inputId: "late-input",
            sourceVersion: "late-source",
            current,
          };
        if (method !== "ContinueReleaseWorkflow")
          throw new Error(`Unexpected questionnaire method: ${method}`);
        const request = body as ContinueReleaseWorkflowRequest;
        requests.push(request);
        if (!completed.has(request.idempotencyKey)) {
          completed.add(request.idempotencyKey);
          if (request.goal === "trackers_projected" && request.intent.trackerIds?.length === 2) {
            current = {
              ...(await workflowPorts().project(
                current,
                request.intent.trackerIds,
                request.intent.projectionInstructions || {},
                "selection",
                new AbortController().signal,
              )),
              dryRun: undefined,
            };
          }
          if (request.goal === "trackers_assessed") {
            current = {
              ...current,
              workflow: { ...current.workflow, revision: current.workflow.revision + 1 },
              projections: {
                ...current.projections!,
                projections: current.projections!.projections.map((projection) => ({
                  ...projection,
                  questionnaireAnswers:
                    projection.trackerId === "GPW"
                      ? {
                          director_imdb:
                            request.intent.projectionInstructions?.GPW?.questionnaire
                              ?.director_imdb || "",
                        }
                      : undefined,
                })),
              },
            };
          }
        }
        return current;
      });
      const { result } = renderHook(useReleaseSession, {
        wrapper: questionnaireWrapper(productionReleaseSessionPorts(), ["GPW"]),
      });
      await waitFor(() => expect(result.current.duplicates.view.questionnaireStatus).toBe("ready"));
      fireEvent.click(screen.getByText(/GPW/, { selector: "summary" }));
      fireEvent.change(screen.getByRole("textbox", { name: "Director IMDb *" }), {
        target: { value: "nm1234567" },
      });
      act(() => result.current.duplicates.chooseTrackers(["GPW", "AITHER"]));
      await waitFor(() => expect(result.current.duplicates.view.questionnaireStatus).toBe("ready"));
      expect(result.current.duplicates.view.questionnaireDirty).toBe(true);
      expect(screen.queryByRole("textbox", { name: "Director IMDb *" })).not.toBeInTheDocument();
      expect(screen.getByRole("status")).toHaveTextContent(
        "Apply tracker answers to refresh the review",
      );
      const apply = screen.getByRole("button", { name: "Apply tracker answers" });
      expect(apply).toBeEnabled();
      fireEvent.click(apply);
      await waitFor(() => expect(result.current.duplicates.view.questionnaireDirty).toBe(false));
      expect(
        requests.find((request) => request.goal === "trackers_assessed")?.intent
          .projectionInstructions?.GPW?.questionnaire,
      ).toEqual({ director_imdb: "nm1234567" });
      expect(
        requests.every((request) =>
          ["trackers_projected", "trackers_assessed"].includes(request.goal),
        ),
      ).toBe(true);
      expect(result.current.duplicates.view.assessment).toBeNull();
    });

    it("keeps selections editable when pure projection operation progress arrives during a checkbox interaction", async () => {
      const prepared = preparedQuestionnaireWorkflow("pending-projection");
      const initial = createDeferred<ReleaseWorkflowCurrent>();
      const terminal = createDeferred<WorkflowOperationStatus>();
      const running = { ...prepared, operation: questionnaireOperation(prepared, "running") };
      let current = prepared;
      const requests: ContinueReleaseWorkflowRequest[] = [];
      const polls = vi.fn(async () => terminal.promise);
      setAppRequestHandlerForTests(async (method, body) => {
        if (method === "GetActiveInput")
          return {
            state: "active",
            revision: 1,
            inputId: "pending-input",
            sourceVersion: "pending-source",
            current: prepared,
          };
        if (method === "GetReleaseWorkflowOperation") return polls();
        if (method === "GetReleaseWorkflow") return current;
        if (method !== "ContinueReleaseWorkflow")
          throw new Error(`Unexpected projection method: ${method}`);
        const request = body as ContinueReleaseWorkflowRequest;
        requests.push(request);
        if (requests.length === 1) return initial.promise;
        if (
          request.intent.trackerIds?.length === 2 &&
          current.projections?.projections.length !== 2
        ) {
          current = {
            ...(await workflowPorts().project(
              current,
              request.intent.trackerIds,
              request.intent.projectionInstructions || {},
              "latest",
              new AbortController().signal,
            )),
            operation: undefined,
          };
        }
        return current;
      });
      const { result } = renderHook(useReleaseSession, {
        wrapper: questionnaireWrapper(productionReleaseSessionPorts(), ["AITHER"]),
      });
      await waitFor(() => expect(requests).toHaveLength(1));
      const blu = screen.getByRole("checkbox", { name: "BLU" });
      expect(blu).toBeEnabled();
      fireEvent.pointerDown(blu);
      await act(async () => initial.resolve(running));
      expect(result.current.duplicates.view.questionnaireStatus).toBe("running");
      expect(blu).toBeEnabled();
      expect(screen.getByRole("button", { name: "Run dupe check" })).toBeDisabled();
      fireEvent.click(blu);
      expect(result.current.duplicates.view.selectedTrackers).toEqual(["AITHER", "BLU"]);
      act(() => result.current.duplicates.answerQuestionnaire("AITHER", "choice", "local draft"));
      current = {
        ...(await workflowPorts().project(
          prepared,
          ["AITHER"],
          {},
          "initial",
          new AbortController().signal,
        )),
        operation: questionnaireOperation(prepared, "completed"),
      };
      await act(async () => terminal.resolve(current.operation!));
      await waitFor(
        () => expect(result.current.duplicates.view.questionnaireStatus).toBe("ready"),
        { timeout: 2500 },
      );
      expect(result.current.duplicates.view.selectedTrackers).toEqual(["AITHER", "BLU"]);
      expect(
        result.current.duplicates.view.projections?.projections.map(
          (projection) => projection.trackerId,
        ),
      ).toEqual(["AITHER", "BLU"]);
      expect(requests.every((request) => request.goal === "trackers_projected")).toBe(true);
      expect(
        requests[requests.length - 1]?.intent.projectionInstructions?.AITHER?.questionnaire,
      ).toEqual({});
      expect(result.current.duplicates.view.preflight).toBeNull();
      expect(result.current.duplicates.view.assessment).toBeNull();
      expect(polls).toHaveBeenCalledOnce();
    });

    it.each([0, 1])(
      "settles a failed pure projection without restoring its old running snapshot (revision advance=%s)",
      async (advance) => {
        const prepared = preparedQuestionnaireWorkflow("failed-projection");
        const failedOperation: WorkflowOperationStatus = {
          ...questionnaireOperation(prepared, "failed"),
          failures: [
            {
              trackerId: "AITHER",
              failure: {
                Code: "no_eligible_trackers",
                Operation: "duplicate_check",
                Message: "Tracker preparation is blocked.",
                Recovery: "select_trackers",
              },
            },
          ],
        };
        let current: ReleaseWorkflowCurrent = {
          ...prepared,
          workflow: { ...prepared.workflow, revision: prepared.workflow.revision + advance },
          operation: failedOperation,
        };
        const requests: ContinueReleaseWorkflowRequest[] = [];
        setAppRequestHandlerForTests(async (method, body) => {
          if (method === "GetActiveInput")
            return {
              state: "active",
              revision: 1,
              inputId: "failed-input",
              sourceVersion: "failed-source",
              current: prepared,
            };
          if (method === "GetReleaseWorkflowOperation") return failedOperation;
          if (method === "GetReleaseWorkflow") return current;
          if (method !== "ContinueReleaseWorkflow")
            throw new Error(`Unexpected projection method: ${method}`);
          const request = body as ContinueReleaseWorkflowRequest;
          requests.push(request);
          if (requests.length === 1)
            return { ...prepared, operation: questionnaireOperation(prepared, "running") };
          if (request.intent.trackerIds?.includes("BLU") && !current.projections) {
            current = {
              ...(await workflowPorts().project(
                current,
                ["BLU"],
                {},
                "recovery",
                new AbortController().signal,
              )),
              operation: undefined,
            };
          }
          return current;
        });
        const { result } = renderHook(useReleaseSession, {
          wrapper: questionnaireWrapper(productionReleaseSessionPorts(), ["AITHER"]),
        });
        await waitFor(
          () => expect(result.current.duplicates.view.questionnaireStatus).toBe("error"),
          { timeout: 2500 },
        );
        expect(result.current.workflow.view.current?.operation?.status).toBe("failed");
        expect(result.current.workflow.view.current?.workflow.revision).toBe(
          prepared.workflow.revision + advance,
        );
        expect(result.current.duplicates.view.status).not.toBe("running");
        expect(screen.getByRole("button", { name: "Retry tracker questions" })).toBeEnabled();
        expect(screen.getByRole("checkbox", { name: "BLU" })).toBeEnabled();
        expect(result.current.duplicates.view.questionnaireError).toBe(
          "Tracker preparation is blocked.",
        );
        act(() => result.current.duplicates.chooseTrackers(["BLU"]));
        await waitFor(() =>
          expect(result.current.duplicates.view.questionnaireStatus).toBe("ready"),
        );
        expect(result.current.duplicates.view.selectedTrackers).toEqual(["BLU"]);
        expect(requests.every((request) => request.goal === "trackers_projected")).toBe(true);
        expect(requests[1].authority?.expectedRevision).toBe(prepared.workflow.revision + advance);
        expect(result.current.duplicates.view.preflight).toBeNull();
        expect(result.current.duplicates.view.assessment).toBeNull();
      },
    );

    it.each(["duplicates_decided", "trackers_assessed"] as const)(
      "keeps explicit %s operations exclusive while their backend operation runs",
      async (goal) => {
        const prepared = await workflowPorts().project(
          preparedQuestionnaireWorkflow("explicit-operation"),
          ["AITHER"],
          {},
          "seed",
          new AbortController().signal,
        );
        const requests: ContinueReleaseWorkflowRequest[] = [];
        setAppRequestHandlerForTests(async (method, body) => {
          if (method === "GetActiveInput")
            return {
              state: "active",
              revision: 1,
              inputId: "explicit-input",
              sourceVersion: "explicit-source",
              current: prepared,
            };
          if (method !== "ContinueReleaseWorkflow")
            throw new Error(`Unexpected explicit operation method: ${method}`);
          const request = body as ContinueReleaseWorkflowRequest;
          requests.push(request);
          return request.goal === "trackers_projected"
            ? prepared
            : {
                ...prepared,
                operation: {
                  ...questionnaireOperation(prepared, "running"),
                  command: goal === "duplicates_decided" ? "check_duplicates" : "project_trackers",
                },
              };
        });
        const { result, unmount } = renderHook(useReleaseSession, {
          wrapper: questionnaireWrapper(productionReleaseSessionPorts(), ["AITHER"]),
        });
        await waitFor(() =>
          expect(result.current.duplicates.view.questionnaireStatus).toBe("ready"),
        );
        let completion!: Promise<boolean>;
        act(() => {
          completion =
            goal === "duplicates_decided"
              ? result.current.duplicates.run()
              : result.current.duplicates.applyQuestionnaireAnswers();
        });
        await waitFor(() => expect(result.current.duplicates.view.status).toBe("running"));
        expect(screen.getByRole("checkbox", { name: "BLU" })).toBeDisabled();
        expect(screen.getByRole("button", { name: /Checking/ })).toBeDisabled();
        await act(async () => {
          expect(await result.current.duplicates.refreshQuestionnaires()).toBe(false);
          expect(await result.current.duplicates.applyQuestionnaireAnswers()).toBe(false);
          expect(await result.current.upload.start()).toBe(false);
        });
        expect(requests.map((request) => request.goal)).toEqual(["trackers_projected", goal]);
        unmount();
        await expect(completion).resolves.toBe(false);
      },
    );

    it("serializes rapid tracker selections and retains unapplied answers without submitting them", async () => {
      const active = workflowPorts();
      const prepared: ReleaseWorkflowCurrent = {
        ...preparedQuestionnaireWorkflow("selection-questions"),
        projectionInstructions: {
          instructions: {
            AITHER: {
              questionnaire: { trailer: null, choices: "First" },
              uploadReleaseName: "Saved.Name",
            },
          },
        } as unknown as ReleaseWorkflowCurrent["projectionInstructions"],
      };
      const first = createDeferred<ReleaseWorkflowCurrent>();
      const project = vi.fn(async (...args: Parameters<TestWorkflowPorts["project"]>) => {
        const projected = await active.project(...args);
        if (args[1].length === 1 && args[1][0] === "AITHER") return first.promise;
        return {
          ...projected,
          projections: {
            ...projected.projections!,
            projections: projected.projections!.projections.map((projection) => ({
              ...projection,
              questionnaire: [{ key: "choices", label: "Choices", required: true }],
            })),
          },
        };
      });
      const preflight = vi.fn(active.preflight);
      const checkDuplicates = vi.fn(active.checkDuplicates);
      const { result } = renderHook(useReleaseSession, {
        wrapper: questionnaireWrapper(
          portsFor({
            resumeWorkflowID: "selection-questions",
            workflow: workflowPorts({
              current: async () => prepared,
              project,
              preflight,
              checkDuplicates,
            }),
          }),
          ["AITHER"],
        ),
      });
      await waitFor(() => expect(project).toHaveBeenCalledTimes(1));
      act(() => result.current.duplicates.answerQuestionnaire("AITHER", "choices", "Second"));
      act(() => result.current.duplicates.confirmReleaseName("AITHER", "Local.Name"));
      act(() => result.current.duplicates.chooseTrackers(["AITHER", "BLU"]));
      act(() => result.current.duplicates.chooseTrackers(["AITHER", "PTP"]));
      expect(result.current.duplicates.view.questionnaires).toEqual([]);
      expect(screen.getByRole("checkbox", { name: "PTP" })).not.toBeDisabled();
      expect(screen.getByRole("button", { name: "Run dupe check" })).toBeDisabled();
      await act(async () => {
        expect(await result.current.duplicates.applyQuestionnaireAnswers()).toBe(false);
        expect(await result.current.upload.start()).toBe(false);
      });
      expect(project).toHaveBeenCalledTimes(1);
      await act(async () =>
        first.resolve(
          await active.project(prepared, ["AITHER"], {}, "old", new AbortController().signal),
        ),
      );
      await waitFor(() => expect(result.current.duplicates.view.questionnaireStatus).toBe("ready"));
      expect(project.mock.calls.map((args) => args[1])).toEqual([["AITHER"], ["AITHER", "PTP"]]);
      expect(project.mock.calls[1]?.[0].workflow.revision).toBeGreaterThan(
        prepared.workflow.revision,
      );
      expect(project.mock.calls[1]?.[2].AITHER).toEqual({
        questionnaire: { trailer: null, choices: "First" },
        uploadReleaseName: "Saved.Name",
      });
      expect(result.current.duplicates.view.selectedTrackers).toEqual(["AITHER", "PTP"]);
      expect(result.current.duplicates.view.questionnaireAnswers.AITHER?.choices).toBe("Second");
      expect(result.current.duplicates.view.questionnaireDirty).toBe(true);
      expect(preflight).not.toHaveBeenCalled();
      expect(checkDuplicates).not.toHaveBeenCalled();
    });

    it("reconciles selection changes after duplicate checking without submitting local drafts", async () => {
      let checked: ReleaseWorkflowCurrent;
      const project = workflowPorts().project;
      const active = workflowPorts({
        current: async () => checked,
        project: async (...args) => {
          const current = await project(...args);
          return {
            ...current,
            projections: current.projections
              ? {
                  ...current.projections,
                  projections: [...current.projections.projections].reverse(),
                }
              : undefined,
          };
        },
      });
      const prepared = preparedQuestionnaireWorkflow("retained-selection");
      const projected = await active.project(
        prepared,
        ["AITHER"],
        {},
        "seed",
        new AbortController().signal,
      );
      checked = await active.checkDuplicates(
        projected,
        false,
        "checked",
        new AbortController().signal,
      );
      const continueWorkflow = vi.fn(active.continue);
      const { result } = renderHook(useReleaseSession, {
        wrapper: questionnaireWrapper(
          portsFor({
            resumeWorkflowID: prepared.workflow.id,
            workflow: { ...active, continue: continueWorkflow },
          }),
          ["AITHER"],
        ),
      });
      await waitFor(() => expect(result.current.duplicates.view.questionnaireStatus).toBe("ready"));
      act(() => result.current.duplicates.answerQuestionnaire("AITHER", "choice", "local draft"));
      act(() => result.current.duplicates.chooseTrackers(["AITHER", "BLU"]));
      await waitFor(() =>
        expect(
          result.current.duplicates.view.assessment?.results.map((lane) => lane.trackerId).sort(),
        ).toEqual(["AITHER", "BLU"]),
      );
      const requests = continueWorkflow.mock.calls.map(([request]) => request);
      expect(
        requests.some(
          (request) =>
            request.goal === "duplicates_decided" && request.intent.trackerIds?.includes("BLU"),
        ),
      ).toBe(true);
      expect(
        requests.every(
          (request) => !request.intent.projectionInstructions?.AITHER?.questionnaire?.choice,
        ),
      ).toBe(true);
      expect(result.current.duplicates.view.questionnaireAnswers.AITHER?.choice).toBe(
        "local draft",
      );
      await waitFor(() => expect(result.current.duplicates.view.questionnaireError).toBe(""));
      act(() => result.current.duplicates.chooseTrackers(["AITHER"]));
      await waitFor(() =>
        expect(
          result.current.duplicates.view.assessment?.results.map((lane) => lane.trackerId).sort(),
        ).toEqual(["AITHER"]),
      );
      expect(result.current.navigation.view.access.screenshots.available).toBe(true);
    });

    it("resumes a retained duplicate check after interrupted selection reconciliation", async () => {
      const seed = workflowPorts();
      const prepared = preparedQuestionnaireWorkflow("pending-selection");
      const projected = await seed.project(
        prepared,
        ["AITHER", "BLU"],
        {},
        "seed",
        new AbortController().signal,
      );
      const interrupted = {
        ...projected,
        inputReadiness: { ...prepared.inputReadiness!, selectedTrackerIds: ["AITHER", "BLU"] },
        continuation: { ...projected.continuation, duplicateReusePending: true },
        dupes: undefined,
      };
      const active = workflowPorts({ current: async () => interrupted });
      const continueWorkflow = vi.fn(active.continue);
      const { result } = renderHook(useReleaseSession, {
        wrapper: questionnaireWrapper(
          portsFor({
            resumeWorkflowID: prepared.workflow.id,
            workflow: { ...active, continue: continueWorkflow },
          }),
          ["AITHER", "BLU"],
        ),
      });
      await waitFor(() => expect(result.current.duplicates.view.questionnaireStatus).toBe("ready"));
      expect(result.current.duplicates.view.assessment).toBeNull();
      act(() => result.current.duplicates.chooseTrackers(["AITHER"]));
      await waitFor(() =>
        expect(
          result.current.duplicates.view.assessment?.results.map((lane) => lane.trackerId),
        ).toEqual(["AITHER"]),
      );
      expect(
        continueWorkflow.mock.calls.some(
          ([request]) =>
            request.goal === "duplicates_decided" && request.intent.trackerIds?.length === 1,
        ),
      ).toBe(true);
    });

    it("replaces a sole tracker with unanswered questions before reconciling retained evidence", async () => {
      const seed = workflowPorts();
      const prepared = preparedQuestionnaireWorkflow("blocked-selection");
      const projected = await seed.project(
        prepared,
        ["BLU"],
        {},
        "seed",
        new AbortController().signal,
      );
      const question = {
        id: "blu-question",
        kind: "answer_questionnaire" as const,
        trackerId: "BLU",
        prompt: "Answer BLU",
        status: "pending" as const,
        workflowRevision: projected.workflow.revision,
        createdAt: "2026-07-20T00:00:00Z",
      };
      let backend: ReleaseWorkflowCurrent = {
        ...projected,
        inputReadiness: { ...prepared.inputReadiness!, selectedTrackerIds: ["BLU"] },
        workflow: { ...projected.workflow, requiredActions: [question] },
        continuation: { ...projected.continuation, duplicateReusePending: true },
        dupes: undefined,
      };
      const active = workflowPorts({
        current: async () => backend,
        project: async (current, trackers, ...rest) => {
          const next = await seed.project(current, trackers, ...rest);
          return {
            ...next,
            workflow: {
              ...next.workflow,
              requiredActions: trackers.includes("BLU") ? [question] : [],
            },
          };
        },
      });
      const continueWorkflow = vi.fn(
        async (request: ContinueReleaseWorkflowRequest, signal: AbortSignal) => {
          // Production duplicate continuation enforces current questions before it
          // can project a replacement selection; projection goals bypass that gate.
          if (
            request.goal === "duplicates_decided" &&
            backend.workflow.requiredActions?.some((action) => action.id === question.id)
          )
            return backend;
          backend = await active.continue(request, signal);
          return backend;
        },
      );
      const { result } = renderHook(useReleaseSession, {
        wrapper: questionnaireWrapper(
          portsFor({
            resumeWorkflowID: prepared.workflow.id,
            workflow: { ...active, continue: continueWorkflow },
          }),
          ["BLU"],
        ),
      });
      await waitFor(() => expect(result.current.duplicates.view.questionnaireStatus).toBe("ready"));
      const before = continueWorkflow.mock.calls.length;
      act(() => result.current.duplicates.chooseTrackers(["AITHER"]));
      await waitFor(() =>
        expect(
          result.current.duplicates.view.assessment?.results.map((lane) => lane.trackerId),
        ).toEqual(["AITHER"]),
      );
      const goals = continueWorkflow.mock.calls.slice(before).map(([request]) => request.goal);
      expect(goals[0]).toBe("trackers_projected");
      expect(goals).toContain("duplicates_decided");
      expect(backend.workflow.requiredActions).toEqual([]);
      expect(result.current.duplicates.view.questionnaireError).toBe("");
    });

    it("does not reconcile duplicate results just because an uploaded tracker remains selected", async () => {
      const seed = workflowPorts();
      const prepared = preparedQuestionnaireWorkflow("uploaded-selection");
      const projected = await seed.project(
        prepared,
        ["AITHER"],
        {},
        "seed",
        new AbortController().signal,
      );
      const checked = await seed.checkDuplicates(
        projected,
        false,
        "checked",
        new AbortController().signal,
      );
      const current = {
        ...checked,
        workflow: {
          ...checked.workflow,
          submissionExclusions: [
            { trackerId: "BLU", reason: "already_uploaded", confirmedAt: "2026-07-20T00:00:00Z" },
          ],
        },
        inputReadiness: { ...prepared.inputReadiness!, selectedTrackerIds: ["AITHER", "BLU"] },
      };
      const checkDuplicates = vi.fn(seed.checkDuplicates);
      const active = workflowPorts({
        current: async () => current,
        checkDuplicates,
        project: async (latest, trackers, ...rest) =>
          seed.project(
            latest,
            trackers.filter((tracker) => tracker !== "BLU"),
            ...rest,
          ),
      });
      const continueWorkflow = vi.fn(active.continue);
      const { result } = renderHook(useReleaseSession, {
        wrapper: questionnaireWrapper(
          portsFor({
            resumeWorkflowID: prepared.workflow.id,
            workflow: { ...active, continue: continueWorkflow },
          }),
          ["AITHER", "BLU"],
        ),
      });
      await waitFor(() => expect(result.current.duplicates.view.questionnaireStatus).toBe("ready"));
      expect(result.current.duplicates.view.selectedTrackers).toEqual(["AITHER", "BLU"]);
      expect(
        continueWorkflow.mock.calls.every(([request]) => request.goal === "trackers_projected"),
      ).toBe(true);
      expect(checkDuplicates).not.toHaveBeenCalled();
    });

    it("aborts a pending question projection when the source changes and suppresses its response", async () => {
      const active = workflowPorts();
      const prepared = preparedQuestionnaireWorkflow("source-questions", "C:\\media\\First");
      const first = createDeferred<ReleaseWorkflowCurrent>();
      let signal: AbortSignal | undefined;
      const project = vi.fn(async (...args: Parameters<TestWorkflowPorts["project"]>) => {
        if (args[0].workflow.id === "source-questions") {
          signal = args[4];
          return first.promise;
        }
        return active.project(...args);
      });
      const { result } = renderHook(useReleaseSession, {
        wrapper: questionnaireWrapper(
          portsFor({
            resumeWorkflowID: "source-questions",
            workflow: workflowPorts({ current: async () => prepared, project }),
          }),
          ["AITHER"],
        ),
      });
      await waitFor(() => expect(project).toHaveBeenCalledTimes(1));
      await act(() => result.current.input.openSource("C:\\media\\Second"));
      expect(signal?.aborted).toBe(true);
      await act(async () =>
        first.resolve(
          await active.project(prepared, ["AITHER"], {}, "old", new AbortController().signal),
        ),
      );
      expect(result.current.identity.view.sourcePath).toBe("C:\\media\\Second");
      expect(
        result.current.duplicates.view.questionnaires.flatMap(
          (projection) => projection.questionnaire || [],
        ),
      ).toEqual([]);
      expect(result.current.workflow.view.current?.workflow.id).toBe("workflow-new");
    });

    it("shows preparation-needed failures without retry loops or remote work", async () => {
      const active = workflowPorts();
      const prepared = preparedQuestionnaireWorkflow("missing-questions");
      const project = vi.fn(async () => {
        throw Object.assign(new Error("Preparation required"), {
          failure: {
            Code: "missing_prerequisite",
            Operation: "duplicate_check",
            Recovery: "complete_prerequisite",
            Message: "Prepare the selected source before reviewing tracker questions.",
          },
        });
      });
      const preflight = vi.fn(active.preflight);
      const checkDuplicates = vi.fn(active.checkDuplicates);
      const { result } = renderHook(useReleaseSession, {
        wrapper: questionnaireWrapper(
          portsFor({
            resumeWorkflowID: "missing-questions",
            workflow: workflowPorts({
              current: async () => prepared,
              project,
              preflight,
              checkDuplicates,
            }),
          }),
          ["AITHER"],
        ),
      });
      await waitFor(() => expect(result.current.duplicates.view.questionnaireStatus).toBe("error"));
      expect(screen.getByRole("status")).toHaveTextContent(
        "Prepare the selected source before reviewing tracker questions.",
      );
      expect(project).toHaveBeenCalledTimes(1);
      expect(preflight).not.toHaveBeenCalled();
      expect(checkDuplicates).not.toHaveBeenCalled();
    });
    it("rejects a projected questionnaire completed after canonical Input corrections change", async () => {
      const active = workflowPorts();
      const projected = await active.project(
        preparedQuestionnaireWorkflow("edited-questions"),
        ["AITHER"],
        {},
        "seed",
        new AbortController().signal,
      );
      const prepared: ReleaseWorkflowCurrent = {
        ...projected,
        projections: {
          ...projected.projections!,
          projections: [
            {
              ...projected.projections!.projections[0],
              questionnaire: [{ key: "choice", label: "Choice", required: true }],
            },
          ],
        },
      };
      const pending = createDeferred<ReleaseWorkflowCurrent>();
      const project = vi.fn(async () => pending.promise);
      const { result } = renderHook(useReleaseSession, {
        wrapper: questionnaireWrapper(
          portsFor({
            resumeWorkflowID: "edited-questions",
            workflow: workflowPorts({ current: async () => prepared, project }),
          }),
          ["AITHER"],
        ),
      });
      await waitFor(() => expect(project).toHaveBeenCalledTimes(1));
      expect(result.current.duplicates.view.questionnaires[0]?.questionnaire).toHaveLength(1);
      act(() => result.current.input.changeMetadata({ Genres: ["Drama"] }));
      expect(result.current.duplicates.view.questionnaires).toEqual([]);
      await act(async () =>
        pending.resolve(
          await active.project(prepared, ["AITHER"], {}, "old", new AbortController().signal),
        ),
      );
      expect(result.current.duplicates.view.questionnaireStatus).toBe("error");
      expect(result.current.duplicates.view.questionnaires).toEqual([]);
      expect(result.current.input.view.intent.metadata.Genres).toEqual(["Drama"]);
      expect(result.current.input.view.correctionDirty).toBe(true);
      expect(project).toHaveBeenCalledTimes(1);
    });
  });

  it("blocks stale reviewed uploads until changed answers are applied", async () => {
    const active = workflowPorts();
    const project: TestWorkflowPorts["project"] = async (...args) => {
      const current = await active.project(...args);
      return {
        ...current,
        projections: current.projections
          ? {
              ...current.projections,
              projections: current.projections.projections.map((projection) => ({
                ...projection,
                questionnaireAnswers: Object.fromEntries(
                  Object.entries(args[2][projection.trackerId]?.questionnaire || {}).filter(
                    ([, value]) => value != null,
                  ),
                ) as Record<string, string>,
                questionnaire: [
                  {
                    key: "choices",
                    label: "Choices",
                    kind: "select",
                    required: true,
                    options: ["First", "Second", "French"],
                    value: (
                      args[2][projection.trackerId]?.questionnaire?.choices ?? "First"
                    ).trim(),
                  },
                ],
              })),
            }
          : undefined,
      };
    };
    const checkDuplicates = vi.fn(active.checkDuplicates);
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ workflow: workflowPorts({ project, checkDuplicates }) })),
    });
    await selectAndPrepare(result, "C:\\media\\Example");
    const evidence = result.current.duplicates.view.assessment;
    act(() => result.current.duplicates.answerQuestionnaire("AITHER", "choices", "First"));
    expect(result.current.upload.view.questionnaireDirty).toBe(true);
    await act(() => result.current.duplicates.applyQuestionnaireAnswers());
    expect(result.current.upload.view.questionnaireDirty).toBe(false);
    act(() => result.current.duplicates.answerQuestionnaire("AITHER", "choices", "Second"));
    expect(result.current.upload.view.questionnaireDirty).toBe(true);
    await act(async () => {
      expect(await result.current.upload.runDryRun()).toBe(false);
      expect(await result.current.upload.start()).toBe(false);
    });
    await act(() => result.current.duplicates.applyQuestionnaireAnswers());
    expect(result.current.upload.view.questionnaireDirty).toBe(false);
    act(() => result.current.duplicates.answerQuestionnaire("AITHER", "choices", " French "));
    expect(result.current.upload.view.questionnaireDirty).toBe(true);
    await act(() => result.current.duplicates.applyQuestionnaireAnswers());
    expect(result.current.upload.view.questionnaireDirty).toBe(false);
    expect(result.current.duplicates.view.assessment).toEqual(evidence);
    expect(checkDuplicates).toHaveBeenCalledTimes(1);
    expect(result.current.duplicates.view.questionnaires[0]?.questionnaire?.[0].value).toBe(
      "French",
    );
  });

  it("applies tracker questionnaire drafts without uploading or starting duplicate searches", async () => {
    const active = workflowPorts();
    const project = vi.fn(active.project);
    const checkDuplicates = vi.fn(active.checkDuplicates);
    const executeUploads = vi.fn(active.executeUploads);
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({ workflow: workflowPorts({ project, checkDuplicates, executeUploads }) }),
      ),
    });
    await selectAndPrepare(result, "C:\\media\\Example");
    project.mockClear();
    checkDuplicates.mockClear();
    executeUploads.mockClear();
    act(() => result.current.duplicates.answerQuestionnaire("AITHER", "choices", "First,Second"));
    await act(() => result.current.duplicates.applyQuestionnaireAnswers());
    expect(project).toHaveBeenCalled();
    expect(project.mock.calls[project.mock.calls.length - 1]?.[2].AITHER?.questionnaire).toEqual({
      choices: "First,Second",
    });
    expect(checkDuplicates).not.toHaveBeenCalled();
    expect(executeUploads).not.toHaveBeenCalled();
  });

  it.each([false, true])(
    "answers exact late tracker requirements on Dupe Checking with previously submitted selection=%s",
    async (alreadySubmitted) => {
      const active = workflowPorts();
      const workflowID = "workflow-late-questions";
      const prepared = workflowCurrentFromPreview(
        workflowCurrent(workflowID, 2),
        preview("C:\\media\\Example", 1),
      );
      const projected = await active.project(
        prepared,
        ["AITHER"],
        {},
        "seed",
        new AbortController().signal,
      );
      const retained: ReleaseWorkflowCurrent = {
        ...projected,
        projectionInstructions: {
          instructions: { AITHER: { questionnaire: { trailer: null } } },
        } as unknown as NonNullable<ReleaseWorkflowCurrent["projectionInstructions"]>,
        workflow: {
          ...projected.workflow,
          submissionExclusions: alreadySubmitted
            ? [
                {
                  trackerId: "UPLOADED",
                  reason: "already_uploaded",
                  confirmedAt: "2026-07-20T00:00:00Z",
                },
              ]
            : [],
        },
        projections: {
          ...projected.projections!,
          id: "projections-late",
          revision: 3,
          projections: projected.projections!.projections.map((projection) => ({
            ...projection,
            questionnaireAnswers: {},
            questionnaire: [
              { key: "poster", label: "Poster URL", required: false, value: "" },
              {
                key: "trailer",
                label: "Trailer",
                required: false,
                value: "https://www.youtube.com/watch?v=EXAMPLE",
              },
            ],
          })),
        },
        dryRun: {
          workflowId: workflowID,
          projectionSet: { id: "projections-late", revision: 3 },
          status: "failed",
          reports: [
            {
              trackerId: "AITHER",
              status: "failed",
              questionnaire: [
                { key: "poster", label: "Poster URL", required: true, value: "" },
                {
                  key: "trailer",
                  label: "Trailer",
                  required: false,
                  value: "https://www.youtube.com/watch",
                },
                { key: "director", label: "Director", required: true, value: "Example Director" },
              ],
            },
          ],
        } as unknown as NonNullable<ReleaseWorkflowCurrent["dryRun"]>,
      };
      const project = vi.fn(async (...args: Parameters<TestWorkflowPorts["project"]>) => {
        const current = await active.project(
          args[0],
          args[1].filter((tracker) => tracker !== "UPLOADED"),
          args[2],
          args[3],
          args[4],
        );
        return {
          ...current,
          dryRun: undefined,
          projections: {
            ...current.projections!,
            id: "projections-applied",
            revision: 4,
            projections: current.projections!.projections.map((projection) => ({
              ...projection,
              questionnaireAnswers: Object.fromEntries(
                Object.entries(args[2].AITHER?.questionnaire || {}).filter(
                  ([, value]) => value != null,
                ),
              ) as Record<string, string>,
              questionnaire: [
                {
                  key: "trailer",
                  label: "Trailer",
                  required: false,
                  value:
                    args[2].AITHER?.questionnaire?.trailer ??
                    "https://www.youtube.com/watch?v=EXAMPLE",
                },
                {
                  key: "poster",
                  label: "Poster URL",
                  required: false,
                  value: args[2].AITHER?.questionnaire?.poster ?? "",
                },
                {
                  key: "director",
                  label: "Director",
                  required: false,
                  value: args[2].AITHER?.questionnaire?.director ?? "Example Director",
                },
              ],
            })),
          },
        };
      });
      const checkDuplicates = vi.fn(active.checkDuplicates);
      const executeUploads = vi.fn(active.executeUploads);
      const { result } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(
          portsFor({
            resumeWorkflowID: workflowID,
            workflow: workflowPorts({
              current: async () => retained,
              project,
              checkDuplicates,
              executeUploads,
              dryRunUploads: async (current) => ({
                ...current,
                dryRun: {
                  ...retained.dryRun!,
                  projectionSet: {
                    id: current.projections!.id,
                    revision: current.projections!.revision,
                  },
                },
              }),
            }),
          }),
        ),
      });
      await waitFor(() =>
        expect(result.current.workflow.view.current?.workflow.id).toBe(workflowID),
      );
      act(() =>
        result.current.duplicates.chooseTrackers(
          alreadySubmitted ? ["AITHER", "UPLOADED"] : ["AITHER"],
        ),
      );
      await waitFor(() =>
        expect(result.current.duplicates.view.questionnaires[0]?.questionnaire?.[0].required).toBe(
          true,
        ),
      );
      act(() =>
        result.current.duplicates.answerQuestionnaire(
          "AITHER",
          "poster",
          "https://example.com/poster.jpg",
        ),
      );
      expect(result.current.upload.view.questionnaireDirty).toBe(true);
      await act(() => result.current.duplicates.applyQuestionnaireAnswers());
      expect(project.mock.calls[project.mock.calls.length - 1]?.[2].AITHER?.questionnaire).toEqual({
        poster: "https://example.com/poster.jpg",
        trailer: null,
      });
      expect(
        result.current.duplicates.view.questionnaires[0]?.questionnaire?.find(
          (field) => field.key === "trailer",
        )?.value,
      ).toBe("https://www.youtube.com/watch?v=EXAMPLE");
      expect(result.current.duplicates.view.questionnaireDirty).toBe(false);
      expect(result.current.duplicates.view.questionnaires[0]?.questionnaire?.[0].required).toBe(
        false,
      );
      await act(() => result.current.upload.runDryRun());
      expect(result.current.duplicates.view.questionnaireDirty).toBe(false);
      act(() =>
        result.current.duplicates.answerQuestionnaire(
          "AITHER",
          "trailer",
          "https://www.youtube.com/watch?v=OTHER",
        ),
      );
      await act(() => result.current.duplicates.applyQuestionnaireAnswers());
      await act(() => result.current.upload.runDryRun());
      expect(result.current.duplicates.view.questionnaireDirty).toBe(false);
      expect(checkDuplicates).not.toHaveBeenCalled();
      expect(executeUploads).not.toHaveBeenCalled();
    },
  );

  it("rejects questionnaire apply for a changed tracker selection", async () => {
    const active = workflowPorts();
    const project = vi.fn(active.project);
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ workflow: workflowPorts({ project }) })),
    });
    await selectAndPrepare(result, "C:\\media\\Example");
    act(() => result.current.duplicates.answerQuestionnaire("AITHER", "choices", "First"));
    act(() => result.current.duplicates.chooseTrackers(["BLU"]));
    project.mockClear();
    await act(async () =>
      expect(await result.current.duplicates.applyQuestionnaireAnswers()).toBe(false),
    );
    expect(result.current.duplicates.view.questionnaires).toEqual([]);
    expect(project).not.toHaveBeenCalled();
  });

  it("carries workflow drafts across refreshes and clears them after another input opens", async () => {
    const { result } = renderHook(useReleaseSession, { wrapper: wrapperFor(portsFor()) });
    await selectAndPrepare(result, "C:\\media\\Example");
    act(() => result.current.upload.chooseTrackers(["AITHER", "BLU"]));
    act(() => result.current.upload.changeOptions({ noSeed: true, runLogLevel: "debug" }));
    act(() => result.current.duplicates.answerQuestionnaire("AITHER", "season", "1"));

    await act(() => result.current.input.prepare());
    expect(result.current.upload.view.selectedTrackers).toEqual(["AITHER", "BLU"]);
    expect(result.current.upload.view.options.noSeed).toBe(true);
    expect(result.current.upload.view.options.runLogLevel).toBe("debug");
    expect(result.current.duplicates.view.questionnaireAnswers.AITHER).toEqual({ season: "1" });

    act(() => result.current.input.selectSource("C:\\media\\Other"));
    expect(result.current.upload.view.selectedTrackers).toEqual(["AITHER", "BLU"]);
    expect(result.current.upload.view.options.noSeed).toBe(true);
    await act(() => result.current.input.prepare());
    expect(result.current.upload.view.selectedTrackers).toEqual([]);
    expect(result.current.upload.view.options.noSeed).toBe(false);
    expect(result.current.upload.view.options.runLogLevel).toBe("info");
    expect(result.current.duplicates.view.questionnaireAnswers).toEqual({});
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
    expect(result.current.screenshots.view.staleReason).toBe("Preparation required.");
  });

  it("retains a failed screenshot plan status so automatic loading stops", async () => {
    const mediaPlan = vi.fn(async () => {
      throw new Error("rate limit exceeded");
    });
    const invalidateTrackers = vi.fn(async () => {
      throw new Error("private unrelated workflow details");
    });
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({ invalidateTrackers, mediaPlan }),
        }),
      ),
    });
    await selectAndPrepare(result, "C:\\media\\Example");

    await act(() => result.current.workflow.invalidateTrackers(["AITHER"], "test"));
    expect(result.current.workflow.view.error).toBe("Workflow request failed. Retry the request.");
    expect(result.current.workflow.view.error).not.toContain("private unrelated workflow details");

    await act(() => result.current.screenshots.load());

    expect(mediaPlan).toHaveBeenCalledOnce();
    expect(result.current.screenshots.view.status).toBe("error");
    expect(result.current.screenshots.view.error).toBe(
      "Screenshot request failed. Retry the request.",
    );
    expect(result.current.screenshots.view.error).not.toContain(
      "private unrelated workflow details",
    );
  });

  it("blocks cached screenshot mutations while audio analysis owns the workflow", async () => {
    const audio = createDeferred<ReleaseWorkflowCurrent>();
    let audioCurrent: ReleaseWorkflowCurrent | null = null;
    const analyzeAudio = vi.fn(async (current: ReleaseWorkflowCurrent) => {
      audioCurrent = current;
      return audio.promise;
    });
    const captureMedia = vi.fn(async (current: ReleaseWorkflowCurrent) => current);
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({ analyzeAudio, captureMedia }),
        }),
      ),
    });
    await selectAndPrepare(result, "C:\\media\\Example");
    await act(() => result.current.screenshots.load());
    expect(result.current.screenshots.view.plan).not.toBeNull();

    let audioCommand!: Promise<boolean>;
    act(() => {
      audioCommand = result.current.audioAnalysis.generate({
        resourceID: "resource-one",
        selection: "primary",
        trackIDs: ["audio-main"],
        variants: ["waveform"],
      });
    });
    await waitFor(() => expect(analyzeAudio).toHaveBeenCalledOnce());
    expect(result.current.screenshots.view.status).toBe("ready");
    expect(result.current.screenshots.view.mutationBlockedReason).toContain(
      "Another workflow operation is running",
    );

    await act(async () => {
      expect(
        await result.current.screenshots.generate("final", [
          { Index: 0, TimestampSeconds: 10, Frame: 240, Source: "manual" },
        ]),
      ).toBe(false);
    });
    expect(captureMedia).not.toHaveBeenCalled();

    if (!audioCurrent) throw new Error("audio command did not receive workflow state");
    audio.resolve(audioCurrent);
    await act(() => audioCommand);
    expect(result.current.screenshots.view.mutationBlockedReason).toBe("");

    await act(async () => {
      expect(
        await result.current.screenshots.generate("final", [
          { Index: 0, TimestampSeconds: 10, Frame: 240, Source: "manual" },
        ]),
      ).toBe(true);
    });
    expect(captureMedia).toHaveBeenCalledOnce();
  });

  it("surfaces saved-image adoption contention and permits retry", async () => {
    const plan = {
      ...(await workflowPorts().mediaPlan("workflow-new", new AbortController().signal)),
      autoAdoptSavedImages: true,
      savedTrackerImages: [
        { trackerId: "AITHER", host: "imgbb", url: "https://images.example.invalid/saved.png" },
      ],
    };
    const screenshot = createDeferred<typeof plan>();
    const mediaPlan = vi.fn(async () => plan);
    mediaPlan.mockImplementationOnce(() => screenshot.promise);
    const audio = createDeferred<ReleaseWorkflowCurrent>();
    let audioCurrent: ReleaseWorkflowCurrent | null = null;
    const analyzeAudio = vi.fn(async (current: ReleaseWorkflowCurrent) => {
      audioCurrent = current;
      return audio.promise;
    });
    const captureMedia = vi.fn(async (current: ReleaseWorkflowCurrent) => current);
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({ workflow: workflowPorts({ mediaPlan, analyzeAudio, captureMedia }) }),
      ),
    });
    await selectAndPrepare(result, "C:\\media\\Example");

    let pendingLoad!: Promise<boolean>;
    act(() => {
      pendingLoad = result.current.screenshots.load();
    });
    let audioCommand!: Promise<boolean>;
    act(() => {
      audioCommand = result.current.audioAnalysis.generate({
        resourceID: "resource-one",
        selection: "primary",
        trackIDs: ["audio-main"],
        variants: ["waveform"],
      });
    });
    await waitFor(() => expect(analyzeAudio).toHaveBeenCalledOnce());
    screenshot.resolve(plan);
    await act(async () => expect(await pendingLoad).toBe(false));

    expect(captureMedia).not.toHaveBeenCalled();
    expect(result.current.screenshots.view.status).toBe("error");
    expect(result.current.screenshots.view.error).toBe(
      "Existing images were not included. Finish the current action, then load suggestions to retry.",
    );

    if (!audioCurrent) throw new Error("audio command did not receive workflow state");
    audio.resolve(audioCurrent);
    await act(() => audioCommand);
    await act(async () => expect(await result.current.screenshots.load()).toBe(true));
    expect(captureMedia).toHaveBeenCalledOnce();
    expect(captureMedia).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ selections: [], savedImagePlanId: plan.id }),
      expect.any(String),
      expect.any(AbortSignal),
    );
    expect(result.current.screenshots.view.error).toBe("");
  });

  it.each(["initial", "externally observed", "newer cancellation"])(
    "restores %s cancellation after delayed image adoption is rejected",
    async (timing) => {
      const workflowID = "workflow-delayed-adoption";
      const base = await workflowPorts().project(
        workflowCurrentFromPreview(
          workflowCurrent(workflowID, 2),
          preview("Example.Release.2026.mkv", 1),
        ),
        ["AITHER"],
        {},
        "adoption-fixture",
        new AbortController().signal,
      );
      const canceled: ReleaseWorkflowCurrent = {
        ...base,
        operation: {
          id: "externally-canceled-operation",
          workflowId: workflowID,
          revision: 2,
          sequence: 2,
          command: "continue",
          operation: "media",
          status: "canceled",
          completed: 0,
          total: 1,
          progress: 0,
          startedAt: "2026-07-20T00:00:00Z",
          updatedAt: "2026-07-20T00:00:00Z",
        },
      };
      let current = timing === "externally observed" ? base : canceled;
      const plan = {
        ...(await workflowPorts().mediaPlan(workflowID, new AbortController().signal)),
        autoAdoptSavedImages: true,
        savedTrackerImages: [
          { trackerId: "AITHER", host: "imgbb", url: "https://images.example.invalid/saved.png" },
        ],
      };
      const screenshot = createDeferred<typeof plan>();
      const captureMedia = vi.fn(async () => {
        throw new Error("Stale adoption request rejected.");
      });
      const { result, unmount } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(
          portsFor({
            resumeWorkflowID: workflowID,
            workflow: workflowPorts({
              current: async () => current,
              mediaPlan: () => screenshot.promise,
              captureMedia,
            }),
          }),
        ),
      });
      try {
        await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
        let pending!: Promise<boolean>;
        act(() => {
          pending = result.current.screenshots.load();
        });
        if (timing !== "initial") {
          current = {
            ...canceled,
            operation: { ...canceled.operation!, id: "newer-canceled-operation" },
          };
          await act(async () => {
            await result.current.workflow.reload();
          });
          expect(result.current.workflow.view.current?.operation).toEqual(current.operation);
        }
        screenshot.resolve(plan);
        await act(async () => expect(await pending).toBe(false));
        expect(captureMedia).toHaveBeenCalledOnce();
        expect(result.current.workflow.view.current?.operation).toEqual(current.operation);
        await act(async () => {
          await result.current.workflow.reload();
        });
        expect(result.current.workflow.view.current?.operation).toEqual(current.operation);
      } finally {
        screenshot.resolve(plan);
        unmount();
      }
    },
  );

  it.each(["questions", "plan", "preview", "replacement plan", "canceled preview"])(
    "restores cancellation details after a rejected %s request",
    async (request) => {
      const workflowID = "workflow-rejected-facet";
      const base = await workflowPorts().project(
        preparedQuestionnaireWorkflow(workflowID),
        ["AITHER"],
        {},
        "rejection-fixture",
        new AbortController().signal,
      );
      let current = { ...base, operation: questionnaireOperation(base, "canceled") };
      const rejection = createDeferred<never>();
      const reject = vi.fn(() => rejection.promise);
      const { result, unmount } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(
          portsFor({
            resumeWorkflowID: workflowID,
            workflow: workflowPorts({
              current: async () => current,
              continue: reject,
              mediaPlan: reject,
              previewFrame: reject,
            }),
          }),
        ),
      });
      try {
        await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
        expect(result.current.workflow.view.current?.operation).toEqual(current.operation);
        let pending!: Promise<boolean>;
        act(() => {
          pending =
            request === "questions"
              ? result.current.duplicates.refreshQuestionnaires()
              : request.endsWith("preview")
                ? result.current.screenshots.previewFrame("", 10)
                : result.current.screenshots.load();
        });
        expect(result.current.workflow.view.current?.operation).toBeUndefined();
        let replaced: Promise<boolean> | undefined;
        if (request === "replacement plan") {
          // Another tab can review a release name without starting a new operation.
          const revision = current.workflow.revision + 1;
          current = {
            ...current,
            workflow: {
              ...current.workflow,
              revision,
              trackerProjections: { ...current.workflow.trackerProjections!, revision },
            },
            projections: { ...current.projections!, revision },
          };
          await act(() => result.current.workflow.reload());
          expect(result.current.screenshots.view.status).toBe("idle");
          replaced = pending;
          act(() => {
            pending = result.current.screenshots.load();
          });
          expect(result.current.workflow.view.current?.operation).toBeUndefined();
        }
        if (request === "canceled preview") {
          await act(() => result.current.workflow.cancel());
          replaced = pending;
          act(() => {
            pending = result.current.screenshots.previewFrame("", 11);
          });
          expect(result.current.workflow.view.current?.operation).toBeUndefined();
        }
        rejection.reject(new Error("Request rejected before acceptance."));
        await act(async () => {
          expect(await pending).toBe(false);
          if (replaced) expect(await replaced).toBe(false);
        });
        expect(reject).toHaveBeenCalledTimes(replaced ? 2 : 1);
        expect(
          request === "questions"
            ? result.current.duplicates.view.questionnaireError
            : result.current.screenshots.view.error,
        ).not.toBe("");
        expect(result.current.workflow.view.current?.operation).toEqual(current.operation);
        await act(async () => {
          await result.current.workflow.reload();
        });
        expect(result.current.workflow.view.current?.operation).toEqual(current.operation);
      } finally {
        unmount();
      }
    },
  );

  it("keeps untyped screenshot command failures local and safe", async () => {
    const captureMedia = vi.fn(async () => {
      throw new Error("private screenshot transport details");
    });
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({ captureMedia }),
        }),
      ),
    });
    await selectAndPrepare(result, "C:\\media\\Example");

    await act(() =>
      result.current.screenshots.generate("final", [
        {
          Index: 0,
          TimestampSeconds: 10,
          Frame: 240,
          Source: "manual",
        },
      ]),
    );

    expect(captureMedia).toHaveBeenCalledOnce();
    expect(result.current.screenshots.view.status).toBe("error");
    expect(result.current.screenshots.view.error).toBe(
      "Screenshot request failed. Retry the request.",
    );
    expect(result.current.screenshots.view.error).not.toContain(
      "private screenshot transport details",
    );

    await act(() => result.current.screenshots.load());

    expect(result.current.screenshots.view.status).toBe("ready");
    expect(result.current.screenshots.view.error).toBe("");
  });

  it("replaces a cached plan error with the newer screenshot command state", async () => {
    const baseMediaPlan = workflowPorts().mediaPlan;
    const planFailure = Object.assign(new Error("private plan refresh details"), {
      failure: {
        Code: "media_plan_failed",
        Operation: "media_plan",
        Message: "Screenshot plan could not be refreshed.",
        Recovery: "retry",
      },
    });
    const mediaPlan = vi
      .fn<ReleaseSessionPorts["workflow"]["mediaPlan"]>()
      .mockImplementationOnce(baseMediaPlan)
      .mockRejectedValue(planFailure);
    const successfulCapture = createDeferred<ReleaseWorkflowCurrent>();
    let captureCurrent: ReleaseWorkflowCurrent | null = null;
    let captureAttempt = 0;
    const captureMedia = vi.fn(async (current: ReleaseWorkflowCurrent) => {
      captureAttempt += 1;
      if (captureAttempt === 1) {
        captureCurrent = current;
        return successfulCapture.promise;
      }
      throw new Error("private screenshot transport details");
    });
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({ captureMedia, mediaPlan }),
        }),
      ),
    });
    await selectAndPrepare(result, "C:\\media\\Example");
    await act(() => result.current.screenshots.load());
    expect(result.current.screenshots.view.plan).not.toBeNull();

    await act(() => result.current.screenshots.load());
    expect(result.current.screenshots.view.status).toBe("error");
    expect(result.current.screenshots.view.error).toBe("Screenshot plan could not be refreshed.");

    let generation!: Promise<boolean>;
    act(() => {
      generation = result.current.screenshots.generate("final", [
        { Index: 0, TimestampSeconds: 10, Frame: 240, Source: "manual" },
      ]);
    });
    await waitFor(() => expect(captureCurrent).not.toBeNull());
    expect(result.current.screenshots.view.status).toBe("running");
    successfulCapture.resolve(captureCurrent!);
    await act(() => generation);

    expect(result.current.screenshots.view.status).toBe("ready");
    expect(result.current.screenshots.view.error).toBe("");
    expect(result.current.screenshots.view.plan).not.toBeNull();

    await act(() => result.current.screenshots.load());
    expect(result.current.screenshots.view.error).toBe("Screenshot plan could not be refreshed.");
    await act(() =>
      result.current.screenshots.generate("final", [
        { Index: 0, TimestampSeconds: 10, Frame: 240, Source: "manual" },
      ]),
    );

    expect(result.current.screenshots.view.status).toBe("error");
    expect(result.current.screenshots.view.error).toBe(
      "Screenshot request failed. Retry the request.",
    );
  });

  it("retains a polled terminal screenshot failure before React commits the operation update", async () => {
    const startedAt = "2026-09-21T00:00:00Z";
    const queued = {
      id: "operation-screenshot-failed",
      workflowId: "workflow-new",
      revision: 6,
      sequence: 1,
      command: "capture_media",
      operation: "media",
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
      message: "Screenshot capture failed.",
      completedAt: "2026-09-21T00:00:01Z",
      updatedAt: "2026-09-21T00:00:01Z",
    } as const;
    let terminalCurrent: ReleaseWorkflowCurrent | null = null;
    const captureMedia = vi.fn(async (current: ReleaseWorkflowCurrent) => {
      terminalCurrent = { ...current, operation: failed };
      return { ...current, operation: queued };
    });
    const operation = vi.fn(async () => failed);
    const current = vi.fn(async () => terminalCurrent ?? workflowCurrent("workflow-new", 6));
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({ captureMedia, current, operation }),
        }),
      ),
    });
    await selectAndPrepare(result, "C:\\media\\Example");

    vi.useFakeTimers();
    try {
      let generation!: Promise<boolean>;
      act(() => {
        generation = result.current.screenshots.generate("final", [
          { Index: 0, TimestampSeconds: 10, Frame: 240, Source: "manual" },
        ]);
      });
      await act(async () => vi.advanceTimersByTimeAsync(1000));
      await act(() => generation);

      expect(operation).toHaveBeenCalledOnce();
      expect(result.current.screenshots.view.status).toBe("error");
      expect(result.current.screenshots.view.error).toBe(
        "Screenshot request failed. Retry the request.",
      );
    } finally {
      vi.useRealTimers();
    }
  });

  it("clears a running screenshot command when same-workflow preparation aborts it", async () => {
    const captureMedia = vi.fn(
      (
        _current: ReleaseWorkflowCurrent,
        _instructions: MediaCaptureInstructions,
        _key: string,
        signal: AbortSignal,
      ) =>
        new Promise<ReleaseWorkflowCurrent>((_resolve, reject) => {
          signal.addEventListener(
            "abort",
            () => reject(new DOMException("Screenshot capture aborted.", "AbortError")),
            { once: true },
          );
        }),
    );
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow: workflowPorts({ captureMedia }),
        }),
      ),
    });
    await selectAndPrepare(result, "C:\\media\\Example");

    let generation!: Promise<boolean>;
    act(() => {
      generation = result.current.screenshots.generate("final", [
        { Index: 0, TimestampSeconds: 10, Frame: 240, Source: "manual" },
      ]);
    });
    await waitFor(() => expect(captureMedia).toHaveBeenCalledOnce());
    expect(result.current.screenshots.view.status).toBe("running");

    await act(() => result.current.input.prepare());
    await act(async () => expect(await generation).toBe(false));

    expect(result.current.screenshots.view.status).not.toBe("running");
  });

  it("publishes and reorders opaque final screenshots through workflow commands", async () => {
    const sourcePath = "C:\\media\\Example";
    const captureMedia = vi.fn(
      async (current: ReleaseWorkflowCurrent, instructions: MediaCaptureInstructions) => {
        const revision = current.workflow.revision + 1;
        return {
          ...current,
          workflow: {
            ...current.workflow,
            revision,
            media: { id: "media-final", revision },
          },
          media: {
            savedImagePlanId: instructions.savedImagePlanId,
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
                discId: "disc-one",
                discName: "Disc 1",
                kind: "screenshot" as const,
                purpose: "final" as const,
                selected: true,
                order: 0,
                index: 0,
              },
              ...(instructions.selections?.length
                ? [
                    {
                      id: "screen-generated",
                      discId: "disc-two",
                      discName: "Disc 2",
                      kind: "screenshot" as const,
                      purpose: "final" as const,
                      selected: true,
                      order: 1,
                      index: 1,
                    },
                  ]
                : []),
            ],
            status: "completed" as const,
            createdAt: "2026-07-20T00:00:00Z",
          },
        };
      },
    );
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
              discType: "BDMV",
              discs: [
                {
                  discId: "disc-one",
                  discName: "Disc 1",
                  durationSeconds: 60,
                  frameRate: 24,
                  suggestedSelections: [
                    {
                      DiscID: "disc-one",
                      Index: 1,
                      TimestampSeconds: 10,
                      Frame: 240,
                      Source: "auto",
                    },
                  ],
                },
                {
                  discId: "disc-two",
                  discName: "Disc 2",
                  durationSeconds: 45,
                  frameRate: 30,
                  suggestedSelections: [
                    {
                      DiscID: "disc-two",
                      Index: 2,
                      TimestampSeconds: 20,
                      Frame: 600,
                      Source: "auto",
                    },
                  ],
                },
              ],
              suggestedSelections: [
                {
                  DiscID: "disc-one",
                  Index: 1,
                  TimestampSeconds: 10,
                  Frame: 240,
                  Source: "auto",
                },
                {
                  DiscID: "disc-two",
                  Index: 2,
                  TimestampSeconds: 20,
                  Frame: 600,
                  Source: "auto",
                },
              ],
              autoAdoptSavedImages: true,
              savedTrackerImages: [
                {
                  trackerId: "AITHER",
                  host: "imgbb",
                  url: "https://images.example.invalid/saved.png",
                },
              ],
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
    expect(result.current.screenshots.view.plan?.Discs?.map((disc) => disc.DiscName)).toEqual([
      "Disc 1",
      "Disc 2",
    ]);
    expect(result.current.screenshots.view.plan?.SavedTrackerImages).toEqual([
      {
        TrackerID: "AITHER",
        Host: "imgbb",
        URL: "https://images.example.invalid/saved.png",
      },
    ]);
    expect(result.current.screenshots.view.plan?.SavedImagePlanID).toBe("media-plan-1");
    expect(captureMedia).toHaveBeenCalledWith(
      expect.objectContaining({ workflow: expect.objectContaining({ id: "workflow-new" }) }),
      expect.objectContaining({
        purpose: "final",
        selections: [],
        savedImagePlanId: "media-plan-1",
      }),
      expect.any(String),
      expect.any(AbortSignal),
    );
    await act(() => result.current.screenshots.load());
    expect(captureMedia).toHaveBeenCalledTimes(1);
    expect(result.current.screenshots.view.finalSelectionArtifactIDs).toEqual(["screen-existing"]);
    await act(() => result.current.screenshots.generate("final"));

    expect(captureMedia).toHaveBeenCalledWith(
      expect.objectContaining({ workflow: expect.objectContaining({ id: "workflow-new" }) }),
      expect.objectContaining({
        purpose: "final",
        selections: [
          {
            DiscID: "disc-one",
            Index: 1,
            TimestampSeconds: 10,
            Frame: 240,
            Source: "auto",
          },
          {
            DiscID: "disc-two",
            Index: 2,
            TimestampSeconds: 20,
            Frame: 600,
            Source: "auto",
          },
        ],
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
    let latestCurrent: ReleaseWorkflowCurrent | null = null;
    let inputChanged: () => void = () => undefined;
    const workflow: ReleaseSessionPorts["workflow"] = {
      ...baseWorkflow,
      continue: async (request, signal) => {
        const current = await baseContinue(request, signal);
        latestCurrent = current;
        if (request.goal === "duplicates_decided") {
          duplicateTransitions += 1;
          if (duplicateTransitions === 4) await finalTransition.promise;
        }
        return current;
      },
    };
    const getActiveInput = vi
      .fn<ReleaseSessionPorts["activeInput"]["get"]>()
      .mockResolvedValueOnce({ state: "empty", revision: 0 })
      .mockImplementation(async () => ({
        state: "active",
        revision: 1,
        inputId: "C:\\media\\Example",
        sourceVersion: "C:\\media\\Example",
        current: latestCurrent,
      }));
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          workflow,
          activeInput: {
            get: getActiveInput,
            subscribe: (onChange) => {
              inputChanged = onChange;
              return () => undefined;
            },
          },
        }),
      ),
    });
    await waitFor(() => expect(getActiveInput).toHaveBeenCalledOnce());
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

    act(() => inputChanged());
    await waitFor(() => expect(getActiveInput).toHaveBeenCalledTimes(2));
    expect(result.current.workflow.view.status).toBe("running");
    expect(result.current.duplicates.view.status).toBe("running");

    finalTransition.resolve(true);
    await act(() => duplicateCommand);
    expect(result.current.workflow.view.status).toBe("ready");
    await act(async () => {
      expect(await result.current.menuImages.capture()).toBe(true);
    });
    expect(captureMedia).toHaveBeenCalledOnce();
    unmount();
  });

  it("keeps DVD menu cancellation pending until its accepted command finishes", async () => {
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
    expect(result.current.upload.view.workflowBusy).toBe(true);
    act(() => result.current.menuImages.cancelCapture());
    expect(result.current.upload.view.workflowBusy).toBe(true);
    expect(result.current.workflow.cancellation.pending).toBe(true);
    expect(captureSignal?.aborted).toBe(false);
    capture.resolve(workflowCurrent("workflow-new", 6));
    await act(() => command);
    expect(captureSignal?.aborted).toBe(true);
    expect(result.current.upload.view.workflowBusy).toBe(false);
    expect(result.current.workflow.cancellation.pending).toBe(false);
    expect(result.current.menuImages.view.status).toBe("idle");
    expect(result.current.menuImages.view.staleReason).toBe("");
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

  it("keeps upload actions disabled while another workflow command owns execution", async () => {
    const workflowID = "workflow-pending-description";
    const current = workflowCurrentWithDescriptions(workflowID, 7, "generated source");
    const pending = createDeferred<ReleaseWorkflowCurrent>();
    const saveDescriptionOverride = vi.fn(() => pending.promise);
    const dryRunUploads = vi.fn(async (value: ReleaseWorkflowCurrent) => value);
    const ports = portsFor({
      resumeWorkflowID: workflowID,
      workflow: workflowPorts({
        current: async () => current,
        saveDescriptionOverride,
        dryRunUploads,
      }),
    });
    function UploadPage() {
      const session = useReleaseSession();
      return <TrackerUploadPage facet={session.upload} />;
    }
    const { result } = renderHook(useReleaseSession, {
      wrapper: ({ children }) => (
        <ReleaseSessionProvider ports={ports}>
          {children}
          <UploadPage />
        </ReleaseSessionProvider>
      ),
    });
    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
    const dryRun = screen.getByRole("button", { name: "Run dry run" });
    expect(dryRun).toBeEnabled();
    let save!: Promise<boolean>;
    act(() => {
      save = result.current.descriptions.save("unit3d");
    });
    await waitFor(() => expect(saveDescriptionOverride).toHaveBeenCalledOnce());
    expect(result.current.workflow.view.status).toBe("running");
    fireEvent.click(dryRun);
    expect(dryRunUploads).not.toHaveBeenCalled();
    expect(dryRun).toBeDisabled();
    pending.resolve(current);
    await act(() => save);
    expect(dryRun).toBeEnabled();
    await act(async () => fireEvent.click(dryRun));
    expect(dryRunUploads).toHaveBeenCalledOnce();
  });

  it("exposes successful preview processing with its resolved timestamp", async () => {
    const previewFrame = workflowPorts().previewFrame;
    const processing = {
      requestedRenderer: "libplacebo",
      renderer: "libplacebo",
      tonemapped: true,
      degraded: false,
    };
    const workflow = workflowPorts({
      previewFrame: async (...args: Parameters<typeof previewFrame>) => ({
        ...(await previewFrame(...args)),
        timestampSeconds: 12.5,
        processing,
      }),
    });
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ workflow })),
    });
    await selectAndPrepare(result, "C:\\media\\Example");
    await act(() => result.current.screenshots.previewFrame("", 12));
    expect(result.current.screenshots.view.previewProcessing).toEqual(processing);
    expect(result.current.screenshots.view.previewTimestampSeconds).toBe(12.5);
  });

  it("keeps image hosting busy during capture and hydrates hosted outcomes", async () => {
    const captureComplete = createDeferred<void>();
    const captureMedia = vi.fn(async (current: ReleaseWorkflowCurrent) => {
      await captureComplete.promise;
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
    let capture!: Promise<boolean>;
    act(() => {
      capture = result.current.screenshots.generate("final", [
        { Index: 0, TimestampSeconds: 1, Frame: 24, Source: "manual" },
        { Index: 1, TimestampSeconds: 2, Frame: 48, Source: "manual" },
      ]);
    });
    try {
      expect(result.current.uploadedImages.view.status).toBe("running");
    } finally {
      await act(async () => {
        captureComplete.resolve();
        await capture;
      });
    }
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
    expect(result.current.screenshots.view.artifacts?.artifacts).toContainEqual(
      expect.objectContaining({
        id: "hosted-1",
        kind: "hosted_image",
        url: "https://example.invalid/1.png",
      }),
    );
  });

  it.each(["deselected", "deleted", "unavailable"] as const)(
    "projects hosted URLs for %s sources through loading and re-entry",
    async (change) => {
      const sourcePath = "C:\\media\\Example";
      const captureMedia = async (current: ReleaseWorkflowCurrent) => {
        const revision = current.workflow.revision + 1;
        return {
          ...current,
          workflow: { ...current.workflow, revision, media: { id: "media-links", revision } },
          media: {
            id: "media-links",
            workflowId: current.workflow.id,
            revision,
            release: current.workflow.release!,
            releaseRef: { SourcePath: sourcePath, Generation: 1 },
            projectionSet: current.workflow.trackerProjections!,
            captureFingerprint: "5".repeat(64),
            requirementsFingerprint: "6".repeat(64),
            imageRequirementsPrepared: true,
            artifacts: [
              {
                id: "kept",
                kind: "screenshot" as const,
                purpose: "final" as const,
                selected: true,
              },
              ...(change === "deselected"
                ? [
                    {
                      id: "removed",
                      kind: "screenshot" as const,
                      purpose: "final" as const,
                      selected: false,
                    },
                  ]
                : []),
              {
                id: "hosted-kept",
                kind: "hosted_image" as const,
                purpose: "final" as const,
                selected: true,
                source: "kept",
                url: "https://images.example.invalid/kept.png",
              },
              // Legacy snapshots can retain a selected hosted record after its source changes.
              {
                id: "hosted-removed",
                kind: "hosted_image" as const,
                purpose: "final" as const,
                selected: true,
                source: "removed",
                sourceUnavailable: change === "unavailable",
                url: "https://images.example.invalid/removed.png",
              },
              {
                id: "hosted-disabled",
                kind: "hosted_image" as const,
                purpose: "final" as const,
                selected: false,
                source: "kept",
                url: "https://images.example.invalid/disabled.png",
              },
            ],
            status: "completed" as const,
            createdAt: "2026-07-16T00:00:00Z",
          },
        };
      };
      const expectedURLs = [
        "https://images.example.invalid/kept.png",
        ...(change === "unavailable" ? ["https://images.example.invalid/removed.png"] : []),
      ];
      const { result, unmount } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(portsFor({ workflow: workflowPorts({ captureMedia }) })),
      });
      await selectAndPrepare(result, sourcePath);
      await act(() => result.current.screenshots.generate("final"));
      const current = result.current.workflow.view.current!;
      expect(result.current.uploadedImages.view.uploaded.map((image) => image.url)).toEqual(
        expectedURLs,
      );
      await act(() => result.current.uploadedImages.load());
      expect(result.current.uploadedImages.view.uploaded.map((image) => image.url)).toEqual(
        expectedURLs,
      );
      unmount();

      const resumed = renderHook(useReleaseSession, {
        wrapper: wrapperFor(
          portsFor({
            resumeWorkflowID: current.workflow.id,
            workflow: workflowPorts({ current: async () => current }),
          }),
        ),
      });
      await waitFor(() => expect(resumed.result.current.workflow.view.current?.media).toBeTruthy());
      expect(resumed.result.current.uploadedImages.view.uploaded.map((image) => image.url)).toEqual(
        expectedURLs,
      );
      resumed.unmount();
    },
  );

  it("marks Input dirty while backend reconciliation accepts a changed tracker selection", async () => {
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
    expect(result.current.input.view.preparationDirty).toBe(true);
    await act(async () => {
      started = await result.current.duplicates.run();
    });
    expect(started).toBe(true);
    expect(result.current.duplicates.view.status).toBe("ready");
  });

  it.each([
    { rejected: false, inClient: false, refreshed: false },
    { rejected: true, inClient: false, refreshed: false },
    { rejected: false, inClient: true, refreshed: false },
    { rejected: false, inClient: false, refreshed: true },
  ])(
    "keeps duplicate acknowledgement authoritative when rejected=$rejected inClient=$inClient refreshed=$refreshed",
    async ({ rejected, inClient, refreshed }) => {
      const fixture = workflowPorts();
      const checkDuplicates = vi.fn(
        async (
          current: ReleaseWorkflowCurrent,
          skipRemote: boolean,
          idempotencyKey: string,
          signal: AbortSignal,
        ) => {
          const checked = await fixture.checkDuplicates(
            current,
            skipRemote,
            idempotencyKey,
            signal,
          );
          return {
            ...checked,
            continuation: {
              ...checked.continuation!,
              availableGoals: checked.continuation!.availableGoals.map((goal) =>
                goal.goal === "media_ready"
                  ? { ...goal, available: false, reason: "Review current duplicate evidence." }
                  : goal,
              ),
            },
            dupes: {
              ...checked.dupes!,
              status: "blocked",
              results: [
                {
                  trackerId: "AITHER",
                  uploadReleaseName: "Example.Release.2026.1080p-GRP",
                  matches: inClient ? [{ id: "existing", reason: "in_client" }] : [],
                  reviewReasons: [
                    { code: "policy_review", message: "Review current policy evidence." },
                  ],
                  search: {
                    complete: true,
                    pages: 1,
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
          continuation: {
            ...current.continuation!,
            availableGoals: current.continuation!.availableGoals.map((goal) =>
              goal.goal === "media_ready" ? { ...goal, available: true, reason: "" } : goal,
            ),
          },
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
      if (rejected) decideDuplicates.mockRejectedValue(new Error("Acknowledgement rejected."));
      if (refreshed) {
        decideDuplicates.mockImplementationOnce(async (current) => ({
          ...current,
          workflow: { ...current.workflow, revision: current.workflow.revision + 1 },
          dupes: { ...current.dupes!, id: "renewed-policy-evidence" },
        }));
      }
      const workflow = workflowPorts({ checkDuplicates, decideDuplicates });
      const mediaPlan = vi.spyOn(workflow, "mediaPlan");
      const { result } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(portsFor({ workflow })),
      });

      act(() => result.current.input.selectSource("C:\\media\\Example Release"));
      act(() => result.current.duplicates.chooseTrackers(["AITHER"]));
      await act(() => result.current.input.prepare());
      await act(() => result.current.duplicates.run());

      const workflowID = result.current.workflow.view.current?.workflow.id;
      expect(result.current.navigation.open("screenshots")).toBe(false);
      expect(result.current.navigation.view.access.screenshots.reason).toBe(
        "Review current duplicate evidence.",
      );

      let loaded = true;
      await act(async () => {
        loaded = await result.current.screenshots.load();
      });
      expect(loaded).toBe(false);
      expect(mediaPlan).not.toHaveBeenCalled();
      expect(result.current.screenshots.view.error).toBe("Review current duplicate evidence.");

      act(() => result.current.duplicates.setIgnored("AITHER", true));
      if (inClient) {
        expect(decideDuplicates).not.toHaveBeenCalled();
        expect(result.current.navigation.open("screenshots")).toBe(false);
        return;
      }
      await waitFor(() =>
        expect(decideDuplicates).toHaveBeenCalledWith(
          expect.anything(),
          { AITHER: "ignored" },
          expect.any(String),
          expect.any(AbortSignal),
        ),
      );
      if (rejected) {
        await waitFor(() => expect(result.current.workflow.view.status).toBe("error"));
        expect(result.current.workflow.view.current?.dupes?.results[0].decision).toBe("pending");
        expect(result.current.duplicates.view.assessment?.results[0].decision).not.toBe("ignored");
        expect(result.current.navigation.open("screenshots")).toBe(false);
        return;
      }
      if (refreshed) {
        await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
        expect(decideDuplicates).toHaveBeenCalledOnce();
        expect(result.current.workflow.view.current?.dupes?.results[0].decision).toBe("pending");
        expect(result.current.navigation.open("screenshots")).toBe(false);
        return;
      }
      await waitFor(() => {
        expect(result.current.workflow.view.current?.workflow.id).toBe(workflowID);
        expect(result.current.navigation.open("screenshots")).toBe(true);
        expect(result.current.workflow.view.current?.dupes?.status).toBe("completed");
        expect(result.current.workflow.view.current?.dupes?.results[0]).toEqual(
          expect.objectContaining({ decision: "ignored", status: "completed" }),
        );
      });
      loaded = false;
      await act(async () => {
        loaded = await result.current.screenshots.load();
      });
      expect(loaded).toBe(true);
      expect(mediaPlan).toHaveBeenCalledWith(workflowID, expect.any(AbortSignal));
      expect(result.current.screenshots.view.error).toBe("");
    },
  );

  it.each(["normal", "debug"] as const)(
    "binds staff-token authority only for live decisions (%s)",
    async (executionMode) => {
      const fixture = workflowPorts();
      const checkDuplicates = vi.fn(
        async (
          current: ReleaseWorkflowCurrent,
          skipRemote: boolean,
          idempotencyKey: string,
          signal: AbortSignal,
        ) => {
          const checked = await fixture.checkDuplicates(
            current,
            skipRemote,
            idempotencyKey,
            signal,
          );
          return {
            ...checked,
            projections: { ...checked.projections!, executionMode },
            dupes: {
              ...checked.dupes!,
              status: "blocked",
              id: "token-assessment",
              results: [
                {
                  trackerId: "AITHER",
                  requiresStaffToken: true,
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
      const continueRequest = vi.spyOn(workflow, "continue");
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
      await waitFor(() =>
        expect(continueRequest).toHaveBeenCalledWith(
          expect.objectContaining({
            intent: expect.objectContaining({
              staffTokenConfirmations:
                executionMode === "debug" ? undefined : { AITHER: "token-assessment" },
            }),
          }),
          expect.any(AbortSignal),
        ),
      );
      await waitFor(() => {
        expect(result.current.workflow.view.current?.dupes?.status).toBe("completed");
        expect(result.current.workflow.view.current?.dupes?.results[0]).toEqual(
          expect.objectContaining({ decision: "ignored", status: "completed" }),
        );
      });
    },
  );

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

describe("saved-image plan authority", () => {
  const comparisonMedia = (
    current: ReleaseWorkflowCurrent,
    imported: boolean,
  ): NonNullable<ReleaseWorkflowCurrent["media"]> => ({
    id: "retained-media",
    workflowId: current.workflow.id,
    revision: 8,
    release: current.workflow.release!,
    releaseRef: { SourcePath: current.release!.release.Source.SourcePath, Generation: 1 },
    projectionSet: { id: "projections-1", revision: 1 },
    captureFingerprint: "capture",
    requirementsFingerprint: "requirements",
    imageRequirementsPrepared: false,
    artifacts: [],
    status: "completed",
    createdAt: "2026-11-13T00:00:00Z",
    comparisons: {
      imported,
      sources: ["Source", "Encode"].map((label, index) => ({
        id: String(index + 1),
        label,
        imported,
        overlay: false,
      })),
      rows: [],
      mainSourceId: "1",
      reuseMainScreenshots: false,
      preferences: { count: 1, darkPercent: 0, motionPercent: 0 },
      fingerprint: "restored-content",
    },
  });

  it("scopes comparison errors to the active command after a successful preview", async () => {
    const prepared = workflowCurrentFromPreview(
      workflowCurrent("comparison-preview", 8),
      preview("Example.mkv", 1),
    );
    const current: ReleaseWorkflowCurrent = {
      ...prepared,
      workflow: { ...prepared.workflow, media: { id: "retained-media", revision: 8 } },
      media: comparisonMedia(prepared, false),
    };
    const failure = {
      Code: "invalid_input",
      Operation: "media",
      Message: "Correct the comparison mapping.",
      Recovery: "edit_input",
    };
    const pendingEdit = createDeferred<void>();
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: current.workflow.id,
          workflow: workflowPorts({
            current: async () => current,
            editComparisons: async () => {
              await pendingEdit.promise;
              throw { failure };
            },
            previewFrame: async (value, _disc, timestampSeconds, _command, _signal, sourceID) => ({
              id: "preview",
              workflowId: value.workflow.id,
              workflowRevision: value.workflow.revision,
              release: value.workflow.release!,
              timestampSeconds,
              comparisonSourceId: sourceID,
              contentUrl: "/preview.png",
              expiresAt: "2026-11-13T01:00:00Z",
            }),
          }),
        }),
      ),
    });
    await waitFor(() => expect(result.current.screenshots.comparisons.view.plan).not.toBeNull());
    let edit: Promise<boolean>;
    act(() => {
      edit = result.current.screenshots.comparisons.edit({
        action: "configure",
        plan: current.media!.comparisons!,
      });
    });
    await waitFor(() => expect(result.current.screenshots.view.activeAction).toBe("comparison"));
    expect(result.current.screenshots.view.status).toBe("running");
    expect(result.current.screenshots.comparisons.view.busy).toBe(true);
    await act(async () => {
      pendingEdit.resolve();
      expect(await edit!).toBe(false);
    });
    expect(result.current.screenshots.comparisons.view.error).toBe(failure.Message);
    await act(async () => {
      expect(await result.current.screenshots.comparisons.preview("1", 1)).not.toBeNull();
    });
    expect(result.current.screenshots.comparisons.view.error).toBe("");
  });

  it.each(["edit", "import"] as const)(
    "retains comparison errors after context projection for %s",
    async (action) => {
      let current = workflowCurrentFromPreview(
        workflowCurrent(`comparison-projection-${action}`, 7),
        preview("Example.mkv", 1),
      );
      current = { ...current, projections: undefined, media: undefined };
      const plan = comparisonMedia(current, action === "import").comparisons!;
      const failure = {
        Code: "invalid_input",
        Operation: "media",
        Message: "Correct the comparison mapping.",
        Recovery: "edit_input",
      };
      const { result } = renderHook(useReleaseSession, {
        wrapper: wrapperFor(
          portsFor({
            resumeWorkflowID: current.workflow.id,
            workflow: workflowPorts({
              current: async () => current,
              continue: async () => {
                current = { ...current, workflow: { ...current.workflow, revision: 8 } };
                return current;
              },
              editComparisons: async () => {
                throw { failure };
              },
            }),
          }),
        ),
      });
      await waitFor(() => expect(result.current.workflow.view.current?.workflow.revision).toBe(7));
      await act(() => result.current.duplicates.chooseTrackers(["HDS"]));
      await act(async () => {
        const accepted =
          action === "edit"
            ? await result.current.screenshots.comparisons.edit({ action: "configure", plan })
            : await result.current.screenshots.comparisons.importImages(
                [new File(["png"], "comparison.png", { type: "image/png" })],
                plan,
              );
        expect(accepted).toBe(false);
      });
      expect(result.current.workflow.view.current?.workflow.revision).toBe(8);
      expect(result.current.screenshots.comparisons.view.error).toBe(failure.Message);
    },
  );

  it("replaces only the selected duplicate-mapped imported image", async () => {
    const prepared = workflowCurrentFromPreview(
      workflowCurrent("comparison-replacement", 8),
      preview("Example.mkv", 1),
    );
    const media = comparisonMedia(prepared, true);
    const plan = {
      ...media.comparisons!,
      rows: [
        {
          id: "row",
          excluded: false,
          cells: [
            { sourceId: "1", artifactId: "first" },
            { sourceId: "1", artifactId: "second" },
          ],
        },
      ],
    };
    const current: ReleaseWorkflowCurrent = {
      ...prepared,
      workflow: { ...prepared.workflow, media: { id: "retained-media", revision: 8 } },
      media: { ...media, comparisons: plan },
    };
    const edit = vi.fn<ReleaseSessionPorts["workflow"]["editComparisons"]>(async () => current);
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: current.workflow.id,
          workflow: workflowPorts({ current: async () => current, editComparisons: edit }),
        }),
      ),
    });
    await waitFor(() => expect(result.current.screenshots.comparisons.view.plan).not.toBeNull());
    await act(() =>
      result.current.screenshots.comparisons.importImages(
        [new File(["png"], "replacement.png", { type: "image/png" })],
        plan,
        { rowID: "row", sourceID: "1", artifactID: "first" },
      ),
    );
    const instructions = edit.mock.calls[0]?.[1];
    expect(instructions?.plan.rows[0]?.cells.map((cell) => cell.artifactId)).toEqual([
      "second",
      "resource-replacement.png",
    ]);
  });

  it("restores comparisons before dupes without ordinary planning or hosting", async () => {
    const workflowID = "workflow-reopened-comparisons";
    let current = workflowCurrentFromPreview(
      workflowCurrent(workflowID, 7),
      preview("C:\\media\\Example.Release.2026.mkv", 1),
    );
    current = {
      ...current,
      continuation: {
        ...current.continuation,
        comparisonRestorePending: true,
        availableGoals: [
          ...current.continuation.availableGoals.filter(
            (goal) => goal.goal !== "media_ready" && goal.goal !== "trackers_projected",
          ),
          { goal: "trackers_projected", available: true },
          { goal: "media_ready", available: false, reason: "Run duplicate checking first." },
        ],
      },
    };
    const events: string[] = [];
    const captureMedia = vi.fn(async (value: ReleaseWorkflowCurrent) => value);
    const uploadImages = vi.fn(async (value: ReleaseWorkflowCurrent) => value);
    const baseMediaPlan = workflowPorts().mediaPlan;
    const continueWorkflow = vi.fn(async (request: ContinueReleaseWorkflowRequest) => {
      events.push("project");
      expect(request.goal).toBe("trackers_projected");
      expect(request.intent.media).toBeUndefined();
      return current;
    });
    const edit = vi.fn(
      async (_value: ReleaseWorkflowCurrent, instructions: ComparisonInstructions) => {
        events.push("restore");
        expect(instructions.action).toBe("restore");
        expect(instructions.plan.sources).toEqual([]);
        current = {
          ...current,
          workflow: {
            ...current.workflow,
            revision: 8,
            media: { id: "retained-media", revision: 8 },
          },
          continuation: { ...current.continuation, comparisonRestorePending: false },
          media: comparisonMedia(current, true),
        };
        return current;
      },
    );
    const ports = portsFor({
      resumeWorkflowID: workflowID,
      workflow: workflowPorts({
        current: async () => current,
        continue: continueWorkflow,
        editComparisons: edit,
        captureMedia,
        uploadImages,
        mediaPlan: async (...args) => {
          events.push("plan");
          return baseMediaPlan(...args);
        },
      }),
    });
    function RestoredScreenshots() {
      const session = useReleaseSession();
      return (
        <ScreenshotsPage
          facet={session.screenshots}
          setLightboxImage={vi.fn()}
          setLightboxAlt={vi.fn()}
        />
      );
    }
    const { result } = renderHook(useReleaseSession, {
      wrapper: ({ children }) => (
        <ReleaseSessionProvider ports={ports}>
          {children}
          <RestoredScreenshots />
        </ReleaseSessionProvider>
      ),
    });
    await waitFor(() =>
      expect(result.current.screenshots.comparisons.view.restorePending).toBe(true),
    );
    await act(() => result.current.duplicates.chooseTrackers(["HDS"]));
    await act(async () => {
      screen.getByRole("button", { name: "Restore retained comparisons" }).click();
    });
    await waitFor(() => expect(result.current.screenshots.comparisons.view.plan).not.toBeNull());
    expect(events).toEqual(["project", "restore"]);
    expect(result.current.screenshots.view.plan).toBeNull();
    expect(
      result.current.screenshots.comparisons.view.plan?.sources.map((source) => source.label),
    ).toEqual(["Source", "Encode"]);
    expect(result.current.screenshots.view.autoLoadPlan).toBe(false);
    expect(captureMedia).not.toHaveBeenCalled();
    expect(uploadImages).not.toHaveBeenCalled();
  });

  it.each([
    "metadata refresh",
    "tracker reassessment",
    "another tab",
    "another tab during plan load",
    "collection disabled",
    "collection flag absent",
  ])("refreshes saved-image authority after %s without resetting frame drafts", async (change) => {
    const sourcePath = "C:\\media\\Example.Release.2026.mkv";
    let planID = "plan-before";
    let autoAdoptSavedImages: boolean | undefined = true;
    let externalSnapshot: ActiveInputSnapshot = { state: "empty", revision: 0 };
    let notifyChange: () => void = () => undefined;
    const deferredPlan =
      createDeferred<Awaited<ReturnType<ReleaseSessionPorts["workflow"]["mediaPlan"]>>>();
    let deferNextPlan = false;
    const mediaPlan = vi.fn(async (workflowID: string) => {
      if (deferNextPlan) {
        deferNextPlan = false;
        return deferredPlan.promise;
      }
      return {
        id: planID,
        workflowId: workflowID,
        revision: 1,
        release: { id: "release-one", revision: 1 },
        projectionSet: { id: "projections-one", revision: 1 },
        durationSeconds: 120,
        frameRate: 24,
        suggestedSelections: [
          { DiscID: "", Index: 0, TimestampSeconds: 10, Frame: 240, Source: "auto" },
        ],
        autoAdoptSavedImages,
        savedTrackerImages: [
          { trackerId: "AITHER", host: "imgbb", url: "https://images.example.invalid/saved.png" },
        ],
        createdAt: "2026-07-20T00:00:00Z",
      };
    });
    const captureMedia = vi.fn(async (current: ReleaseWorkflowCurrent) => current);
    const ports = portsFor({
      workflow: workflowPorts({ mediaPlan, captureMedia }),
      activeInput: {
        get: async () => externalSnapshot,
        subscribe: (callback) => {
          notifyChange = callback;
          return () => undefined;
        },
      },
    });
    function ScreenshotEditor() {
      const session = useReleaseSession();
      return session.navigation.view.access.screenshots.available ? (
        <ScreenshotsPage
          facet={session.screenshots}
          setLightboxImage={() => undefined}
          setLightboxAlt={() => undefined}
        />
      ) : null;
    }
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: ({ children }) => (
        <ReleaseSessionProvider ports={ports}>
          {children}
          <ScreenshotEditor />
        </ReleaseSessionProvider>
      ),
    });
    try {
      await selectAndPrepare(result, sourcePath);
      await waitFor(() =>
        expect(result.current.screenshots.view.plan?.SavedImagePlanID).toBe("plan-before"),
      );
      act(() =>
        result.current.screenshots.changeSelection(0, { TimestampSeconds: 42, Frame: 1008 }),
      );
      const selections = result.current.screenshots.view.selections;
      let pendingLoad: Promise<boolean> | undefined;
      const initialPlan = await mediaPlan.mock.results[0].value;
      if (change === "another tab during plan load") {
        deferNextPlan = true;
        act(() => {
          pendingLoad = result.current.screenshots.load();
        });
      }
      planID = "plan-after";
      if (change.startsWith("collection")) {
        autoAdoptSavedImages = change === "collection disabled" ? false : undefined;
      }
      if (change.startsWith("another tab")) {
        const current = result.current.workflow.view.current!;
        const active = result.current.input.view.activeInput;
        externalSnapshot = {
          state: "active",
          revision: active.revision,
          inputId: active.inputID,
          sourceVersion: active.sourceVersion,
          current: {
            ...current,
            workflow: {
              ...current.workflow,
              revision: current.workflow.revision + 1,
              trackerProjections: {
                id: current.workflow.trackerProjections!.id,
                revision: current.workflow.trackerProjections!.revision + 1,
              },
            },
          },
        };
        act(notifyChange);
      } else {
        if (change === "metadata refresh") {
          await act(() =>
            result.current.input.prepareSource(sourcePath, result.current.input.view.intent),
          );
        }
        await act(() => result.current.duplicates.run());
      }
      await waitFor(() =>
        expect(result.current.screenshots.view.plan?.SavedImagePlanID).toBe("plan-after"),
      );
      if (pendingLoad) {
        deferredPlan.resolve(initialPlan);
        await act(() => pendingLoad);
        expect(result.current.screenshots.view.plan?.SavedImagePlanID).toBe("plan-after");
      }
      expect(result.current.screenshots.view.selections).toEqual(selections);
      expect(mediaPlan).toHaveBeenCalledTimes(pendingLoad ? 3 : 2);
      if (change.startsWith("collection")) {
        expect(captureMedia).toHaveBeenCalledTimes(1);
      } else {
        await waitFor(() =>
          expect(captureMedia).toHaveBeenCalledWith(
            expect.anything(),
            expect.objectContaining({ savedImagePlanId: "plan-after", selections: [] }),
            expect.any(String),
            expect.any(AbortSignal),
          ),
        );
      }
    } finally {
      unmount();
    }
  });
});

it("requests single/all Blu-ray details through exact preparation authority and retains manual selection", async () => {
  const workflowID = "workflow-bluray-details";
  const sourcePath = "Example.Release.2026.mkv";
  const listing = {
    ...preview(sourcePath, 1),
    Bluray: {
      SelectedReleaseID: "102",
      Threshold: 0,
      Candidates: [
        { ReleaseID: "101", Title: "Example A", Score: 0, Accepted: false },
        { ReleaseID: "102", Title: "Example B", Score: 0, Accepted: true },
      ],
    },
  } as MetadataPreview;
  let current = workflowCurrentFromPreview(workflowCurrent(workflowID, 2), listing);
  current = {
    ...current,
    factInstructions: {
      ...current.factInstructions!,
      instructions: {
        ...current.factInstructions!.instructions,
        Bluray: { Mode: "manual", Release: "102" },
        SourceLookup: "https://tracker.example/torrents/blu-ray.com/123",
      },
    },
  };
  const continuation = vi.fn(async (request: ContinueReleaseWorkflowRequest) => {
    if (request.intent.preparation) {
      current = {
        ...current,
        workflow: { ...current.workflow, revision: current.workflow.revision + 1 },
        factInstructions: {
          ...current.factInstructions!,
          instructions: request.intent.preparation.Instructions,
        },
        operation: {
          id: "detail-operation",
          workflowId: workflowID,
          revision: current.workflow.revision + 1,
          sequence: 1,
          command: "prepare_release",
          operation: "preparation",
          status: "completed",
          progress: 100,
          completed: 1,
          total: 1,
          startedAt: "2026-07-20T00:00:00Z",
          updatedAt: "2026-07-20T00:00:00Z",
          items: [
            {
              id: "101",
              kind: "bluray_detail",
              label: "Example A",
              status: "completed",
              blurayCandidate: {
                ...listing.Bluray!.Candidates[0],
                DetailsStatus: "complete",
                Score: 98,
                ReleaseDate: "2026-01-02",
              },
            },
          ],
        },
      };
    }
    return current;
  });
  function CandidateCards() {
    return (
      <BlurayCandidatesPage
        facet={useReleaseSession().input}
        setLightboxImage={() => undefined}
        setLightboxAlt={() => undefined}
      />
    );
  }
  const Wrapper = wrapperFor(
    portsFor({
      resumeWorkflowID: workflowID,
      workflow: workflowPorts({ current: async () => current, continue: continuation }),
    }),
  );
  const { result, unmount } = renderHook(useReleaseSession, {
    wrapper: ({ children }) => (
      <Wrapper>
        {children}
        <CandidateCards />
      </Wrapper>
    ),
  });
  await waitFor(() => expect(result.current.input.view.selectedSource).toBe(sourcePath));
  for (const [releaseID, request] of [
    ["101", { ReleaseID: "101", All: false }],
    [undefined, { ReleaseID: "", All: true }],
  ] as const) {
    await act(() => result.current.input.fetchCandidateDetails(releaseID));
    expect(continuation).toHaveBeenCalledWith(
      expect.objectContaining({
        authority: { workflowId: workflowID, expectedRevision: current.workflow.revision - 1 },
        intent: expect.objectContaining({
          preparation: expect.objectContaining({
            Force: true,
            Instructions: expect.objectContaining({ Bluray: { Mode: "manual", Release: "102" } }),
            Controls: expect.objectContaining({ BlurayDetails: request }),
          }),
        }),
      }),
      expect.any(AbortSignal),
    );
    const card = within(screen.getByRole("heading", { name: "Example A" }).closest("section")!);
    expect(card.getByText("98.0/100")).toBeVisible();
    expect(card.getByText("2026-01-02")).toBeVisible();
  }
  act(() => result.current.input.changeSourceLookupURL("https://www.blu-ray.com/movies/New/103/"));
  await act(() => result.current.input.fetchCandidateDetails("101"));
  const editedRequest = continuation.mock.calls
    .map(([request]) => request)
    .reverse()
    .find((request) => request.intent.preparation?.Controls?.BlurayDetails);
  expect(editedRequest?.intent.preparation?.Instructions).toMatchObject({
    Bluray: null,
    SourceLookup: "https://www.blu-ray.com/movies/New/103/",
  });
  act(() =>
    result.current.input.changeSourceLookupURL("https://tracker.example/torrents/blu-ray.com/123"),
  );
  await act(() => result.current.input.selectCandidate("101"));
  expect(current.factInstructions!.instructions.Bluray).toEqual({ Mode: "manual", Release: "101" });
  await act(() => result.current.input.setCandidateSelection("skip"));
  expect(current.factInstructions!.instructions.Bluray).toEqual({ Mode: "skip", Release: "" });
  await act(() => result.current.input.setCandidateSelection("auto"));
  expect(current.factInstructions!.instructions.Bluray).toEqual({ Mode: "auto", Release: "" });
  expect(current.factInstructions!.instructions.SourceLookup).toBe(
    "https://tracker.example/torrents/blu-ray.com/123",
  );
  unmount();
});

it.each([
  "owned preparation",
  "owned legacy then shared",
  "owned shared then legacy",
  "owned input cancellation",
  "owned readiness after tracker change",
  "existing preparation",
  "existing audio analysis",
  "concurrent preparation",
])("limits candidate cancellation to its own operation: %s", async (scenario) => {
  const workflowID = "workflow-bluray-cancellation";
  const sourcePath = "Example.Release.2026.mkv";
  const ownsOperation = scenario.startsWith("owned");
  const operation: WorkflowOperationStatus = {
    id: ownsOperation ? "candidate-operation" : "existing-operation",
    workflowId: workflowID,
    revision: 2,
    sequence: 1,
    command:
      scenario === "owned readiness after tracker change" ? "evaluate_input_readiness" : "fixture",
    operation: scenario === "existing audio analysis" ? "analyze_audio" : "preparation",
    status: "running",
    progress: 0,
    completed: 0,
    total: 1,
    startedAt: "2026-07-20T00:00:00Z",
    updatedAt: "2026-07-20T00:00:00Z",
  };
  const canceled: WorkflowOperationStatus = { ...operation, status: "canceled", sequence: 2 };
  let current = workflowCurrentFromPreview(workflowCurrent(workflowID, 2), preview(sourcePath, 1));
  if (scenario.startsWith("existing")) current = { ...current, operation };
  const terminal = createDeferred<WorkflowOperationStatus>();
  const continuation = vi.fn(async (request: ContinueReleaseWorkflowRequest) => {
    if (scenario === "owned readiness after tracker change")
      expect(request.intent.trackerIds).toEqual(["BETA"]);
    current = { ...current, operation };
    return ownsOperation ? { ...current, preparationOperationId: operation.id } : current;
  });
  const cancelOperation = vi.fn(async () => {
    expect(
      JSON.parse(window.sessionStorage.getItem("upbrr.cancellingReleaseOperation") || "null"),
    ).toEqual({ workflowID, operationID: operation.id });
    current = { ...current, operation: canceled };
    terminal.resolve(canceled);
    return canceled;
  });
  const cancelWorkflow = vi.fn();
  const { result, unmount } = renderHook(useReleaseSession, {
    wrapper: wrapperFor(
      portsFor({
        resumeWorkflowID: workflowID,
        workflow: workflowPorts({
          current: async () => current,
          continue: continuation,
          operation: async () => terminal.promise,
          cancelOperation,
          cancel: cancelWorkflow,
        }),
      }),
    ),
  });
  await waitFor(() => expect(result.current.input.view.selectedSource).toBe(sourcePath));
  if (scenario === "owned readiness after tracker change")
    act(() => result.current.duplicates.chooseTrackers(["BETA"]));
  expect(result.current.input.view.candidateDetailsCancelable).toBe(false);
  let pending: Promise<boolean> | undefined;
  act(() => {
    pending = result.current.input.fetchCandidateDetails("101");
  });
  await waitFor(() => expect(continuation).toHaveBeenCalled());
  await waitFor(() =>
    expect(result.current.input.view.candidateDetailsCancelable).toBe(ownsOperation),
  );
  if (scenario === "owned legacy then shared" || scenario === "owned shared then legacy") {
    await act(async () => {
      const first =
        scenario === "owned legacy then shared"
          ? result.current.input.cancelCandidateDetails()
          : result.current.workflow.cancel();
      const second =
        scenario === "owned legacy then shared"
          ? result.current.workflow.cancel()
          : result.current.input.cancelCandidateDetails();
      expect(await first).toBe(true);
      expect(await second).toBe(true);
    });
    expect(cancelOperation).toHaveBeenCalledOnce();
    expect(result.current.workflow.cancellation.pending).toBe(false);
    expect(result.current.workflow.cancellation.message).toMatch(/^Work canceled/);
    expect(result.current.input.view.error).toBe("");
  } else if (scenario === "owned input cancellation") {
    act(() => result.current.input.cancelPreparation());
    await waitFor(() => expect(cancelOperation).toHaveBeenCalled());
  } else {
    await act(() => result.current.input.cancelCandidateDetails());
  }
  if (ownsOperation) {
    expect(cancelOperation).toHaveBeenCalledWith(workflowID, operation.id, expect.any(AbortSignal));
    expect(window.sessionStorage.getItem("upbrr.cancellingReleaseOperation")).toBeNull();
    if (!scenario.includes("shared"))
      await waitFor(() => expect(result.current.input.view.status).toBe("cancelled"));
    else expect(result.current.workflow.view.current?.operation?.status).toBe("canceled");
  } else {
    expect(cancelOperation).not.toHaveBeenCalled();
  }
  expect(cancelWorkflow).not.toHaveBeenCalled();
  unmount();
  terminal.resolve(canceled);
  await act(() => pending);
});

it.each(["owned", "foreign preparation", "foreign audio"] as const)(
  "keeps shared candidate cancellation scoped while its reply is pending: %s",
  async (scenario) => {
    const workflowID = "workflow-candidate-pending-cancel";
    const sourcePath = "Example.Release.2026.mkv";
    const ownsOperation = scenario === "owned";
    const operation: WorkflowOperationStatus = {
      id: ownsOperation ? "owned-candidate" : "foreign-operation",
      workflowId: workflowID,
      revision: 2,
      sequence: 1,
      command: "fixture",
      operation: scenario === "foreign audio" ? "analyze_audio" : "preparation",
      status: "running",
      progress: 0,
      completed: 0,
      total: 1,
      startedAt: "2026-07-20T00:00:00Z",
      updatedAt: "2026-07-20T00:00:00Z",
    };
    const canceled = { ...operation, status: "canceled" as const, sequence: 2 };
    let current = workflowCurrentFromPreview(
      workflowCurrent(workflowID, 2),
      preview(sourcePath, 1),
    );
    const reply = createDeferred<ReleaseWorkflowCurrent>();
    const continuation = vi.fn(() => reply.promise);
    const cancelOperation = vi.fn(async () => {
      current = { ...current, operation: canceled };
      return canceled;
    });
    const cancelWorkflow = vi.fn();
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({
            current: async () => current,
            continue: continuation,
            operation: async () => operation,
            cancelOperation,
            cancel: cancelWorkflow,
          }),
        }),
      ),
    });
    await waitFor(() => expect(result.current.input.view.selectedSource).toBe(sourcePath));
    let inspection!: Promise<boolean>;
    act(() => {
      inspection = result.current.input.fetchCandidateDetails("101");
    });
    await waitFor(() => expect(continuation).toHaveBeenCalledOnce());
    let cancellation!: Promise<boolean>;
    act(() => {
      cancellation = result.current.workflow.cancel();
    });
    expect(result.current.workflow.cancellation.pending).toBe(true);
    expect(cancelOperation).not.toHaveBeenCalled();
    await act(async () => {
      current = { ...current, operation };
      reply.resolve(ownsOperation ? { ...current, preparationOperationId: operation.id } : current);
      expect(await cancellation).toBe(ownsOperation);
      expect(await inspection).toBe(false);
    });
    if (ownsOperation)
      expect(cancelOperation).toHaveBeenCalledWith(
        workflowID,
        operation.id,
        expect.any(AbortSignal),
      );
    else expect(cancelOperation).not.toHaveBeenCalled();
    expect(cancelWorkflow).not.toHaveBeenCalled();
    expect(result.current.workflow.cancellation.pending).toBe(false);
    expect(result.current.input.view.status).toBe("cancelled");
    expect(result.current.input.view.selectedSource).toBe(sourcePath);
    unmount();
  },
);

it.each([
  "owned",
  "owned after refresh",
  "owned marker with newer operation",
  "owned marker without current operation",
  "owned marker lookup failed",
  "owned marker already canceled",
  "owned marker already completed",
  "other tab",
  "correction only",
])("handles Input Cancel before candidate acceptance: %s", async (scenario) => {
  const workflowID = "workflow-bluray-pending-cancel";
  const sourcePath = "Example.Release.2026.mkv";
  const operation: WorkflowOperationStatus = {
    id: "candidate-operation",
    workflowId: workflowID,
    revision: 2,
    sequence: 1,
    command: "fixture",
    operation: "preparation",
    status: "running",
    progress: 0,
    completed: 0,
    total: 1,
    startedAt: "2026-07-20T00:00:00Z",
    updatedAt: "2026-07-20T00:00:00Z",
  };
  const current = workflowCurrentFromPreview(
    workflowCurrent(workflowID, 2),
    preview(sourcePath, 1),
  );
  const accepted = createDeferred<ReleaseWorkflowCurrent>();
  const terminal = createDeferred<WorkflowOperationStatus>();
  let calls = 0;
  const continuation = vi.fn(
    async (_request: ContinueReleaseWorkflowRequest, _signal: AbortSignal) => {
      calls += 1;
      if (scenario === "owned after refresh" && calls === 1)
        return {
          ...current,
          workflow: { ...current.workflow, revision: 3 },
          operation: { ...operation, id: "completed-operation", status: "completed" as const },
        };
      return accepted.promise;
    },
  );
  const cancelOperation = vi.fn(async () => operation);
  const operationStatus = vi.fn(async (_workflowID: string, operationID: string) => {
    expect(operationID).toBe(operation.id);
    if (scenario === "owned marker lookup failed") throw new Error("status unavailable");
    if (scenario.startsWith("owned marker") && operationStatus.mock.calls.length === 1)
      return {
        ...operation,
        status: scenario.endsWith("already canceled")
          ? ("canceled" as const)
          : scenario.endsWith("already completed")
            ? ("completed" as const)
            : operation.status,
      };
    return terminal.promise;
  });
  const { result, unmount } = renderHook(useReleaseSession, {
    wrapper: wrapperFor(
      portsFor({
        resumeWorkflowID: workflowID,
        workflow: workflowPorts({
          current: async () => current,
          continue: continuation,
          cancelOperation,
          operation: operationStatus,
        }),
      }),
    ),
  });
  await waitFor(() => expect(result.current.input.view.selectedSource).toBe(sourcePath));
  let pending!: Promise<boolean>;
  act(() => {
    pending = result.current.input.fetchCandidateDetails("101");
  });
  await waitFor(() =>
    expect(continuation).toHaveBeenCalledTimes(scenario === "owned after refresh" ? 2 : 1),
  );
  expect(result.current.input.view.candidateDetailsCancelable).toBe(false);
  act(() => result.current.input.cancelPreparation());
  expect(continuation.mock.calls[0][1].aborted).toBe(false);
  expect(result.current.input.view.status).not.toBe("cancelled");
  act(() => result.current.input.cancelPreparation());
  accepted.resolve(
    scenario.startsWith("owned marker")
      ? {
          ...current,
          operation:
            scenario === "owned marker without current operation"
              ? undefined
              : { ...operation, id: "another-tabs-operation" },
          preparationOperationId: operation.id,
        }
      : scenario.startsWith("owned")
        ? { ...current, operation, preparationOperationId: operation.id }
        : scenario === "other tab"
          ? { ...current, operation: { ...operation, id: "another-tabs-operation" } }
          : { ...current, workflow: { ...current.workflow, revision: 3 } },
  );
  const owned =
    scenario.startsWith("owned") &&
    scenario !== "owned marker lookup failed" &&
    !scenario.includes("already");
  if (owned) {
    await waitFor(() => expect(cancelOperation).toHaveBeenCalledOnce());
    expect(result.current.input.view.status).not.toBe("cancelled");
    terminal.resolve({ ...operation, status: "canceled" });
  }
  await act(() => pending);
  if (owned)
    expect(cancelOperation).toHaveBeenCalledWith(workflowID, operation.id, expect.any(AbortSignal));
  else expect(cancelOperation).not.toHaveBeenCalled();
  expect(continuation).toHaveBeenCalledTimes(scenario === "owned after refresh" ? 2 : 1);
  if (scenario === "owned marker lookup failed") {
    expect(result.current.input.view.status).toBe("error");
    expect(result.current.input.view.error).toMatch(/could not confirm cancellation/i);
  } else if (scenario === "owned marker already completed") {
    expect(result.current.input.view.status).toBe("error");
    expect(result.current.input.view.error).toMatch(/finished before cancellation/i);
  } else expect(result.current.input.view.status).toBe("cancelled");
  unmount();
});

it.each([
  "unmount",
  "deadline",
  "cancel request deadline",
  "acceptance deadline",
  "source change deadline",
])("bounds candidate cancellation polling: %s", async (end) => {
  const cancelBeforeAcceptance = end === "acceptance deadline" || end === "source change deadline";
  const workflowID = "workflow-bluray-bounded-cancel";
  const sourcePath = "Example.Release.2026.mkv";
  const operation: WorkflowOperationStatus = {
    id: "candidate-operation",
    workflowId: workflowID,
    revision: 2,
    sequence: 1,
    command: "prepare_release",
    operation: "preparation",
    status: "running",
    progress: 0,
    completed: 0,
    total: 1,
    startedAt: "2026-07-20T00:00:00Z",
    updatedAt: "2026-07-20T00:00:00Z",
  };
  const canceled = { ...operation, status: "canceled" as const };
  const accepted = createDeferred<ReleaseWorkflowCurrent>();
  const current = workflowCurrentFromPreview(
    workflowCurrent(workflowID, 2),
    preview(sourcePath, 1),
  );
  const waits: ReturnType<typeof createDeferred<WorkflowOperationStatus>>[] = [];
  const pollSignals: AbortSignal[] = [];
  const pendingOperation = (signal: AbortSignal) => {
    const wait = createDeferred<WorkflowOperationStatus>();
    waits.push(wait);
    signal.addEventListener("abort", () => wait.reject(signal.reason), { once: true });
    return wait.promise;
  };
  const poll = vi.fn(async (_workflowID: string, _operationID: string, signal: AbortSignal) => {
    pollSignals.push(signal);
    return pendingOperation(signal);
  });
  let cancellationSignal!: AbortSignal;
  const cancel = vi.fn(async (_workflowID: string, _operationID: string, signal: AbortSignal) => {
    cancellationSignal = signal;
    return end === "cancel request deadline" || cancelBeforeAcceptance
      ? pendingOperation(signal)
      : operation;
  });
  const { result, unmount } = renderHook(useReleaseSession, {
    wrapper: wrapperFor(
      portsFor({
        resumeWorkflowID: workflowID,
        workflow: workflowPorts({
          current: async () => current,
          continue: async () =>
            cancelBeforeAcceptance
              ? accepted.promise
              : { ...current, operation, preparationOperationId: operation.id },
          operation: poll,
          cancelOperation: cancel,
        }),
      }),
    ),
  });
  await waitFor(() => expect(result.current.input.view.selectedSource).toBe(sourcePath));
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  let pending!: Promise<boolean>;
  let cancellation!: Promise<boolean>;
  let cancellationSettled = false;
  try {
    act(() => {
      pending = result.current.input.fetchCandidateDetails("101");
    });
    await act(async () => vi.advanceTimersByTimeAsync(0));
    if (cancelBeforeAcceptance) {
      expect(result.current.input.view.candidateDetailsCancelable).toBe(false);
      act(() => result.current.input.cancelPreparation());
      await act(async () => vi.advanceTimersByTimeAsync(1000));
      accepted.resolve({ ...current, operation, preparationOperationId: operation.id });
      await act(async () => vi.advanceTimersByTimeAsync(0));
    }
    if (!cancelBeforeAcceptance)
      expect(result.current.input.view.candidateDetailsCancelable).toBe(true);
    act(() => {
      cancellation = cancelBeforeAcceptance
        ? pending
        : result.current.input.cancelCandidateDetails();
      void cancellation.then(() => {
        cancellationSettled = true;
      });
    });
    await act(async () => vi.advanceTimersByTimeAsync(1000));
    if (end === "cancel request deadline" || cancelBeforeAcceptance)
      expect(pollSignals).not.toContain(cancellationSignal);
    else expect(pollSignals).toContain(cancellationSignal);
    expect(cancellationSettled).toBe(false);
    expect(cancellationSignal.aborted).toBe(false);
    if (end === "source change deadline") {
      act(() => result.current.input.selectSource("Another.Release.2026.mkv"));
      expect(cancel).toHaveBeenCalledOnce();
      expect(cancel).toHaveBeenCalledWith(workflowID, operation.id, cancellationSignal);
    }
    if (end === "unmount") unmount();
    else
      await act(async () => vi.advanceTimersByTimeAsync(cancelBeforeAcceptance ? 28_000 : 29_000));
    expect(cancellationSignal.aborted).toBe(true);
    await act(() => Promise.all([pending, cancellation]));
    expect(cancellationSettled).toBe(true);
    const calls = poll.mock.calls.length;
    await act(async () => vi.advanceTimersByTimeAsync(2000));
    expect(poll).toHaveBeenCalledTimes(calls);
    if (end === "source change deadline") {
      expect(result.current.input.view.sourceDraft).toBe("Another.Release.2026.mkv");
    } else if (end !== "unmount") {
      expect(result.current.input.view.status).toBe("error");
      expect(result.current.input.view.error).toMatch(/could not confirm cancellation/i);
    }
  } finally {
    unmount();
    await act(async () => {
      waits.forEach((wait) => wait.resolve(canceled));
      await Promise.all([pending, cancellation]);
    });
    vi.useRealTimers();
  }
});

it("bounds Input Cancel while candidate acceptance stays pending", async () => {
  const workflowID = "workflow-bluray-stalled-acceptance";
  const sourcePath = "Example.Release.2026.mkv";
  const current = workflowCurrentFromPreview(
    workflowCurrent(workflowID, 2),
    preview(sourcePath, 1),
  );
  const accepted = createDeferred<ReleaseWorkflowCurrent>();
  let signal!: AbortSignal;
  const continuation = vi.fn(
    async (_request: ContinueReleaseWorkflowRequest, pendingSignal: AbortSignal) => {
      signal = pendingSignal;
      signal.addEventListener("abort", () => accepted.reject(signal.reason), { once: true });
      return accepted.promise;
    },
  );
  const cancelOperation = vi.fn();
  const { result, unmount } = renderHook(useReleaseSession, {
    wrapper: wrapperFor(
      portsFor({
        resumeWorkflowID: workflowID,
        workflow: workflowPorts({
          current: async () => current,
          continue: continuation,
          cancelOperation,
        }),
      }),
    ),
  });
  await waitFor(() => expect(result.current.input.view.selectedSource).toBe(sourcePath));
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  let pending!: Promise<boolean>;
  let settled = false;
  try {
    act(() => {
      pending = result.current.input.fetchCandidateDetails("101");
      void pending.then(() => {
        settled = true;
      });
    });
    await act(async () => vi.advanceTimersByTimeAsync(0));
    expect(continuation).toHaveBeenCalledOnce();
    act(() => result.current.input.cancelPreparation());
    await act(async () => vi.advanceTimersByTimeAsync(15_000));
    act(() => result.current.input.cancelPreparation());
    await act(async () => vi.advanceTimersByTimeAsync(14_999));
    expect(signal.aborted).toBe(false);
    expect(settled).toBe(false);
    expect(result.current.input.view.status).not.toBe("cancelled");
    await act(async () => vi.advanceTimersByTimeAsync(1));
    expect(signal.aborted).toBe(true);
    await act(async () => expect(await pending).toBe(false));
    expect(result.current.input.view.status).toBe("error");
    expect(result.current.input.view.error).toMatch(/could not confirm cancellation/i);
    expect(cancelOperation).not.toHaveBeenCalled();
  } finally {
    unmount();
    await act(() => pending);
    vi.useRealTimers();
  }
});

it("reports uncertain candidate cancellation when the acceptance response is lost", async () => {
  const workflowID = "workflow-bluray-uncertain-cancel";
  const sourcePath = "Example.Release.2026.mkv";
  const current = workflowCurrentFromPreview(
    workflowCurrent(workflowID, 2),
    preview(sourcePath, 1),
  );
  const accepted = createDeferred<ReleaseWorkflowCurrent>();
  const continuation = vi.fn(async () => accepted.promise);
  const cancelOperation = vi.fn();
  const { result, unmount } = renderHook(useReleaseSession, {
    wrapper: wrapperFor(
      portsFor({
        resumeWorkflowID: workflowID,
        workflow: workflowPorts({
          current: async () => current,
          continue: continuation,
          cancelOperation,
        }),
      }),
    ),
  });
  await waitFor(() => expect(result.current.input.view.selectedSource).toBe(sourcePath));
  let pending!: Promise<boolean>;
  act(() => {
    pending = result.current.input.fetchCandidateDetails("101");
  });
  await waitFor(() => expect(continuation).toHaveBeenCalledOnce());
  act(() => result.current.input.cancelPreparation());
  accepted.reject(new Error("response lost"));
  await act(() => pending);
  expect(cancelOperation).not.toHaveBeenCalled();
  expect(result.current.input.view.status).toBe("error");
  expect(result.current.input.view.error).toMatch(/could not confirm cancellation/i);
  unmount();
});

it.each(["rejected", "accepted lookup failed"])(
  "restores a previous candidate cancellation only before acceptance: %s",
  async (outcome) => {
    const workflowID = "workflow-candidate-rejected-rerun";
    const sourcePath = "Example.Release.2026.mkv";
    const current: ReleaseWorkflowCurrent = {
      ...workflowCurrentFromPreview(workflowCurrent(workflowID, 2), preview(sourcePath, 1)),
      operation: {
        id: "canceled-preparation",
        workflowId: workflowID,
        revision: 2,
        sequence: 2,
        command: "prepare_release",
        operation: "preparation",
        status: "canceled",
        completed: 0,
        total: 1,
        progress: 0,
        startedAt: "2026-07-20T00:00:00Z",
        updatedAt: "2026-07-20T00:00:00Z",
      },
    };
    const accepted = createDeferred<ReleaseWorkflowCurrent>();
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({
            current: async () => current,
            continue: () => accepted.promise,
            operation: async () => {
              throw new Error("Accepted operation lookup failed.");
            },
          }),
        }),
      ),
    });
    try {
      await waitFor(() => expect(result.current.input.view.selectedSource).toBe(sourcePath));
      let pending!: Promise<boolean>;
      act(() => {
        pending = result.current.input.fetchCandidateDetails("101");
      });
      expect(result.current.workflow.view.current?.operation).toBeUndefined();
      if (outcome === "rejected") accepted.reject(new Error("Candidate request rejected."));
      else
        accepted.resolve({
          ...current,
          operation: undefined,
          preparationOperationId: "accepted-operation",
        });
      await act(async () => expect(await pending).toBe(false));
      expect(result.current.input.view.error).not.toBe("");
      await act(async () => {
        await result.current.workflow.reload();
      });
      expect(result.current.workflow.view.current?.operation).toEqual(
        outcome === "rejected" ? current.operation : undefined,
      );
    } finally {
      unmount();
    }
  },
);

it("retains a restored remote-search waiver across browser stages until an explicit recheck", async () => {
  const workflowID = "workflow-waiver";
  const fixture = workflowPorts();
  const checked = await fixture.checkDuplicates(
    workflowCurrent(workflowID, 2),
    true,
    "seed",
    new AbortController().signal,
  );
  const current = { ...checked, dupes: { ...checked.dupes!, skipRemote: true } };
  const continueWorkflow = vi.fn(async (_request: ContinueReleaseWorkflowRequest) => current);
  const { result, unmount } = renderHook(useReleaseSession, {
    wrapper: wrapperFor(
      portsFor({
        resumeWorkflowID: workflowID,
        workflow: workflowPorts({ current: async () => current, continue: continueWorkflow }),
      }),
    ),
  });
  try {
    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
    await act(() =>
      result.current.workflow.captureMedia({
        screenshotCount: 1,
        purpose: "final",
        captureDvdMenus: false,
      }),
    );
    await act(() =>
      result.current.workflow.generateDescriptions({
        imageHost: { FailedHosts: [], SkipUpload: true },
        options: {
          AudioAnalysis: false,
          AudioImages: "both",
          AudioTracks: "primary",
          CaptureDVDMenus: false,
          InteractionMode: "interactive",
          KeepFolder: false,
          KeepImages: false,
          NoSeed: true,
          OnlyID: false,
          RunLogLevel: "info",
          Screens: 0,
          SkipAutoTorrent: false,
        },
      }),
    );
    await act(() => result.current.workflow.dryRunUploads());
    expect(
      continueWorkflow.mock.calls.map(([request]) => [
        request.goal,
        request.intent.skipRemoteDuplicates,
      ]),
    ).toEqual([
      ["media_ready", true],
      ["descriptions_ready", true],
      ["dry_run", true],
    ]);
    await act(() => result.current.workflow.checkDuplicates(false));
    expect(continueWorkflow.mock.lastCall?.[0].intent.skipRemoteDuplicates).toBe(false);
  } finally {
    unmount();
  }
});

it.each([
  "trackers_assessed",
  "duplicates_decided",
  "media_ready",
  "descriptions_ready",
  "dry_run",
] as const)(
  "confirms %s cancellation without continuing, releases no input, and permits explicit retry",
  async (goal) => {
    const workflowID = `workflow-cancel-${goal}`;
    const base = workflowCurrentFromPreview(
      workflowCurrent(workflowID, 2),
      preview("C:\\media\\Example", 1),
    );
    const operation: WorkflowOperationStatus = {
      id: "cancel-operation",
      workflowId: workflowID,
      revision: 2,
      sequence: 1,
      command: "continue",
      operation: goal === "trackers_assessed" ? "preparation" : goal,
      status: "running",
      completed: 0,
      total: 2,
      progress: 0,
      startedAt: "2026-07-20T00:00:00Z",
      updatedAt: "2026-07-20T00:00:00Z",
    };
    let current = base;
    let terminal = createDeferred<WorkflowOperationStatus>();
    const continueWorkflow = vi.fn(async () => {
      current = { ...base, operation };
      return current;
    });
    const cancelOperation = vi.fn(
      async (_workflowID: string, _operationID: string) => current.operation!,
    );
    const readCurrent = vi.fn(async () => current);
    const close = vi.fn();
    const cancelWorkflow = vi.fn();
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          activeInput: { release: close },
          workflow: workflowPorts({
            current: readCurrent,
            continue: continueWorkflow,
            operation: async () => terminal.promise,
            cancelOperation,
            cancel: cancelWorkflow,
          }),
        }),
      ),
    });
    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
    const start = () => {
      const workflow = result.current.workflow;
      switch (goal) {
        case "trackers_assessed":
          return workflow.project(["AITHER"]);
        case "duplicates_decided":
          return workflow.checkDuplicates();
        case "media_ready":
          return workflow.captureMedia({
            screenshotCount: 1,
            purpose: "final",
            captureDvdMenus: false,
          });
        case "descriptions_ready":
          return workflow.generateDescriptions({
            imageHost: { FailedHosts: [], SkipUpload: true },
            options: {
              AudioAnalysis: false,
              AudioImages: "both",
              AudioTracks: "primary",
              CaptureDVDMenus: false,
              InteractionMode: "interactive",
              KeepFolder: false,
              KeepImages: false,
              NoSeed: true,
              OnlyID: false,
              RunLogLevel: "info",
              Screens: 0,
              SkipAutoTorrent: false,
            },
          });
        case "dry_run":
          return workflow.dryRunUploads();
      }
    };
    let command!: Promise<boolean>;
    let cancellation!: Promise<boolean>;
    try {
      act(() => {
        command = start();
      });
      await waitFor(() =>
        expect(result.current.workflow.view.current?.operation?.id).toBe(operation.id),
      );
      let reloading: Promise<boolean> | undefined;
      if (goal === "trackers_assessed" || goal === "duplicates_decided") {
        act(() => {
          reloading = result.current.workflow.reload();
        });
        await waitFor(() => expect(result.current.input.view.status).toBe("running"));
      }
      const ownedInput = result.current.input.view.activeInput;
      act(() => {
        cancellation = result.current.workflow.cancel();
      });
      await waitFor(() => expect(cancelOperation).toHaveBeenCalledOnce());
      expect(result.current.workflow.cancellation.pending).toBe(true);
      expect(result.current.workflow.view.current?.operation?.status).toBe("running");
      expect(continueWorkflow).toHaveBeenCalledOnce();
      expect(close).not.toHaveBeenCalled();
      expect(cancelWorkflow).not.toHaveBeenCalled();
      current = { ...base, operation: { ...operation, status: "canceled", sequence: 3 } };
      terminal.resolve(current.operation!);
      await act(async () => {
        expect(await command).toBe(false);
        expect(await cancellation).toBe(true);
        if (reloading) expect(await reloading).toBe(false);
      });
      expect(result.current.workflow.cancellation.pending).toBe(false);
      expect(result.current.workflow.view.current?.operation?.status).toBe("canceled");
      expect(result.current.input.view.activeInput).toEqual(ownedInput);
      expect(result.current.workflow.view.current?.release).toEqual(base.release);
      expect(result.current.input.view.status).toBe("ready");
      expect(result.current.workflow.view.failure).toBeNull();
      expect(result.current.workflow.view.error).toBe("");
      expect(continueWorkflow).toHaveBeenCalledOnce();
      const rejected = createDeferred<ReleaseWorkflowCurrent>();
      continueWorkflow.mockImplementationOnce(async () => rejected.promise);
      act(() => {
        command = start();
      });
      readCurrent.mockRejectedValueOnce(new Error("Background read failed."));
      await act(async () => {
        expect(await result.current.workflow.reload()).toBe(false);
      });
      await act(async () => {
        await result.current.workflow.reload();
      });
      expect(result.current.workflow.view.current?.operation).toBeUndefined();
      let staleRead: Promise<boolean> | undefined;
      if (goal === "media_ready") {
        terminal = createDeferred<WorkflowOperationStatus>();
        readCurrent.mockResolvedValueOnce({ ...base, operation });
        act(() => {
          staleRead = result.current.workflow.reload();
        });
        await waitFor(() =>
          expect(result.current.workflow.view.current?.operation?.status).toBe("running"),
        );
      }
      rejected.reject(new Error("Request rejected before acceptance."));
      await act(async () => {
        expect(await command).toBe(false);
      });
      expect(result.current.workflow.view.error).not.toBe("");
      if (staleRead) {
        expect(result.current.workflow.view.current?.operation).toEqual(operation);
        terminal.resolve(current.operation!);
        await act(async () => {
          await staleRead;
        });
      }
      expect(result.current.workflow.view.current?.operation).toEqual(current.operation);
      await act(async () => {
        await result.current.workflow.reload();
      });
      expect(result.current.workflow.view.current?.operation).toEqual(current.operation);
      for (let retry = 1; retry <= 2; retry += 1) {
        terminal = createDeferred<WorkflowOperationStatus>();
        const accepted = createDeferred<ReleaseWorkflowCurrent>();
        const nextOperation = { ...operation, id: `retry-operation-${retry}` };
        continueWorkflow.mockImplementationOnce(async () => accepted.promise);
        act(() => {
          command = start();
        });
        expect(result.current.workflow.cancellation.message).toBe("");
        expect(result.current.workflow.view.current?.operation?.status).not.toBe("canceled");
        expect(result.current.workflow.cancellation.available).toBe(true);
        await act(async () => {
          await result.current.workflow.reload();
        });
        expect(result.current.workflow.view.current?.operation?.status).not.toBe("canceled");
        current = { ...base, operation: nextOperation };
        accepted.resolve(current);
        await waitFor(() =>
          expect(result.current.workflow.view.current?.operation?.id).toBe(nextOperation.id),
        );
        act(() => {
          cancellation = result.current.workflow.cancel();
        });
        await waitFor(() => expect(cancelOperation).toHaveBeenCalledTimes(retry + 1));
        expect(cancelOperation.mock.calls[retry]?.slice(0, 2)).toEqual([
          workflowID,
          nextOperation.id,
        ]);
        expect(result.current.workflow.cancellation.pending).toBe(true);
        current = { ...base, operation: { ...nextOperation, status: "canceled", sequence: 3 } };
        terminal.resolve(current.operation!);
        await act(async () => {
          expect(await command).toBe(false);
          expect(await cancellation).toBe(true);
        });
        expect(result.current.workflow.cancellation.pending).toBe(false);
        expect(result.current.workflow.view.current?.operation?.status).toBe("canceled");
      }
      expect(continueWorkflow).toHaveBeenCalledTimes(4);
      expect(close).not.toHaveBeenCalled();
    } finally {
      terminal.resolve({ ...operation, status: "canceled" });
      unmount();
    }
  },
);

it("settles hydrated Input state when a command finishes after its background read", async () => {
  const workflowID = "workflow-background-completion";
  const base = workflowCurrentFromPreview(
    workflowCurrent(workflowID, 2),
    preview("Example.Release.2026.mkv", 1),
  );
  const terminal = createDeferred<WorkflowOperationStatus>();
  let current = base;
  const operation = questionnaireOperation(base, "running");
  const continuation = vi.fn(async () => {
    current = { ...base, operation };
    return current;
  });
  const { result, unmount } = renderHook(useReleaseSession, {
    wrapper: wrapperFor(
      portsFor({
        resumeWorkflowID: workflowID,
        workflow: workflowPorts({
          current: async () => current,
          continue: continuation,
          operation: async () => terminal.promise,
        }),
      }),
    ),
  });
  try {
    await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
    let command!: Promise<boolean>;
    act(() => {
      command = result.current.workflow.project(["AITHER"]);
    });
    await waitFor(() => expect(continuation).toHaveBeenCalledOnce());
    current = { ...base, operation: { ...operation, status: "completed", sequence: 3 } };
    await act(() => result.current.workflow.reload());
    expect(result.current.input.view.status).toBe("running");
    expect(result.current.workflow.cancellation.available).toBe(true);
    terminal.resolve(current.operation!);
    await act(async () => expect(await command).toBe(true));
    expect(result.current.workflow.view.status).toBe("ready");
    expect(result.current.input.view.status).toBe("ready");
    expect(result.current.workflow.cancellation.available).toBe(false);
  } finally {
    terminal.resolve({ ...operation, status: "completed" });
    unmount();
  }
});

it.each(["matching", "different workflow", "different operation"])(
  "restores only an exact saved cancellation after reload: %s",
  async (scenario) => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const workflowID = "workflow-cancel-reload";
    const operation: WorkflowOperationStatus = {
      id: "reload-operation",
      workflowId: workflowID,
      revision: 2,
      sequence: 1,
      command: "continue",
      operation: "dry_run",
      status: "running",
      completed: 0,
      total: 2,
      progress: 0,
      startedAt: "2026-07-20T00:00:00Z",
      updatedAt: "2026-07-20T00:00:00Z",
    };
    window.sessionStorage.setItem(
      "upbrr.cancellingReleaseOperation",
      JSON.stringify({
        workflowID: scenario === "different workflow" ? "old-workflow" : workflowID,
        operationID: scenario === "different operation" ? "old-operation" : operation.id,
      }),
    );
    let current = {
      ...workflowCurrentFromPreview(
        workflowCurrent(workflowID, 2),
        preview("C:\\media\\Example", 1),
      ),
      operation,
    };
    const terminal = createDeferred<WorkflowOperationStatus>();
    const cancelOperation = vi.fn(async () => operation);
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          resumeWorkflowID: workflowID,
          workflow: workflowPorts({
            current: async () => current,
            operation: async () => terminal.promise,
            cancelOperation,
          }),
        }),
      ),
    });
    try {
      await act(async () => vi.advanceTimersByTimeAsync(0));
      expect(result.current.workflow.view.current?.operation?.id).toBe(operation.id);
      expect(result.current.workflow.cancellation.pending).toBe(scenario === "matching");
      if (scenario === "matching")
        expect(cancelOperation).toHaveBeenCalledWith(
          workflowID,
          operation.id,
          expect.any(AbortSignal),
        );
      else expect(cancelOperation).not.toHaveBeenCalled();
      current = {
        ...current,
        operation: {
          ...operation,
          status: scenario === "matching" ? "canceled" : "completed",
          sequence: 2,
        },
      };
      await act(async () => {
        terminal.resolve(current.operation);
        await vi.advanceTimersByTimeAsync(1000);
      });
      expect(result.current.workflow.view.status).toBe("ready");
      expect(result.current.workflow.view.current?.operation?.status).toBe(
        current.operation.status,
      );
      expect(result.current.input.view.activeInput.inputID).toBe(`input-${workflowID}`);
      expect(result.current.workflow.cancellation.pending).toBe(false);
    } finally {
      terminal.resolve({ ...operation, status: "canceled" });
      unmount();
      vi.useRealTimers();
    }
  },
);

it.each(["completed", "interrupted", "failed", "canceled"] as const)(
  "retains a terminal %s outcome when the cancel request fails",
  async (status) => {
    const workflowID = "workflow-cancel-race";
    const operation: WorkflowOperationStatus = {
      id: "race-operation",
      workflowId: workflowID,
      revision: 2,
      sequence: 1,
      command: "continue",
      operation: "upload",
      status: "running",
      completed: 0,
      total: 2,
      progress: 0,
      startedAt: "2026-07-20T00:00:00Z",
      updatedAt: "2026-07-20T00:00:00Z",
    };
    let current = {
      ...workflowCurrentFromPreview(
        workflowCurrent(workflowID, 2),
        preview("C:\\media\\Example", 1),
      ),
      operation,
    };
    const terminal = createDeferred<WorkflowOperationStatus>();
    let cancellations = 0;
    const cancelOperation = vi.fn(async () => {
      if (++cancellations > 1 && status === "canceled") return operation;
      throw new Error("Synthetic cancellation request failed.");
    });
    const continueWorkflow = vi.fn();
    const Wrapper = wrapperFor(
      portsFor({
        resumeWorkflowID: workflowID,
        workflow: workflowPorts({
          current: async () => current,
          operation: async () => terminal.promise,
          cancelOperation,
          continue: continueWorkflow,
        }),
      }),
    );
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: ({ children }) => (
        <Wrapper>
          <WorkflowCancellation />
          {children}
        </Wrapper>
      ),
    });
    try {
      await waitFor(() =>
        expect(result.current.workflow.view.current?.operation?.id).toBe(operation.id),
      );
      let confirmation!: Promise<boolean>;
      act(() => {
        confirmation = result.current.workflow.cancel();
      });
      expect(result.current.workflow.cancellation.pending).toBe(true);
      await waitFor(() => expect(cancelOperation).toHaveBeenCalledOnce());
      if (status === "canceled") {
        await waitFor(() => expect(result.current.workflow.cancellation.retry).toBe(true));
        act(() => {
          fireEvent.click(screen.getByRole("button", { name: "Retry cancellation" }));
        });
        await waitFor(() => expect(cancelOperation).toHaveBeenCalledTimes(2));
        expect(cancelOperation.mock.calls[1]).toEqual(cancelOperation.mock.calls[0]);
        expect(result.current.workflow.cancellation.pending).toBe(true);
        expect(screen.getByRole("button", { name: "Cancelling…" })).toBeDisabled();
        expect(result.current.workflow.view.current?.operation?.status).toBe("running");
      }
      const failure = {
        Code: "remote_outcome_unknown" as const,
        Operation: "upload" as const,
        Message: "Review the synthetic remote outcome.",
        Recovery: "review_again" as const,
      };
      current = {
        ...current,
        operation: {
          ...operation,
          status,
          sequence: 2,
          ...(status === "failed" || status === "interrupted" ? { failures: [{ failure }] } : {}),
        },
      };
      terminal.resolve(current.operation);
      await act(async () => {
        expect(await confirmation).toBe(true);
      });
      expect(result.current.workflow.view.current?.operation?.status).toBe(status);
      if (status === "failed" || status === "interrupted") {
        expect(result.current.workflow.view.failure).toEqual(failure);
        expect(result.current.workflow.cancellation.message).toMatch(/error.*recovery/i);
      } else {
        expect(result.current.workflow.cancellation.message).toMatch(
          status === "canceled" ? /work canceled/i : /review retained results/i,
        );
      }
      expect(result.current.input.view.activeInput.inputID).toBe(`input-${workflowID}`);
      expect(continueWorkflow).not.toHaveBeenCalled();
      expect(result.current.workflow.cancellation.pending).toBe(false);
    } finally {
      terminal.resolve({ ...operation, status });
      unmount();
    }
  },
);

it.each([false, true])(
  "confirms preacceptance verification cleanup and permits retry when admission was not yet registered (%s)",
  async (early) => {
    const opened = createDeferred<ActiveInputSnapshot>();
    const stopped = createDeferred<ActiveInputSnapshot>();
    const open = vi.fn(async () => opened.promise);
    const cancelOperation = vi.fn();
    let attempts = 0;
    const cancelOpen = vi.fn(async () => {
      if (early && attempts++ === 0)
        throw Object.assign(new Error("Synthetic admission not registered."), {
          failure: {
            Code: "missing_prerequisite",
            Operation: "preparation",
            Message: "Admission unavailable.",
            Recovery: "refresh_release",
          },
        });
      opened.reject(new Error("Synthetic verifier cancelled."));
      return stopped.promise;
    });
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          activeInput: { open, cancelOpen },
          workflow: workflowPorts({ cancelOperation }),
        }),
      ),
    });
    try {
      await waitFor(() => expect(result.current.workflow.view.status).toBe("ready"));
      let preparation!: Promise<boolean>;
      let confirmation!: Promise<boolean>;
      act(() => {
        preparation = result.current.input.openSource("C:\\media\\Example.Release.2026.mkv");
      });
      await waitFor(() => expect(open).toHaveBeenCalledOnce());
      act(() => {
        confirmation = result.current.workflow.cancel();
      });
      await waitFor(() => expect(cancelOpen).toHaveBeenCalledOnce());
      if (early) {
        await waitFor(() => expect(result.current.workflow.cancellation.retry).toBe(true));
        act(() => {
          void result.current.workflow.cancel();
        });
        await waitFor(() => expect(cancelOpen).toHaveBeenCalledTimes(2));
      }
      expect(result.current.workflow.cancellation.pending).toBe(true);
      expect(result.current.input.view.status).toBe("running");
      expect(cancelOperation).not.toHaveBeenCalled();
      stopped.resolve({ state: "empty", revision: 2 });
      await act(async () => {
        expect(await preparation).toBe(false);
        expect(await confirmation).toBe(true);
      });
      expect(result.current.input.view.status).toBe("cancelled");
      expect(result.current.input.view.activeInput).toMatchObject({ state: "empty", revision: 2 });
      expect(result.current.input.view.sourceDraft).toBe("C:\\media\\Example.Release.2026.mkv");
      expect(result.current.workflow.cancellation.pending).toBe(false);
    } finally {
      stopped.resolve({ state: "empty", revision: 2 });
      unmount();
    }
  },
);

it("keeps progress and terminal confirmation live while the cancellation response is delayed", async () => {
  vi.useFakeTimers();
  const workflowID = "workflow-delayed-cancel";
  const operation: WorkflowOperationStatus = {
    id: "delayed-operation",
    workflowId: workflowID,
    revision: 2,
    sequence: 1,
    command: "continue",
    operation: "dry_run",
    status: "running",
    completed: 0,
    total: 2,
    progress: 0,
    startedAt: "2026-07-20T00:00:00Z",
    updatedAt: "2026-07-20T00:00:00Z",
  };
  let current = {
    ...workflowCurrentFromPreview(workflowCurrent(workflowID, 2), preview("C:\\media\\Example", 1)),
    operation,
  };
  const receipt = createDeferred<WorkflowOperationStatus>();
  let polled = 0;
  const cancelOperation = vi.fn(async () => receipt.promise);
  const { result, unmount } = renderHook(useReleaseSession, {
    wrapper: wrapperFor(
      portsFor({
        resumeWorkflowID: workflowID,
        workflow: workflowPorts({
          current: async () => current,
          cancelOperation,
          operation: async () =>
            ++polled === 1 ? { ...operation, progress: 50, sequence: 3 } : current.operation,
        }),
      }),
    ),
  });
  try {
    await act(async () => Promise.resolve());
    let confirmation!: Promise<boolean>;
    act(() => {
      confirmation = result.current.workflow.cancel();
    });
    await act(async () => vi.advanceTimersByTimeAsync(1000));
    expect(cancelOperation).toHaveBeenCalledOnce();
    expect(result.current.workflow.cancellation.pending).toBe(true);
    expect(result.current.workflow.view.current?.operation?.progress).toBe(50);
    current = { ...current, operation: { ...operation, status: "canceled", sequence: 4 } };
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
      expect(await confirmation).toBe(true);
    });
    expect(result.current.workflow.view.current?.operation?.status).toBe("canceled");
    expect(result.current.workflow.cancellation.pending).toBe(false);
    await act(async () => {
      receipt.resolve({ ...operation, sequence: 2 });
      await Promise.resolve();
    });
    expect(result.current.workflow.view.current?.operation?.status).toBe("canceled");
    expect(result.current.workflow.view.current?.operation?.sequence).toBe(4);
  } finally {
    receipt.resolve({ ...operation, status: "canceled" });
    unmount();
    vi.useRealTimers();
  }
});

it.each(["questions", "screenshots", "screenshots with backend"] as const)(
  "keeps explicitly cancelled %s loading settled on its actual page until retry",
  async (kind) => {
    const workflowID = `workflow-cancel-loader-${kind}`;
    const active = workflowPorts();
    const prepared = preparedQuestionnaireWorkflow(workflowID);
    const base =
      kind !== "questions"
        ? await active.project(prepared, ["AITHER"], {}, "seed", new AbortController().signal)
        : prepared;
    const pending = createDeferred<ReleaseWorkflowCurrent>();
    const pendingPlan =
      createDeferred<
        ReturnType<ReleaseSessionPorts["workflow"]["mediaPlan"]> extends Promise<infer T>
          ? T
          : never
      >();
    const operation: WorkflowOperationStatus = {
      id: "loader-operation",
      workflowId: workflowID,
      revision: 2,
      sequence: 1,
      command: "continue",
      operation: "projection",
      status: "running",
      completed: 0,
      total: 2,
      progress: 0,
      startedAt: "2026-07-20T00:00:00Z",
      updatedAt: "2026-07-20T00:00:00Z",
    };
    const project = vi.fn(active.project).mockImplementationOnce(async () => pending.promise);
    const mediaPlan = vi
      .fn(active.mediaPlan)
      .mockImplementationOnce(async () => pendingPlan.promise);
    const captureMedia = vi.fn(active.captureMedia);
    const current = { ...base, operation: { ...operation, status: "canceled" as const } };
    const ports = portsFor({
      resumeWorkflowID: workflowID,
      workflow: workflowPorts({
        current: async () => base,
        project,
        mediaPlan,
        captureMedia,
        cancelOperation: async () => current.operation,
        operation: async () => current.operation,
      }),
    });
    const Surface = () => {
      const session = useReleaseSession();
      return (
        <>
          <WorkflowCancellation />
          {kind === "questions" ? (
            <QuestionnaireSurface />
          ) : (
            <ScreenshotsPage
              facet={session.screenshots}
              setLightboxImage={() => undefined}
              setLightboxAlt={() => undefined}
            />
          )}
        </>
      );
    };
    const { result, unmount } = renderHook(useReleaseSession, {
      wrapper: ({ children }) => (
        <ReleaseSessionProvider ports={ports}>
          <Surface />
          {children}
        </ReleaseSessionProvider>
      ),
    });
    try {
      await waitFor(() =>
        expect(kind === "questions" ? project : mediaPlan).toHaveBeenCalledOnce(),
      );
      let backend: Promise<boolean> | undefined;
      if (kind === "screenshots with backend") {
        act(() => {
          backend = result.current.workflow.project(["AITHER"]);
        });
        await waitFor(() => expect(project).toHaveBeenCalledOnce());
      }
      let confirmation!: Promise<boolean>;
      act(() => {
        confirmation = result.current.workflow.cancel();
      });
      if (kind === "questions" || kind === "screenshots with backend")
        pending.resolve({ ...base, operation });
      await act(async () => {
        expect(await confirmation).toBe(true);
      });
      if (kind === "screenshots with backend") {
        await act(async () => {
          await backend;
          pendingPlan.resolve({
            ...(await active.mediaPlan(base.workflow.id, new AbortController().signal)),
            autoAdoptSavedImages: true,
            savedTrackerImages: [
              { trackerId: "AITHER", host: "synthetic", url: "https://images.example/saved.png" },
            ],
          });
        });
        expect(captureMedia).not.toHaveBeenCalled();
      }
      expect(
        kind === "questions"
          ? result.current.duplicates.view.questionnaireStatus
          : result.current.screenshots.view.status,
      ).toBe(kind === "questions" ? "error" : "cancelled");
      expect(kind === "questions" ? project : mediaPlan).toHaveBeenCalledOnce();
      await act(async () => {
        if (kind === "questions") await result.current.duplicates.refreshQuestionnaires();
        else await result.current.screenshots.load();
      });
      expect(kind === "questions" ? project : mediaPlan).toHaveBeenCalledTimes(2);
    } finally {
      pending.resolve(current);
      pendingPlan.resolve(await active.mediaPlan(base.workflow.id, new AbortController().signal));
      unmount();
    }
  },
);
