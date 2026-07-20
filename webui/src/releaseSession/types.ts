// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type {
  DescriptionBuilderPreview,
  DVDMenuCaptureResult,
  DupeCheckSnapshot,
  ExternalIDOverrides,
  ImageUploadProgressUpdate,
  MetadataPreview,
  OperationFailure,
  PlaylistInfo,
  PreparationProgressUpdate,
  ReleaseNameOverrides,
  ReleaseRef,
  ScreenshotImage,
  ScreenshotPlan,
  ScreenshotPurpose,
  ScreenshotResult,
  ScreenshotSelection,
  TrackerDryRunPreview,
  TrackerEligibility,
  TrackerPreview,
  TrackerUploadSnapshot,
  UploadedImageLink,
  UploadImageHostFailure,
  UploadReviewResult,
} from "../types";

export type ReleaseRoute =
  | "input"
  | "trackerData"
  | "duplicates"
  | "screenshots"
  | "menuImages"
  | "uploadedImages"
  | "descriptions"
  | "upload";

export type RouteAccess = Readonly<{ available: boolean; reason: string }>;
export type FacetStatus = "idle" | "running" | "ready" | "error";

/** Lifecycle of one correlation-scoped canonical preparation attempt. */
export type PreparationStatus =
  | "idle"
  | "running"
  | "awaiting_input"
  | "ready"
  | "error"
  | "cancelled";

/** Blu-ray playlist discovery and selection lifecycle within preparation. */
export type PlaylistStatus =
  | "idle"
  | "discovering"
  | "awaiting_selection"
  | "processing"
  | "complete"
  | "error"
  | "cancelled";

/** Ordered advisory preparation stage retained after transport correlation is validated. */
export type PreparationStep = Readonly<
  Omit<PreparationProgressUpdate, "correlationID" | "status"> & {
    status: PreparationProgressUpdate["status"] | "awaiting_input";
  }
>;

export type PreparationIntent = Readonly<{
  sourceLookupURL: string;
  identity: Readonly<ExternalIDOverrides>;
  releaseName: Readonly<ReleaseNameOverrides>;
  playlist: Readonly<{ Set: boolean; Selected: readonly string[]; UseAll: boolean }>;
}>;

export type UploadRunOptions = Readonly<{
  noSeed: boolean;
  runLogLevel: string;
}>;

export type IdentityFacet = Readonly<{
  view: Readonly<{
    sessionRevision: number;
    sourcePath: string;
    release: ReleaseRef | null;
    preview: MetadataPreview | null;
  }>;
}>;

export type NavigationFacet = Readonly<{
  view: Readonly<{ access: Readonly<Record<ReleaseRoute, RouteAccess>> }>;
  open(route: ReleaseRoute): boolean;
}>;

/** Source selection, canonical preparation, progress, and prerequisite controls. */
export type InputFacet = Readonly<{
  view: Readonly<{
    sourceDraft: string;
    selectedSource: string;
    status: PreparationStatus;
    error: string;
    failure: OperationFailure | null;
    preparationDirty: boolean;
    intent: PreparationIntent;
    selectedTrackers: readonly string[];
    progress: Readonly<{
      correlationID: string;
      status: PreparationStatus;
      message: string;
      steps: readonly PreparationStep[];
    }>;
    preview: MetadataPreview | null;
    trackerData: readonly TrackerPreview[];
    playlist: Readonly<{
      status: PlaylistStatus;
      required: boolean;
      candidates: readonly PlaylistInfo[];
      selected: readonly string[];
      useAll: boolean;
      error: string;
    }>;
  }>;
  updateSourceDraft(value: string): void;
  selectSource(value: string): void;
  changeSourceLookupURL(value: string): void;
  changeIdentity(value: Readonly<ExternalIDOverrides>): void;
  changeReleaseName(value: Readonly<ReleaseNameOverrides>): void;
  chooseTrackers(trackers: readonly string[]): void;
  choosePlaylists(playlists: readonly string[], useAll: boolean): void;
  confirmPlaylists(): Promise<boolean>;
  cancelPlaylistSelection(): void;
  prepareSource(sourcePath: string, intent: PreparationIntent): Promise<boolean>;
  resetSource(sourcePath: string, intent: PreparationIntent): Promise<boolean>;
  prepare(): Promise<boolean>;
  reset(): Promise<boolean>;
  confirmBDMVRescan(): Promise<boolean>;
  selectCandidate(releaseID: string): Promise<boolean>;
}>;

/** Duplicate-check commands and retained per-tracker Job progress for the active generation. */
export type DuplicatesFacet = Readonly<{
  view: Readonly<{
    status: FacetStatus;
    snapshot: DupeCheckSnapshot | null;
    eligibility: TrackerEligibility | null;
    ignoredTrackers: readonly string[];
    selectedTrackers: readonly string[];
    error: string;
    transientError: string;
  }>;
  run(): Promise<boolean>;
  cancel(): Promise<boolean>;
  chooseTrackers(trackers: readonly string[]): void;
  setIgnored(tracker: string, ignored: boolean): void;
}>;

