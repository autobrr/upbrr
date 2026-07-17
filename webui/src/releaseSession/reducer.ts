// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type {
  DescriptionBuilderGroup,
  DescriptionBuilderPreview,
  DVDMenuCaptureResult,
  ExternalIDOverrides,
  ImageUploadProgressUpdate,
  MetadataPreview,
  OperationFailure,
  PlaylistInfo,
  ReleaseNameOverrides,
  ReleaseRef,
  ScreenshotPlan,
  ScreenshotResult,
  ScreenshotSelection,
  TrackerDryRunPreview,
  UploadedImageLink,
  UploadImageHostFailure,
  UploadReviewResult,
} from "../types";
import type {
  FacetStatus,
  MenuImagePreview,
  PreparationIntent,
  PreparationStatus,
  PreparationStep,
  PlaylistStatus,
  UploadedImageCandidate,
  UploadRunOptions,
} from "./types";

export type WorkflowState<T> = Readonly<{
  revision: number;
  status: FacetStatus;
  value: T | null;
  staleReason: string;
  error: string;
}>;

type ScreenshotState = WorkflowState<ScreenshotPlan> &
  Readonly<{
    result: ScreenshotResult | null;
    previewImage: string;
    selections: readonly ScreenshotSelection[];
    finalSelectionPaths: readonly string[];
  }>;
type MenuImageState = WorkflowState<readonly MenuImagePreview[]> &
  Readonly<{ capture: DVDMenuCaptureResult | null }>;
type UploadedImageState = WorkflowState<{
  candidates: readonly UploadedImageCandidate[];
  uploaded: readonly UploadedImageLink[];
}> &
  Readonly<{
    host: string;
    selectedPaths: readonly string[];
    failures: readonly UploadImageHostFailure[];
    progress: Readonly<{
      correlationID: string;
      attempts: readonly ImageUploadProgressUpdate[];
    }>;
  }>;
type DescriptionState = WorkflowState<DescriptionBuilderPreview> &
  Readonly<{
    inputRevision: number;
    rawByGroup: Readonly<Record<string, string>>;
    renderedByGroup: Readonly<Record<string, string>>;
    dirtyGroups: readonly string[];
    notice: string;
  }>;
type PlaylistState = Readonly<{
  status: PlaylistStatus;
  required: boolean;
  candidates: readonly PlaylistInfo[];
  selected: readonly string[];
  useAll: boolean;
  error: string;
}>;

type PreparationAttemptState = Readonly<{
  correlationID: string;
  sourcePath: string;
  commandRevision: number;
  status: PreparationStatus;
  message: string;
  steps: readonly PreparationStep[];
  error: string;
  failure: OperationFailure | null;
}>;

/** Canonical release-session state; revisions and correlation reject stale async results. */
export type SessionState = Readonly<{
  sessionRevision: number;
  commandRevision: number;
  sourceDraft: string;
  selectedSource: string;
  preparation: PreparationAttemptState;
  preparationDirty: boolean;
  preparationIntent: PreparationIntent;
  playlist: PlaylistState;
  release: ReleaseRef | null;
  preview: MetadataPreview | null;
  selectedTrackers: readonly string[];
  ignoredDupesFor: readonly string[];
  questionnaireAnswers: Readonly<Record<string, Readonly<Record<string, string>>>>;
  uploadOptions: UploadRunOptions;
  uploadInputRevision: number;
  duplicatesError: string;
  uploadError: string;
  screenshots: ScreenshotState;
  menuImages: MenuImageState;
  uploadedImages: UploadedImageState;
  descriptions: DescriptionState;
  dryRun: WorkflowState<TrackerDryRunPreview>;
  review: WorkflowState<UploadReviewResult>;
}>;

type FacetName =
  | "screenshots"
  | "menuImages"
  | "uploadedImages"
  | "descriptions"
  | "dryRun"
  | "review";

