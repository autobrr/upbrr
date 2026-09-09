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
  CorrectionFieldRef,
  ReleaseCorrectionValues,
} from "../api/generated/release-workflow";
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
  inputEditRevision: number;
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
  correctionDirty: boolean;
  inputEditRevision: number;
  preparationIntent: PreparationIntent;
  correctionResetFields: readonly CorrectionFieldRef[];
  correctionConfirmFields: readonly CorrectionFieldRef[];
  correctionValueFields: readonly CorrectionFieldRef[];
  trackerInputAnswers: Readonly<Record<string, Readonly<Record<string, string | null>>>>;
  playlist: PlaylistState;
  release: ReleaseRef | null;
  preview: MetadataPreview | null;
  selectedTrackers: readonly string[];
  trackerSelectionTouched: boolean;
  trackerSelectionInitialized: boolean;
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
  | Readonly<{ type: "correction_reset"; field: CorrectionFieldRef }>
  | Readonly<{ type: "correction_confirmed"; field: CorrectionFieldRef }>
  | Readonly<{
      type: "tracker_input_answered";
      tracker: string;
      key: string;
      value: string | null;
    }>
  | Readonly<{ type: "tracker_source_id_changed"; tracker: string; value: string }>
  | Readonly<{ type: "preparation_policy_changed"; value: PreparationIntent["policy"] }>
  | Readonly<{ type: "client_search_changed"; value: PreparationIntent["search"] }>
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
      inputEditRevision: number;
      correlationID: string;
      intent: PreparationIntent;
    }>
  | Readonly<{
      type: "preparation_succeeded";
      sourcePath: string;
      commandRevision: number;
      correlationID: string;
      preview: MetadataPreview;
      intent?: PreparationIntent;
      trackerInputsAccepted?: boolean;
      selectedTrackers?: readonly string[];
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
  | Readonly<{ type: "trackers_received"; trackers: readonly string[] }>
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
  trackerSourceIDs: {},
  policy: { keepFolder: false, keepImages: false, onlyID: false },
  search: { skip: false, client: "" },
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
  trackerSourceIDs: { ...intent.trackerSourceIDs },
  policy: { ...intent.policy },
  search: { ...intent.search },
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
    inputEditRevision: 0,
    status: "idle",
    error: "",
    failure: null,
  },
  preparationDirty: false,
  correctionDirty: false,
  inputEditRevision: 0,
  preparationIntent: emptyIntent(),
  correctionResetFields: [],
  correctionConfirmFields: [],
  correctionValueFields: [],
  trackerInputAnswers: {},
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
  trackerSelectionInitialized: false,
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

const correctionRefKey = (value: CorrectionFieldRef) =>
  `${value.field}\u0000${value.trackId || ""}`;

const mergeCorrectionRefs = (
  previous: readonly CorrectionFieldRef[],
  changed: readonly CorrectionFieldRef[],
) => {
  const merged = new Map(previous.map((field) => [correctionRefKey(field), field]));
  changed.forEach((field) => merged.set(correctionRefKey(field), field));
  return [...merged.values()];
};

const sameCorrectionValue = (left: unknown, right: unknown) =>
  JSON.stringify(left) === JSON.stringify(right);

const identityFieldKeys = {
  "identity.tmdb": "TMDBID",
  "identity.imdb": "IMDBID",
  "identity.tvdb": "TVDBID",
  "identity.tvmaze": "TVmazeID",
  "identity.mal": "MALID",
} as const;

const releaseNameFieldKeys = {
  "release_name.category": "Category",
  "release_name.type": "Type",
  "release_name.source": "Source",
  "release_name.resolution": "Resolution",
  "release_name.tag": "Tag",
  "release_name.service": "Service",
  "release_name.edition": "Edition",
  "release_name.season": "Season",
  "release_name.episode": "Episode",
  "release_name.episode_title": "EpisodeTitle",
  "release_name.manual_year": "ManualYear",
  "release_name.manual_date": "ManualDate",
  "release_name.use_season_episode": "UseSeasonEpisode",
  "release_name.no_season": "NoSeason",
  "release_name.no_year": "NoYear",
  "release_name.no_aka": "NoAKA",
  "release_name.no_tag": "NoTag",
  "release_name.no_episode_title": "NoEpisodeTitle",
  "release_name.no_distributor": "NoDistributor",
  "release_name.no_edition": "NoEdition",
  "release_name.no_dub": "NoDub",
  "release_name.no_dual": "NoDual",
  "release_name.dual_audio": "DualAudio",
  "release_name.region": "Region",
} as const;

