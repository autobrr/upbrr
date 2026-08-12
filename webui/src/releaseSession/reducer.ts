// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type {
  ExternalIDOverrides,
  ImageUploadProgressUpdate,
  MetadataPreview,
  OperationFailure,
  PlaylistInfo,
  ReleaseNameOverrides,
  ReleaseRef,
  ScreenshotPlan,
  ScreenshotSelection,
  UploadImageHostFailure,
} from "../types";
import type {
  FacetStatus,
  HostedImageView,
  MenuImagePreview,
  PreparationIntent,
  PreparationStatus,
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
    previewImage: string;
    selections: readonly ScreenshotSelection[];
    finalSelectionArtifactIDs: readonly string[];
  }>;
type MenuImageState = WorkflowState<readonly MenuImagePreview[]>;
type UploadedImageState = WorkflowState<{
  candidates: readonly UploadedImageCandidate[];
  uploaded: readonly HostedImageView[];
}> &
  Readonly<{
    selectedArtifactIDs: readonly string[];
    failures: readonly UploadImageHostFailure[];
    failedHosts: readonly string[];
    progress: Readonly<{
      correlationID: string;
      attempts: readonly ImageUploadProgressUpdate[];
    }>;
  }>;
type DescriptionState = WorkflowState<never> &
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
  trackerSelectionTouched: boolean;
  ignoredDupesFor: readonly string[];
  /** Tracker-name edits retained for the active prepared source. */
  releaseNameOverrides: Readonly<Record<string, string>>;
  questionnaireAnswers: Readonly<Record<string, Readonly<Record<string, string>>>>;
  uploadOptions: UploadRunOptions;
  duplicatesError: string;
  uploadError: string;
  screenshots: ScreenshotState;
  menuImages: MenuImageState;
  uploadedImages: UploadedImageState;
  descriptions: DescriptionState;
}>;

type FacetName = "screenshots" | "menuImages" | "uploadedImages" | "descriptions";