/** Closed set of source, preparation, asset, review, and Job state transitions. */
export type SessionAction =
  | Readonly<{ type: "draft_changed"; value: string }>
  | Readonly<{ type: "source_selected"; sourcePath: string }>
  | Readonly<{ type: "source_lookup_changed"; value: string }>
  | Readonly<{ type: "identity_changed"; value: Readonly<ExternalIDOverrides> }>
  | Readonly<{ type: "release_name_changed"; value: Readonly<ReleaseNameOverrides> }>
  | Readonly<{
      type: "playlist_required";
      sourcePath: string;
      commandRevision: number;
      correlationID: string;
      candidates: readonly PlaylistInfo[];
      error: string;
    }>
  | Readonly<{ type: "playlist_draft_changed"; playlists: readonly string[]; useAll: boolean }>
  | Readonly<{ type: "playlist_dismissed" }>
  | Readonly<{
      type: "playlist_resumed";
      sourcePath: string;
      commandRevision: number;
      correlationID: string;
      intent: PreparationIntent;
    }>
  | Readonly<{
      type: "preparation_started";
      sourcePath: string;
      commandRevision: number;
      correlationID: string;
      intent: PreparationIntent;
    }>
  | Readonly<{
      type: "preparation_progressed";
      sourcePath: string;
      commandRevision: number;
      correlationID: string;
      step: PreparationStep;
    }>
  | Readonly<{
      type: "preparation_succeeded";
      sourcePath: string;
      commandRevision: number;
      correlationID: string;
      preview: MetadataPreview;
    }>
  | Readonly<{
      type: "preparation_failed";
      sourcePath: string;
      commandRevision: number;
      correlationID: string;
      error: string;
      failure: OperationFailure | null;
    }>
  | Readonly<{ type: "trackers_chosen"; trackers: readonly string[] }>
  | Readonly<{ type: "dupe_ignore_changed"; tracker: string; ignored: boolean }>
  | Readonly<{ type: "questionnaire_answered"; tracker: string; key: string; value: string }>
  | Readonly<{ type: "upload_options_changed"; value: Partial<UploadRunOptions> }>
  | Readonly<{
      type: "screenshot_selection_changed";
      index: number;
      value: Partial<Pick<ScreenshotSelection, "TimestampSeconds" | "Frame">>;
    }>
  | Readonly<{ type: "screenshot_final_paths_changed"; imagePaths: readonly string[] }>
  | Readonly<{ type: "job_command_started"; kind: "duplicates" | "upload" }>
  | Readonly<{ type: "job_command_failed"; kind: "duplicates" | "upload"; error: string }>
  | Readonly<{
      type: "workflow_started";
      facet: FacetName;
      sessionRevision: number;
      revision: number;
    }>
  | Readonly<{
      type: "workflow_failed";
      facet: FacetName;
      sessionRevision: number;
      revision: number;
      error: string;
    }>
  | Readonly<{
      type: "workflow_canceled";
      facet: FacetName;
      sessionRevision: number;
      revision: number;
    }>
  | Readonly<{
      type: "screenshots_loaded";
      sessionRevision: number;
      revision: number;
      plan: ScreenshotPlan;
      result?: ScreenshotResult;
      changed?: boolean;
      reseedDrafts?: boolean;
      finalSelectionPaths?: readonly string[];
    }>
  | Readonly<{
      type: "screenshot_previewed";
      sessionRevision: number;
      revision: number;
      image: string;
    }>
  | Readonly<{
      type: "menu_images_loaded";
      sessionRevision: number;
      revision: number;
      images: readonly MenuImagePreview[];
      capture?: DVDMenuCaptureResult;
      changed?: boolean;
    }>
  | Readonly<{
      type: "uploaded_images_loaded";
      sessionRevision: number;
      revision: number;
      candidates: readonly UploadedImageCandidate[];
      uploaded: readonly UploadedImageLink[];
      failures?: readonly UploadImageHostFailure[];
      changed?: boolean;
    }>
  | Readonly<{
      type: "uploaded_images_progress_reset";
      sessionRevision: number;
      revision: number;
      correlationID: string;
    }>
  | Readonly<{
      type: "uploaded_images_progressed";
      sessionRevision: number;
      revision: number;
      update: ImageUploadProgressUpdate;
    }>
  | Readonly<{ type: "upload_host_changed"; host: string }>
  | Readonly<{ type: "upload_image_selected"; imagePath: string; selected: boolean }>
  | Readonly<{ type: "upload_images_selected_all"; selected: boolean }>
  | Readonly<{ type: "description_edited"; groupKey: string; raw: string }>
  | Readonly<{
      type: "descriptions_loaded";
      sessionRevision: number;
      revision: number;
      preview: DescriptionBuilderPreview;
    }>
  | Readonly<{
      type: "description_rendered";
      sessionRevision: number;
      revision: number;
      inputRevision: number;
      groupKey: string;
      html: string;
    }>
  | Readonly<{
      type: "description_saved";
      sessionRevision: number;
      revision: number;
      inputRevision: number;
      group: DescriptionBuilderGroup;
    }>
  | Readonly<{
      type: "dry_run_loaded";
      sessionRevision: number;
      revision: number;
      inputRevision: number;
      preview: TrackerDryRunPreview;
    }>
  | Readonly<{
      type: "review_loaded";
      sessionRevision: number;
      revision: number;
      inputRevision: number;
      review: UploadReviewResult;
    }>;