const metadataFieldKeys = {
  "metadata.distributor": "Distributor",
  "metadata.original_language": "OriginalLanguage",
  "metadata.personal_release": "PersonalRelease",
  "metadata.commentary": "Commentary",
  "metadata.web_dv": "WebDV",
  "metadata.stream_optimized": "StreamOptimized",
  "metadata.anime": "Anime",
  "metadata.title": "Title",
  "metadata.alternate_title": "AlternateTitle",
  "metadata.original_title": "OriginalTitle",
  "metadata.genres": "Genres",
  "metadata.audio_languages": "AudioLanguages",
  "metadata.subtitle_languages": "SubtitleLanguages",
  "metadata.hardcoded_subs": "HardcodedSubs",
  "metadata.hardcoded_subtitle_languages": "HardcodedSubtitleLanguages",
} as const;

const changedCorrectionFields = (
  previous: PreparationIntent,
  next: PreparationIntent,
): CorrectionFieldRef[] => {
  const changed: CorrectionFieldRef[] = [];
  const compareFields = (
    before: object,
    after: object,
    fields: Readonly<Record<string, string>>,
  ) => {
    for (const [field, key] of Object.entries(fields)) {
      const beforeValue = before[key as keyof typeof before];
      const afterValue = after[key as keyof typeof after];
      if (
        Object.prototype.hasOwnProperty.call(before, key) !==
          Object.prototype.hasOwnProperty.call(after, key) ||
        !sameCorrectionValue(beforeValue, afterValue)
      ) {
        changed.push({ field });
      }
    }
  };
  compareFields(previous.identity, next.identity, identityFieldKeys);
  compareFields(previous.releaseName, next.releaseName, releaseNameFieldKeys);
  compareFields(previous.metadata, next.metadata, metadataFieldKeys);

  const previousTracks = new Map(
    (previous.metadata.TrackLanguages || []).map((track) => [track.trackId, track]),
  );
  const nextTracks = new Map(
    (next.metadata.TrackLanguages || []).map((track) => [track.trackId, track]),
  );
  for (const trackID of new Set([...previousTracks.keys(), ...nextTracks.keys()])) {
    if (!sameCorrectionValue(previousTracks.get(trackID), nextTracks.get(trackID))) {
      changed.push({ field: "metadata.track_languages", trackId: trackID });
    }
  }
  return changed;
};

/** Selects only fields edited in the current correction patch. */
export const correctionValuesFor = (
  intent: PreparationIntent,
  fields: readonly CorrectionFieldRef[],
): ReleaseCorrectionValues => {
  const identity: Record<string, unknown> = {};
  const releaseName: Record<string, unknown> = {};
  const metadata: Record<string, unknown> = {};
  const trackIDs = new Set(
    fields
      .filter((field) => field.field === "metadata.track_languages")
      .map((field) => field.trackId || ""),
  );
  for (const field of fields) {
    const identityKey = identityFieldKeys[field.field as keyof typeof identityFieldKeys];
    if (identityKey && Object.prototype.hasOwnProperty.call(intent.identity, identityKey)) {
      identity[identityKey] = intent.identity[identityKey];
    }
    const releaseNameKey = releaseNameFieldKeys[field.field as keyof typeof releaseNameFieldKeys];
    if (
      releaseNameKey &&
      Object.prototype.hasOwnProperty.call(intent.releaseName, releaseNameKey)
    ) {
      releaseName[releaseNameKey] = intent.releaseName[releaseNameKey];
    }
    const metadataKey = metadataFieldKeys[field.field as keyof typeof metadataFieldKeys];
    if (metadataKey && Object.prototype.hasOwnProperty.call(intent.metadata, metadataKey)) {
      metadata[metadataKey] = intent.metadata[metadataKey];
    }
  }
  const trackLanguages = (intent.metadata.TrackLanguages || []).filter((track) =>
    trackIDs.has(track.trackId),
  );
  if (trackLanguages.length > 0) metadata.TrackLanguages = trackLanguages;
  return {
    Identity: identity,
    ReleaseName: releaseName,
    Metadata: metadata,
  } as ReleaseCorrectionValues;
};

