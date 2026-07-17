// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type {
  DescriptionBuilderGroup,
  RuleAuthorization,
  DescriptionBuilderPreview,
  DVDMenuCaptureResult,
  ImageUploadProgressUpdate,
  MetadataPreview,
  PlaylistInfo,
  PreparationProgressUpdate,
  ReleaseRef,
  ScreenshotImage,
  ScreenshotPlan,
  ScreenshotPurpose,
  ScreenshotResult,
  ScreenshotSelection,
  TrackerDryRunPreview,
  UploadedImageLink,
  UploadImagesResult,
  UploadReviewResult,
} from "../types";
import type { PreparationIntent, UploadRunOptions } from "./types";

/** One-shot permissions for a preparation command; never reusable release facts. */
export type PreparationControls = Readonly<{ confirmBDMVRescan: boolean }>;

/** Preparation mutations coordinated by the release-session owner. */
export type PreparationOperation = "prepare" | "reset" | "candidate";

/** Correlated, cancellable preparation command delivered to one transport adapter. */
export type PreparationCommand = Readonly<{
  /** Mutation requested by the release-session owner. */
  operation: PreparationOperation;
  /** Unique attempt identifier used to reject stale progress and completion. */
  correlationID: string;
  /** Selected host filesystem source path. */
  sourcePath: string;
  /** Fact-producing input owned by this preparation attempt. */
  intent: PreparationIntent;
  /** One-shot permissions excluded from reusable preparation facts. */
  controls: PreparationControls;
  /** Candidate ID required only by the candidate operation. */
  releaseID?: string;
  /** Aborts transport work when the attempt is replaced or the provider unmounts. */
  signal: AbortSignal;
  /** Receives only progress correlated by the transport to this command. */
  onProgress(update: PreparationProgressUpdate): void;
}>;

/** Host operations used to classify a source and execute canonical preparation. */
export type ReleasePreparationPorts = Readonly<{
  detectDiscType(sourcePath: string, signal: AbortSignal): Promise<string>;
  discoverPlaylists(sourcePath: string, signal: AbortSignal): Promise<PlaylistInfo[]>;
  execute(command: PreparationCommand): Promise<MetadataPreview>;
}>;

export type ScreenshotPorts = Readonly<{
  load(release: ReleaseRef, signal: AbortSignal): Promise<ScreenshotPlan>;
  generate(
    release: ReleaseRef,
    selections: readonly ScreenshotSelection[],
    purpose: ScreenshotPurpose,
    signal: AbortSignal,
  ): Promise<ScreenshotResult>;
  previewFrame(release: ReleaseRef, timestampSeconds: number, signal: AbortSignal): Promise<string>;
  remove(release: ReleaseRef, imagePath: string, signal: AbortSignal): Promise<void>;
  removeTrackerURL(release: ReleaseRef, url: string, signal: AbortSignal): Promise<void>;
  saveFinal(
    release: ReleaseRef,
    images: readonly ScreenshotImage[],
    signal: AbortSignal,
  ): Promise<void>;
  readImage(path: string, signal: AbortSignal): Promise<string>;
}>;

export type MenuImagePorts = Readonly<{
  list(release: ReleaseRef, signal: AbortSignal): Promise<ScreenshotImage[]>;
  readImage(path: string, signal: AbortSignal): Promise<string>;
  importPaths(release: ReleaseRef, paths: readonly string[], signal: AbortSignal): Promise<void>;
  capture(release: ReleaseRef, signal: AbortSignal): Promise<DVDMenuCaptureResult>;
  remove(release: ReleaseRef, imagePath: string, signal: AbortSignal): Promise<void>;
}>;

export type UploadedImagePorts = Readonly<{
  listCandidates(release: ReleaseRef, signal: AbortSignal): Promise<ScreenshotImage[]>;
  readImage(path: string, signal: AbortSignal): Promise<string>;
  listUploaded(release: ReleaseRef, signal: AbortSignal): Promise<UploadedImageLink[]>;
  upload(
    command: Readonly<{
      correlationID: string;
      release: ReleaseRef;
      trackers: readonly string[];
      host: string;
      images: readonly ScreenshotImage[];
      signal: AbortSignal;
      onProgress(update: ImageUploadProgressUpdate): void;
    }>,
  ): Promise<UploadImagesResult>;
  remove(release: ReleaseRef, imagePath: string, host: string, signal: AbortSignal): Promise<void>;
}>;

export type DescriptionPorts = Readonly<{
  load(
    release: ReleaseRef,
    trackers: readonly string[],
    signal: AbortSignal,
  ): Promise<DescriptionBuilderPreview>;
  render(raw: string, signal: AbortSignal): Promise<string>;
  save(
    release: ReleaseRef,
    groupKey: string,
    raw: string,
    trackers: readonly string[],
    signal: AbortSignal,
  ): Promise<DescriptionBuilderGroup>;
}>;

export type UploadCommand = Readonly<{
  release: ReleaseRef;
  trackers: readonly string[];
  ignoreDupesFor: readonly string[];
  ruleAuthorizations: readonly RuleAuthorization[];
  questionnaireAnswers: Readonly<Record<string, Readonly<Record<string, string>>>>;
  descriptionGroups: readonly DescriptionBuilderGroup[];
  options: UploadRunOptions;
}>;

/** Dry-run command bound to duplicate evidence; live rule authorizations are intentionally absent. */
export type DryRunCommand = Omit<UploadCommand, "ruleAuthorizations"> &
  Readonly<{ dupeJobID: string }>;

export type UploadPorts = Readonly<{
  dryRun(command: DryRunCommand, signal: AbortSignal): Promise<TrackerDryRunPreview>;
  review(command: UploadCommand, signal: AbortSignal): Promise<UploadReviewResult>;
}>;

export type ReleaseSessionPorts = Readonly<{
  preparation: ReleasePreparationPorts;
  screenshots: ScreenshotPorts;
  menuImages: MenuImagePorts;
  uploadedImages: UploadedImagePorts;
  descriptions: DescriptionPorts;
  upload: UploadPorts;
}>;