const emptyIntent = (): PreparationIntent => ({
  sourceLookupURL: "",
  identity: {},
  releaseName: {},
  playlist: { Set: false, Selected: [], UseAll: false },
});

const clonePreparationIntent = (intent: PreparationIntent): PreparationIntent => ({
  sourceLookupURL: intent.sourceLookupURL,
  identity: { ...intent.identity },
  releaseName: { ...intent.releaseName },
  playlist: {
    Set: intent.playlist.Set,
    Selected: [...intent.playlist.Selected],
    UseAll: intent.playlist.UseAll,
  },
});

const emptyOptions = (): UploadRunOptions => ({ noSeed: false, runLogLevel: "info" });

const emptyWorkflow = <T>(): WorkflowState<T> => ({
  revision: 0,
  status: "idle",
  value: null,
  staleReason: "Preparation required.",
  error: "",
});

/** Creates detached initial state for one release-session provider instance. */
export const initialSessionState = (): SessionState => ({
  sessionRevision: 0,
  commandRevision: 0,
  sourceDraft: "",
  selectedSource: "",
  preparation: {
    correlationID: "",
    sourcePath: "",
    commandRevision: 0,
    status: "idle",
    message: "",
    steps: [],
    error: "",
    failure: null,
  },
  preparationDirty: false,
  preparationIntent: emptyIntent(),
  playlist: {
    status: "idle",
    required: false,
    candidates: [],
    selected: [],
    useAll: false,
    error: "",
  },
  release: null,
  preview: null,
  selectedTrackers: [],
  ignoredDupesFor: [],
  questionnaireAnswers: {},
  uploadOptions: emptyOptions(),
  uploadInputRevision: 0,
  duplicatesError: "",
  uploadError: "",
  screenshots: {
    ...emptyWorkflow<ScreenshotPlan>(),
    result: null,
    previewImage: "",
    selections: [],
    finalSelectionPaths: [],
  },
  menuImages: { ...emptyWorkflow<readonly MenuImagePreview[]>(), capture: null },
  uploadedImages: {
    ...emptyWorkflow<{
      candidates: readonly UploadedImageCandidate[];
      uploaded: readonly UploadedImageLink[];
    }>(),
    host: "",
    selectedPaths: [],
    failures: [],
    progress: { correlationID: "", attempts: [] },
  },
  descriptions: {
    ...emptyWorkflow<DescriptionBuilderPreview>(),
    inputRevision: 0,
    rawByGroup: {},
    renderedByGroup: {},
    dirtyGroups: [],
    notice: "",
  },
  dryRun: emptyWorkflow(),
  review: emptyWorkflow(),
});

const normalizeNames = (values: readonly string[]) =>
  Array.from(new Set(values.map((value) => value.trim().toUpperCase()).filter(Boolean)));

const preparationMatches = (
  state: SessionState,
  sourcePath: string,
  commandRevision: number,
  correlationID: string,
) =>
  sourcePath === state.selectedSource &&
  commandRevision === state.commandRevision &&
  correlationID === state.preparation.correlationID;

const upsertPreparationStep = (
  steps: readonly PreparationStep[],
  step: PreparationStep,
): readonly PreparationStep[] =>
  [...steps.filter((current) => current.phase !== step.phase), { ...step }].sort(
    (left, right) => left.order - right.order || left.phase.localeCompare(right.phase),
  );

const completeRunningPreparationSteps = (steps: readonly PreparationStep[]) =>
  steps.map((step) =>
    step.status === "running" ? { ...step, status: "completed" as const } : step,
  );