const withoutProperty = <T extends object>(value: T, key: keyof T): T => {
  const next = { ...value };
  Reflect.deleteProperty(next, key);
  return next;
};

const withoutCorrection = (
  intent: PreparationIntent,
  ref: CorrectionFieldRef,
): PreparationIntent => {
  const identityKey = identityFieldKeys[ref.field as keyof typeof identityFieldKeys];
  if (identityKey) return { ...intent, identity: withoutProperty(intent.identity, identityKey) };
  const releaseNameKey = releaseNameFieldKeys[ref.field as keyof typeof releaseNameFieldKeys];
  if (releaseNameKey) {
    return { ...intent, releaseName: withoutProperty(intent.releaseName, releaseNameKey) };
  }
  if (ref.field === "metadata.track_languages") {
    const retained = (intent.metadata.TrackLanguages || []).filter(
      (correction) => correction.trackId !== ref.trackId,
    );
    return {
      ...intent,
      metadata:
        retained.length > 0
          ? { ...intent.metadata, TrackLanguages: retained }
          : withoutProperty(intent.metadata, "TrackLanguages"),
    };
  }
  const metadataKey = metadataFieldKeys[ref.field as keyof typeof metadataFieldKeys];
  if (metadataKey) return { ...intent, metadata: withoutProperty(intent.metadata, metadataKey) };
  return intent;
};

const preparationIntentChanged = (
  state: SessionState,
  intent: PreparationIntent,
  correction = true,
  changedFields: readonly CorrectionFieldRef[] = [],
): SessionState => {
  const changedKeys = new Set(changedFields.map(correctionRefKey));
  return {
    ...state,
    preparationIntent: intent,
    preparationDirty: Boolean(state.release),
    correctionDirty: state.correctionDirty || correction,
    inputEditRevision: state.inputEditRevision + 1,
    correctionValueFields: mergeCorrectionRefs(state.correctionValueFields, changedFields),
    correctionResetFields: state.correctionResetFields.filter(
      (field) => !changedKeys.has(correctionRefKey(field)),
    ),
    correctionConfirmFields: state.correctionConfirmFields.filter(
      (field) => !changedKeys.has(correctionRefKey(field)),
    ),
  };
};

