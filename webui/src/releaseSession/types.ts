// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type {
  ExternalIDOverrides,
  ImageUploadProgressUpdate,
  MetadataPreview,
  OperationFailure,
  PlaylistInfo,
  PrepareInput,
  ReleaseNameOverrides,
  ReleaseRef,
  ScreenshotPlan,
  ScreenshotPurpose,
  ScreenshotSelection,
  TrackerPreview,
  UploadImageHostFailure,
} from "../types";
import type {
  DupeAssessment,
  DupeDecision,
  DescriptionInstructions,
  DescriptionSet,
  MediaArtifactSet,
  MediaCaptureInstructions,
  ReleaseWorkflowCurrent,
  TrackerPreflightAssessment,
  TrackerReleaseProjectionSet,
  TrackerProjectionInstructions,
  UploadDryRunResult,
  UploadResult,
} from "../api/generated/release-workflow";

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

/** Duplicate-check commands, naming review, and exact workflow-owned tracker outcomes. */
export type DuplicatesFacet = Readonly<{
  view: Readonly<{
    status: FacetStatus;
    assessment?: DupeAssessment | null;
    projections?: TrackerReleaseProjectionSet | null;
    preflight?: TrackerPreflightAssessment | null;
    completed: number;
    total: number;
    ignoredTrackers: readonly string[];
    selectedTrackers: readonly string[];
    /** Local tracker-name edits keyed by normalized tracker ID. */
    releaseNameOverrides: Readonly<Record<string, string>>;
    error: string;
  }>;
  run(): Promise<boolean>;
  cancel(): Promise<boolean>;
  chooseTrackers(trackers: readonly string[]): void;
  /** Stores a tracker-name edit locally without resolving its backend action. */
  confirmReleaseName(tracker: string, value: string): void;
  /** Resolves or reopens the backend naming action using the current local edit. */
  acknowledgeReleaseName(tracker: string, acknowledged: boolean): Promise<boolean>;
  setIgnored(tracker: string, ignored: boolean): void;
}>;

/** Screenshot planning, generation, preview, removal, ordering, and final selection state. */
export type ScreenshotsFacet = Readonly<{
  view: Readonly<{
    revision: number;
    status: FacetStatus;
    plan: ScreenshotPlan | null;
    artifacts?: MediaArtifactSet | null;
    workflowMode?: boolean;
    selections: readonly ScreenshotSelection[];
    finalSelectionArtifactIDs: readonly string[];
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
  remove(artifactID: string): Promise<boolean>;
  removeMany(artifactIDs: readonly string[]): Promise<boolean>;
  selectFinal(artifactID: string, selected: boolean): Promise<boolean>;
  reorderFinal(fromIndex: number, toIndex: number): Promise<boolean>;
  saveFinal(): Promise<boolean>;
  selectArtifact(artifactID: string, selected: boolean): Promise<boolean>;
  deleteArtifacts(artifactIDs: readonly string[]): Promise<boolean>;
  readImage(artifactID: string): Promise<string>;
}>;

export type MediaImageView = Readonly<{
  artifactID: string;
  index: number;
  timestampSeconds: number;
  purpose: ScreenshotPurpose;
  width: number;
  height: number;
  sizeBytes: number;
}>;

export type MenuImagePreview = Readonly<{ image: MediaImageView; contentURL: string }>;
export type UploadedImageCandidate = Readonly<{
  image: MediaImageView;
  contentURL: string;
}>;
export type HostedImageView = Readonly<{
  artifactID: string;
  host: string;
  url: string;
  sizeBytes: number;
  uploadedAt: string;
}>;

export type MenuImagesFacet = Readonly<{
  view: Readonly<{
    revision: number;
    status: FacetStatus;
    images: readonly MenuImagePreview[];
    artifacts?: MediaArtifactSet | null;
    staleReason: string;
    error: string;
  }>;
  load(): Promise<boolean>;
  importFiles(files: readonly File[]): Promise<boolean>;
  capture(): Promise<boolean>;
  cancelCapture(): void;
  remove(artifactID: string): Promise<boolean>;
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
    uploaded: readonly HostedImageView[];
    selectedArtifactIDs: readonly string[];
    failures: readonly UploadImageHostFailure[];
    progress: ImageUploadProgress;
    staleReason: string;
    error: string;
  }>;
  load(): Promise<boolean>;
  select(artifactID: string, selected: boolean): void;
  selectAll(selected: boolean): void;
  upload(): Promise<boolean>;
  remove(artifactID: string, host: string): Promise<boolean>;
}>;

export type DescriptionsFacet = Readonly<{
  view: Readonly<{
    revision: number;
    status: FacetStatus;
    artifact?: DescriptionSet | null;
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
    projections: TrackerReleaseProjectionSet | null;
    ignoredDupesFor: readonly string[];
    questionnaireAnswers: Readonly<Record<string, Readonly<Record<string, string>>>>;
    options: UploadRunOptions;
    dryRunStatus: FacetStatus;
    uploadStatus: FacetStatus;
    dryRunResult: UploadDryRunResult | null;
    result: UploadResult | null;
    error: string;
  }>;
  chooseTrackers(trackers: readonly string[]): void;
  answerQuestionnaire(tracker: string, key: string, value: string): void;
  changeOptions(options: Partial<UploadRunOptions>): void;
  runDryRun(): Promise<boolean>;
  start(): Promise<boolean>;
  cancel(): Promise<boolean>;
  retry(): Promise<boolean>;
  retryClientInjection(): Promise<boolean>;
}>;

/** Authoritative backend workflow state and intent-only transition methods. */
export type WorkflowFacet = Readonly<{
  view: Readonly<{
    status: FacetStatus;
    current: ReleaseWorkflowCurrent | null;
    error: string;
    failure: OperationFailure | null;
  }>;
  reload(): Promise<boolean>;
  begin(input: PrepareInput): Promise<boolean>;
  project(
    trackers: readonly string[],
    instructions?: Readonly<Record<string, TrackerProjectionInstructions>>,
  ): Promise<boolean>;
  preflight(): Promise<boolean>;
  checkDuplicates(skipRemote?: boolean): Promise<boolean>;
  decideDuplicates(decisions: Readonly<Record<string, DupeDecision>>): Promise<boolean>;
  captureMedia(instructions: MediaCaptureInstructions): Promise<boolean>;
  generateDescriptions(instructions: DescriptionInstructions): Promise<boolean>;
  dryRunUploads(): Promise<boolean>;
  executeUploads(): Promise<boolean>;
  retryFailedUploads(): Promise<boolean>;
  retryClientInjections(): Promise<boolean>;
  invalidateTrackers(trackerIDs: readonly string[], reason: string): Promise<boolean>;
}>;

/** Sole public active-release workflow interface. */
export type ReleaseSession = Readonly<{
  workflow: WorkflowFacet;
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