const failCurrentPreparationStep = (steps: readonly PreparationStep[]) => {
  const next = [...steps];
  for (let index = next.length - 1; index >= 0; index -= 1) {
    if (next[index].status !== "running") continue;
    next[index] = { ...next[index], status: "failed" };
    break;
  }
  return next;
};

const invalidate = <T>(
  previous: WorkflowState<T>,
  reason: string,
  clear: boolean,
): WorkflowState<T> => ({
  revision: previous.revision + 1,
  status: "idle",
  value: clear ? null : previous.value,
  staleReason: reason,
  error: "",
});

const invalidateAuthority = (state: SessionState, reason: string, clear: boolean) => ({
  dryRun: invalidate(state.dryRun, reason, clear),
  review: invalidate(state.review, reason, true),
});

const invalidateAssetConsumers = (state: SessionState) => ({
  descriptions: {
    ...invalidate(state.descriptions, "Image assets changed.", false),
    inputRevision: state.descriptions.inputRevision + 1,
    rawByGroup: state.descriptions.rawByGroup,
    renderedByGroup: state.descriptions.renderedByGroup,
    dirtyGroups: state.descriptions.dirtyGroups,
    notice: "",
  },
  ...invalidateAuthority(state, "Image assets changed.", false),
  uploadInputRevision: state.uploadInputRevision + 1,
});

const invalidateReleaseWork = (state: SessionState, reason: string) => ({
  screenshots: {
    ...invalidate(state.screenshots, reason, true),
    result: null,
    previewImage: "",
    selections: [],
    finalSelectionPaths: [],
  },
  menuImages: { ...invalidate(state.menuImages, reason, true), capture: null },
  uploadedImages: {
    ...invalidate(state.uploadedImages, reason, true),
    host: state.uploadedImages.host,
    selectedPaths: [],
    failures: [],
    progress: { correlationID: "", attempts: [] },
  },
  descriptions: {
    ...invalidate(state.descriptions, reason, true),
    inputRevision: state.descriptions.inputRevision + 1,
    rawByGroup: {},
    renderedByGroup: {},
    dirtyGroups: [],
    notice: "",
  },
  ...invalidateAuthority(state, reason, true),
});

const workflowFor = (state: SessionState, facet: FacetName): WorkflowState<unknown> => state[facet];

const workflowMatches = (
  state: SessionState,
  facet: FacetName,
  sessionRevision: number,
  revision: number,
) => state.sessionRevision === sessionRevision && workflowFor(state, facet).revision === revision;

const startWorkflow = <T extends WorkflowState<unknown>>(value: T, revision: number): T =>
  ({
    ...value,
    revision,
    status: "running",
    error: "",
  }) as T;

const failWorkflow = <T extends WorkflowState<unknown>>(value: T, error: string): T =>
  ({
    ...value,
    status: "error",
    error,
  }) as T;

const readyWorkflow = <T extends WorkflowState<unknown>>(value: T): T =>
  ({
    ...value,
    status: "ready",
    staleReason: "",
    error: "",
  }) as T;

const preparationIntentChanged = (
  state: SessionState,
  intent: PreparationIntent,
): SessionState => ({
  ...state,
  ...invalidateAuthority(state, "Preparation input changed.", false),
  preparationIntent: intent,
  preparationDirty: Boolean(state.release),
  uploadInputRevision: state.uploadInputRevision + 1,
});

const upsertDescriptionGroup = (
  preview: DescriptionBuilderPreview | null,
  group: DescriptionBuilderGroup,
): DescriptionBuilderPreview => {
  const groups = [...(preview?.Groups || [])];
  const index = groups.findIndex((current) => current.GroupKey === group.GroupKey);
  if (index >= 0) groups[index] = group;
  else groups.push(group);
  return { SourcePath: preview?.SourcePath || "", Groups: groups };
};