const trackerSelectionChanged = (
  state: SessionState,
  trackers: readonly string[],
  touched: boolean,
): SessionState => ({
  ...state,
  preparationDirty: touched ? Boolean(state.release) : state.preparationDirty,
  inputEditRevision: touched ? state.inputEditRevision + 1 : state.inputEditRevision,
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
  trackerSelectionInitialized: true,
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
          inputEditRevision: 0,
          status: "idle",
          error: "",
          failure: null,
        },
        preparationDirty: false,
        correctionDirty: false,
        inputEditRevision: 0,
        preparationIntent: emptyIntent(),
        correctionResetFields: [],
        correctionConfirmFields: [],
        correctionValueFields: [],
        trackerInputAnswers: {},
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
        trackerSelectionInitialized: false,
        ignoredDupesFor: [],
        releaseNameOverrides: {},
        questionnaireAnswers: {},
        uploadOptions: emptyOptions(),
        duplicatesError: "",
        uploadError: "",
      };
    }
    case "source_lookup_changed":
      return preparationIntentChanged(
        state,
        {
          ...state.preparationIntent,
          sourceLookupURL: action.value,
        },
        false,
      );
    case "identity_changed": {
      const intent = {
        ...state.preparationIntent,
        identity: { ...action.value },
      };
      return preparationIntentChanged(
        state,
        intent,
        true,
        changedCorrectionFields(state.preparationIntent, intent),
      );
    }
    case "metadata_changed": {
      const intent = {
        ...state.preparationIntent,
        metadata: { ...action.value },
      };
      return preparationIntentChanged(
        state,
        intent,
        true,
        changedCorrectionFields(state.preparationIntent, intent),
      );
    }
    case "release_name_changed": {
      const intent = {
        ...state.preparationIntent,
        releaseName: { ...action.value },
      };
      return preparationIntentChanged(
        state,
        intent,
        true,
        changedCorrectionFields(state.preparationIntent, intent),
      );
    }
    case "correction_reset": {
      const key = correctionRefKey(action.field);
      return {
        ...state,
        preparationIntent: withoutCorrection(state.preparationIntent, action.field),
        preparationDirty: Boolean(state.release),
        correctionDirty: true,
        inputEditRevision: state.inputEditRevision + 1,
        correctionResetFields: [
          ...state.correctionResetFields.filter((field) => correctionRefKey(field) !== key),
          { ...action.field },
        ],
        correctionConfirmFields: state.correctionConfirmFields.filter(
          (field) => correctionRefKey(field) !== key,
        ),
        correctionValueFields: state.correctionValueFields.filter(
          (field) => correctionRefKey(field) !== key,
        ),
      };
    }
    case "correction_confirmed": {
      const key = correctionRefKey(action.field);
      return {
        ...state,
        preparationDirty: Boolean(state.release),
        correctionDirty: true,
        inputEditRevision: state.inputEditRevision + 1,
        correctionResetFields: state.correctionResetFields.filter(
          (field) => correctionRefKey(field) !== key,
        ),
        correctionConfirmFields: [
          ...state.correctionConfirmFields.filter((field) => correctionRefKey(field) !== key),
          { ...action.field },
        ],
        correctionValueFields: state.correctionValueFields.filter(
          (field) => correctionRefKey(field) !== key,
        ),
      };
    }
    case "tracker_input_answered": {
      const tracker = action.tracker.trim().toUpperCase();
      const key = action.key.trim();
      if (!tracker || !key) return state;
      const current = { ...(state.trackerInputAnswers[tracker] || {}) };
      current[key] = action.value;
      const trackerInputAnswers = { ...state.trackerInputAnswers };
      trackerInputAnswers[tracker] = current;
      return {
        ...state,
        trackerInputAnswers,
        preparationDirty: Boolean(state.release),
        inputEditRevision: state.inputEditRevision + 1,
      };
    }
    case "tracker_source_id_changed": {
      const tracker = action.tracker.trim().toUpperCase();
      if (!tracker) return state;
      const trackerSourceIDs = { ...state.preparationIntent.trackerSourceIDs };
      const value = action.value.trim();
      if (value) trackerSourceIDs[tracker] = value;
      else delete trackerSourceIDs[tracker];
      return preparationIntentChanged(
        state,
        { ...state.preparationIntent, trackerSourceIDs },
        false,
      );
    }
    case "preparation_policy_changed":
      return preparationIntentChanged(
        state,
        { ...state.preparationIntent, policy: { ...action.value } },
        false,
      );
    case "client_search_changed":
      return preparationIntentChanged(
        state,
        { ...state.preparationIntent, search: { ...action.value } },
        false,
      );
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
          selected: candidates.length === 1 ? [candidates[0].id] : [],
          useAll: false,
          error: action.error,
        },
      };
    }
    case "playlist_draft_changed":
      return {
        ...state,
        preparationDirty: Boolean(state.release),
        inputEditRevision: state.inputEditRevision + 1,
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
          inputEditRevision: action.inputEditRevision,
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
      const inputChanged = state.inputEditRevision !== state.preparation.inputEditRevision;
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
        preparationDirty: inputChanged,
        correctionDirty: inputChanged ? state.correctionDirty : false,
        preparationIntent:
          !inputChanged && action.intent
            ? clonePreparationIntent(action.intent)
            : state.preparationIntent,
        correctionResetFields: inputChanged ? state.correctionResetFields : [],
        correctionConfirmFields: inputChanged ? state.correctionConfirmFields : [],
        correctionValueFields: inputChanged ? state.correctionValueFields : [],
        trackerInputAnswers:
          !inputChanged && action.trackerInputsAccepted
            ? Object.fromEntries(
                Object.entries(state.trackerInputAnswers).filter(
                  ([tracker]) => !action.selectedTrackers?.includes(tracker),
                ),
              )
            : state.trackerInputAnswers,
        selectedTrackers:
          !inputChanged && action.selectedTrackers !== undefined
            ? normalizeNames(action.selectedTrackers)
            : state.selectedTrackers,
        trackerSelectionTouched:
          inputChanged || action.selectedTrackers === undefined
            ? state.trackerSelectionTouched
            : false,
        trackerSelectionInitialized:
          inputChanged || action.selectedTrackers === undefined
            ? state.trackerSelectionInitialized
            : true,
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
    case "trackers_received":
      return state.trackerSelectionTouched
        ? state
        : trackerSelectionChanged(state, action.trackers, false);
    case "default_trackers_received": {
      if (action.sessionRevision !== state.sessionRevision || state.trackerSelectionInitialized) {
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