/** Closed set of source, preparation, asset, review, and Job state transitions. */
export type SessionAction =
  | Readonly<{ type: "draft_changed"; value: string }>
  | Readonly<{
      type: "source_selected";
      sourcePath: string;
      defaultTrackers?: readonly string[];
    }>
  | Readonly<{ type: "source_lookup_changed"; value: string }>
  | Readonly<{ type: "identity_changed"; value: Readonly<ExternalIDOverrides> }>
  | Readonly<{ type: "metadata_changed"; value: PreparationIntent["metadata"] }>
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
  | Readonly<{
      type: "default_trackers_received";
      sessionRevision: number;
      trackers: readonly string[];
    }>
  | Readonly<{ type: "dupe_ignore_changed"; tracker: string; ignored: boolean }>
  | Readonly<{ type: "release_name_confirmed"; tracker: string; value: string }>
  | Readonly<{ type: "questionnaire_answered"; tracker: string; key: string; value: string }>
  | Readonly<{ type: "upload_options_changed"; value: Partial<UploadRunOptions> }>
  | Readonly<{
      type: "screenshot_selection_changed";
      index: number;
      value: Partial<Pick<ScreenshotSelection, "TimestampSeconds" | "Frame">>;
    }>
  | Readonly<{
      type: "screenshot_final_artifacts_changed";
      artifactIDs: readonly string[];
    }>
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
      changed?: boolean;
      reseedDrafts?: boolean;
      finalSelectionArtifactIDs?: readonly string[];
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
      changed?: boolean;
    }>
  | Readonly<{
      type: "uploaded_images_loaded";
      sessionRevision: number;
      revision: number;
      candidates: readonly UploadedImageCandidate[];
      uploaded: readonly HostedImageView[];
      failures?: readonly UploadImageHostFailure[];
      failedHosts?: readonly string[];
      changed?: boolean;
    }>
  | Readonly<{
      type: "workflow_upload_candidates_changed";
      candidates: readonly UploadedImageCandidate[];
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
  | Readonly<{ type: "upload_image_selected"; artifactID: string; selected: boolean }>
  | Readonly<{ type: "upload_images_selected_all"; selected: boolean }>
  | Readonly<{ type: "description_edited"; groupKey: string; raw: string }>
  | Readonly<{ type: "description_dirty_cleared"; groupKey?: string; notice?: string }>
  | Readonly<{
      type: "description_rendered";
      sessionRevision: number;
      revision: number;
      inputRevision: number;
      groupKey: string;
      html: string;
    }>;

const emptyIntent = (): PreparationIntent => ({
  sourceLookupURL: "",
  identity: {},
  metadata: {},
  releaseName: {},
  playlist: { Set: false, Selected: [], UseAll: false },
});

const clonePreparationIntent = (intent: PreparationIntent): PreparationIntent => ({
  sourceLookupURL: intent.sourceLookupURL,
  identity: { ...intent.identity },
  metadata: { ...intent.metadata },
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
  trackerSelectionTouched: false,
  ignoredDupesFor: [],
  releaseNameOverrides: {},
  questionnaireAnswers: {},
  uploadOptions: emptyOptions(),
  duplicatesError: "",
  uploadError: "",
  screenshots: {
    ...emptyWorkflow<ScreenshotPlan>(),
    previewImage: "",
    selections: [],
    finalSelectionArtifactIDs: [],
  },
  menuImages: emptyWorkflow<readonly MenuImagePreview[]>(),
  uploadedImages: {
    ...emptyWorkflow<{
      candidates: readonly UploadedImageCandidate[];
      uploaded: readonly HostedImageView[];
    }>(),
    selectedArtifactIDs: [],
    failures: [],
    failedHosts: [],
    progress: { correlationID: "", attempts: [] },
  },
  descriptions: {
    ...emptyWorkflow<never>(),
    inputRevision: 0,
    rawByGroup: {},
    renderedByGroup: {},
    dirtyGroups: [],
    notice: "",
  },
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

const invalidateAssetConsumers = (state: SessionState) => ({
  descriptions: {
    ...invalidate(state.descriptions, "Image assets changed.", false),
    inputRevision: state.descriptions.inputRevision + 1,
    rawByGroup: state.descriptions.rawByGroup,
    renderedByGroup: state.descriptions.renderedByGroup,
    dirtyGroups: state.descriptions.dirtyGroups,
    notice: "",
  },
});

const invalidateReleaseWork = (state: SessionState, reason: string) => ({
  screenshots: {
    ...invalidate(state.screenshots, reason, true),
    previewImage: "",
    selections: [],
    finalSelectionArtifactIDs: [],
  },
  menuImages: invalidate(state.menuImages, reason, true),
  uploadedImages: {
    ...invalidate(state.uploadedImages, reason, true),
    selectedArtifactIDs: [],
    failures: [],
    failedHosts: [],
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
  preparationIntent: intent,
  preparationDirty: Boolean(state.release),
});

const trackerSelectionChanged = (
  state: SessionState,
  trackers: readonly string[],
  touched: boolean,
): SessionState => ({
  ...state,
  screenshots:
    state.screenshots.status === "error"
      ? {
          ...state.screenshots,
          status: "idle",
          error: "",
        }
      : state.screenshots,
  descriptions: {
    ...invalidate(state.descriptions, "Tracker selection changed.", true),
    inputRevision: state.descriptions.inputRevision + 1,
    rawByGroup: {},
    renderedByGroup: {},
    dirtyGroups: [],
    notice: "",
  },
  selectedTrackers: normalizeNames(trackers),
  trackerSelectionTouched: touched,
});

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
        selectedTrackers: normalizeNames(action.defaultTrackers || []),
        trackerSelectionTouched: false,
        ignoredDupesFor: [],
        releaseNameOverrides: {},
        questionnaireAnswers: {},
        uploadOptions: emptyOptions(),
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
    case "metadata_changed":
      return preparationIntentChanged(state, {
        ...state.preparationIntent,
        metadata: { ...action.value },
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
        preparationDirty: Boolean(state.release),
        playlist: {
          ...state.playlist,
          selected: [...action.playlists],
          useAll: action.useAll,
          error: "",
        },
      };
    case "playlist_dismissed":
      return {
        ...state,
        preparation: {
          ...state.preparation,
          status: "cancelled",
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
          error: "",
          failure: null,
        },
        preparationDirty: false,
        release: { SourcePath: acceptedSource, Generation: release.Generation },
        preview: action.preview,
        releaseNameOverrides: {},
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
          error: action.error,
          failure: action.failure ? { ...action.failure } : null,
        },
        playlist:
          state.playlist.status === "processing"
            ? { ...state.playlist, status: "error", error: action.error }
            : state.playlist,
      };
    case "trackers_chosen":
      return trackerSelectionChanged(state, action.trackers, true);
    case "default_trackers_received": {
      if (action.sessionRevision !== state.sessionRevision || state.trackerSelectionTouched) {
        return state;
      }
      const trackers = normalizeNames(action.trackers);
      if (
        trackers.length === state.selectedTrackers.length &&
        trackers.every((tracker, index) => tracker === state.selectedTrackers[index])
      ) {
        return state;
      }
      return trackerSelectionChanged(state, trackers, false);
    }
    case "dupe_ignore_changed": {
      const tracker = action.tracker.trim().toUpperCase();
      if (!tracker) return state;
      const ignored = new Set(state.ignoredDupesFor);
      if (action.ignored) ignored.add(tracker);
      else ignored.delete(tracker);
      return {
        ...state,
        ignoredDupesFor: [...ignored],
      };
    }
    case "release_name_confirmed": {
      const tracker = action.tracker.trim().toUpperCase();
      if (!tracker) return state;
      return {
        ...state,
        releaseNameOverrides: {
          ...state.releaseNameOverrides,
          [tracker]: action.value,
        },
      };
    }
    case "questionnaire_answered": {
      const tracker = action.tracker.trim().toUpperCase();
      const key = action.key.trim();
      if (!tracker || !key) return state;
      return {
        ...state,
        questionnaireAnswers: {
          ...state.questionnaireAnswers,
          [tracker]: { ...state.questionnaireAnswers[tracker], [key]: action.value },
        },
      };
    }
    case "upload_options_changed":
      return {
        ...state,
        uploadOptions: { ...state.uploadOptions, ...action.value },
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
    case "screenshot_final_artifacts_changed":
      return {
        ...state,
        screenshots: {
          ...state.screenshots,
          finalSelectionArtifactIDs: Array.from(
            new Set(action.artifactIDs.map((artifactID) => artifactID.trim()).filter(Boolean)),
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
          selections: action.reseedDrafts
            ? action.plan.SuggestedSelections || []
            : state.screenshots.selections,
          finalSelectionArtifactIDs:
            action.finalSelectionArtifactIDs ??
            (action.reseedDrafts
              ? (action.plan.FinalSelections || []).map((image) => image.Path).filter(Boolean)
              : state.screenshots.finalSelectionArtifactIDs),
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
          selectedArtifactIDs: action.candidates
            .map((item) => item.image.artifactID)
            .filter(Boolean),
          failures: action.failures ?? [],
          failedHosts: action.failedHosts ?? state.uploadedImages.failedHosts,
        },
      };
    case "workflow_upload_candidates_changed": {
      const available = new Set(
        action.candidates.map((candidate) => candidate.image.artifactID).filter(Boolean),
      );
      const known = new Set(
        (state.uploadedImages.value?.candidates || [])
          .map((candidate) => candidate.image.artifactID)
          .filter(Boolean),
      );
      const selected = new Set(
        state.uploadedImages.selectedArtifactIDs.filter((artifactID) => available.has(artifactID)),
      );
      for (const artifactID of available) {
        if (!known.has(artifactID)) selected.add(artifactID);
      }
      return {
        ...state,
        uploadedImages: {
          ...state.uploadedImages,
          value: {
            candidates: action.candidates,
            uploaded: state.uploadedImages.value?.uploaded || [],
          },
          selectedArtifactIDs: [...selected],
        },
      };
    }
    case "uploaded_images_progress_reset":
      if (!workflowMatches(state, "uploadedImages", action.sessionRevision, action.revision))
        return state;
      return {
        ...state,
        uploadedImages: {
          ...state.uploadedImages,
          ...(action.correlationID ? { failedHosts: [] } : {}),
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
    case "upload_image_selected": {
      const selected = new Set(state.uploadedImages.selectedArtifactIDs);
      if (action.selected) selected.add(action.artifactID);
      else selected.delete(action.artifactID);
      return {
        ...state,
        uploadedImages: { ...state.uploadedImages, selectedArtifactIDs: [...selected] },
      };
    }
    case "upload_images_selected_all":
      return {
        ...state,
        uploadedImages: {
          ...state.uploadedImages,
          selectedArtifactIDs: action.selected
            ? (state.uploadedImages.value?.candidates || [])
                .map((item) => item.image.artifactID)
                .filter(Boolean)
            : [],
        },
      };
    case "description_edited": {
      const groupKey = action.groupKey.trim();
      if (!groupKey) return state;
      return {
        ...state,
        descriptions: {
          ...state.descriptions,
          inputRevision: state.descriptions.inputRevision + 1,
          rawByGroup: { ...state.descriptions.rawByGroup, [groupKey]: action.raw },
          dirtyGroups: Array.from(new Set([...state.descriptions.dirtyGroups, groupKey])),
          notice: "",
        },
      };
    }
    case "description_dirty_cleared": {
      const groupKey = action.groupKey?.trim() || "";
      const keepEntry = ([key]: [string, string]) => !groupKey || key !== groupKey;
      return {
        ...state,
        descriptions: {
          ...state.descriptions,
          rawByGroup: Object.fromEntries(
            Object.entries(state.descriptions.rawByGroup).filter(keepEntry),
          ),
          renderedByGroup: Object.fromEntries(
            Object.entries(state.descriptions.renderedByGroup).filter(keepEntry),
          ),
          dirtyGroups: groupKey
            ? state.descriptions.dirtyGroups.filter((key) => key !== groupKey)
            : [],
          notice: action.notice || "",
        },
      };
    }
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
  }
};