/** Applies one transition, ignoring stale revision- or correlation-scoped completions. */
export const sessionReducer = (state: SessionState, action: SessionAction): SessionState => {
  switch (action.type) {
    case "draft_changed":
      return { ...state, sourceDraft: action.value };
    case "source_selected": {
      const sourcePath = action.sourcePath.trim();
      if (sourcePath === state.selectedSource) return { ...state, sourceDraft: sourcePath };
      return {
        ...state,
        ...invalidateReleaseWork(state, "Source changed."),
        sessionRevision: state.sessionRevision + 1,
        commandRevision: state.commandRevision + 1,
        sourceDraft: sourcePath,
        selectedSource: sourcePath,
        preparation: {
          correlationID: "",
          sourcePath,
          commandRevision: state.commandRevision + 1,
          status: "idle",
          message: "",
          steps: [],
          error: "",
          failure: null,
        },
        preparationDirty: false,
        preparationIntent: emptyIntent(),
        playlist: {
          status: "idle",
          required: false,
          candidates: [],
          selected: [],
          useAll: false,
          error: "",
        },
        release: null,
        preview: null,
        selectedTrackers: [],
        ignoredDupesFor: [],
        questionnaireAnswers: {},
        uploadOptions: emptyOptions(),
        uploadInputRevision: state.uploadInputRevision + 1,
        duplicatesError: "",
        uploadError: "",
      };
    }
    case "source_lookup_changed":
      return preparationIntentChanged(state, {
        ...state.preparationIntent,
        sourceLookupURL: action.value,
      });
    case "identity_changed":
      return preparationIntentChanged(state, {
        ...state.preparationIntent,
        identity: { ...action.value },
      });
    case "release_name_changed":
      return preparationIntentChanged(state, {
        ...state.preparationIntent,
        releaseName: { ...action.value },
      });
    case "playlist_required": {
      if (
        !preparationMatches(state, action.sourcePath, action.commandRevision, action.correlationID)
      ) {
        return state;
      }
      const candidates = action.candidates.map((candidate) => ({
        ...candidate,
        items: (candidate.items || []).map((item) => ({ ...item })),
      }));
      return {
        ...state,
        preparation: {
          ...state.preparation,
          status: action.error ? "error" : "awaiting_input",
          message: action.error || "Select one or more Blu-ray playlists.",
          error: action.error,
          failure: null,
        },
        playlist: {
          status: action.error ? "error" : "awaiting_selection",
          required: true,
          candidates,
          selected: candidates.length === 1 ? [candidates[0].file] : [],
          useAll: false,
          error: action.error,
        },
      };
    }
    case "playlist_draft_changed":
      return {
        ...state,
        ...invalidateAuthority(state, "Playlist selection changed.", false),
        preparationDirty: Boolean(state.release),
        playlist: {
          ...state.playlist,
          selected: [...action.playlists],
          useAll: action.useAll,
          error: "",
        },
        uploadInputRevision: state.uploadInputRevision + 1,
      };
    case "playlist_dismissed":
      return {
        ...state,
        preparation: {
          ...state.preparation,
          status: "cancelled",
          message: "Playlist selection cancelled.",
        },
        playlist: { ...state.playlist, status: "cancelled", required: false, error: "" },
      };
    case "playlist_resumed":
      if (
        !preparationMatches(state, action.sourcePath, action.commandRevision, action.correlationID)
      ) {
        return state;
      }
      return {
        ...state,
        preparationIntent: clonePreparationIntent(action.intent),
        preparation: {
          ...state.preparation,
          status: "running",
          message: "Processing selected Blu-ray playlists.",
          error: "",
          failure: null,
        },
        playlist: { ...state.playlist, status: "processing", required: false, error: "" },
      };
    case "preparation_started":
      if (action.sourcePath !== state.selectedSource) return state;
      return {
        ...state,
        commandRevision: action.commandRevision,
        preparationIntent: clonePreparationIntent(action.intent),
        preparation: {
          correlationID: action.correlationID,
          sourcePath: action.sourcePath,
          commandRevision: action.commandRevision,
          status: "running",
          message: "Preparing release metadata.",
          steps: [],
          error: "",
          failure: null,
        },
        playlist: {
          ...state.playlist,
          status: action.intent.playlist.Set ? "processing" : "idle",
          required: false,
          selected: action.intent.playlist.Set
            ? [...action.intent.playlist.Selected]
            : state.playlist.selected,
          useAll: action.intent.playlist.Set
            ? action.intent.playlist.UseAll
            : state.playlist.useAll,
          error: "",
        },
      };
    case "preparation_progressed":
      if (
        !preparationMatches(state, action.sourcePath, action.commandRevision, action.correlationID)
      ) {
        return state;
      }
      return {
        ...state,
        preparation: {
          ...state.preparation,
          message: action.step.message || state.preparation.message,
          steps: upsertPreparationStep(state.preparation.steps, action.step),
        },
        playlist:
          action.step.phase === "playlist_discovery" && action.step.status === "running"
            ? { ...state.playlist, status: "discovering", error: "" }
            : state.playlist,
      };
    case "preparation_succeeded": {
      if (
        !preparationMatches(state, action.sourcePath, action.commandRevision, action.correlationID)
      ) {
        return state;
      }
      const release = action.preview.Release;
      const acceptedSource = release?.SourcePath?.trim() || action.preview.SourcePath.trim();
      if (!acceptedSource || !release?.Generation || acceptedSource !== action.sourcePath) {
        return {
          ...state,
          preparation: {
            ...state.preparation,
            status: "error",
            message: "Preparation returned a different source.",
            error: "Preparation returned a different source.",
            failure: null,
          },
        };
      }
      return {
        ...state,
        ...invalidateReleaseWork(state, "Prepared generation changed."),
        sessionRevision: state.sessionRevision + 1,
        sourceDraft: acceptedSource,
        selectedSource: acceptedSource,
        preparation: {
          ...state.preparation,
          status: "ready",
          message: "Metadata preparation complete.",
          steps: completeRunningPreparationSteps(state.preparation.steps),
          error: "",
          failure: null,
        },
        preparationDirty: false,
        release: { SourcePath: acceptedSource, Generation: release.Generation },
        preview: action.preview,
        playlist: {
          ...state.playlist,
          status:
            state.playlist.status === "processing" || state.playlist.status === "complete"
              ? "complete"
              : state.playlist.status,
          required: false,
          error: "",
        },
        duplicatesError: "",
        uploadError: "",
      };
    }
    case "preparation_failed":
      if (
        !preparationMatches(state, action.sourcePath, action.commandRevision, action.correlationID)
      ) {
        return state;
      }
      return {
        ...state,
        preparation: {
          ...state.preparation,
          status: "error",
          message: action.error,
          steps: failCurrentPreparationStep(state.preparation.steps),
          error: action.error,
          failure: action.failure ? { ...action.failure } : null,
        },
        playlist:
          state.playlist.status === "processing"
            ? { ...state.playlist, status: "error", error: action.error }
            : state.playlist,
      };
    case "trackers_chosen": {
      const trackers = normalizeNames(action.trackers);
      return {
        ...state,
        ...invalidateAuthority(state, "Tracker selection changed.", false),
        selectedTrackers: trackers,
        uploadInputRevision: state.uploadInputRevision + 1,
      };
    }
    case "dupe_ignore_changed": {
      const tracker = action.tracker.trim().toUpperCase();
      if (!tracker) return state;
      const ignored = new Set(state.ignoredDupesFor);
      if (action.ignored) ignored.add(tracker);
      else ignored.delete(tracker);
      return {
        ...state,
        ...invalidateAuthority(state, "Duplicate override changed.", false),
        ignoredDupesFor: [...ignored],
        uploadInputRevision: state.uploadInputRevision + 1,
      };
    }
    case "questionnaire_answered": {
      const tracker = action.tracker.trim().toUpperCase();
      const key = action.key.trim();
      if (!tracker || !key) return state;
      return {
        ...state,
        ...invalidateAuthority(state, "Questionnaire answers changed.", false),
        questionnaireAnswers: {
          ...state.questionnaireAnswers,
          [tracker]: { ...state.questionnaireAnswers[tracker], [key]: action.value },
        },
        uploadInputRevision: state.uploadInputRevision + 1,
      };
    }
    case "upload_options_changed":
      return {
        ...state,
        ...invalidateAuthority(state, "Upload options changed.", false),
        uploadOptions: { ...state.uploadOptions, ...action.value },
        uploadInputRevision: state.uploadInputRevision + 1,
      };
    case "screenshot_selection_changed":
      return {
        ...state,
        screenshots: {
          ...state.screenshots,
          selections: state.screenshots.selections.map((selection, index) =>
            index === action.index ? { ...selection, ...action.value } : selection,
          ),
        },
      };
    case "screenshot_final_paths_changed":
      return {
        ...state,
        screenshots: {
          ...state.screenshots,
          finalSelectionPaths: Array.from(
            new Set(action.imagePaths.map((path) => path.trim()).filter(Boolean)),
          ),
        },
      };
    case "job_command_started":
      return action.kind === "duplicates"
        ? { ...state, duplicatesError: "" }
        : { ...state, uploadError: "" };
    case "job_command_failed":
      return action.kind === "duplicates"
        ? { ...state, duplicatesError: action.error }
        : { ...state, uploadError: action.error };
    case "workflow_started":
      if (action.sessionRevision !== state.sessionRevision) return state;
      return { ...state, [action.facet]: startWorkflow(state[action.facet], action.revision) };
    case "workflow_failed":
      if (!workflowMatches(state, action.facet, action.sessionRevision, action.revision))
        return state;
      return { ...state, [action.facet]: failWorkflow(state[action.facet], action.error) };
    case "workflow_canceled":
      if (!workflowMatches(state, action.facet, action.sessionRevision, action.revision))
        return state;
      return {
        ...state,
        [action.facet]: {
          ...state[action.facet],
          revision: state[action.facet].revision + 1,
          status: "idle",
          staleReason: "Operation canceled.",
          error: "",
        },
      };
    case "screenshots_loaded":
      if (!workflowMatches(state, "screenshots", action.sessionRevision, action.revision))
        return state;
      return {
        ...state,
        ...(action.changed ? invalidateAssetConsumers(state) : {}),
        screenshots: {
          ...readyWorkflow(state.screenshots),
          value: action.plan,
          result: action.result ?? state.screenshots.result,
          selections: action.reseedDrafts
            ? action.plan.SuggestedSelections || []
            : state.screenshots.selections,
          finalSelectionPaths:
            action.finalSelectionPaths ??
            (action.reseedDrafts
              ? (action.plan.FinalSelections || []).map((image) => image.Path).filter(Boolean)
              : state.screenshots.finalSelectionPaths),
        },
      };
    case "screenshot_previewed":
      if (!workflowMatches(state, "screenshots", action.sessionRevision, action.revision))
        return state;
      return {
        ...state,
        screenshots: { ...readyWorkflow(state.screenshots), previewImage: action.image },
      };
    case "menu_images_loaded":
      if (!workflowMatches(state, "menuImages", action.sessionRevision, action.revision))
        return state;
      return {
        ...state,
        ...(action.changed ? invalidateAssetConsumers(state) : {}),
        menuImages: {
          ...readyWorkflow(state.menuImages),
          value: action.images,
          capture: action.capture ?? state.menuImages.capture,
        },
      };
    case "uploaded_images_loaded":
      if (!workflowMatches(state, "uploadedImages", action.sessionRevision, action.revision))
        return state;
      return {
        ...state,
        ...(action.changed ? invalidateAssetConsumers(state) : {}),
        uploadedImages: {
          ...readyWorkflow(state.uploadedImages),
          value: { candidates: action.candidates, uploaded: action.uploaded },
          selectedPaths: action.candidates.map((item) => item.image.Path).filter(Boolean),
          failures: action.failures ?? [],
        },
      };
    case "uploaded_images_progress_reset":
      if (!workflowMatches(state, "uploadedImages", action.sessionRevision, action.revision))
        return state;
      return {
        ...state,
        uploadedImages: {
          ...state.uploadedImages,
          progress: { correlationID: action.correlationID, attempts: [] },
        },
      };
    case "uploaded_images_progressed": {
      if (!workflowMatches(state, "uploadedImages", action.sessionRevision, action.revision))
        return state;
      if (state.uploadedImages.progress.correlationID !== action.update.correlationID) return state;
      const total = Math.max(0, action.update.total);
      const attemptID = action.update.attemptID.trim();
      if (!attemptID) return state;
      const update: ImageUploadProgressUpdate = {
        ...action.update,
        attemptID,
        completed: Math.max(0, Math.min(action.update.completed, total)),
        total,
        succeeded: Math.max(0, action.update.succeeded),
        failed: Math.max(0, action.update.failed),
        reused: Math.max(0, action.update.reused),
        trackers: [...action.update.trackers],
      };
      const attempts = state.uploadedImages.progress.attempts.some(
        (current) => current.attemptID === attemptID,
      )
        ? state.uploadedImages.progress.attempts.map((current) =>
            current.attemptID === attemptID ? update : current,
          )
        : [...state.uploadedImages.progress.attempts, update];
      return {
        ...state,
        uploadedImages: {
          ...state.uploadedImages,
          progress: { ...state.uploadedImages.progress, attempts },
        },
      };
    }
    case "upload_host_changed":
      return { ...state, uploadedImages: { ...state.uploadedImages, host: action.host } };
    case "upload_image_selected": {
      const selected = new Set(state.uploadedImages.selectedPaths);
      if (action.selected) selected.add(action.imagePath);
      else selected.delete(action.imagePath);
      return {
        ...state,
        uploadedImages: { ...state.uploadedImages, selectedPaths: [...selected] },
      };
    }
    case "upload_images_selected_all":
      return {
        ...state,
        uploadedImages: {
          ...state.uploadedImages,
          selectedPaths: action.selected
            ? (state.uploadedImages.value?.candidates || [])
                .map((item) => item.image.Path)
                .filter(Boolean)
            : [],
        },
      };
    case "description_edited": {
      const groupKey = action.groupKey.trim();
      if (!groupKey) return state;
      return {
        ...state,
        ...invalidateAuthority(state, "Description changed.", false),
        descriptions: {
          ...state.descriptions,
          inputRevision: state.descriptions.inputRevision + 1,
          rawByGroup: { ...state.descriptions.rawByGroup, [groupKey]: action.raw },
          dirtyGroups: Array.from(new Set([...state.descriptions.dirtyGroups, groupKey])),
          notice: "",
        },
        uploadInputRevision: state.uploadInputRevision + 1,
      };
    }
    case "descriptions_loaded":
      if (!workflowMatches(state, "descriptions", action.sessionRevision, action.revision))
        return state;
      return {
        ...state,
        ...invalidateAuthority(state, "Descriptions regenerated.", false),
        descriptions: {
          ...readyWorkflow(state.descriptions),
          inputRevision: state.descriptions.inputRevision + 1,
          value: action.preview,
          rawByGroup: Object.fromEntries(
            (action.preview.Groups || []).map((group) => [
              group.GroupKey,
              group.RawDescription || "",
            ]),
          ),
          renderedByGroup: Object.fromEntries(
            (action.preview.Groups || []).map((group) => [
              group.GroupKey,
              group.RawDescriptionHTML || "",
            ]),
          ),
          dirtyGroups: [],
          notice: "",
        },
        uploadInputRevision: state.uploadInputRevision + 1,
      };
    case "description_rendered":
      if (
        !workflowMatches(state, "descriptions", action.sessionRevision, action.revision) ||
        action.inputRevision !== state.descriptions.inputRevision
      ) {
        return state;
      }
      return {
        ...state,
        descriptions: {
          ...readyWorkflow(state.descriptions),
          renderedByGroup: {
            ...state.descriptions.renderedByGroup,
            [action.groupKey]: action.html,
          },
        },
      };
    case "description_saved":
      if (
        !workflowMatches(state, "descriptions", action.sessionRevision, action.revision) ||
        action.inputRevision !== state.descriptions.inputRevision
      ) {
        return state;
      }
      return {
        ...state,
        ...invalidateAuthority(state, "Description saved; run dry run again.", false),
        descriptions: {
          ...readyWorkflow(state.descriptions),
          inputRevision: state.descriptions.inputRevision + 1,
          value: upsertDescriptionGroup(state.descriptions.value, action.group),
          rawByGroup: {
            ...state.descriptions.rawByGroup,
            [action.group.GroupKey]: action.group.RawDescription || "",
          },
          renderedByGroup: {
            ...state.descriptions.renderedByGroup,
            [action.group.GroupKey]: action.group.RawDescriptionHTML || "",
          },
          dirtyGroups: state.descriptions.dirtyGroups.filter(
            (groupKey) => groupKey !== action.group.GroupKey,
          ),
          notice: "Description saved.",
        },
        uploadInputRevision: state.uploadInputRevision + 1,
      };
    case "dry_run_loaded":
      if (
        !workflowMatches(state, "dryRun", action.sessionRevision, action.revision) ||
        action.inputRevision !== state.uploadInputRevision
      ) {
        return state;
      }
      return { ...state, dryRun: { ...readyWorkflow(state.dryRun), value: action.preview } };
    case "review_loaded":
      if (
        !workflowMatches(state, "review", action.sessionRevision, action.revision) ||
        action.inputRevision !== state.uploadInputRevision
      ) {
        return state;
      }
      return { ...state, review: { ...readyWorkflow(state.review), value: action.review } };
  }
};