/** Screenshot planning, generation, preview, removal, ordering, and final selection state. */
export type ScreenshotsFacet = Readonly<{
  view: Readonly<{
    revision: number;
    status: FacetStatus;
    plan: ScreenshotPlan | null;
    result: ScreenshotResult | null;
    selections: readonly ScreenshotSelection[];
    finalSelectionPaths: readonly string[];
    previewImage: string;
    staleReason: string;
    error: string;
  }>;
  load(): Promise<boolean>;
  changeSelection(
    index: number,
    value: Partial<Pick<ScreenshotSelection, "TimestampSeconds" | "Frame">>,
  ): void;
  generate(
    purpose: ScreenshotPurpose,
    selections?: readonly ScreenshotSelection[],
  ): Promise<boolean>;
  previewFrame(timestampSeconds: number): Promise<boolean>;
  remove(imagePath: string): Promise<boolean>;
  removeMany(imagePaths: readonly string[]): Promise<boolean>;
  removeTrackerURL(url: string): Promise<boolean>;
  removeTrackerURLs(urls: readonly string[]): Promise<boolean>;
  selectFinal(imagePath: string, selected: boolean): Promise<boolean>;
  reorderFinal(fromIndex: number, toIndex: number): Promise<boolean>;
  saveFinal(): Promise<boolean>;
  readImage(path: string): Promise<string>;
}>;

export type MenuImagePreview = Readonly<{ image: ScreenshotImage; dataURI: string }>;
export type UploadedImageCandidate = Readonly<{ image: ScreenshotImage; dataURI: string }>;

export type MenuImagesFacet = Readonly<{
  view: Readonly<{
    revision: number;
    status: FacetStatus;
    images: readonly MenuImagePreview[];
    capture: DVDMenuCaptureResult | null;
    staleReason: string;
    error: string;
  }>;
  load(): Promise<boolean>;
  importPaths(paths: readonly string[]): Promise<boolean>;
  capture(): Promise<boolean>;
  cancelCapture(): void;
  remove(imagePath: string): Promise<boolean>;
}>;

/** Correlated absolute host-attempt snapshots for one image upload command. */
export type ImageUploadProgress = Readonly<{
  correlationID: string;
  attempts: readonly ImageUploadProgressUpdate[];
}>;

/** Image-host candidate selection, upload progress, results, and removal state. */
export type UploadedImagesFacet = Readonly<{
  view: Readonly<{
    revision: number;
    status: FacetStatus;
    candidates: readonly UploadedImageCandidate[];
    uploaded: readonly UploadedImageLink[];
    selectedPaths: readonly string[];
    host: string;
    failures: readonly UploadImageHostFailure[];
    progress: ImageUploadProgress;
    staleReason: string;
    error: string;
  }>;
  load(): Promise<boolean>;
  chooseHost(host: string): void;
  select(imagePath: string, selected: boolean): void;
  selectAll(selected: boolean): void;
  upload(): Promise<boolean>;
  remove(imagePath: string, host: string): Promise<boolean>;
}>;

export type DescriptionsFacet = Readonly<{
  view: Readonly<{
    revision: number;
    status: FacetStatus;
    preview: DescriptionBuilderPreview | null;
    rawByGroup: Readonly<Record<string, string>>;
    renderedByGroup: Readonly<Record<string, string>>;
    dirtyGroups: readonly string[];
    staleReason: string;
    notice: string;
    error: string;
  }>;
  load(): Promise<boolean>;
  edit(groupKey: string, raw: string): void;
  render(groupKey: string): Promise<boolean>;
  save(groupKey: string): Promise<boolean>;
  reset(groupKey: string): Promise<boolean>;
}>;

export type UploadFacet = Readonly<{
  view: Readonly<{
    revision: number;
    selectedTrackers: readonly string[];
    eligibility: TrackerEligibility | null;
    ignoredDupesFor: readonly string[];
    authorizedRulesByTracker: Readonly<Record<string, readonly string[]>>;
    questionnaireAnswers: Readonly<Record<string, Readonly<Record<string, string>>>>;
    options: UploadRunOptions;
    dryRunStatus: FacetStatus;
    dryRun: TrackerDryRunPreview | null;
    dryRunStaleReason: string;
    reviewStatus: FacetStatus;
    review: UploadReviewResult | null;
    reviewStaleReason: string;
    snapshot: TrackerUploadSnapshot | null;
    error: string;
    transientError: string;
  }>;
  chooseTrackers(trackers: readonly string[]): void;
  answerQuestionnaire(tracker: string, key: string, value: string): void;
  changeOptions(options: Partial<UploadRunOptions>): void;
  setRuleAuthorized(tracker: string, rule: string, authorized: boolean): void;
  runDryRun(): Promise<boolean>;
  review(): Promise<boolean>;
  start(): Promise<boolean>;
  cancel(): Promise<boolean>;
  retry(): Promise<boolean>;
}>;

/** Sole public active-release workflow interface. */
export type ReleaseSession = Readonly<{
  identity: IdentityFacet;
  navigation: NavigationFacet;
  input: InputFacet;
  duplicates: DuplicatesFacet;
  screenshots: ScreenshotsFacet;
  menuImages: MenuImagesFacet;
  uploadedImages: UploadedImagesFacet;
  descriptions: DescriptionsFacet;
  upload: UploadFacet;
}>;
