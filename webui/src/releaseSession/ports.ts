// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type {
  ActiveInputSnapshot,
  AudioAnalysisInstructions,
  ContinueReleaseWorkflowRequest,
  FramePreview,
  MediaPlan,
  OpenActiveInputRequest,
  Operation,
  ReconcileActiveInputRequest,
  RecoverLegacyActiveInputRequest,
  ReleaseActiveInputRequest,
  ReleaseWorkflowCurrent,
  UploadResultRef,
  WorkflowResourceRef,
} from "../api/generated/release-workflow";
import type { SourceVerificationProgress } from "./types";

/** Authoritative singleton-input transport; failed requests reject instead of fabricating local state. */
export type ActiveInputPorts = Readonly<{
  get(signal: AbortSignal): Promise<ActiveInputSnapshot>;
  open(request: OpenActiveInputRequest, signal: AbortSignal): Promise<ActiveInputSnapshot>;
  release(request: ReleaseActiveInputRequest, signal: AbortSignal): Promise<ActiveInputSnapshot>;
  recover(
    request: RecoverLegacyActiveInputRequest,
    signal: AbortSignal,
  ): Promise<ActiveInputSnapshot>;
  reconcile(
    request: ReconcileActiveInputRequest,
    signal: AbortSignal,
  ): Promise<ActiveInputSnapshot>;
  /** Signals input changes and stream reconnections, delivers verification progress, and returns cleanup. */
  subscribe(
    callback: () => void,
    onVerification: (update: SourceVerificationProgress) => void,
  ): () => void;
}>;

export type ReleaseWorkflowPorts = Readonly<{
  continue(
    request: ContinueReleaseWorkflowRequest,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  current(workflowID: string, signal: AbortSignal): Promise<ReleaseWorkflowCurrent>;
  operation(workflowID: string, operationID: string, signal: AbortSignal): Promise<Operation>;
  cancelOperation(workflowID: string, operationID: string, signal: AbortSignal): Promise<Operation>;
  analyzeAudio(
    current: ReleaseWorkflowCurrent,
    instructions: AudioAnalysisInstructions,
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  setAudioAnalysisEnabled(
    current: ReleaseWorkflowCurrent,
    enabled: boolean,
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  audioAnalysisURL(
    current: ReleaseWorkflowCurrent,
    analysisID: string,
    analysisRevision: number,
    artifactID: string,
  ): string;
  mediaPlan(workflowID: string, signal: AbortSignal): Promise<MediaPlan>;
  previewFrame(
    current: ReleaseWorkflowCurrent,
    discID: string,
    timestampSeconds: number,
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<FramePreview>;
  setMediaSelection(
    current: ReleaseWorkflowCurrent,
    artifactIDs: readonly string[],
    selected: boolean,
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  reorderMedia(
    current: ReleaseWorkflowCurrent,
    artifactIDs: readonly string[],
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  deleteMedia(
    current: ReleaseWorkflowCurrent,
    artifactIDs: readonly string[],
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  stageMedia(
    current: ReleaseWorkflowCurrent,
    file: File,
    signal: AbortSignal,
  ): Promise<WorkflowResourceRef>;
  attachMedia(
    current: ReleaseWorkflowCurrent,
    resources: readonly WorkflowResourceRef[],
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  uploadImages(
    current: ReleaseWorkflowCurrent,
    artifactIDs: readonly string[],
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  retryImageHost(
    current: ReleaseWorkflowCurrent,
    artifactIDs: readonly string[],
    host: string,
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  removeHostedImages(
    current: ReleaseWorkflowCurrent,
    artifactIDs: readonly string[],
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  mediaURL(current: ReleaseWorkflowCurrent, artifactID: string): string;
  saveDescriptionOverride(
    current: ReleaseWorkflowCurrent,
    groupKey: string,
    source: string,
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  resetDescriptionOverride(
    current: ReleaseWorkflowCurrent,
    groupKey: string,
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  retryFailedUploads(
    current: ReleaseWorkflowCurrent,
    result: UploadResultRef,
    trackerIDs: readonly string[],
    noSeed: boolean,
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  retryClientInjections(
    current: ReleaseWorkflowCurrent,
    result: UploadResultRef,
    trackerIDs: readonly string[],
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  cancel(
    workflowID: string,
    reason: string,
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
  invalidateTrackers(
    current: ReleaseWorkflowCurrent,
    trackerIDs: readonly string[],
    reason: string,
    idempotencyKey: string,
    signal: AbortSignal,
  ): Promise<ReleaseWorkflowCurrent>;
}>;

export type DescriptionPorts = Readonly<{
  render(raw: string, signal: AbortSignal): Promise<string>;
}>;

export type ReleaseSessionPorts = Readonly<{
  activeInput: ActiveInputPorts;
  workflow: ReleaseWorkflowPorts;
  descriptions: DescriptionPorts;
}>;
