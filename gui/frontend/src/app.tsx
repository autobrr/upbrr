// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { KeyboardEvent as ReactKeyboardEvent } from "react";
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { OnFileDrop, OnFileDropOff } from "../wailsjs/runtime/runtime";
import {
  EventsOn,
  isBrowserMode,
  isBrowserNativeBrowseAvailable,
  isRuntimePathCaseInsensitive,
  subscribeBrowserNativeBrowseAvailability,
} from "./utils/runtime";
import DescriptionBuilderPage from "./pages/description_builder";
import BlurayCandidatesPage from "./pages/bluray_candidates";
import DupeCheckPage from "./pages/dupe_check";
import InputPage from "./pages/input";
import HistoryPage from "./pages/history/index";
import LoggingPage from "./pages/logging";
import PlaylistSelectionPage from "./pages/playlist_selection";
import ScreenshotsPage from "./pages/screenshots";
import MenuImagesPage from "./pages/menu_images";
import SettingsPage from "./pages/settings";
import TrackerDataPage from "./pages/tracker_data";
import TrackerUploadPage from "./pages/tracker_upload";
import UploadImagesPage from "./pages/upload_images";
import { useSettingsState } from "./hooks/useSettingsState";
import { useScreenshots } from "./hooks/useScreenshots";
import { useUploadImages } from "./hooks/useUploadImages";
import { useTrackerIcons } from "./hooks/useTrackerIcons";
import { cn } from "./utils/cn";
import type {
  ConfigMap,
  ApplicationInfo,
  BrowseDirectoryResponse,
  DescriptionBuilderPreview,
  DupeCheckResult,
  DupeCheckSnapshot,
  DupeCheckSummary,
  ExternalIDInfo,
  ExternalIDOverrides,
  ExternalIDs,
  HistoryEntry,
  HistoryOverview,
  ImageHostPolicyMetadata,
  MetadataOverrideEditState,
  MetadataOverrides,
  MetadataOverrideTouchedState,
  MetadataPreview,
  MetadataProgressUpdate,
  PreparationPreview,
  ReleaseNameEditState,
  ReleaseNameOverrides,
  ReleaseNameTouchedState,
  ScreenshotImage,
  ScreenshotPlan,
  ScreenshotPreviewImage,
  ScreenshotPurpose,
  ScreenshotResult,
  ScreenshotSelection,
  TrackerQuestionnaire,
  TrackerAuthCapability,
  TrackerAuthLoginRequest,
  TrackerAuthStatus,
  TrackerDryRunPreview,
  TrackerUploadSnapshot,
  WebAuthStatus,
  UploadedImageLink,
  UploadImagesResult,
  UploadProgressUpdate,
} from "./types";
import {
  formatLabel,
  isSkipAutoTorrentEnabled,
  normalizeDefaultTrackerList,
} from "./utils/settings";
import {
  addSourcePathHistoryEntry,
  defaultInputHistoryLimit,
  filterBrowseEntries,
  inferSourcePathMode,
  normalizeSourcePathHistory,
  resolveInputHistoryLimit,
  sameSourcePath,
  type SourcePathHistoryEntry,
  type SourcePathMode,
  sourcePathHistoryStorageKey,
} from "./utils/inputHistory";
import { handleExternalLinkClick } from "./utils/externalLinks";
import { normalizeJobStatus } from "./utils/jobStatus";
import { isMetadataProgressPathMatch } from "./utils/metadataProgress";
import {
  hasFilteredEmptyUploadTrackerSelection as hasFilteredEmptyUploadTrackerSelectionState,
  resolveSelectedUploadTrackers,
} from "./utils/trackerSelection";
import { normalizeMetadataOverrides, normalizeOverrides, normalizeReleaseOverrides } from "./utils";

const appLayoutClass =
  "relative z-[1] block min-h-screen ml-[204px] max-[960px]:ml-0 max-[960px]:pb-[78px]";

const sidebarClass =
  "fixed left-0 top-0 z-[1000] flex h-screen w-[204px] flex-col gap-2.5 border-r border-white/10 bg-[var(--panel)]/95 p-2.5 backdrop-blur max-[960px]:bottom-0 max-[960px]:top-auto max-[960px]:h-auto max-[960px]:w-full max-[960px]:flex-row max-[960px]:items-center max-[960px]:gap-2 max-[960px]:border-r-0 max-[960px]:border-t max-[960px]:p-2";

const sidebarGroupClass =
  "grid gap-1 rounded-lg border border-[rgba(148,163,184,0.18)] bg-[rgba(148,163,184,0.08)] p-1.5 max-[960px]:flex max-[960px]:flex-wrap max-[960px]:gap-1 max-[960px]:p-1";

const sidebarFooterClass = `${sidebarGroupClass} mt-auto max-[960px]:mt-0`;

const navButtonClass = (active: boolean, nested = false) =>
  cn(
    "w-full rounded-md border border-transparent bg-transparent px-2 py-1.5 text-left text-[0.84rem] font-semibold leading-tight text-[var(--muted)] transition hover:bg-white/10 hover:text-[var(--text)] max-[960px]:w-auto max-[960px]:py-1.5",
    nested && "pl-4 text-[0.8rem] font-medium max-[960px]:pl-2",
    active &&
      "border-[var(--sidebar-active-border)] bg-[var(--sidebar-active-bg)] text-[var(--sidebar-active-text)] shadow-[0_8px_22px_rgba(245,185,66,0.18)] hover:bg-[var(--sidebar-active-bg)] hover:text-[var(--sidebar-active-text)]",
  );

const sidebarButtonClass = (active = false) =>
  cn(
    "flex min-h-[30px] w-full items-center justify-start gap-1.5 rounded-md border border-transparent bg-transparent px-2 py-1.5 text-[0.84rem] font-semibold leading-tight text-[var(--muted)] transition hover:bg-white/10 hover:text-[var(--text)] max-[960px]:w-auto max-[960px]:min-h-7 max-[960px]:py-1",
    active &&
      "border-[var(--sidebar-active-border)] bg-[var(--sidebar-active-bg)] text-[var(--sidebar-active-text)] shadow-[0_8px_22px_rgba(245,185,66,0.18)] hover:bg-[var(--sidebar-active-bg)] hover:text-[var(--sidebar-active-text)]",
  );

const sidebarAppDetailsClass =
  "mt-1 grid grid-cols-[1fr_auto] items-center gap-1.5 px-2 py-1.5 text-[0.72rem] leading-tight text-[var(--muted)] max-[960px]:hidden";

type AppBridgeWithApplicationInfo = {
  GetApplicationInfo?: () => Promise<ApplicationInfo>;
};

const emptyDupeSummary: DupeCheckSummary = {
  SourcePath: "",
  Results: [],
  Notes: [],
};

const emptyPreview: MetadataPreview = {
  SourcePath: "",
  TrackerName: "",
  ReleaseName: "",
  Warnings: [],
  ReleaseNameOverrides: {},
  ExternalIDs: {
    TMDBID: 0,
    IMDBID: 0,
    TVDBID: 0,
    TVmazeID: 0,
    Category: "",
    SourceTMDB: "",
    SourceIMDB: "",
    SourceTVDB: "",
    SourceTVmaze: "",
  },
  ExternalIDCandidates: {
    TMDB: [],
    IMDB: [],
    TMDBAutoSelected: false,
    IMDBAutoSelected: false,
  },
  ExternalIDInfo: [],
  ExternalPreview: [],
  Bluray: undefined,
  TrackerData: [],
  TrackerRuleFailures: {},
};

const emptyTrackerDryRun: TrackerDryRunPreview = {
  SourcePath: "",
  Trackers: [],
};

const cloneQuestionnaireAnswers = (input: Record<string, Record<string, string>>) =>
  Object.fromEntries(
    Object.entries(input).map(([tracker, values]) => [
      tracker,
      Object.fromEntries(
        Object.entries(values || {}).map(([key, value]) => [key, String(value ?? "")]),
      ),
    ]),
  );

const buildQuestionnaireAnswerDefaults = (
  questionnaire: TrackerQuestionnaire | null | undefined,
  existing: Record<string, string> | undefined,
) => {
  const next: Record<string, string> = { ...(existing || {}) };
  (questionnaire?.Fields || []).forEach((field) => {
    if (next[field.Key] === undefined) {
      next[field.Key] = field.Value || "";
    }
  });
  return next;
};

const splitTrackerLabel = (value: string) =>
  value
    .split(",")
    .map((entry) => entry.toLowerCase().trim())
    .filter((entry) => entry.length > 0);

/** Returns normalized tracker labels whose skipped duplicate-check result should block upload. */
export const ruleBlockingTrackerLabels = (result: Pick<DupeCheckResult, "Tracker">) => {
  const next = new Set<string>();
  const normalized = result.Tracker.toLowerCase().trim();
  const splitLabels = splitTrackerLabel(result.Tracker);

  if (normalized) {
    next.add(normalized);
  }
  splitLabels.forEach((tracker) => next.add(tracker));
  return next;
};

const isFinishedDupeTrackerStatus = (status: string) => {
  switch (status.toLowerCase().trim()) {
    case "complete":
    case "completed":
    case "skipped":
    case "failed":
    case "canceled":
      return true;
    default:
      return false;
  }
};

/**
 * Converts a finished per-tracker job state into the same result shape used by
 * grouped summaries. Queued/running states without a concrete result are ignored
 * so upload eligibility is derived only from completed tracker outcomes.
 */
const dupeResultFromTrackerState = (
  state: DupeCheckSnapshot["trackers"][number],
): DupeCheckResult | null => {
  const resultTracker = String(state.result?.Tracker || "").trim();
  const status = String(state.result?.Status || state.status || "").trim();
  if (!resultTracker && !isFinishedDupeTrackerStatus(status)) return null;
  const tracker = String(resultTracker || state.tracker || "").trim();
  if (!tracker) return null;
  return {
    ...state.result,
    Tracker: tracker,
    Status: status,
  };
};

/** Returns per-tracker dupe results from a snapshot, excluding unfinished placeholders. */
const dupeResultsFromSnapshot = (snapshot: DupeCheckSnapshot | null) =>
  (snapshot?.trackers || [])
    .map(dupeResultFromTrackerState)
    .filter((result): result is DupeCheckResult => Boolean(result));

const emptyDescriptionBuilder: DescriptionBuilderPreview = {
  SourcePath: "",
  Groups: [],
};

/**
 * Returns true when every available release tracker has an explicit false selection.
 *
 * Missing tracker keys are treated as uninitialized state, not user deselection.
 */
export const hasExplicitEmptyReleaseTrackerSelection = (
  trackerUploadItems: Array<{ name: string }>,
  releasePageTrackerSelection: Record<string, boolean>,
) => {
  if (trackerUploadItems.length === 0) {
    return false;
  }
  return trackerUploadItems.every(
    (item) =>
      Object.prototype.hasOwnProperty.call(releasePageTrackerSelection, item.name) &&
      !releasePageTrackerSelection[item.name],
  );
};

const hasOwnSelection = (value: Record<string, boolean>, key: string) =>
  Object.prototype.hasOwnProperty.call(value, key);

/**
 * Normalizes source paths for lightweight context comparisons.
 *
 * This keeps host path identity checks independent of slash direction and
 * trailing separators without replacing the runtime-aware same-path helper.
 */
const normalizePathContext = (value: string, caseInsensitive: boolean) => {
  let normalized = value.trim().replace(/\\/g, "/").replace(/\/+$/, "");
  if (caseInsensitive) {
    normalized = normalized.toLowerCase();
  }
  return normalized;
};

const normalizeBdmvPathContext = (value: string, caseInsensitive: boolean) =>
  normalizePathContext(value, caseInsensitive).replace(/(^|\/)bdmv(?=\/|$)/gi, "$1BDMV");

/**
 * Returns true when two source paths refer to the same eligibility context.
 *
 * A selected Blu-ray folder may be represented by either the release root or
 * its BDMV child folder; both must share dupe/rule tracker eligibility.
 */
const isSourcePathContextMatch = (left: string, right: string, caseInsensitive: boolean) => {
  if (sameSourcePath(left, right, caseInsensitive)) {
    return true;
  }
  const normalizedLeft = normalizeBdmvPathContext(left, caseInsensitive);
  const normalizedRight = normalizeBdmvPathContext(right, caseInsensitive);
  if (!normalizedLeft || !normalizedRight) {
    return false;
  }
  return (
    normalizedLeft === `${normalizedRight}/BDMV` || normalizedRight === `${normalizedLeft}/BDMV`
  );
};

const upsertBuilderGroup = (
  preview: DescriptionBuilderPreview,
  nextGroup: DescriptionBuilderPreview["Groups"][number],
): DescriptionBuilderPreview => {
  const nextGroups = [...(preview.Groups || [])];
  const existingIndex = nextGroups.findIndex((group) => group.GroupKey === nextGroup.GroupKey);
  if (existingIndex >= 0) {
    nextGroups[existingIndex] = nextGroup;
  } else {
    nextGroups.push(nextGroup);
  }
  return {
    ...preview,
    Groups: nextGroups,
  };
};

const bdinfoProgressEvent = "bdinfo:progress";
const metadataProgressEvent = "metadata:progress";
const dupeCheckEventPrefix = "dupe:job:";
const trackerUploadEventPrefix = "upload:job:";
const trackerUploadProgressEvent = "upload:progress";
const runLogLevels = ["error", "warn", "info", "debug", "trace"] as const;

type SourcePathSelection = {
  path: string;
  mode: SourcePathMode;
  waitsForPlaylistSelection: boolean;
};

const progressUpdatePrefixes = new Set([
  "scanning",
  "initialize",
  "playlist",
  "clipinfo",
  "stream",
]);

const progressLineKey = (line: string): string | null => {
  const match = /^([A-Za-z_]+):\s+/.exec(line);
  if (!match) {
    return null;
  }
  const key = match[1].toLowerCase();
  return progressUpdatePrefixes.has(key) ? key : null;
};

const appendBoundedProgressLine = (lines: string[], line: string) => {
  if (lines.length >= 300) {
    return [...lines.slice(-299), line];
  }
  return [...lines, line];
};

const upsertProgressLine = (lines: string[], line: string) => {
  const key = progressLineKey(line);
  if (!key) {
    return appendBoundedProgressLine(lines, line);
  }

  const existingIndex = lines.findIndex((existing) => progressLineKey(existing) === key);
  if (existingIndex < 0) {
    return appendBoundedProgressLine(lines, line);
  }

  if (lines[existingIndex] === line) {
    return lines;
  }

  const next = [...lines];
  next[existingIndex] = line;
  return next;
};

/** Returns whether a background job status should keep progress recovery active. */
const isRunningJobStatus = (status: string) => {
  const normalized = normalizeJobStatus(status);
  return normalized === "queued" || normalized === "running";
};

declare global {
  var go:
    | {
        guiapp?: {
          App?: {
            BrowsePath: () => Promise<string>;
            BrowseFile: () => Promise<string>;
            BrowseFiles: () => Promise<string[]>;
            BrowseImageFiles: () => Promise<string[]>;
            BrowseFolder: () => Promise<string>;
            BrowseDirectory: (
              path: string,
              mode: "file" | "folder",
            ) => Promise<BrowseDirectoryResponse>;
            OpenExternalURL?: (url: string) => Promise<void>;
            DetectDiscType: (path: string) => Promise<string>;
            FetchMetadata: (
              path: string,
              sourceLookupURL: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              metadataOverrides: MetadataOverrides,
              trackers: string[],
            ) => Promise<MetadataPreview>;
            ResetMetadata: (
              path: string,
              sourceLookupURL: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              metadataOverrides: MetadataOverrides,
              trackers: string[],
            ) => Promise<MetadataPreview>;
            SelectBlurayCandidate: (path: string, releaseID: string) => Promise<MetadataPreview>;
            FetchDescriptionBuilder: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              metadataOverrides: MetadataOverrides,
              trackers: string[],
              ignoreDupesFor: string[],
            ) => Promise<DescriptionBuilderPreview>;
            FetchPreparation: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              metadataOverrides: MetadataOverrides,
              trackers: string[],
              ignoreDupesFor: string[],
            ) => Promise<PreparationPreview>;
            FetchTrackerDryRun: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              metadataOverrides: MetadataOverrides,
              trackers: string[],
              ignoreDupesFor: string[],
              questionnaireAnswers: Record<string, Record<string, string>>,
              descriptionGroups: DescriptionBuilderPreview["Groups"],
              debug: boolean,
              noSeed: boolean,
              runLogLevel: string,
            ) => Promise<TrackerDryRunPreview>;
            CheckDupes: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              metadataOverrides: MetadataOverrides,
              trackers: string[],
            ) => Promise<DupeCheckSummary>;
            StartDupeCheck: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              metadataOverrides: MetadataOverrides,
              trackers: string[],
            ) => Promise<string>;
            CancelDupeCheck: (jobID: string) => Promise<void>;
            GetDupeCheckSnapshot: (jobID: string) => Promise<DupeCheckSnapshot>;
            FetchScreenshotPlan: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              metadataOverrides: MetadataOverrides,
            ) => Promise<ScreenshotPlan>;
            GenerateScreenshots: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              metadataOverrides: MetadataOverrides,
              selections: ScreenshotSelection[],
              purpose: ScreenshotPurpose,
            ) => Promise<ScreenshotResult>;
            PreviewScreenshotFrame: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              metadataOverrides: MetadataOverrides,
              timestampSeconds: number,
            ) => Promise<string>;
            DeleteScreenshot: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              metadataOverrides: MetadataOverrides,
              imagePath: string,
            ) => Promise<void>;
            SaveFinalScreenshotSelections: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              metadataOverrides: MetadataOverrides,
              images: ScreenshotImage[],
            ) => Promise<void>;
            ImportMenuImages: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              metadataOverrides: MetadataOverrides,
              paths: string[],
            ) => Promise<void>;
            ReadScreenshotImage: (path: string) => Promise<string>;
            ListUploadCandidates: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              metadataOverrides: MetadataOverrides,
            ) => Promise<ScreenshotImage[]>;
            ListUploadedImages: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              metadataOverrides: MetadataOverrides,
            ) => Promise<UploadedImageLink[]>;
            UploadImages: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              metadataOverrides: MetadataOverrides,
              trackers: string[],
              host: string,
              images: ScreenshotImage[],
            ) => Promise<UploadImagesResult>;
            DeleteUploadedImage: (path: string, imagePath: string, host: string) => Promise<void>;
            RenderDescription: (raw: string) => Promise<string>;
            SaveDescriptionOverride: (
              path: string,
              groupKey: string,
              raw: string,
              trackers: string[],
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              metadataOverrides: MetadataOverrides,
            ) => Promise<DescriptionBuilderPreview["Groups"][number]>;
            DiscoverPlaylists: (path: string) => Promise<any[]>;
            SavePlaylistSelection: (
              path: string,
              playlists: string[],
              useAll: boolean,
            ) => Promise<void>;
            LoadPlaylistSelection: (path: string) => Promise<any>;
            GetConfig: () => Promise<string>;
            GetDefaultConfig: () => Promise<string>;
            GetWebAuthStatus: () => Promise<WebAuthStatus>;
            CreateWebAuth: (username: string, password: string) => Promise<WebAuthStatus>;
            SaveConfig: (payload: string) => Promise<void>;
            ExportConfig: () => Promise<string>;
            ImportConfig: () => Promise<{ message: string; warnings: string[] }>;
            GetLogPath: () => Promise<string>;
            GetRecentLogs: (limit: number) => Promise<any[]>;
            StartLogStream: () => Promise<string>;
            StopLogStream: (streamID: string) => Promise<void>;
            GetLogExclusions: () => Promise<string[]>;
            UpdateLogExclusions: (patterns: string[]) => Promise<void>;
            ListKnownTrackers: () => Promise<string[]>;
            ListTrackerAuthCapabilities?: () => Promise<TrackerAuthCapability[]>;
            GetTrackerAuthStatus?: (tracker: string) => Promise<TrackerAuthStatus>;
            ImportTrackerAuthCookies?: (tracker: string) => Promise<TrackerAuthStatus>;
            ImportTrackerAuthCookieContent?: (
              tracker: string,
              fileName: string,
              content: string,
            ) => Promise<TrackerAuthStatus>;
            TestTrackerAuth?: (tracker: string) => Promise<TrackerAuthStatus>;
            LoginTrackerAuth?: (
              tracker: string,
              req: TrackerAuthLoginRequest,
            ) => Promise<TrackerAuthStatus>;
            SubmitTrackerAuth2FA?: (
              challengeID: string,
              code: string,
            ) => Promise<TrackerAuthStatus>;
            DeleteTrackerAuth?: (tracker: string) => Promise<TrackerAuthStatus>;
            GetImageHostPolicyMetadata: () => Promise<ImageHostPolicyMetadata>;
            ListHistory: () => Promise<HistoryEntry[]>;
            GetHistoryOverview: (sourcePath: string) => Promise<HistoryOverview>;
            DeleteHistoryRelease: (sourcePath: string) => Promise<void>;
            StartTrackerUpload: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              metadataOverrides: MetadataOverrides,
              trackers: string[],
              ignoreDupesFor: string[],
              questionnaireAnswers: Record<string, Record<string, string>>,
              descriptionGroups: DescriptionBuilderPreview["Groups"],
              debug: boolean,
              noSeed: boolean,
              runLogLevel: string,
            ) => Promise<string>;
            CancelTrackerUpload: (jobID: string) => Promise<void>;
            RetryFailedTrackerUpload: (jobID: string) => Promise<string>;
            GetTrackerUploadSnapshot: (jobID: string) => Promise<TrackerUploadSnapshot>;
            GetTrackerIcon?: (domain: string, customURL: string) => Promise<string>;
          };
        };
      }
    | undefined;
}

const parseIDInput = (provider: string, value: string) => {
  const trimmed = value.trim();
  if (!trimmed) return 0;
  let normalized = trimmed;
  if (provider === "imdb" && /^tt/i.test(trimmed)) {
    normalized = trimmed.slice(2);
  }
  if (!/^\d+$/.test(normalized)) return null;
  return Number(normalized);
};

const providerOrder = ["tmdb", "imdb", "tvdb", "tvmaze"] as const;

const filterAndOrderExternalIDs = (info: ExternalIDInfo[]) => {
  const orderIndex = new Map<string, number>(
    providerOrder.map((provider, index) => [provider, index]),
  );

  return [...info].sort((left, right) => {
    const leftIndex = orderIndex.get(left.Provider) ?? providerOrder.length;
    const rightIndex = orderIndex.get(right.Provider) ?? providerOrder.length;
    if (leftIndex !== rightIndex) return leftIndex - rightIndex;
    return left.Provider.localeCompare(right.Provider);
  });
};

const formatNumber = (value: number) => (value ? value.toString() : "");

const buildIDEditState = (ids: ExternalIDs) => ({
  tmdb: formatNumber(ids.TMDBID),
  imdb: formatNumber(ids.IMDBID),
  tvdb: formatNumber(ids.TVDBID),
  tvmaze: formatNumber(ids.TVmazeID),
});

const buildReleaseEditState = (overrides?: ReleaseNameOverrides): ReleaseNameEditState => ({
  category: overrides?.Category ?? "",
  type: overrides?.Type ?? "",
  source: overrides?.Source ?? "",
  resolution: overrides?.Resolution ?? "",
  tag: overrides?.Tag ?? "",
  service: overrides?.Service ?? "",
  edition: overrides?.Edition ?? "",
  season: overrides?.Season ?? "",
  episode: overrides?.Episode ?? "",
  episodeTitle: overrides?.EpisodeTitle ?? "",
  manualYear: overrides?.ManualYear ? overrides.ManualYear.toString() : "",
  manualDate: overrides?.ManualDate ?? "",
  useSeasonEpisode: Boolean(overrides?.UseSeasonEpisode),
  noSeason: Boolean(overrides?.NoSeason),
  noYear: Boolean(overrides?.NoYear),
  noAKA: Boolean(overrides?.NoAKA),
  noTag: Boolean(overrides?.NoTag),
  noEdition: Boolean(overrides?.NoEdition),
  noDub: Boolean(overrides?.NoDub),
  noDual: Boolean(overrides?.NoDual),
  dualAudio: Boolean(overrides?.DualAudio),
  region: overrides?.Region ?? "",
});

const buildReleaseTouchedState = (overrides?: ReleaseNameOverrides): ReleaseNameTouchedState => ({
  category: overrides?.Category !== undefined && overrides?.Category !== null,
  type: overrides?.Type !== undefined && overrides?.Type !== null,
  source: overrides?.Source !== undefined && overrides?.Source !== null,
  resolution: overrides?.Resolution !== undefined && overrides?.Resolution !== null,
  tag: overrides?.Tag !== undefined && overrides?.Tag !== null,
  service: overrides?.Service !== undefined && overrides?.Service !== null,
  edition: overrides?.Edition !== undefined && overrides?.Edition !== null,
  season: overrides?.Season !== undefined && overrides?.Season !== null,
  episode: overrides?.Episode !== undefined && overrides?.Episode !== null,
  episodeTitle: overrides?.EpisodeTitle !== undefined && overrides?.EpisodeTitle !== null,
  manualYear: overrides?.ManualYear !== undefined && overrides?.ManualYear !== null,
  manualDate: overrides?.ManualDate !== undefined && overrides?.ManualDate !== null,
  useSeasonEpisode:
    overrides?.UseSeasonEpisode !== undefined && overrides?.UseSeasonEpisode !== null,
  noSeason: overrides?.NoSeason !== undefined && overrides?.NoSeason !== null,
  noYear: overrides?.NoYear !== undefined && overrides?.NoYear !== null,
  noAKA: overrides?.NoAKA !== undefined && overrides?.NoAKA !== null,
  noTag: overrides?.NoTag !== undefined && overrides?.NoTag !== null,
  noEdition: overrides?.NoEdition !== undefined && overrides?.NoEdition !== null,
  noDub: overrides?.NoDub !== undefined && overrides?.NoDub !== null,
  noDual: overrides?.NoDual !== undefined && overrides?.NoDual !== null,
  dualAudio: overrides?.DualAudio !== undefined && overrides?.DualAudio !== null,
  region: overrides?.Region !== undefined && overrides?.Region !== null,
});

const buildMetadataEditState = (overrides?: MetadataOverrides): MetadataOverrideEditState => ({
  distributor: overrides?.Distributor ?? "",
  originalLanguage: overrides?.OriginalLanguage ?? "",
  personalRelease: boolOverrideEditValue(overrides?.PersonalRelease),
  commentary: boolOverrideEditValue(overrides?.Commentary),
  webDV: boolOverrideEditValue(overrides?.WebDV),
  streamOptimized: boolOverrideEditValue(overrides?.StreamOptimized),
  anime: boolOverrideEditValue(overrides?.Anime),
});

const buildMetadataTouchedState = (
  overrides?: MetadataOverrides,
): MetadataOverrideTouchedState => ({
  distributor: overrides?.Distributor !== undefined && overrides?.Distributor !== null,
  originalLanguage:
    overrides?.OriginalLanguage !== undefined && overrides?.OriginalLanguage !== null,
  personalRelease: overrides?.PersonalRelease !== undefined && overrides?.PersonalRelease !== null,
  commentary: overrides?.Commentary !== undefined && overrides?.Commentary !== null,
  webDV: overrides?.WebDV !== undefined && overrides?.WebDV !== null,
  streamOptimized: overrides?.StreamOptimized !== undefined && overrides?.StreamOptimized !== null,
  anime: overrides?.Anime !== undefined && overrides?.Anime !== null,
});

const boolOverrideEditValue = (value?: boolean | null): "" | "true" | "false" => {
  if (value === undefined || value === null) return "";
  return value ? "true" : "false";
};

const parseBoolOverrideEditValue = (value: "" | "true" | "false") => {
  if (value === "") return null;
  return value === "true";
};

const normalizeTag = (value: string) => {
  const trimmed = value.trim();
  if (!trimmed) return "";
  if (trimmed.startsWith("-")) return trimmed;
  return `-${trimmed}`;
};

const isValidManualDate = (value: string) => {
  if (!value.trim()) return true;
  return /^\d{4}-\d{2}-\d{2}$/.test(value.trim());
};

type ThemeMode = "light" | "dark" | "auto";

const emptyWebAuthStatus: WebAuthStatus = {
  path: "",
  exists: false,
  usable: false,
  canCreate: false,
  username: "",
  allowUnencryptedExport: false,
  browseRoot: "",
  allowUnrestrictedBrowse: false,
  encryptionEnabled: false,
  message: "",
};

export default function App() {
  const browserMode = isBrowserMode();
  const browserNativeBrowseAvailable = useSyncExternalStore(
    subscribeBrowserNativeBrowseAvailability,
    isBrowserNativeBrowseAvailable,
    () => true,
  );
  const [path, setPath] = useState("");
  const [sourcePathHistory, setSourcePathHistory] = useState<SourcePathHistoryEntry[]>(() => {
    try {
      return normalizeSourcePathHistory(
        JSON.parse(localStorage.getItem(sourcePathHistoryStorageKey) || "[]"),
        defaultInputHistoryLimit,
        isRuntimePathCaseInsensitive(),
      );
    } catch {
      return [];
    }
  });
  const [sourcePathMode, setSourcePathMode] = useState<SourcePathMode | undefined>();
  const [currentDiscType, setCurrentDiscType] = useState("");
  const [sourceLookupURL, setSourceLookupURL] = useState("");
  const [loading, setLoading] = useState(false);
  const [metadataResetting, setMetadataResetting] = useState(false);
  const [error, setError] = useState("");
  const [preview, setPreview] = useState<MetadataPreview>(emptyPreview);
  const [idEdits, setIdEdits] = useState(() => buildIDEditState(emptyPreview.ExternalIDs));
  const [releaseEdits, setReleaseEdits] = useState(() =>
    buildReleaseEditState(emptyPreview.ReleaseNameOverrides),
  );
  const [releaseTouched, setReleaseTouched] = useState(() =>
    buildReleaseTouchedState(emptyPreview.ReleaseNameOverrides),
  );
  const [metadataEdits, setMetadataEdits] = useState(() => buildMetadataEditState({}));
  const [metadataTouched, setMetadataTouched] = useState(() => buildMetadataTouchedState({}));
  const [showExternalIDInputUI, setShowExternalIDInputUI] = useState(true);
  const [selectedProvider, setSelectedProvider] = useState<string>("");
  const [activeTab, setActiveTab] = useState("input");
  const [theme, setTheme] = useState<ThemeMode>("auto");
  const [renderedDescriptions, setRenderedDescriptions] = useState<Record<string, boolean>>({});
  const [bluraySelecting, setBluraySelecting] = useState(false);
  const [bluraySelectionError, setBluraySelectionError] = useState("");
  const [lightboxImage, setLightboxImage] = useState<string>("");
  const [lightboxAlt, setLightboxAlt] = useState<string>("");
  const [lightboxFit, setLightboxFit] = useState<boolean>(true);
  const [showPlaylistSelection, setShowPlaylistSelection] = useState(false);
  const [playlistSelectionPath, setPlaylistSelectionPath] = useState("");
  const [playlistAutoPreparing, setPlaylistAutoPreparing] = useState(false);
  const [playlistPreparationError, setPlaylistPreparationError] = useState("");
  const [playlistPreparationTrackerSnapshot, setPlaylistPreparationTrackerSnapshot] = useState<{
    selectedTrackers: string[];
    emptySelection: boolean;
  } | null>(null);
  const [bdinfoProgressLines, setBdinfoProgressLines] = useState<string[]>([]);
  const bdinfoProgressActiveRef = useRef(false);
  const [metadataProgressActive, setMetadataProgressActive] = useState(false);
  const [metadataProgressUpdates, setMetadataProgressUpdates] = useState<MetadataProgressUpdate[]>(
    [],
  );
  // Mirrors metadata progress state synchronously so events emitted during the
  // same tick as a fetch/reset request are not dropped by a stale React closure.
  const metadataProgressActiveRef = useRef(false);
  const metadataProgressTargetRef = useRef("");
  const [dupeSummary, setDupeSummary] = useState<DupeCheckSummary>(emptyDupeSummary);
  const [dupeLoading, setDupeLoading] = useState(false);
  const [dupeError, setDupeError] = useState("");
  const [dupeChecked, setDupeChecked] = useState(false);
  const [dupeCheckJobID, setDupeCheckJobID] = useState("");
  const [dupeCheckSnapshot, setDupeCheckSnapshot] = useState<DupeCheckSnapshot | null>(null);
  const [dupeIgnore, setDupeIgnore] = useState<Record<string, boolean>>({});
  const [dupeTrackerFlags, setDupeTrackerFlags] = useState<Record<string, boolean>>({});
  const [builderPreview, setBuilderPreview] =
    useState<DescriptionBuilderPreview>(emptyDescriptionBuilder);
  const [builderRawByGroup, setBuilderRawByGroup] = useState<Record<string, string>>({});
  const [builderRenderedByGroup, setBuilderRenderedByGroup] = useState<Record<string, string>>({});
  const [builderExpandedGroups, setBuilderExpandedGroups] = useState<Record<string, boolean>>({});
  const [builderLoading, setBuilderLoading] = useState(false);
  const [builderError, setBuilderError] = useState("");
  const [builderDirtyByGroup, setBuilderDirtyByGroup] = useState<Record<string, boolean>>({});
  const [builderRenderLoading, setBuilderRenderLoading] = useState(false);
  const [builderSaved, setBuilderSaved] = useState("");
  const [builderSaving, setBuilderSaving] = useState(false);
  const [builderRefreshing, setBuilderRefreshing] = useState(false);
  const [builderProgressMessage, setBuilderProgressMessage] = useState("");
  const builderProgressTimers = useRef<number[]>([]);
  const [builderAutoRequestKey, setBuilderAutoRequestKey] = useState("");
  const [uploadToggles, setUploadToggles] = useState<Record<string, boolean>>({});
  const [uploadSkipClientInjection, setUploadSkipClientInjection] = useState(false);
  const [trackerUploadRunning, setTrackerUploadRunning] = useState(false);
  const [trackerUploadError, setTrackerUploadError] = useState("");
  const [trackerUploadJobID, setTrackerUploadJobID] = useState("");
  const [trackerUploadSnapshot, setTrackerUploadSnapshot] = useState<TrackerUploadSnapshot | null>(
    null,
  );
  const [trackerDryRunLoading, setTrackerDryRunLoading] = useState(false);
  const [trackerDryRunError, setTrackerDryRunError] = useState("");
  const [trackerDryRunPreview, setTrackerDryRunPreview] =
    useState<TrackerDryRunPreview>(emptyTrackerDryRun);
  const [trackerDryRunProgress, setTrackerDryRunProgress] = useState<UploadProgressUpdate | null>(
    null,
  );
  const [trackerQuestionnaireAnswers, setTrackerQuestionnaireAnswers] = useState<
    Record<string, Record<string, string>>
  >({});
  const [releasePageTrackerSelection, setReleasePageTrackerSelection] = useState<
    Record<string, boolean>
  >({});
  const [runDebug, setRunDebug] = useState(false);
  const [runLogLevel, setRunLogLevel] = useState("info");
  const [runLogLevelTouched, setRunLogLevelTouched] = useState(false);
  const [liveCaptureLoading, setLiveCaptureLoading] = useState(false);
  const [finalDragIndex, setFinalDragIndex] = useState<number | null>(null);
  const [settingsExporting, setSettingsExporting] = useState(false);
  const [settingsImporting, setSettingsImporting] = useState(false);
  const [importConfirmOpen, setImportConfirmOpen] = useState(false);
  const [configOpStatus, setConfigOpStatus] = useState<{
    type: "success" | "error" | "warning";
    title: string;
    message: string;
    warnings?: string[];
  } | null>(null);
  const [webAuthStatus, setWebAuthStatus] = useState<WebAuthStatus | null>(null);
  const [webAuthLoading, setWebAuthLoading] = useState(false);
  const [webAuthCreating, setWebAuthCreating] = useState(false);
  const [webAuthUsername, setWebAuthUsername] = useState("");
  const [webAuthPassword, setWebAuthPassword] = useState("");
  const [webAuthConfirm, setWebAuthConfirm] = useState("");
  const [webAuthError, setWebAuthError] = useState("");
  const [applicationInfo, setApplicationInfo] = useState<ApplicationInfo | null>(null);
  const configOpTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const sourcePathDropHandlerRef = useRef<(paths: string[]) => void>(() => undefined);
  const [hostBrowserMode, setHostBrowserMode] = useState<"file" | "folder" | null>(null);
  const [hostBrowser, setHostBrowser] = useState<BrowseDirectoryResponse | null>(null);
  const [hostBrowserLoading, setHostBrowserLoading] = useState(false);
  const [hostBrowserError, setHostBrowserError] = useState("");
  const [hostBrowserSearch, setHostBrowserSearch] = useState("");
  const [debouncedHostBrowserSearch, setDebouncedHostBrowserSearch] = useState("");
  const hostBrowserEntryRefs = useRef<Array<HTMLDivElement | null>>([]);

  const builderDirty = useMemo(
    () => Object.values(builderDirtyByGroup).some(Boolean),
    [builderDirtyByGroup],
  );
  const builderReady = useMemo(() => {
    const normalizedPath = path.trim();
    if (!normalizedPath) {
      return false;
    }
    return builderPreview.SourcePath === normalizedPath && builderPreview.Groups !== undefined;
  }, [builderPreview.SourcePath, builderPreview.Groups, path]);

  const {
    configData,
    settingsLoading,
    settingsDirty,
    settingsSaved,
    settingsError,
    settingsSection,
    settingsSections,
    showAdvancedToggle,
    advancedOpen,
    setSettingsSection,
    setSettingsAdvanced,
    loadSettings,
    handleSaveSettings,
    renderImageHostingSection,
    renderTrackerSection,
    renderTorrentClientsSection,
    renderField,
    sectionFieldMeta,
    updateConfigValue,
    updateScreenshotConfigValue,
    configuredImageHosts,
    screenshotConfig,
    buildSavePayload,
    clearSettingsStatus,
    markSettingsSaved,
    resolveImageHostLabel,
    trackerSelectionNames,
  } = useSettingsState({ activeTab });

  const inputHistoryLimit = useMemo(() => {
    const mainSettings = ((configData as ConfigMap | null)?.MainSettings ??
      null) as ConfigMap | null;
    return resolveInputHistoryLimit(mainSettings?.InputHistoryLimit);
  }, [configData]);
  const useFavicons = useMemo(() => {
    const mainSettings = ((configData as ConfigMap | null)?.MainSettings ??
      null) as ConfigMap | null;
    return typeof mainSettings?.UseFavicons === "boolean" ? mainSettings.UseFavicons : true;
  }, [configData]);
  const faviconOnly = useMemo(() => {
    const mainSettings = ((configData as ConfigMap | null)?.MainSettings ??
      null) as ConfigMap | null;
    return typeof mainSettings?.FaviconOnly === "boolean" ? mainSettings.FaviconOnly : false;
  }, [configData]);

  const persistSourcePathHistory = useCallback((entries: SourcePathHistoryEntry[]) => {
    try {
      if (entries.length === 0) {
        localStorage.removeItem(sourcePathHistoryStorageKey);
        return;
      }
      localStorage.setItem(sourcePathHistoryStorageKey, JSON.stringify(entries));
    } catch {
      // Storage may be unavailable in locked-down browser sessions.
    }
  }, []);

  const rememberSourcePath = useCallback(
    (value: string, mode?: SourcePathMode) => {
      setSourcePathHistory((prev) => {
        const next = addSourcePathHistoryEntry(
          prev,
          value,
          mode ?? inferSourcePathMode(value),
          inputHistoryLimit,
          isRuntimePathCaseInsensitive(),
        );
        persistSourcePathHistory(next);
        return next;
      });
    },
    [inputHistoryLimit, persistSourcePathHistory],
  );

  const handleSourcePathChange = useCallback((value: string) => {
    setPath(value);
    setSourcePathMode(undefined);
  }, []);

  useEffect(() => {
    setSourcePathHistory((prev) => {
      const next = normalizeSourcePathHistory(
        prev,
        inputHistoryLimit,
        isRuntimePathCaseInsensitive(),
      );
      persistSourcePathHistory(next);
      return next;
    });
  }, [inputHistoryLimit, persistSourcePathHistory]);

  const configuredRunLogLevel = useMemo(() => {
    const loggingSection = ((configData as ConfigMap | null)?.Logging ?? null) as ConfigMap | null;
    const rawLevel = String(loggingSection?.Level ?? "info")
      .toLowerCase()
      .trim();
    return runLogLevels.includes(rawLevel as (typeof runLogLevels)[number]) ? rawLevel : "info";
  }, [configData]);

  useEffect(() => {
    if (runLogLevelTouched) {
      return;
    }
    setRunLogLevel(runDebug ? "debug" : configuredRunLogLevel);
  }, [configuredRunLogLevel, runDebug, runLogLevelTouched]);

  // Initialize theme from localStorage and detect system preference
  useEffect(() => {
    const savedTheme = (localStorage.getItem("theme") as ThemeMode | null) || "auto";
    setTheme(savedTheme);
    applyTheme(savedTheme);
  }, []);

  // Apply theme to document
  const applyTheme = (themeValue: ThemeMode) => {
    const root = document.documentElement;
    let effectiveTheme = themeValue;

    if (themeValue === "auto") {
      const prefersDark = globalThis.matchMedia("(prefers-color-scheme: dark)").matches;
      effectiveTheme = prefersDark ? "dark" : "light";
    }

    // Remove existing theme classes
    root.classList.remove("light", "dark");
    // Add the effective theme class
    root.classList.add(effectiveTheme);
  };

  const handleThemeToggle = () => {
    const themes: ThemeMode[] = ["auto", "light", "dark"];
    const currentIndex = themes.indexOf(theme);
    const nextTheme = themes[(currentIndex + 1) % themes.length];
    setTheme(nextTheme);
    localStorage.setItem("theme", nextTheme);
    applyTheme(nextTheme);
  };

  useEffect(() => {
    let cancelled = false;
    const getter = (globalThis.go?.guiapp?.App as AppBridgeWithApplicationInfo | undefined)
      ?.GetApplicationInfo;
    if (!getter) {
      return () => {
        cancelled = true;
      };
    }

    void getter()
      .then((info) => {
        if (!cancelled) {
          setApplicationInfo(info);
        }
      })
      .catch(() => undefined);

    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!lightboxImage) return;
    setLightboxFit(true);
  }, [lightboxImage]);

  useEffect(() => {
    // Keep BDInfo progress subscribed before preparation starts; the backend
    // can emit first lines before React commits playlistAutoPreparing state.
    const off = EventsOn(bdinfoProgressEvent, (payload: any) => {
      if (!bdinfoProgressActiveRef.current) {
        return;
      }
      const line = typeof payload === "string" ? payload : payload?.line;
      if (typeof line !== "string") {
        return;
      }
      const trimmed = line.trim();
      if (!trimmed) {
        return;
      }
      setBdinfoProgressLines((prev) => upsertProgressLine(prev, trimmed));
    });

    return () => {
      if (typeof off === "function") {
        off();
      }
    };
  }, []);

  useEffect(() => {
    // Keep one stable metadata progress listener for the app lifetime; refs
    // carry the active request state without resubscribing mid-fetch.
    const off = EventsOn(metadataProgressEvent, (payload: any) => {
      if (!metadataProgressActiveRef.current) {
        return;
      }
      const eventPath = typeof payload?.path === "string" ? payload.path : "";
      const progressTarget = metadataProgressTargetRef.current;
      if (!isMetadataProgressPathMatch(eventPath, progressTarget)) {
        return;
      }

      const update: MetadataProgressUpdate = {
        path: eventPath,
        phase: typeof payload?.phase === "string" ? payload.phase : "",
        message: typeof payload?.message === "string" ? payload.message : "",
        status: typeof payload?.status === "string" ? payload.status : "",
        level: typeof payload?.level === "string" ? payload.level : "info",
        timestamp: typeof payload?.timestamp === "string" ? payload.timestamp : "",
      };

      if (update.phase === "complete" && update.status === "completed") {
        metadataProgressActiveRef.current = false;
        metadataProgressTargetRef.current = "";
        setMetadataProgressActive(false);
        setMetadataProgressUpdates([]);
        return;
      }

      setMetadataProgressUpdates((prev) => {
        const next = [...prev, update];
        return next.length > 100 ? next.slice(-100) : next;
      });
    });

    return () => {
      if (typeof off === "function") {
        off();
      }
    };
  }, []);

  const getThemeIcon = () => {
    if (theme === "auto") return "🔄";
    if (theme === "light") return "☀️";
    return "🌙";
  };

  const getThemeLabel = () => {
    if (theme === "auto") return "Auto";
    if (theme === "light") return "Light";
    return "Dark";
  };

  const skipAutoTorrentEnabled = isSkipAutoTorrentEnabled(configData);
  const hasTrackerData =
    !skipAutoTorrentEnabled && preview.TrackerData && preview.TrackerData.length > 0;
  const hasBlurayData = Boolean(preview.Bluray);
  const hasPreview = Boolean(preview.SourcePath);

  useEffect(() => {
    if (skipAutoTorrentEnabled && activeTab === "tracker") {
      setActiveTab("input");
    }
  }, [activeTab, skipAutoTorrentEnabled]);

  const dupeEffectiveResults = useMemo(() => {
    const trackerResults = dupeResultsFromSnapshot(dupeCheckSnapshot);
    return trackerResults.length > 0 ? trackerResults : dupeSummary.Results || [];
  }, [dupeCheckSnapshot, dupeSummary.Results]);

  useEffect(() => {
    setDupeIgnore((prev) => {
      if (dupeEffectiveResults.length === 0) {
        return prev;
      }
      let changed = false;
      const next = { ...prev };
      dupeEffectiveResults.forEach((result) => {
        const tracker = result.Tracker;
        if (!tracker) return;
        if (next[tracker] === undefined) {
          next[tracker] = false;
          changed = true;
        }
      });
      return changed ? next : prev;
    });
  }, [dupeEffectiveResults]);

  useEffect(() => {
    if (dupeEffectiveResults.length === 0) {
      setDupeTrackerFlags({});
      return;
    }
    const next: Record<string, boolean> = {};
    dupeEffectiveResults.forEach((result) => {
      const tracker = result.Tracker;
      if (!tracker) return;
      const ignored = dupeIgnore[tracker] ?? false;
      const skipped = Boolean(result.Skipped);
      next[tracker] = Boolean(result.HasDupes) && !ignored && !skipped;
    });
    setDupeTrackerFlags(next);
  }, [dupeEffectiveResults, dupeIgnore]);

  const dupedTrackerSet = useMemo(() => {
    const next = new Set<string>();
    Object.entries(dupeTrackerFlags).forEach(([tracker, hasDupes]) => {
      if (!hasDupes) return;
      const normalized = tracker.toLowerCase().trim();
      if (normalized) next.add(normalized);
      splitTrackerLabel(tracker).forEach((entry) => next.add(entry));
    });
    return next;
  }, [dupeTrackerFlags]);

  const ruleSkippedTrackerSet = useMemo(() => {
    const next = new Set<string>();
    dupeEffectiveResults.forEach((result) => {
      if (!result.Tracker || !result.Skipped) return;
      ruleBlockingTrackerLabels(result).forEach((tracker) => next.add(tracker));
    });
    return next;
  }, [dupeEffectiveResults]);

  const failedDupeTrackerSet = useMemo(() => {
    const next = new Set<string>();
    dupeEffectiveResults.forEach((result) => {
      if (!result.Tracker) return;
      const status = String(result.Status || "")
        .toLowerCase()
        .trim();
      const hasError = Boolean(String(result.Error || "").trim());
      if (status !== "failed" && !hasError) return;
      const normalized = result.Tracker.toLowerCase().trim();
      if (normalized) next.add(normalized);
      splitTrackerLabel(result.Tracker).forEach((tracker) => next.add(tracker));
    });
    return next;
  }, [dupeEffectiveResults]);

  const ruleSkipReasons = useMemo(() => {
    const next: Record<string, string> = {};
    dupeEffectiveResults.forEach((result) => {
      if (!result.Tracker || !result.Skipped) return;
      const reason = (result.SkipReason || result.Notes?.join(" ") || "rule check failed").trim();
      ruleBlockingTrackerLabels(result).forEach((tracker) => {
        next[tracker] = reason || "rule check failed";
      });
    });
    return next;
  }, [dupeEffectiveResults]);

  const ignoredDupeTrackers = useMemo(() => {
    const next = new Set<string>();
    Object.entries(dupeIgnore).forEach(([tracker, ignored]) => {
      if (!ignored) return;
      const normalized = tracker.toLowerCase().trim();
      if (normalized) next.add(normalized);
      splitTrackerLabel(tracker).forEach((entry) => next.add(entry));
    });
    return Array.from(next);
  }, [dupeIgnore]);

  const trackerUploadItems = useMemo(() => {
    if (!configData || !configData.Trackers || typeof configData.Trackers !== "object") {
      return [];
    }

    const trackerRoot = configData.Trackers as ConfigMap;
    const rawEntries = trackerRoot.Trackers;
    const entriesRoot =
      rawEntries && typeof rawEntries === "object" && !Array.isArray(rawEntries)
        ? (rawEntries as ConfigMap)
        : {};
    const entries = Object.entries(entriesRoot).filter(
      ([, value]) => value && typeof value === "object" && !Array.isArray(value),
    ) as Array<[string, ConfigMap]>;
    const visibleTrackerSet = new Set(trackerSelectionNames);

    return entries
      .filter(([name]) => visibleTrackerSet.has(name))
      .map(([name, config]) => ({ name, config }))
      .sort((left, right) => left.name.localeCompare(right.name));
  }, [configData, trackerSelectionNames]);
  const trackerIconSrcByName = useTrackerIcons(trackerUploadItems, useFavicons);

  const defaultTrackerSet = useMemo(() => {
    if (!configData || !configData.Trackers || typeof configData.Trackers !== "object") {
      return new Set<string>();
    }
    const trackerRoot = configData.Trackers as ConfigMap;
    const defaults = normalizeDefaultTrackerList(trackerRoot.DefaultTrackers);
    return new Set(defaults.map((entry) => entry.toLowerCase()));
  }, [configData]);

  const idOverrideState = useMemo(() => {
    const parsed = {
      tmdb: parseIDInput("tmdb", idEdits.tmdb),
      imdb: parseIDInput("imdb", idEdits.imdb),
      tvdb: parseIDInput("tvdb", idEdits.tvdb),
      tvmaze: parseIDInput("tvmaze", idEdits.tvmaze),
    };

    const invalid = Object.values(parsed).includes(null);
    const overrides: ExternalIDOverrides = {
      TMDBID:
        parsed.tmdb !== null && parsed.tmdb !== preview.ExternalIDs.TMDBID ? parsed.tmdb : null,
      IMDBID:
        parsed.imdb !== null && parsed.imdb !== preview.ExternalIDs.IMDBID ? parsed.imdb : null,
      TVDBID:
        parsed.tvdb !== null && parsed.tvdb !== preview.ExternalIDs.TVDBID ? parsed.tvdb : null,
      TVmazeID:
        parsed.tvmaze !== null && parsed.tvmaze !== preview.ExternalIDs.TVmazeID
          ? parsed.tvmaze
          : null,
    };
    const dirty = Object.values(overrides).some((value) => value !== null);

    return { overrides, dirty, invalid };
  }, [idEdits, preview.ExternalIDs]);

  const releaseOverrideState = useMemo(() => {
    // Safety check: ensure state is initialized
    if (!releaseEdits || !releaseTouched) {
      return { overrides: {}, dirty: false, invalid: false };
    }

    const overrides: ReleaseNameOverrides = {};
    const stored =
      preview.ReleaseNameOverrides && typeof preview.ReleaseNameOverrides === "object"
        ? preview.ReleaseNameOverrides
        : {};
    let invalid = false;

    const readTrimmed = (value: string | null | undefined) => (value || "").trim();
    const stringDirty = (
      touched: boolean,
      current: string | null | undefined,
      storedValue?: string | null,
    ) => {
      if (!touched) return false;
      if (storedValue === undefined || storedValue === null) return true;
      return readTrimmed(current) !== readTrimmed(storedValue);
    };
    const boolDirty = (
      touched: boolean,
      current: boolean | null | undefined,
      storedValue?: boolean | null,
    ) => {
      if (!touched) return false;
      if (storedValue === undefined || storedValue === null) return true;
      return Boolean(current) !== Boolean(storedValue);
    };

    if (releaseTouched.category) overrides.Category = readTrimmed(releaseEdits.category);
    if (releaseTouched.type) overrides.Type = readTrimmed(releaseEdits.type);
    if (releaseTouched.source) overrides.Source = readTrimmed(releaseEdits.source);
    if (releaseTouched.resolution) overrides.Resolution = readTrimmed(releaseEdits.resolution);
    if (releaseTouched.tag) overrides.Tag = normalizeTag(releaseEdits.tag);
    if (releaseTouched.service) overrides.Service = readTrimmed(releaseEdits.service);
    if (releaseTouched.edition) overrides.Edition = readTrimmed(releaseEdits.edition);
    if (releaseTouched.season) overrides.Season = readTrimmed(releaseEdits.season);
    if (releaseTouched.episode) overrides.Episode = readTrimmed(releaseEdits.episode);
    if (releaseTouched.episodeTitle)
      overrides.EpisodeTitle = readTrimmed(releaseEdits.episodeTitle);

    if (releaseTouched.manualYear) {
      const trimmed = readTrimmed(releaseEdits.manualYear);
      if (!trimmed) {
        overrides.ManualYear = 0;
      } else if (!/^\d+$/.test(trimmed)) {
        invalid = true;
      } else {
        overrides.ManualYear = Number(trimmed);
      }
    }

    if (releaseTouched.manualDate) {
      const trimmed = readTrimmed(releaseEdits.manualDate);
      overrides.ManualDate = trimmed;
      if (!isValidManualDate(trimmed)) {
        invalid = true;
      }
    }

    if (releaseTouched.useSeasonEpisode) {
      overrides.UseSeasonEpisode = Boolean(releaseEdits.useSeasonEpisode);
    }

    if (releaseTouched.noSeason) overrides.NoSeason = releaseEdits.noSeason;
    if (releaseTouched.noYear) overrides.NoYear = releaseEdits.noYear;
    if (releaseTouched.noAKA) overrides.NoAKA = releaseEdits.noAKA;
    if (releaseTouched.noTag) overrides.NoTag = releaseEdits.noTag;
    if (releaseTouched.noEdition) overrides.NoEdition = releaseEdits.noEdition;
    if (releaseTouched.noDub) overrides.NoDub = releaseEdits.noDub;
    if (releaseTouched.noDual) overrides.NoDual = releaseEdits.noDual;
    if (releaseTouched.dualAudio) overrides.DualAudio = releaseEdits.dualAudio;
    if (releaseTouched.region) overrides.Region = readTrimmed(releaseEdits.region);

    const dirty =
      stringDirty(releaseTouched.category, releaseEdits.category, stored.Category) ||
      stringDirty(releaseTouched.type, releaseEdits.type, stored.Type) ||
      stringDirty(releaseTouched.source, releaseEdits.source, stored.Source) ||
      stringDirty(releaseTouched.resolution, releaseEdits.resolution, stored.Resolution) ||
      stringDirty(releaseTouched.tag, normalizeTag(releaseEdits.tag), stored.Tag) ||
      stringDirty(releaseTouched.service, releaseEdits.service, stored.Service) ||
      stringDirty(releaseTouched.edition, releaseEdits.edition, stored.Edition) ||
      stringDirty(releaseTouched.season, releaseEdits.season, stored.Season) ||
      stringDirty(releaseTouched.episode, releaseEdits.episode, stored.Episode) ||
      stringDirty(releaseTouched.episodeTitle, releaseEdits.episodeTitle, stored.EpisodeTitle) ||
      stringDirty(releaseTouched.manualDate, releaseEdits.manualDate, stored.ManualDate) ||
      boolDirty(
        releaseTouched.useSeasonEpisode,
        releaseEdits.useSeasonEpisode,
        stored.UseSeasonEpisode,
      ) ||
      boolDirty(releaseTouched.noSeason, releaseEdits.noSeason, stored.NoSeason) ||
      boolDirty(releaseTouched.noYear, releaseEdits.noYear, stored.NoYear) ||
      boolDirty(releaseTouched.noAKA, releaseEdits.noAKA, stored.NoAKA) ||
      boolDirty(releaseTouched.noTag, releaseEdits.noTag, stored.NoTag) ||
      boolDirty(releaseTouched.noEdition, releaseEdits.noEdition, stored.NoEdition) ||
      boolDirty(releaseTouched.noDub, releaseEdits.noDub, stored.NoDub) ||
      boolDirty(releaseTouched.noDual, releaseEdits.noDual, stored.NoDual) ||
      boolDirty(releaseTouched.dualAudio, releaseEdits.dualAudio, stored.DualAudio) ||
      stringDirty(releaseTouched.region, releaseEdits.region, stored.Region) ||
      (() => {
        if (!releaseTouched.manualYear) return false;
        if (stored.ManualYear === undefined || stored.ManualYear === null) return true;
        return readTrimmed(releaseEdits.manualYear) !== String(stored.ManualYear);
      })();

    return { overrides, dirty, invalid };
  }, [releaseEdits, releaseTouched, preview.ReleaseNameOverrides]);

  const metadataOverrideState = useMemo(() => {
    const overrides: MetadataOverrides = {};
    const distributor = metadataEdits.distributor.trim();
    const originalLanguage = metadataEdits.originalLanguage.trim();
    if (metadataTouched.distributor && distributor !== "") {
      overrides.Distributor = distributor;
    }
    if (metadataTouched.originalLanguage && originalLanguage !== "") {
      overrides.OriginalLanguage = originalLanguage;
    }
    const personalRelease = parseBoolOverrideEditValue(metadataEdits.personalRelease);
    const commentary = parseBoolOverrideEditValue(metadataEdits.commentary);
    const webDV = parseBoolOverrideEditValue(metadataEdits.webDV);
    const streamOptimized = parseBoolOverrideEditValue(metadataEdits.streamOptimized);
    const anime = parseBoolOverrideEditValue(metadataEdits.anime);
    if (metadataTouched.personalRelease && personalRelease !== null) {
      overrides.PersonalRelease = personalRelease;
    }
    if (metadataTouched.commentary && commentary !== null) {
      overrides.Commentary = commentary;
    }
    if (metadataTouched.webDV && webDV !== null) {
      overrides.WebDV = webDV;
    }
    if (metadataTouched.streamOptimized && streamOptimized !== null) {
      overrides.StreamOptimized = streamOptimized;
    }
    if (metadataTouched.anime && anime !== null) {
      overrides.Anime = anime;
    }
    return {
      overrides,
      dirty: Object.keys(overrides).length > 0,
      invalid: false,
    };
  }, [metadataEdits, metadataTouched]);

  // Screenshot workflow hook (now idOverrideState/releaseOverrideState are defined)
  const screenshots = useScreenshots({
    path,
    idOverrideState,
    releaseOverrideState,
    metadataOverrideState,
  });

  // Destructure commonly used screenshot variables
  const {
    livePreviewSeconds,
    setLivePreviewSeconds,
    livePreviewError,
    setLivePreviewError,
    livePreviewLoading,
    setLivePreviewLoading,
    livePreviewImage,
    setLivePreviewImage,
    livePreviewRequestId,
    screenshotsSettingsSaving,
    setScreenshotsSettingsSaving,
    loadScreenshotPlan,
    readScreenshotImage,
    setExistingImages,
    resetScreenshotState: resetScreenshots,
    handleDeleteTrackerImageURL,
  } = screenshots;

  /**
   * Resolves upload-eligible trackers for the requested source path.
   *
   * Dupe and rule-failure filters apply only when their snapshot source path
   * matches the requested path context, preventing stale blocks from a previous
   * source path from changing metadata fetch or playlist preparation payloads.
   */
  const resolveUploadTrackerEligibilityForPath = useCallback(
    (sourcePath: string) => {
      const targetPath = sourcePath.trim();
      const currentPath = path.trim();
      const dupeSourcePath = String(
        dupeSummary.SourcePath || dupeCheckSnapshot?.sourcePath || "",
      ).trim();
      const pathCaseInsensitive = isRuntimePathCaseInsensitive();
      const dupeSourceMatchesTarget =
        targetPath !== "" &&
        dupeSourcePath !== "" &&
        isSourcePathContextMatch(dupeSourcePath, targetPath, pathCaseInsensitive);
      const uploadTogglesMatchTarget =
        targetPath === "" ||
        currentPath === "" ||
        isSourcePathContextMatch(currentPath, targetPath, pathCaseInsensitive);
      const effectiveUploadToggles: Record<string, boolean> = {};

      trackerUploadItems.forEach((item) => {
        const normalized = item.name.toLowerCase().trim();
        if (uploadTogglesMatchTarget && hasOwnSelection(uploadToggles, item.name)) {
          effectiveUploadToggles[item.name] = uploadToggles[item.name];
          return;
        }
        if (hasOwnSelection(releasePageTrackerSelection, item.name)) {
          effectiveUploadToggles[item.name] = releasePageTrackerSelection[item.name];
          return;
        }
        effectiveUploadToggles[item.name] = defaultTrackerSet.has(normalized);
      });

      const emptyTrackerSet = new Set<string>();
      const scopedDupedTrackerSet = dupeSourceMatchesTarget ? dupedTrackerSet : emptyTrackerSet;
      const scopedRuleSkippedTrackerSet = dupeSourceMatchesTarget
        ? ruleSkippedTrackerSet
        : emptyTrackerSet;
      const scopedFailedDupeTrackerSet = dupeSourceMatchesTarget
        ? failedDupeTrackerSet
        : emptyTrackerSet;
      const selectedTrackers = resolveSelectedUploadTrackers({
        trackerUploadItems,
        releasePageTrackerSelection,
        uploadToggles: effectiveUploadToggles,
        dupedTrackerSet: scopedDupedTrackerSet,
        ruleSkippedTrackerSet: scopedRuleSkippedTrackerSet,
        failedDupeTrackerSet: scopedFailedDupeTrackerSet,
      });

      return {
        selectedTrackers,
        emptySelection:
          hasExplicitEmptyReleaseTrackerSelection(
            trackerUploadItems,
            releasePageTrackerSelection,
          ) ||
          hasFilteredEmptyUploadTrackerSelectionState({
            trackerUploadItems,
            releasePageTrackerSelection,
            uploadToggles: effectiveUploadToggles,
            dupedTrackerSet: scopedDupedTrackerSet,
            ruleSkippedTrackerSet: scopedRuleSkippedTrackerSet,
            failedDupeTrackerSet: scopedFailedDupeTrackerSet,
          }),
      };
    },
    [
      defaultTrackerSet,
      dupeCheckSnapshot?.sourcePath,
      dupeSummary.SourcePath,
      dupedTrackerSet,
      failedDupeTrackerSet,
      path,
      releasePageTrackerSelection,
      ruleSkippedTrackerSet,
      trackerUploadItems,
      uploadToggles,
    ],
  );

  const selectedUploadTrackerEligibility = useMemo(
    () => resolveUploadTrackerEligibilityForPath(path),
    [path, resolveUploadTrackerEligibilityForPath],
  );
  const selectedUploadImageTrackers = selectedUploadTrackerEligibility.selectedTrackers;
  const dupeFiltersMatchCurrentPath = useMemo(() => {
    const currentPath = path.trim();
    const dupeSourcePath = String(
      dupeSummary.SourcePath || dupeCheckSnapshot?.sourcePath || "",
    ).trim();
    return (
      currentPath !== "" &&
      dupeSourcePath !== "" &&
      isSourcePathContextMatch(dupeSourcePath, currentPath, isRuntimePathCaseInsensitive())
    );
  }, [dupeCheckSnapshot?.sourcePath, dupeSummary.SourcePath, path]);

  // Upload images workflow hook
  const uploadImages = useUploadImages({
    path,
    idOverrideState,
    releaseOverrideState,
    metadataOverrideState,
    uploadCandidates: screenshots.uploadCandidates,
    configuredImageHosts,
    selectedTrackers: selectedUploadImageTrackers,
  });
  const {
    refreshUploadedImages,
    resetUploadState,
    setUploadSelections,
    setUploadHost,
    uploadHost,
  } = uploadImages;

  // Tracker image URL handling
  const trackerImageURLs = useMemo(() => {
    const urls = new Set<string>();
    (preview.TrackerData || []).forEach((tracker) => {
      (tracker.ImageURLs || []).forEach((url) => {
        if (url) {
          urls.add(url);
        }
      });
    });
    if (screenshots.deletedTrackerImages.length === 0) {
      return Array.from(urls);
    }
    const deleted = new Set(screenshots.deletedTrackerImages);
    return Array.from(urls).filter((url) => !deleted.has(url));
  }, [preview.TrackerData, screenshots.deletedTrackerImages]);

  const handleDeleteAllTrackerImageURLs = useCallback(async () => {
    if (trackerImageURLs.length === 0) {
      return;
    }
    if (!globalThis.confirm("Remove all tracker images from the list?")) {
      return;
    }
    for (const url of trackerImageURLs) {
      await handleDeleteTrackerImageURL(url);
    }
  }, [trackerImageURLs, handleDeleteTrackerImageURL]);

  const uploadCandidatePaths = useMemo(() => {
    return new Set(
      screenshots.uploadCandidates
        .map((item) => item.image.Path)
        .filter((path): path is string => Boolean(path)),
    );
  }, [screenshots.uploadCandidates]);

  const resetScreenshotState = useCallback(() => {
    resetScreenshots();
    resetUploadState();
    setUploadToggles({});
    setFinalDragIndex(null);
    setLiveCaptureLoading(false);
  }, [resetScreenshots, resetUploadState]);

  const resetFreshWorkflowState = useCallback(
    (nextActiveTab = "input") => {
      setPath("");
      setSourcePathMode(undefined);
      setSourceLookupURL("");
      setLoading(false);
      setMetadataResetting(false);
      setError("");
      setPreview(emptyPreview);
      setIdEdits(buildIDEditState(emptyPreview.ExternalIDs));
      setReleaseEdits(buildReleaseEditState(emptyPreview.ReleaseNameOverrides));
      setReleaseTouched(buildReleaseTouchedState(emptyPreview.ReleaseNameOverrides));
      setShowExternalIDInputUI(true);
      setSelectedProvider("");
      setActiveTab(nextActiveTab);
      setRenderedDescriptions({});
      setLightboxImage("");
      setLightboxAlt("");
      setShowPlaylistSelection(false);
      setPlaylistSelectionPath("");
      setPlaylistAutoPreparing(false);
      bdinfoProgressActiveRef.current = false;
      setPlaylistPreparationError("");
      setBdinfoProgressLines([]);
      metadataProgressTargetRef.current = "";
      metadataProgressActiveRef.current = false;
      setMetadataProgressActive(false);
      setMetadataProgressUpdates([]);
      setDupeSummary(emptyDupeSummary);
      setDupeLoading(false);
      setDupeError("");
      setDupeChecked(false);
      setDupeCheckJobID("");
      setDupeCheckSnapshot(null);
      setDupeIgnore({});
      setDupeTrackerFlags({});
      setBuilderPreview(emptyDescriptionBuilder);
      setBuilderRawByGroup({});
      setBuilderRenderedByGroup({});
      setBuilderExpandedGroups({});
      setBuilderLoading(false);
      setBuilderError("");
      setBuilderDirtyByGroup({});
      setBuilderRenderLoading(false);
      setBuilderSaved("");
      setBuilderSaving(false);
      setBuilderRefreshing(false);
      setBuilderAutoRequestKey("");
      resetScreenshotState();
      setTrackerUploadRunning(false);
      setTrackerUploadError("");
      setTrackerUploadJobID("");
      setTrackerUploadSnapshot(null);
      setTrackerDryRunLoading(false);
      setTrackerDryRunError("");
      setTrackerDryRunPreview(emptyTrackerDryRun);
      setTrackerDryRunProgress(null);
      setTrackerQuestionnaireAnswers({});
      setReleasePageTrackerSelection({});
      setRunDebug(false);
      setRunLogLevel(configuredRunLogLevel);
      setRunLogLevelTouched(false);
      setLiveCaptureLoading(false);
      setHostBrowserMode(null);
      setHostBrowser(null);
      setHostBrowserLoading(false);
      setHostBrowserError("");
    },
    [configuredRunLogLevel, resetScreenshotState],
  );

  const handleHistoryReleaseDeleted = useCallback(
    (deletedPath: string) => {
      // The input path can be edited after loading history; reset based on the displayed release.
      const loadedPath = (preview.SourcePath || path).trim();
      if (!sameSourcePath(loadedPath, deletedPath, isRuntimePathCaseInsensitive())) {
        return;
      }
      resetFreshWorkflowState("history");
    },
    [path, preview.SourcePath, resetFreshWorkflowState],
  );

  // Helper functions for screenshot management (not in the hook)
  const handleDeleteExistingImage = (image: ScreenshotImage) => {
    if (!image.Path) return;
    const deletedPath = image.Path;
    screenshots.setExistingImages((prev) =>
      prev.filter((entry) => entry.image.Path !== deletedPath),
    );
    if (screenshots.finalImagesRef.current.length > 0) {
      screenshots.saveFinalSelections(
        screenshots.finalImagesRef.current.filter((entry) => entry.image.Path !== deletedPath),
      );
    }
  };

  const mergeFinalSelections = (
    current: ScreenshotPreviewImage[],
    additions: ScreenshotPreviewImage[],
  ) => {
    if (additions.length === 0) return current;
    const seen = new Map<string, number>();
    const merged = [...current];
    merged.forEach((item, index) => {
      if (item.image.Path) {
        seen.set(item.image.Path, index);
      }
    });
    additions.forEach((item) => {
      const pathValue = item.image.Path;
      if (!pathValue) return;
      const existingIndex = seen.get(pathValue);
      if (existingIndex === undefined) {
        const ts = item.image.TimestampSeconds || 0;
        if (ts > 0) {
          const insertAt = merged.findIndex((entry) => {
            const entryTs = entry.image.TimestampSeconds || 0;
            return entryTs > 0 && entryTs > ts;
          });
          if (insertAt >= 0) {
            merged.splice(insertAt, 0, item);
            seen.clear();
            merged.forEach((entry, idx) => {
              if (entry.image.Path) {
                seen.set(entry.image.Path, idx);
              }
            });
            return;
          }
        }
        seen.set(pathValue, merged.length);
        merged.push(item);
        return;
      }
      merged[existingIndex] = item;
    });
    return merged;
  };

  const reindexSelectionsByTimestamp = (selections: ScreenshotSelection[], targetIndex: number) => {
    const resolveTimestamp = (entry: ScreenshotSelection) => {
      if (Number.isFinite(entry.TimestampSeconds) && entry.TimestampSeconds > 0) {
        return entry.TimestampSeconds;
      }
      if (Number.isFinite(entry.Frame) && entry.Frame > 0) {
        return entry.Frame / previewFrameRate;
      }
      return 0;
    };

    const ordered = selections
      .map((entry) => ({ entry, originalIndex: entry.Index, ts: resolveTimestamp(entry) }))
      .sort((left, right) => {
        if (left.ts !== right.ts) return left.ts - right.ts;
        return left.originalIndex - right.originalIndex;
      });

    let resolvedIndex = -1;
    const nextSelections = ordered.map((item, index) => {
      if (item.originalIndex === targetIndex) {
        resolvedIndex = index;
      }
      return item.entry;
    });

    return { selections: nextSelections, targetIndex: resolvedIndex };
  };

  const normalizeSelectionTimestamp = (entry: ScreenshotSelection) => {
    if (Number.isFinite(entry.TimestampSeconds) && entry.TimestampSeconds > 0) {
      return entry.TimestampSeconds;
    }
    if (Number.isFinite(entry.Frame) && entry.Frame > 0) {
      return entry.Frame / previewFrameRate;
    }
    return 0;
  };

  const desiredScreenCount = () => {
    if (
      screenshotConfig &&
      typeof screenshotConfig.Screens === "number" &&
      screenshotConfig.Screens > 0
    ) {
      return screenshotConfig.Screens;
    }
    if (
      screenshots.screenshotPlan &&
      Array.isArray(screenshots.screenshotPlan.SuggestedSelections)
    ) {
      return screenshots.screenshotPlan.SuggestedSelections.length;
    }
    return 0;
  };

  const regenerateAutoSelections = (current: ScreenshotSelection[]) => {
    const targetCount = desiredScreenCount();
    if (targetCount <= 0) {
      return current;
    }

    const manual = current.filter((entry) => (entry.Source || "auto").toLowerCase() !== "auto");
    if (manual.length >= targetCount) {
      return current.filter((entry) => (entry.Source || "auto").toLowerCase() !== "auto");
    }

    const candidates = (screenshots.screenshotPlan?.SuggestedSelections || []).filter((entry) => {
      const source = (entry.Source || "auto").toLowerCase();
      return source === "auto";
    });

    const tolerance = previewFrameRate > 0 ? 1 / previewFrameRate : 0;
    const filtered = candidates.filter((entry) => {
      const ts = normalizeSelectionTimestamp(entry);
      return !manual.some(
        (manualEntry) => Math.abs(normalizeSelectionTimestamp(manualEntry) - ts) <= tolerance,
      );
    });

    const needed = Math.max(0, targetCount - manual.length);
    const auto = filtered.slice(0, needed).map((entry) => ({
      ...entry,
      Source: "auto",
    }));

    return [...manual, ...auto];
  };

  const namingOverrides = useMemo(() => {
    const stored =
      preview.ReleaseNameOverrides && typeof preview.ReleaseNameOverrides === "object"
        ? preview.ReleaseNameOverrides
        : {};
    const overrides = releaseOverrideState?.dirty
      ? releaseOverrideState.overrides
      : preview.ReleaseNameOverrides || {};
    return Object.entries(overrides || {}).filter(([key, value]) => {
      if (value === null || value === undefined) return false;
      const storedValue = (stored as Record<string, unknown>)[key];
      if (typeof value === "string") {
        const current = value.trim();
        const prev = typeof storedValue === "string" ? storedValue.trim() : "";
        return current !== prev;
      }
      if (typeof value === "number") {
        const prev = typeof storedValue === "number" ? storedValue : 0;
        return value !== prev;
      }
      if (typeof value === "boolean") {
        const prev = typeof storedValue === "boolean" ? storedValue : false;
        return value !== prev;
      }
      return false;
    });
  }, [preview.ReleaseNameOverrides, releaseOverrideState]);

  const refreshDisabled =
    loading ||
    !path.trim() ||
    (!idOverrideState?.dirty &&
      !releaseOverrideState?.dirty &&
      !metadataOverrideState?.dirty &&
      !sourceLookupURL.trim()) ||
    idOverrideState?.invalid ||
    releaseOverrideState?.invalid ||
    metadataOverrideState?.invalid;

  const applyPreviewResult = (
    result: MetadataPreview,
    options: { switchToInput?: boolean } = {},
  ) => {
    const { switchToInput = true } = options;
    setPreview(result);
    if (switchToInput) {
      setActiveTab("input");
    }
    setIdEdits(buildIDEditState(result.ExternalIDs));
    setReleaseEdits(buildReleaseEditState(result.ReleaseNameOverrides || {}));
    setReleaseTouched(buildReleaseTouchedState(result.ReleaseNameOverrides || {}));
    const orderedIDs = filterAndOrderExternalIDs(result.ExternalIDInfo || []);
    if (orderedIDs.length > 0) {
      setSelectedProvider(orderedIDs[0].Provider);
    } else {
      setSelectedProvider("");
    }
    setDupeSummary(emptyDupeSummary);
    setDupeError("");
    setBuilderPreview(emptyDescriptionBuilder);
    setBuilderRawByGroup({});
    setBuilderRenderedByGroup({});
    setBuilderExpandedGroups({});
    setBuilderError("");
    setBuilderDirtyByGroup({});
    setBuilderSaved("");
    setBuilderRefreshing(false);
    setBuilderAutoRequestKey("");
    resetScreenshotState();
  };

  const clearHostBrowserSearch = () => {
    setHostBrowserSearch("");
    setDebouncedHostBrowserSearch("");
  };

  const openHostBrowser = async (mode: "file" | "folder", startPath = "") => {
    const browser = globalThis.go?.guiapp?.App?.BrowseDirectory;
    if (!browser) {
      setError("Browse is unavailable in this build.");
      return;
    }
    setHostBrowserMode(mode);
    setHostBrowserLoading(true);
    setHostBrowserError("");
    clearHostBrowserSearch();
    try {
      const selectedStart = startPath || path.trim();
      const result = await browser(selectedStart, mode);
      setHostBrowser(result);
    } catch (err) {
      setHostBrowserError(String(err));
    } finally {
      setHostBrowserLoading(false);
    }
  };

  const runBrowse = async (mode: "file" | "folder") => {
    setError("");
    const app = globalThis.go?.guiapp?.App;
    if (browserMode && app?.BrowseDirectory) {
      await openHostBrowser(mode);
      return;
    }
    const browse =
      mode === "file" ? app?.BrowseFile || app?.BrowsePath : app?.BrowseFolder || app?.BrowsePath;
    if (!browse) {
      setError("Browse is unavailable in this build.");
      return;
    }
    try {
      const selected = await browse();
      if (selected) {
        await handlePathSelected(selected, mode);
      }
    } catch (err) {
      setError(String(err));
    }
  };

  const handleBrowseFile = async () => {
    await runBrowse("file");
  };

  const handleBrowseFolder = async () => {
    await runBrowse("folder");
  };

  const closeHostBrowser = () => {
    setHostBrowserMode(null);
    setHostBrowser(null);
    setHostBrowserError("");
    clearHostBrowserSearch();
  };

  useEffect(() => {
    const timer = setTimeout(() => {
      setDebouncedHostBrowserSearch(hostBrowserSearch);
    }, 250);
    return () => clearTimeout(timer);
  }, [hostBrowserSearch]);

  const hostBrowserEntries = useMemo(
    () => filterBrowseEntries(hostBrowser?.entries || [], debouncedHostBrowserSearch),
    [hostBrowser?.entries, debouncedHostBrowserSearch],
  );

  useEffect(() => {
    hostBrowserEntryRefs.current = [];
  }, [hostBrowserEntries]);

  useEffect(() => {
    if (!hostBrowserMode || hostBrowserLoading) {
      return;
    }

    hostBrowserEntryRefs.current.find((entry) => entry !== null)?.focus();
  }, [hostBrowser?.currentPath, hostBrowserLoading, hostBrowserMode]);

  const browseHostDirectory = async (nextPath: string) => {
    if (!hostBrowserMode) {
      return;
    }
    await openHostBrowser(hostBrowserMode, nextPath);
  };

  const selectHostPath = async (selectedPath: string, isDir: boolean) => {
    if (!hostBrowserMode) {
      return;
    }
    if (hostBrowserMode === "folder") {
      await handlePathSelected(selectedPath, "folder");
      closeHostBrowser();
      return;
    }
    if (isDir) {
      await browseHostDirectory(selectedPath);
      return;
    }
    await handlePathSelected(selectedPath, "file");
    closeHostBrowser();
  };

  const moveHostBrowserEntryFocus = (currentIndex: number, direction: 1 | -1) => {
    const entries = hostBrowserEntryRefs.current.filter((entry): entry is HTMLDivElement =>
      Boolean(entry),
    );
    if (entries.length === 0) {
      return;
    }
    const current = hostBrowserEntryRefs.current[currentIndex];
    const resolvedIndex = current ? entries.indexOf(current) : -1;
    const nextIndex =
      resolvedIndex >= 0
        ? (resolvedIndex + direction + entries.length) % entries.length
        : direction > 0
          ? 0
          : entries.length - 1;
    entries[nextIndex]?.focus();
  };

  const handleHostBrowserEntryKeyDown = (
    event: ReactKeyboardEvent<HTMLDivElement>,
    entry: BrowseDirectoryResponse["entries"][number],
    index: number,
  ) => {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      event.stopPropagation();
      moveHostBrowserEntryFocus(index, 1);
      return;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      event.stopPropagation();
      moveHostBrowserEntryFocus(index, -1);
      return;
    }
    if (event.key === "Enter") {
      event.preventDefault();
      event.stopPropagation();
      void selectHostPath(entry.path, entry.isDir);
    }
  };

  const detectDiscType = async (selectedPath: string): Promise<string> => {
    const detector = globalThis.go?.guiapp?.App?.DetectDiscType;
    if (detector) {
      try {
        const discType = await detector(selectedPath);
        return discType.trim().toUpperCase();
      } catch {
        // Fall through to path heuristics when detection is unavailable.
      }
    }

    const upperPath = selectedPath.replace(/\\/g, "/").toUpperCase();
    if (/(^|\/)BDMV(\/|$)/.test(upperPath)) {
      return "BDMV";
    }
    if (/(^|\/)VIDEO_TS(\/|$)/.test(upperPath)) {
      return "DVD";
    }
    return "";
  };

  // Auto-detect BDMV and show playlist selection
  const handlePathSelected = async (
    selectedPath: string,
    mode?: SourcePathMode,
  ): Promise<SourcePathSelection | null> => {
    const trimmedPath = selectedPath.trim();
    if (!trimmedPath) {
      return null;
    }
    const selectedMode = mode ?? inferSourcePathMode(trimmedPath);
    setPath(trimmedPath);
    setSourcePathMode(selectedMode);
    rememberSourcePath(trimmedPath, selectedMode);
    const discType = await detectDiscType(trimmedPath);
    setCurrentDiscType(discType);
    setShowExternalIDInputUI(true);
    setPlaylistPreparationError("");
    setBdinfoProgressLines([]);
    setPlaylistAutoPreparing(false);
    bdinfoProgressActiveRef.current = false;

    if (selectedMode === "file") {
      setShowPlaylistSelection(false);
      setPlaylistSelectionPath("");
      setPlaylistPreparationTrackerSnapshot(null);
      setActiveTab("input");
      return { path: trimmedPath, mode: selectedMode, waitsForPlaylistSelection: false };
    }

    if (discType !== "BDMV") {
      setShowPlaylistSelection(false);
      setPlaylistSelectionPath("");
      setPlaylistPreparationTrackerSnapshot(null);
      setActiveTab("input");
      return { path: trimmedPath, mode: selectedMode, waitsForPlaylistSelection: false };
    }

    const upperPath = trimmedPath.toUpperCase();
    let bdmvPath = trimmedPath;

    if (!upperPath.includes("\\BDMV") && !upperPath.includes("/BDMV")) {
      bdmvPath = `${trimmedPath}/BDMV`;
    }

    const playlistTrackerEligibility = resolveUploadTrackerEligibilityForPath(trimmedPath);

    // Set the path for playlist discovery (component will discover the playlists)
    setPlaylistSelectionPath(bdmvPath);
    setPlaylistPreparationTrackerSnapshot({
      selectedTrackers: playlistTrackerEligibility.selectedTrackers,
      emptySelection: playlistTrackerEligibility.emptySelection,
    });
    setShowPlaylistSelection(true);
    return { path: trimmedPath, mode: selectedMode, waitsForPlaylistSelection: true };
  };

  const handleSourcePathHistorySelect = async (entry: SourcePathHistoryEntry) => {
    setError("");
    await handlePathSelected(entry.path, entry.mode);
  };

  const runPlaylistBDInfo = async () => {
    setPlaylistPreparationError("");
    const fetcher = globalThis.go?.guiapp?.App?.FetchPreparation;
    if (!fetcher) {
      setPlaylistPreparationError("Preparation preview is unavailable in this build.");
      return false;
    }
    if (!path.trim()) {
      setPlaylistPreparationError("Please select a file or folder.");
      return false;
    }
    const selectedTrackers =
      playlistPreparationTrackerSnapshot?.selectedTrackers ??
      selectedUploadTrackerEligibility.selectedTrackers;
    const emptySelection =
      playlistPreparationTrackerSnapshot?.emptySelection ??
      selectedUploadTrackerEligibility.emptySelection;
    if (selectedTrackers.length === 0 && emptySelection) {
      setPlaylistPreparationError("Select at least one tracker before preparing playlists.");
      return false;
    }
    try {
      await fetcher(
        path.trim(),
        normalizeOverrides(idOverrideState?.overrides || {}),
        normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
        normalizeMetadataOverrides(metadataOverrideState?.overrides || {}),
        selectedTrackers,
        [],
      );
      return true;
    } catch (err) {
      setPlaylistPreparationError(String(err));
      return false;
    }
  };

  const handlePlaylistSelectionComplete = async () => {
    setPlaylistPreparationError("");
    setBdinfoProgressLines([]);
    bdinfoProgressActiveRef.current = true;
    setPlaylistAutoPreparing(true);
    const completed = await runPlaylistBDInfo();
    bdinfoProgressActiveRef.current = false;
    setPlaylistAutoPreparing(false);
    if (completed) {
      setShowPlaylistSelection(false);
      setPlaylistSelectionPath("");
      setPlaylistPreparationTrackerSnapshot(null);
      setActiveTab("input");
    }
  };

  const runFetch = async (
    overrides: ExternalIDOverrides,
    nameOverrides: ReleaseNameOverrides,
    metadataOverrides: MetadataOverrides,
    hideExternalIDInputUIOnSuccess = false,
    options: { targetPath?: string; targetMode?: SourcePathMode; switchToInput?: boolean } = {},
  ) => {
    setError("");
    setDupeChecked(false);
    setDupeSummary(emptyDupeSummary);
    setBuilderPreview(emptyDescriptionBuilder);
    setBuilderRawByGroup({});
    setBuilderRenderedByGroup({});
    setBuilderExpandedGroups({});
    setBuilderDirtyByGroup({});
    setBluraySelectionError("");
    const fetcher = globalThis.go?.guiapp?.App?.FetchMetadata;
    if (!fetcher) {
      setError("Fetch metadata is unavailable in this build.");
      return;
    }
    const targetPath = (options.targetPath ?? path).trim();
    if (!targetPath) {
      setError("Please select a file or folder.");
      return;
    }
    const trackerEligibility = resolveUploadTrackerEligibilityForPath(targetPath);
    const selectedTrackers = trackerEligibility.selectedTrackers;
    if (selectedTrackers.length === 0 && trackerEligibility.emptySelection) {
      setError("Select at least one tracker before fetching metadata.");
      return;
    }
    metadataProgressTargetRef.current = targetPath;
    metadataProgressActiveRef.current = true;
    setMetadataProgressUpdates([]);
    setMetadataProgressActive(true);
    setLoading(true);
    try {
      const result = await fetcher(
        targetPath,
        sourceLookupURL.trim(),
        normalizeOverrides(overrides),
        normalizeReleaseOverrides(nameOverrides),
        normalizeMetadataOverrides(metadataOverrides),
        selectedTrackers,
      );
      applyPreviewResult(result, { switchToInput: options.switchToInput });
      rememberSourcePath(
        targetPath,
        options.targetMode ?? sourcePathMode ?? inferSourcePathMode(targetPath),
      );
      setShowExternalIDInputUI(!hideExternalIDInputUIOnSuccess);
    } catch (err) {
      setError(String(err));
    } finally {
      metadataProgressActiveRef.current = false;
      setMetadataProgressActive(false);
      setLoading(false);
    }
  };

  const handleFetch = async () => {
    await runFetch(
      idOverrideState?.overrides || {},
      releaseOverrideState?.overrides || {},
      metadataOverrideState?.overrides || {},
      false,
    );
  };

  const handleSourcePathDrop = async (paths: string[]) => {
    if (loading) {
      setError("Metadata fetch is already running.");
      return;
    }
    const droppedPath = paths.find((candidate) => candidate.trim())?.trim() || "";
    if (!droppedPath) {
      setError("Dropped file path was empty.");
      return;
    }
    setError("");
    const selection = await handlePathSelected(droppedPath);
    if (!selection || selection.waitsForPlaylistSelection) {
      return;
    }
    await runFetch({}, {}, {}, false, {
      targetPath: selection.path,
      targetMode: selection.mode,
    });
  };

  sourcePathDropHandlerRef.current = (paths: string[]) => {
    void handleSourcePathDrop(paths);
  };

  useEffect(() => {
    const runtime = (
      globalThis as typeof globalThis & {
        runtime?: { OnFileDrop?: unknown; OnFileDropOff?: unknown };
      }
    ).runtime;
    if (browserMode || typeof runtime?.OnFileDrop !== "function") {
      return;
    }
    OnFileDrop((_x, _y, paths) => {
      sourcePathDropHandlerRef.current(paths);
    }, true);
    return () => {
      if (typeof runtime.OnFileDropOff === "function") {
        OnFileDropOff();
      }
    };
  }, [browserMode]);

  const clearEditAttributesState = () => {
    setIdEdits(buildIDEditState(emptyPreview.ExternalIDs));
    setReleaseEdits(buildReleaseEditState({}));
    setReleaseTouched(buildReleaseTouchedState({}));
    setMetadataEdits(buildMetadataEditState({}));
    setMetadataTouched(buildMetadataTouchedState({}));
  };

  const handleRefresh = async () => {
    if (
      (!idOverrideState?.dirty &&
        !releaseOverrideState?.dirty &&
        !metadataOverrideState?.dirty &&
        !sourceLookupURL.trim()) ||
      idOverrideState?.invalid ||
      releaseOverrideState?.invalid ||
      metadataOverrideState?.invalid
    ) {
      return;
    }
    await runFetch(
      idOverrideState?.overrides || {},
      releaseOverrideState?.overrides || {},
      metadataOverrideState?.overrides || {},
      true,
    );
  };

  const handleResetMetadata = async () => {
    setError("");
    const resetter = globalThis.go?.guiapp?.App?.ResetMetadata;
    if (!resetter) {
      setError("Metadata reset is unavailable in this build.");
      return;
    }
    if (!path.trim()) {
      setError("Please select a file or folder.");
      return;
    }
    const selectedTrackers = selectedUploadImageTrackers;
    if (selectedTrackers.length === 0 && selectedUploadTrackerEligibility.emptySelection) {
      setError("Select at least one tracker before resetting metadata.");
      return;
    }
    if (
      !globalThis.confirm(
        "Remove cached metadata and temporary files for this content, then refetch metadata?",
      )
    ) {
      return;
    }
    const targetPath = path.trim();
    clearEditAttributesState();
    metadataProgressTargetRef.current = targetPath;
    metadataProgressActiveRef.current = true;
    setMetadataProgressUpdates([]);
    setMetadataProgressActive(true);
    setLoading(true);
    setMetadataResetting(true);
    try {
      const result = await resetter(
        targetPath,
        sourceLookupURL.trim(),
        {},
        {},
        {},
        selectedTrackers,
      );
      applyPreviewResult(result);
      setShowExternalIDInputUI(true);
    } catch (err) {
      setError(String(err));
    } finally {
      metadataProgressActiveRef.current = false;
      setMetadataProgressActive(false);
      setLoading(false);
      setMetadataResetting(false);
    }
  };

  const handleSelectBlurayCandidate = async (releaseID: string) => {
    setBluraySelectionError("");
    const selector = globalThis.go?.guiapp?.App?.SelectBlurayCandidate;
    if (!selector) {
      setBluraySelectionError("Blu-ray candidate selection is unavailable in this build.");
      return;
    }
    const targetPath = (preview.SourcePath || path).trim();
    if (!targetPath || !releaseID.trim()) {
      setBluraySelectionError("Path and release candidate are required.");
      return;
    }
    setBluraySelecting(true);
    try {
      const result = await selector(targetPath, releaseID.trim());
      applyPreviewResult(result, { switchToInput: false });
    } catch (err) {
      setBluraySelectionError(String(err));
    } finally {
      setBluraySelecting(false);
    }
  };

  const runDescriptionBuilder = useCallback(
    async (overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides) => {
      const clearBuilderProgressTimers = () => {
        builderProgressTimers.current.forEach((timer) => window.clearTimeout(timer));
        builderProgressTimers.current = [];
      };
      clearBuilderProgressTimers();
      setBuilderError("");
      setBuilderSaved("");
      setBuilderProgressMessage("");
      const fetcher = globalThis.go?.guiapp?.App?.FetchDescriptionBuilder;
      if (!fetcher) {
        setBuilderError("Description builder is unavailable in this build.");
        return;
      }
      if (!path.trim()) {
        setBuilderError("Please select a file or folder.");
        return;
      }
      const selectedTrackers = selectedUploadImageTrackers;
      if (
        selectedTrackers.length === 0 &&
        hasExplicitEmptyReleaseTrackerSelection(trackerUploadItems, releasePageTrackerSelection)
      ) {
        setBuilderError("Select at least one tracker before building descriptions.");
        return;
      }
      if (selectedTrackers.length === 0) {
        setBuilderError("Enable at least one tracker in Upload Targets.");
        return;
      }
      setBuilderLoading(true);
      setBuilderProgressMessage("Preparing metadata and tracker selection...");
      builderProgressTimers.current = [
        window.setTimeout(
          () => setBuilderProgressMessage("Checking image-host requirements..."),
          900,
        ),
        window.setTimeout(
          () =>
            setBuilderProgressMessage("Rehosting required comparison and description images..."),
          2500,
        ),
        window.setTimeout(
          () => setBuilderProgressMessage("Still rehosting images and building descriptions..."),
          5000,
        ),
        window.setTimeout(
          () => setBuilderProgressMessage("Large image upload still running..."),
          15000,
        ),
        window.setTimeout(
          () => setBuilderProgressMessage("Waiting for image hosts to finish..."),
          30000,
        ),
      ];
      try {
        const result = await fetcher(
          path.trim(),
          normalizeOverrides(overrides),
          normalizeReleaseOverrides(nameOverrides),
          normalizeMetadataOverrides(metadataOverrideState?.overrides || {}),
          selectedTrackers,
          ignoredDupeTrackers,
        );
        setBuilderPreview(result);
        setBuilderRawByGroup(
          Object.fromEntries(
            (result.Groups || []).map((group) => [group.GroupKey, group.RawDescription || ""]),
          ),
        );
        setBuilderRenderedByGroup(
          Object.fromEntries(
            (result.Groups || []).map((group) => [group.GroupKey, group.RawDescriptionHTML || ""]),
          ),
        );
        setBuilderExpandedGroups((prev) => {
          const next: Record<string, boolean> = {};
          (result.Groups || []).forEach((group) => {
            next[group.GroupKey] = prev[group.GroupKey] ?? false;
          });
          return next;
        });
        setBuilderDirtyByGroup({});
        clearBuilderProgressTimers();
        setBuilderProgressMessage("Refreshing uploaded image records...");
        await refreshUploadedImages();
      } catch (err) {
        setBuilderError(String(err));
      } finally {
        clearBuilderProgressTimers();
        setBuilderProgressMessage("");
        setBuilderLoading(false);
      }
    },
    [
      path,
      releasePageTrackerSelection,
      trackerUploadItems,
      selectedUploadImageTrackers,
      ignoredDupeTrackers,
      refreshUploadedImages,
      metadataOverrideState,
    ],
  );

  const refreshDescriptionBuilder = useCallback(async () => {
    if (builderDirty) {
      const shouldRefresh = window.confirm(
        "Refreshing descriptions will discard unsaved description edits. Continue?",
      );
      if (!shouldRefresh) {
        return;
      }
    }

    setBuilderRefreshing(true);
    try {
      await runDescriptionBuilder(
        idOverrideState?.overrides || {},
        releaseOverrideState?.overrides || {},
      );
    } finally {
      setBuilderRefreshing(false);
    }
  }, [builderDirty, idOverrideState, releaseOverrideState, runDescriptionBuilder]);

  useEffect(() => {
    return () => {
      builderProgressTimers.current.forEach((timer) => window.clearTimeout(timer));
      builderProgressTimers.current = [];
    };
  }, []);

  const resetBuilderDescription = async (
    groupKey: string,
    overrides: ExternalIDOverrides,
    nameOverrides: ReleaseNameOverrides,
  ) => {
    setBuilderError("");
    setBuilderSaved("");
    const saver = globalThis.go?.guiapp?.App?.SaveDescriptionOverride;
    if (!saver) {
      setBuilderError("Description saving is unavailable in this build.");
      return;
    }
    if (!path.trim()) {
      setBuilderError("Please select a file or folder.");
      return;
    }
    const currentGroup = (builderPreview.Groups || []).find((group) => group.GroupKey === groupKey);
    if (!currentGroup) {
      setBuilderError("Description group not found.");
      return;
    }
    setBuilderLoading(true);
    try {
      const updatedGroup = await saver(
        path.trim(),
        groupKey,
        "",
        currentGroup.Trackers || [],
        normalizeOverrides(overrides),
        normalizeReleaseOverrides(nameOverrides),
        normalizeMetadataOverrides(metadataOverrideState?.overrides || {}),
      );
      setBuilderPreview((prev) => upsertBuilderGroup(prev, updatedGroup));
      setBuilderRawByGroup((prev) => ({ ...prev, [groupKey]: updatedGroup.RawDescription || "" }));
      setBuilderRenderedByGroup((prev) => ({
        ...prev,
        [groupKey]: updatedGroup.RawDescriptionHTML || "",
      }));
      setBuilderDirtyByGroup((prev) => ({ ...prev, [groupKey]: false }));
      setBuilderSaved("Description reset.");
    } catch (err) {
      setBuilderError(String(err));
    } finally {
      setBuilderLoading(false);
    }
  };

  const renderBuilderDescription = async (groupKey: string) => {
    setBuilderError("");
    const renderer = globalThis.go?.guiapp?.App?.RenderDescription;
    if (!renderer) {
      setBuilderError("Description rendering is unavailable in this build.");
      return;
    }
    const raw = builderRawByGroup[groupKey] || "";
    if (!raw.trim()) {
      setBuilderRenderedByGroup((prev) => ({ ...prev, [groupKey]: "" }));
      return;
    }
    setBuilderRenderLoading(true);
    try {
      const html = await renderer(raw);
      setBuilderRenderedByGroup((prev) => ({ ...prev, [groupKey]: html || "" }));
    } catch (err) {
      setBuilderError(String(err));
    } finally {
      setBuilderRenderLoading(false);
    }
  };

  const saveBuilderDescription = async (groupKey: string) => {
    setBuilderError("");
    setBuilderSaved("");
    const saver = globalThis.go?.guiapp?.App?.SaveDescriptionOverride;
    if (!saver) {
      setBuilderError("Description saving is unavailable in this build.");
      return;
    }
    if (!path.trim()) {
      setBuilderError("Please select a file or folder.");
      return;
    }
    const currentGroup = (builderPreview.Groups || []).find((group) => group.GroupKey === groupKey);
    if (!currentGroup) {
      setBuilderError("Description group not found.");
      return;
    }
    setBuilderSaving(true);
    try {
      const updatedGroup = await saver(
        path.trim(),
        groupKey,
        builderRawByGroup[groupKey] || "",
        currentGroup.Trackers || [],
        normalizeOverrides(idOverrideState?.overrides || {}),
        normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
        normalizeMetadataOverrides(metadataOverrideState?.overrides || {}),
      );
      const nextPreview = upsertBuilderGroup(builderPreview, updatedGroup);
      const shouldRefreshDryRun =
        path.trim() === String(trackerDryRunPreview.SourcePath || "").trim() &&
        (trackerDryRunPreview.Trackers || []).length > 0;

      setBuilderPreview(nextPreview);
      setBuilderRawByGroup((prev) => ({ ...prev, [groupKey]: updatedGroup.RawDescription || "" }));
      setBuilderRenderedByGroup((prev) => ({
        ...prev,
        [groupKey]: updatedGroup.RawDescriptionHTML || "",
      }));
      setBuilderSaved("Description saved.");
      setBuilderDirtyByGroup((prev) => ({ ...prev, [groupKey]: false }));

      if (shouldRefreshDryRun) {
        try {
          await runTrackerDryRun(nextPreview.Groups || [], false);
          setBuilderSaved("Description saved. Dry run refreshed.");
        } catch (err) {
          setTrackerDryRunError(`Description saved, but dry run refresh failed: ${String(err)}`);
        }
      }
    } catch (err) {
      setBuilderError(String(err));
    } finally {
      setBuilderSaving(false);
    }
  };

  const runScreenshotCapture = async (
    selections: ScreenshotSelection[],
    purpose: ScreenshotPurpose,
  ) => {
    const runner = globalThis.go?.guiapp?.App?.GenerateScreenshots;
    if (!runner) {
      throw new Error("Screenshot capture is unavailable in this build.");
    }
    return runner(
      path.trim(),
      normalizeOverrides(idOverrideState?.overrides || {}),
      normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
      normalizeMetadataOverrides(metadataOverrideState?.overrides || {}),
      selections,
      purpose,
    );
  };

  const warmMetadataCache = async () => {
    const fetcher = globalThis.go?.guiapp?.App?.FetchMetadata;
    if (!fetcher) {
      return;
    }
    if (!path.trim()) {
      return;
    }
    const selectedTrackers = selectedUploadImageTrackers;
    if (selectedTrackers.length === 0 && selectedUploadTrackerEligibility.emptySelection) {
      return;
    }
    await fetcher(
      path.trim(),
      sourceLookupURL.trim(),
      normalizeOverrides(idOverrideState?.overrides || {}),
      normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
      normalizeMetadataOverrides(metadataOverrideState?.overrides || {}),
      selectedTrackers,
    );
  };

  const previewFrameRate = useMemo(() => {
    const rate = screenshots.screenshotPlan?.FrameRate || 0;
    return rate > 0 ? rate : 24;
  }, [screenshots.screenshotPlan]);

  const previewDuration = useMemo(
    () => screenshots.screenshotPlan?.DurationSeconds || 0,
    [screenshots.screenshotPlan],
  );

  const clampPreviewSeconds = useCallback(
    (value: number) => {
      if (!Number.isFinite(value)) return 0;
      if (previewDuration > 0) {
        return Math.min(Math.max(value, 0), previewDuration);
      }
      return Math.max(value, 0);
    },
    [previewDuration],
  );

  const livePreviewFrame = useMemo(() => {
    if (previewFrameRate <= 0) return 0;
    const seconds = clampPreviewSeconds(livePreviewSeconds);
    const frame = Math.round(seconds * previewFrameRate);
    return Number.isFinite(frame) ? frame : 0;
  }, [livePreviewSeconds, previewFrameRate, clampPreviewSeconds]);

  const runLivePreviewAt = async (timestampSeconds: number) => {
    setLivePreviewError("");
    if (!path.trim()) {
      setLivePreviewError("Please select a file or folder.");
      return;
    }
    if (!screenshots.screenshotPlan) {
      setLivePreviewError("Load suggestions to enable live preview.");
      return;
    }

    const previewer = globalThis.go?.guiapp?.App?.PreviewScreenshotFrame;
    if (!previewer) {
      setLivePreviewError("Live preview is unavailable in this build.");
      return;
    }

    const requestId = livePreviewRequestId.current + 1;
    livePreviewRequestId.current = requestId;
    setLivePreviewLoading(true);
    const timestamp = clampPreviewSeconds(timestampSeconds);
    try {
      const dataUri = await previewer(
        path.trim(),
        normalizeOverrides(idOverrideState?.overrides || {}),
        normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
        normalizeMetadataOverrides(metadataOverrideState?.overrides || {}),
        timestamp,
      );
      if (livePreviewRequestId.current !== requestId) {
        return;
      }
      setLivePreviewImage(dataUri);
    } catch (err) {
      if (livePreviewRequestId.current !== requestId) {
        return;
      }
      setLivePreviewError(String(err));
    } finally {
      if (livePreviewRequestId.current === requestId) {
        setLivePreviewLoading(false);
      }
    }
  };

  const runLivePreview = async () => {
    await runLivePreviewAt(livePreviewSeconds);
  };

  const stepLivePreview = (direction: number) => {
    const step = 1 / previewFrameRate;
    const next = clampPreviewSeconds(livePreviewSeconds + direction * step);
    setLivePreviewSeconds(next);
    void runLivePreviewAt(next);
  };

  const handlePreviewSelection = async (selection: ScreenshotSelection) => {
    screenshots.setScreenshotsError("");
    if (!path.trim()) {
      screenshots.setScreenshotsError("Please select a file or folder.");
      return;
    }
    screenshots.setPreviewLoadingIndex(selection.Index);
    try {
      const result = await runScreenshotCapture([selection], "preview");
      const images = result.Images || [];
      const previews = await Promise.all(images.map(screenshots.readScreenshotImage));
      screenshots.setPreviewImages((prev) => {
        const merged = new Map<string, ScreenshotPreviewImage>();
        prev.forEach((item) => {
          if (item.image.Path) {
            merged.set(item.image.Path, item);
          }
        });
        previews.forEach((item) => {
          if (item.image.Path) {
            merged.set(item.image.Path, item);
          }
        });
        return Array.from(merged.values());
      });
    } catch (err) {
      screenshots.setScreenshotsError(String(err));
    } finally {
      screenshots.setPreviewLoadingIndex(null);
    }
  };

  const handleCapturePreviewFrame = async () => {
    screenshots.setScreenshotsError("");
    if (!path.trim()) {
      screenshots.setScreenshotsError("Please select a file or folder.");
      return;
    }

    const timestamp = clampPreviewSeconds(livePreviewSeconds);
    const baseSelections =
      screenshots.screenshotSelections.length > 0
        ? screenshots.screenshotSelections
        : screenshots.screenshotPlan?.SuggestedSelections || [];
    if (baseSelections.length === 0) {
      screenshots.setScreenshotsError("No screenshot selections available.");
      return;
    }

    const autoSelections = baseSelections.filter((entry) => {
      const source = (entry.Source || "auto").toLowerCase();
      return source === "auto";
    });
    const candidates = autoSelections.length > 0 ? autoSelections : baseSelections;

    const resolveTimestamp = (entry: ScreenshotSelection) => {
      if (Number.isFinite(entry.TimestampSeconds) && entry.TimestampSeconds > 0) {
        return entry.TimestampSeconds;
      }
      if (Number.isFinite(entry.Frame) && entry.Frame > 0) {
        return entry.Frame / previewFrameRate;
      }
      return 0;
    };

    const closest = candidates.reduce(
      (best, entry) => {
        const currentDiff = Math.abs(resolveTimestamp(entry) - timestamp);
        if (!best) return entry;
        const bestDiff = Math.abs(resolveTimestamp(best) - timestamp);
        if (currentDiff < bestDiff) return entry;
        return best;
      },
      undefined as ScreenshotSelection | undefined,
    );

    if (!closest) {
      screenshots.setScreenshotsError("No screenshot selections available.");
      return;
    }

    const frame = Math.max(0, Math.round(timestamp * previewFrameRate));
    const selection: ScreenshotSelection = {
      Index: closest.Index,
      TimestampSeconds: timestamp,
      Frame: frame,
      Source: "manual",
    };

    const updatedSelections = baseSelections.map((entry) =>
      entry.Index === selection.Index ? selection : entry,
    );
    const regenerated = regenerateAutoSelections(updatedSelections);
    const reindexed = reindexSelectionsByTimestamp(regenerated, selection.Index);
    const tolerance = previewFrameRate > 0 ? 1 / previewFrameRate : 0.01;
    const manualSelection = reindexed.selections.find((entry) => {
      const source = (entry.Source || "").toLowerCase();
      if (source !== "manual") return false;
      const ts = resolveTimestamp(entry);
      if (!Number.isFinite(ts) || ts <= 0) return false;
      return Math.abs(ts - timestamp) <= tolerance;
    });
    const resolvedSelection =
      manualSelection ||
      (reindexed.targetIndex >= 0 ? reindexed.selections[reindexed.targetIndex] : undefined);
    if (!resolvedSelection) {
      screenshots.setScreenshotsError("Failed to resolve capture index.");
      return;
    }
    screenshots.setScreenshotSelections(reindexed.selections);

    const captureSelection: ScreenshotSelection = {
      ...selection,
      Index: resolvedSelection.Index,
    };

    setLiveCaptureLoading(true);
    try {
      const result = await runScreenshotCapture([captureSelection], "preview");
      const images = result.Images || [];
      const previews = await Promise.all(images.map(screenshots.readScreenshotImage));
      screenshots.setPreviewImages((prev) => {
        const merged = new Map<string, ScreenshotPreviewImage>();
        prev.forEach((item) => {
          if (item.image.Path) {
            merged.set(item.image.Path, item);
          }
        });
        previews.forEach((item) => {
          if (item.image.Path) {
            merged.set(item.image.Path, item);
          }
        });
        return Array.from(merged.values());
      });
      if (previews.length > 0) {
        const mergedFinals = mergeFinalSelections(screenshots.finalImagesRef.current, previews);
        await screenshots.saveFinalSelections(mergedFinals);
      }
    } catch (err) {
      screenshots.setScreenshotsError(String(err));
    } finally {
      setLiveCaptureLoading(false);
    }
  };

  const buildExistingSelectionIndexSet = () => {
    const indices = new Set<number>();
    const addImages = (images: ScreenshotImage[] | undefined) => {
      if (!images || images.length === 0) return;
      images.forEach((image) => {
        if (Number.isFinite(image.Index)) {
          indices.add(image.Index);
        }
      });
    };
    addImages(screenshots.screenshotPlan?.ExistingScreenshots);
    addImages(screenshots.existingImages.map((entry) => entry.image));
    addImages(screenshots.finalImagesRef.current.map((entry) => entry.image));
    return indices;
  };

  const handleGenerateScreenshots = async () => {
    screenshots.setScreenshotsError("");
    if (!path.trim()) {
      screenshots.setScreenshotsError("Please select a file or folder.");
      return;
    }
    let selections = screenshots.screenshotSelections;
    if (selections.length === 0) {
      const plan = await screenshots.loadScreenshotPlan(false);
      selections = plan?.SuggestedSelections || [];
    }
    if (selections.length === 0) {
      screenshots.setScreenshotsError("No screenshot selections available.");
      return;
    }
    const existingIndices = buildExistingSelectionIndexSet();
    const filteredSelections = selections.filter((entry) => !existingIndices.has(entry.Index));
    if (filteredSelections.length === 0) {
      screenshots.setScreenshotsError("All requested screenshots already exist.");
      return;
    }
    screenshots.setScreenshotsLoading(true);
    try {
      const result = await runScreenshotCapture(filteredSelections, "final");
      screenshots.setFinalResult(result);
      const images = result.Images || [];
      const previews = await Promise.all(images.map(screenshots.readScreenshotImage));
      const merged = mergeFinalSelections(screenshots.finalImagesRef.current, previews);
      await screenshots.saveFinalSelections(merged);
    } catch (err) {
      screenshots.setScreenshotsError(String(err));
    } finally {
      screenshots.setScreenshotsLoading(false);
    }
  };

  const applyDupeCheckSnapshot = useCallback((snapshot: DupeCheckSnapshot) => {
    setDupeCheckSnapshot(snapshot);
    setDupeSummary(snapshot.summary || emptyDupeSummary);

    const normalized = normalizeJobStatus(snapshot.status);
    const running = isRunningJobStatus(normalized);
    setDupeLoading(running);

    if (normalized === "completed") {
      setDupeChecked(true);
      setDupeError("");
    } else if (normalized === "completed_with_errors") {
      setDupeChecked(true);
      setDupeError(snapshot.error || "One or more tracker dupe checks failed.");
    } else if (normalized === "failed" || normalized === "canceled") {
      setDupeChecked(false);
      setDupeError(snapshot.error || "Dupe check failed.");
    }
  }, []);

  const handleDupeCheck = async () => {
    setDupeError("");
    const starter = globalThis.go?.guiapp?.App?.StartDupeCheck;
    const snapshotLoader = globalThis.go?.guiapp?.App?.GetDupeCheckSnapshot;
    if (!starter) {
      setDupeError("Dupe checking is unavailable in this build.");
      return;
    }
    if (!path.trim()) {
      setDupeError("Please select a file or folder.");
      return;
    }
    if (idOverrideState?.invalid || releaseOverrideState?.invalid) {
      setDupeError("Fix invalid overrides before checking dupes.");
      return;
    }
    const selectedTrackers = getSelectedTrackers();
    if (selectedTrackers.length === 0) {
      setDupeError("Select at least one tracker before checking dupes.");
      return;
    }
    setDupeChecked(false);
    setDupeSummary(emptyDupeSummary);
    setDupeLoading(true);
    let jobID = "";
    try {
      jobID = await starter(
        path.trim(),
        normalizeOverrides(idOverrideState?.overrides || {}),
        normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
        normalizeMetadataOverrides(metadataOverrideState?.overrides || {}),
        selectedTrackers,
      );
    } catch (err) {
      const message = String(err);
      // Starter failures do not create a durable job, so clear the polling gates.
      setDupeLoading(false);
      setDupeCheckJobID("");
      setDupeCheckSnapshot(null);
      setDupeChecked(false);
      if (message.includes("dupe check requires metadata preview")) {
        setDupeError("Fetch metadata first to cache a preview before checking dupes.");
      } else {
        setDupeError(message);
      }
      return;
    }
    setDupeCheckJobID(jobID);
    // The first snapshot is best-effort; once a job exists, events and fallback
    // polling own lifecycle updates.
    if (snapshotLoader) {
      try {
        const snapshot = await snapshotLoader(jobID);
        applyDupeCheckSnapshot(snapshot);
      } catch {
        // Keep tracking the job even when this initial fetch is transiently unavailable.
      }
    }
  };

  useEffect(() => {
    if (!dupeCheckJobID) {
      return;
    }

    const eventName = `${dupeCheckEventPrefix}${dupeCheckJobID}`;
    const off = EventsOn(eventName, (payload: any) => {
      if (payload?.jobID !== dupeCheckJobID) {
        return;
      }
      applyDupeCheckSnapshot(payload as DupeCheckSnapshot);
    });

    return () => {
      if (typeof off === "function") {
        off();
      }
    };
  }, [applyDupeCheckSnapshot, dupeCheckJobID]);

  useEffect(() => {
    // Poll active jobs as a fallback for missed early job events; live events
    // still drive updates when the runtime stream is healthy.
    const currentStatus = String(dupeCheckSnapshot?.status || "");
    if (!dupeCheckJobID || (!dupeLoading && !isRunningJobStatus(currentStatus))) {
      return;
    }
    const snapshotLoader = globalThis.go?.guiapp?.App?.GetDupeCheckSnapshot;
    if (!snapshotLoader) {
      return;
    }

    let stopped = false;
    let timer: number | undefined;
    const loadSnapshot = async () => {
      try {
        const snapshot = await snapshotLoader(dupeCheckJobID);
        if (!stopped) {
          applyDupeCheckSnapshot(snapshot);
        }
      } catch {
        // Event delivery remains primary; transient polling failures should not replace UI errors.
      }
      if (!stopped) {
        timer = window.setTimeout(loadSnapshot, 1000);
      }
    };

    timer = window.setTimeout(loadSnapshot, 1000);
    return () => {
      stopped = true;
      if (timer !== undefined) {
        window.clearTimeout(timer);
      }
    };
  }, [applyDupeCheckSnapshot, dupeCheckJobID, dupeCheckSnapshot?.status, dupeLoading]);

  useEffect(() => {
    setDupeChecked(false);
    setDupeCheckJobID("");
    setDupeCheckSnapshot(null);
    setDupeSummary(emptyDupeSummary);
    setDupeIgnore({});
    setDupeTrackerFlags({});
    setBuilderPreview(emptyDescriptionBuilder);
    setBuilderRawByGroup({});
    setBuilderRenderedByGroup({});
    setBuilderExpandedGroups({});
    setBuilderError("");
    setBuilderDirtyByGroup({});
    setBuilderSaved("");
    setBuilderRefreshing(false);
    setBuilderAutoRequestKey("");
    setTrackerUploadRunning(false);
    setTrackerUploadError("");
    setTrackerUploadJobID("");
    setTrackerUploadSnapshot(null);
    setTrackerDryRunLoading(false);
    setTrackerDryRunError("");
    setTrackerDryRunPreview(emptyTrackerDryRun);
    setTrackerDryRunProgress(null);
    bdinfoProgressActiveRef.current = false;
    metadataProgressTargetRef.current = "";
    metadataProgressActiveRef.current = false;
    setMetadataProgressActive(false);
    setMetadataProgressUpdates([]);
    resetScreenshotState();
  }, [path, resetScreenshotState]);

  useEffect(() => {
    if (activeTab !== "description_builder") return;
    if (!dupeChecked) return;
    if (builderLoading || builderSaving) return;
    if (builderDirty) return;
    const normalizedPath = path.trim();
    if (!normalizedPath) return;
    const requestKey = JSON.stringify({
      path: normalizedPath,
      external: normalizeOverrides(idOverrideState?.overrides || {}),
      release: normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
      metadata: normalizeMetadataOverrides(metadataOverrideState?.overrides || {}),
    });
    if (builderAutoRequestKey === requestKey) return;
    setBuilderAutoRequestKey(requestKey);
    runDescriptionBuilder(idOverrideState?.overrides || {}, releaseOverrideState?.overrides || {});
  }, [
    activeTab,
    dupeChecked,
    builderLoading,
    builderSaving,
    builderDirty,
    path,
    idOverrideState,
    releaseOverrideState,
    metadataOverrideState,
    builderAutoRequestKey,
    runDescriptionBuilder,
  ]);

  useEffect(() => {
    if (activeTab !== "upload") return;
    if (builderReady) return;
    setActiveTab("description_builder");
  }, [activeTab, builderReady]);

  useEffect(() => {
    if (activeTab !== "screenshots") return;
    if (!dupeChecked) return;
    if (screenshots.screenshotPlan || screenshots.screenshotsLoading) return;
    loadScreenshotPlan();
  }, [
    activeTab,
    dupeChecked,
    screenshots.screenshotPlan,
    screenshots.screenshotsLoading,
    loadScreenshotPlan,
  ]);

  useEffect(() => {
    if (activeTab !== "upload_images") return;
    if (!dupeChecked) return;
    if (screenshots.screenshotPlan || screenshots.screenshotsLoading) return;
    loadScreenshotPlan();
  }, [
    activeTab,
    dupeChecked,
    screenshots.screenshotPlan,
    screenshots.screenshotsLoading,
    loadScreenshotPlan,
  ]);

  useEffect(() => {
    if (activeTab !== "upload_images") return;
    if (!path.trim()) return;
    const loadUploadCandidates = async () => {
      try {
        const candidates = await globalThis.go?.guiapp?.App?.ListUploadCandidates(
          path.trim(),
          normalizeOverrides(idOverrideState?.overrides || {}),
          normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
          normalizeMetadataOverrides(metadataOverrideState?.overrides || {}),
        );
        if (!candidates || candidates.length === 0) {
          setExistingImages([]);
          await refreshUploadedImages();
          return;
        }
        const previews = await Promise.all(
          candidates.map(async (image: ScreenshotImage) => {
            try {
              return await readScreenshotImage(image);
            } catch {
              return null;
            }
          }),
        );
        setExistingImages(
          previews.filter((entry): entry is ScreenshotPreviewImage => Boolean(entry)),
        );
        await refreshUploadedImages();
      } catch (err) {
        console.error("Failed to load upload candidates:", err);
      }
    };
    loadUploadCandidates();
  }, [
    activeTab,
    path,
    idOverrideState,
    releaseOverrideState,
    metadataOverrideState,
    setExistingImages,
    readScreenshotImage,
    refreshUploadedImages,
  ]);

  useEffect(() => {
    if (trackerUploadItems.length === 0) return;
    setUploadToggles((prev) => {
      const next = { ...prev };
      trackerUploadItems.forEach((item) => {
        const normalized = item.name.toLowerCase();
        const hasReleaseSelection = Object.prototype.hasOwnProperty.call(
          releasePageTrackerSelection,
          item.name,
        );
        if (hasReleaseSelection && !releasePageTrackerSelection[item.name]) {
          delete next[item.name];
          return;
        }
        if (dupeFiltersMatchCurrentPath && failedDupeTrackerSet.has(normalized)) {
          next[item.name] = false;
          return;
        }
        if (
          dupeFiltersMatchCurrentPath &&
          (dupedTrackerSet.has(normalized) || ruleSkippedTrackerSet.has(normalized))
        ) {
          next[item.name] = false;
          return;
        }
        if (next[item.name] === undefined) {
          // Prioritize release page selection, then fall back to defaults
          if (hasReleaseSelection) {
            next[item.name] = releasePageTrackerSelection[item.name];
          } else {
            next[item.name] = defaultTrackerSet.has(normalized);
          }
        }
      });
      const trackerNames = new Set(trackerUploadItems.map((item) => item.name));
      Object.keys(next).forEach((name) => {
        if (!trackerNames.has(name)) {
          delete next[name];
          return;
        }
        if (
          Object.prototype.hasOwnProperty.call(releasePageTrackerSelection, name) &&
          !releasePageTrackerSelection[name]
        ) {
          delete next[name];
        }
      });
      return next;
    });
  }, [
    trackerUploadItems,
    defaultTrackerSet,
    dupeFiltersMatchCurrentPath,
    dupedTrackerSet,
    ruleSkippedTrackerSet,
    failedDupeTrackerSet,
    releasePageTrackerSelection,
  ]);

  useEffect(() => {
    if (screenshots.uploadCandidates.length === 0) {
      setUploadSelections({});
      return;
    }
    setUploadSelections((prev) => {
      const next: Record<string, boolean> = { ...prev };
      screenshots.uploadCandidates.forEach((item) => {
        const pathValue = item.image.Path;
        if (!pathValue) return;
        if (next[pathValue] === undefined) {
          next[pathValue] = true;
        }
      });
      Object.keys(next).forEach((key) => {
        if (!uploadCandidatePaths.has(key)) {
          delete next[key];
        }
      });
      return next;
    });
  }, [screenshots.uploadCandidates, uploadCandidatePaths, setUploadSelections]);

  useEffect(() => {
    if (uploadHost || configuredImageHosts.length === 0) return;
    setUploadHost(configuredImageHosts[0]);
  }, [configuredImageHosts, uploadHost, setUploadHost]);

  // Initialize release page tracker selection when preview loads or trackers change
  useEffect(() => {
    if (trackerUploadItems.length === 0) {
      setReleasePageTrackerSelection({});
      return;
    }
    setReleasePageTrackerSelection((prev) => {
      const next = { ...prev };
      trackerUploadItems.forEach((item) => {
        const normalized = item.name.toLowerCase();
        if (next[item.name] === undefined) {
          // Initialize from defaults
          next[item.name] = defaultTrackerSet.has(normalized);
        }
      });
      // Remove trackers no longer in the list
      const trackerNames = trackerUploadItems.map((item) => item.name);
      Object.keys(next).forEach((name) => {
        if (!trackerNames.includes(name)) {
          delete next[name];
        }
      });
      return next;
    });
  }, [trackerUploadItems, defaultTrackerSet]);

  useEffect(() => {
    setTrackerQuestionnaireAnswers({});
    setTrackerDryRunPreview(emptyTrackerDryRun);
    setTrackerDryRunError("");
    setTrackerDryRunProgress(null);
  }, [path]);

  // releasePageTrackerSelection is raw input-page state. Workflows that must
  // honor upload eligibility use selectedUploadImageTrackers instead.
  const getSelectedTrackers = () => {
    return Object.entries(releasePageTrackerSelection)
      .filter(([, selected]) => selected)
      .map(([name]) => name);
  };

  const getSelectedUploadTrackers = useCallback(
    () => selectedUploadImageTrackers,
    [selectedUploadImageTrackers],
  );

  const updateTrackerQuestionnaireAnswer = useCallback(
    (tracker: string, key: string, value: string) => {
      setTrackerQuestionnaireAnswers((prev) => {
        const trackerKey = tracker.toUpperCase().trim();
        return {
          ...prev,
          [trackerKey]: {
            ...(prev[trackerKey] || {}),
            [key]: value,
          },
        };
      });
    },
    [],
  );

  /** Applies an upload job snapshot from either live events or polling fallback. */
  const applyTrackerUploadSnapshot = useCallback((snapshot: TrackerUploadSnapshot) => {
    setTrackerUploadSnapshot(snapshot);
    const normalized = normalizeJobStatus(snapshot.status);
    const running = isRunningJobStatus(normalized);
    setTrackerUploadRunning(running);
    if (normalized === "completed") {
      setTrackerUploadError("");
    } else if (
      normalized === "completed_with_errors" ||
      normalized === "failed" ||
      normalized === "canceled"
    ) {
      setTrackerUploadError(snapshot.error || "Upload finished with errors.");
    }
  }, []);

  const clearTrackerUploadJob = useCallback(() => {
    setTrackerUploadJobID("");
    setTrackerUploadSnapshot(null);
  }, []);

  const handleStartTrackerUpload = useCallback(async () => {
    setTrackerUploadError("");
    const starter = globalThis.go?.guiapp?.App?.StartTrackerUpload;
    const snapshotLoader = globalThis.go?.guiapp?.App?.GetTrackerUploadSnapshot;
    if (!starter) {
      setTrackerUploadError("Tracker upload is unavailable in this build.");
      return;
    }
    if (!path.trim()) {
      setTrackerUploadError("Please select a file or folder.");
      return;
    }
    if (idOverrideState?.invalid || releaseOverrideState?.invalid) {
      setTrackerUploadError("Fix invalid overrides before uploading.");
      return;
    }
    const selectedTrackers = getSelectedUploadTrackers();
    if (selectedTrackers.length === 0) {
      setTrackerUploadError("Enable at least one tracker in Upload Targets.");
      return;
    }
    const missingRequiredFields: string[] = [];
    selectedTrackers.forEach((tracker) => {
      const dryRunEntry = (trackerDryRunPreview.Trackers || []).find(
        (entry) =>
          String(entry?.Tracker || "")
            .toLowerCase()
            .trim() === tracker.toLowerCase().trim(),
      );
      const questionnaire = dryRunEntry?.Questionnaire;
      if (!questionnaire?.Fields?.length) {
        return;
      }
      const trackerAnswers = buildQuestionnaireAnswerDefaults(
        questionnaire,
        trackerQuestionnaireAnswers[tracker.toUpperCase().trim()],
      );
      questionnaire.Fields.forEach((field) => {
        if (field.Required && !String(trackerAnswers[field.Key] || "").trim()) {
          missingRequiredFields.push(`${tracker}: ${field.Label || field.Key}`);
        }
      });
    });
    if (missingRequiredFields.length > 0) {
      setTrackerUploadError(
        `Complete required questionnaire fields before uploading: ${missingRequiredFields.join(", ")}`,
      );
      return;
    }
    setTrackerUploadRunning(true);
    clearTrackerUploadJob();
    let jobID = "";
    try {
      jobID = await starter(
        path.trim(),
        normalizeOverrides(idOverrideState?.overrides || {}),
        normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
        normalizeMetadataOverrides(metadataOverrideState?.overrides || {}),
        selectedTrackers,
        ignoredDupeTrackers,
        cloneQuestionnaireAnswers(trackerQuestionnaireAnswers),
        builderPreview.Groups || [],
        runDebug,
        uploadSkipClientInjection,
        runLogLevel,
      );
    } catch (err) {
      setTrackerUploadRunning(false);
      clearTrackerUploadJob();
      setTrackerUploadError(String(err));
      return;
    }
    setTrackerUploadJobID(jobID);
    if (snapshotLoader) {
      try {
        const snapshot = await snapshotLoader(jobID);
        applyTrackerUploadSnapshot(snapshot);
      } catch {
        // Events and polling continue tracking the started job when bootstrap refresh fails.
      }
    }
  }, [
    path,
    idOverrideState,
    releaseOverrideState,
    metadataOverrideState,
    getSelectedUploadTrackers,
    ignoredDupeTrackers,
    trackerDryRunPreview,
    trackerQuestionnaireAnswers,
    builderPreview,
    runDebug,
    uploadSkipClientInjection,
    runLogLevel,
    applyTrackerUploadSnapshot,
    clearTrackerUploadJob,
  ]);

  const runTrackerDryRun = useCallback(
    async (descriptionGroups: DescriptionBuilderPreview["Groups"], surfaceError = true) => {
      if (surfaceError) {
        setTrackerDryRunError("");
      }
      const fetcher = globalThis.go?.guiapp?.App?.FetchTrackerDryRun;
      if (!fetcher) {
        const message = "Tracker dry run is unavailable in this build.";
        if (surfaceError) {
          setTrackerDryRunError(message);
          return null;
        }
        throw new Error(message);
      }
      if (!path.trim()) {
        const message = "Please select a file or folder.";
        if (surfaceError) {
          setTrackerDryRunError(message);
          return null;
        }
        throw new Error(message);
      }
      if (idOverrideState?.invalid || releaseOverrideState?.invalid) {
        const message = "Fix invalid overrides before running dry run.";
        if (surfaceError) {
          setTrackerDryRunError(message);
          return null;
        }
        throw new Error(message);
      }
      const selectedTrackers = getSelectedUploadTrackers();
      if (selectedTrackers.length === 0) {
        const message = "Enable at least one tracker in Upload Targets.";
        if (surfaceError) {
          setTrackerDryRunError(message);
          return null;
        }
        throw new Error(message);
      }

      setTrackerDryRunLoading(true);
      setTrackerDryRunProgress({
        sourcePath: path.trim(),
        tracker: "",
        task: "dry_run",
        status: "running",
        message: "Starting dry run",
        completedPieces: 0,
        totalPieces: 0,
        percent: 0,
        hashRateMiB: 0,
        timestamp: new Date().toISOString(),
      });
      try {
        const result = await fetcher(
          path.trim(),
          normalizeOverrides(idOverrideState?.overrides || {}),
          normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
          normalizeMetadataOverrides(metadataOverrideState?.overrides || {}),
          selectedTrackers,
          ignoredDupeTrackers,
          cloneQuestionnaireAnswers(trackerQuestionnaireAnswers),
          descriptionGroups,
          runDebug,
          uploadSkipClientInjection,
          runLogLevel,
        );
        setTrackerDryRunPreview(result || emptyTrackerDryRun);
        setTrackerQuestionnaireAnswers((prev) => {
          const next = cloneQuestionnaireAnswers(prev);
          (result?.Trackers || []).forEach((entry) => {
            const trackerKey = String(entry?.Tracker || "")
              .toUpperCase()
              .trim();
            if (!trackerKey) {
              return;
            }
            next[trackerKey] = buildQuestionnaireAnswerDefaults(
              entry.Questionnaire,
              next[trackerKey],
            );
          });
          return next;
        });
        return result || emptyTrackerDryRun;
      } catch (err) {
        if (surfaceError) {
          setTrackerDryRunError(String(err));
          return null;
        }
        throw err;
      } finally {
        setTrackerDryRunLoading(false);
      }
    },
    [
      path,
      idOverrideState,
      releaseOverrideState,
      metadataOverrideState,
      getSelectedUploadTrackers,
      ignoredDupeTrackers,
      trackerQuestionnaireAnswers,
      runDebug,
      uploadSkipClientInjection,
      runLogLevel,
    ],
  );

  const handleRunTrackerDryRun = useCallback(async () => {
    await runTrackerDryRun(builderPreview.Groups || []);
  }, [builderPreview, runTrackerDryRun]);

  useEffect(() => {
    const off = EventsOn(trackerUploadProgressEvent, (payload: any) => {
      const update = payload as UploadProgressUpdate;
      const updatePath = String(update?.sourcePath || "").trim();
      if (updatePath && updatePath !== path.trim()) {
        return;
      }
      setTrackerDryRunProgress(update);
    });

    return () => {
      if (typeof off === "function") {
        off();
      }
    };
  }, [path]);

  const handleCancelTrackerUpload = useCallback(async () => {
    setTrackerUploadError("");
    if (!trackerUploadJobID) {
      return;
    }
    const cancel = globalThis.go?.guiapp?.App?.CancelTrackerUpload;
    if (!cancel) {
      setTrackerUploadError("Tracker upload cancel is unavailable in this build.");
      return;
    }
    try {
      await cancel(trackerUploadJobID);
    } catch (err) {
      setTrackerUploadError(String(err));
    }
  }, [trackerUploadJobID]);

  const handleRetryFailedTrackerUpload = useCallback(async () => {
    setTrackerUploadError("");
    if (!trackerUploadJobID) {
      return;
    }
    const retry = globalThis.go?.guiapp?.App?.RetryFailedTrackerUpload;
    const snapshotLoader = globalThis.go?.guiapp?.App?.GetTrackerUploadSnapshot;
    if (!retry) {
      setTrackerUploadError("Tracker retry is unavailable in this build.");
      return;
    }
    setTrackerUploadRunning(true);
    let nextJobID = "";
    try {
      nextJobID = await retry(trackerUploadJobID);
    } catch (err) {
      setTrackerUploadRunning(false);
      setTrackerUploadError(String(err));
      return;
    }
    setTrackerUploadJobID(nextJobID);
    setTrackerUploadSnapshot(null);
    if (snapshotLoader) {
      try {
        const snapshot = await snapshotLoader(nextJobID);
        applyTrackerUploadSnapshot(snapshot);
      } catch {
        // Events and polling continue tracking the replacement job when bootstrap refresh fails.
      }
    }
  }, [applyTrackerUploadSnapshot, trackerUploadJobID]);

  useEffect(() => {
    if (!trackerUploadJobID) {
      return;
    }

    const eventName = `${trackerUploadEventPrefix}${trackerUploadJobID}`;
    const off = EventsOn(eventName, (payload: any) => {
      if (payload?.jobID !== trackerUploadJobID) {
        return;
      }
      applyTrackerUploadSnapshot(payload as TrackerUploadSnapshot);
    });

    return () => {
      if (typeof off === "function") {
        off();
      }
    };
  }, [applyTrackerUploadSnapshot, trackerUploadJobID]);

  useEffect(() => {
    // Poll active upload jobs as a fallback for missed upload snapshot events.
    const currentStatus = String(trackerUploadSnapshot?.status || "");
    if (!trackerUploadJobID || (!trackerUploadRunning && !isRunningJobStatus(currentStatus))) {
      return;
    }
    const snapshotLoader = globalThis.go?.guiapp?.App?.GetTrackerUploadSnapshot;
    if (!snapshotLoader) {
      return;
    }

    let stopped = false;
    let timer: number | undefined;
    const loadSnapshot = async () => {
      try {
        const snapshot = await snapshotLoader(trackerUploadJobID);
        if (!stopped) {
          applyTrackerUploadSnapshot(snapshot);
        }
      } catch {
        // Event delivery remains primary; transient polling failures should not replace UI errors.
      }
      if (!stopped) {
        timer = window.setTimeout(loadSnapshot, 1000);
      }
    };

    timer = window.setTimeout(loadSnapshot, 1000);
    return () => {
      stopped = true;
      if (timer !== undefined) {
        window.clearTimeout(timer);
      }
    };
  }, [
    applyTrackerUploadSnapshot,
    trackerUploadJobID,
    trackerUploadRunning,
    trackerUploadSnapshot?.status,
  ]);

  const markReleaseTouched = (key: keyof ReleaseNameTouchedState) => {
    setReleaseTouched((prev) => ({ ...prev, [key]: true }));
  };

  const markMetadataTouched = (key: keyof MetadataOverrideTouchedState) => {
    setMetadataTouched((prev) => ({ ...prev, [key]: true }));
  };

  const applyScreenshotSettings = async () => {
    screenshots.setScreenshotsError("");
    clearSettingsStatus();
    const saveConfig = globalThis.go?.guiapp?.App?.SaveConfig;
    if (!saveConfig) {
      screenshots.setScreenshotsError("Settings are unavailable in this build.");
      return;
    }
    const payload = buildSavePayload();
    if (!payload) {
      screenshots.setScreenshotsError("Settings are not loaded.");
      return;
    }
    setScreenshotsSettingsSaving(true);
    try {
      await saveConfig(payload);
      markSettingsSaved("Settings saved and applied.");
      await warmMetadataCache();
      await screenshots.loadScreenshotPlan();
    } catch (err) {
      screenshots.setScreenshotsError(String(err));
    } finally {
      setScreenshotsSettingsSaving(false);
    }
  };

  const showConfigOpStatus = useCallback((status: NonNullable<typeof configOpStatus>) => {
    if (configOpTimerRef.current) clearTimeout(configOpTimerRef.current);
    setConfigOpStatus(status);
    if (status.type === "success") {
      configOpTimerRef.current = setTimeout(() => setConfigOpStatus(null), 8000);
    }
  }, []);

  const dismissConfigOpStatus = useCallback(() => {
    if (configOpTimerRef.current) clearTimeout(configOpTimerRef.current);
    setConfigOpStatus(null);
  }, []);

  useEffect(() => {
    return () => {
      if (configOpTimerRef.current) clearTimeout(configOpTimerRef.current);
    };
  }, []);

  const handleExportSettings = async () => {
    clearSettingsStatus();
    dismissConfigOpStatus();
    const exportConfig = globalThis.go?.guiapp?.App?.ExportConfig;
    if (!exportConfig) {
      showConfigOpStatus({
        type: "error",
        title: "Export Failed",
        message: "Settings export is unavailable in this build.",
      });
      return;
    }

    setSettingsExporting(true);
    try {
      const exportedPath = await exportConfig();
      if (exportedPath?.trim()) {
        showConfigOpStatus({
          type: "success",
          title: "Configuration Exported",
          message: `Saved to ${exportedPath}`,
        });
      }
    } catch (err) {
      showConfigOpStatus({ type: "error", title: "Export Failed", message: String(err) });
    } finally {
      setSettingsExporting(false);
    }
  };

  const handleImportConfigRequest = () => {
    clearSettingsStatus();
    dismissConfigOpStatus();
    setImportConfirmOpen(true);
  };

  const handleImportConfigCancel = () => {
    if (settingsImporting) return;
    setImportConfirmOpen(false);
  };

  const handleImportConfigConfirm = async () => {
    const importConfig = globalThis.go?.guiapp?.App?.ImportConfig;
    if (!importConfig) {
      setImportConfirmOpen(false);
      showConfigOpStatus({
        type: "error",
        title: "Import Failed",
        message: "Config import is unavailable in this build.",
      });
      return;
    }

    setSettingsImporting(true);
    try {
      const result = await importConfig();
      const message = (result?.message ?? "").trim();
      if (!message) {
        return;
      }
      const warnings = result?.warnings ?? [];
      if (warnings.length > 0) {
        showConfigOpStatus({ type: "warning", title: "Imported with Warnings", message, warnings });
      } else {
        showConfigOpStatus({ type: "success", title: "Configuration Imported", message });
      }
      loadSettings();
    } catch (err) {
      showConfigOpStatus({ type: "error", title: "Import Failed", message: String(err) });
    } finally {
      setSettingsImporting(false);
      setImportConfirmOpen(false);
    }
  };

  const loadWebAuthStatus = useCallback(async () => {
    const getWebAuthStatus = globalThis.go?.guiapp?.App?.GetWebAuthStatus;
    if (!getWebAuthStatus) {
      setWebAuthStatus(null);
      setWebAuthError("");
      return;
    }

    setWebAuthLoading(true);
    setWebAuthError("");
    try {
      const status = await getWebAuthStatus();
      setWebAuthStatus(status);
    } catch (err) {
      setWebAuthStatus({ ...emptyWebAuthStatus, message: "Unable to load web auth status." });
      setWebAuthError(String(err));
    } finally {
      setWebAuthLoading(false);
    }
  }, []);

  const handleCreateWebAuth = useCallback(async () => {
    clearSettingsStatus();
    dismissConfigOpStatus();
    setWebAuthError("");

    if (webAuthPassword !== webAuthConfirm) {
      setWebAuthError("Passwords do not match.");
      return;
    }

    const createWebAuth = globalThis.go?.guiapp?.App?.CreateWebAuth;
    if (!createWebAuth) {
      setWebAuthError("Web auth bootstrap is unavailable in this build.");
      return;
    }

    setWebAuthCreating(true);
    try {
      const status = await createWebAuth(webAuthUsername, webAuthPassword);
      setWebAuthStatus(status);
      setWebAuthPassword("");
      setWebAuthConfirm("");
      markSettingsSaved("Web auth created. Future secret saves and exports can use encryption.");
    } catch (err) {
      setWebAuthError(String(err));
    } finally {
      setWebAuthCreating(false);
    }
  }, [
    clearSettingsStatus,
    dismissConfigOpStatus,
    markSettingsSaved,
    webAuthConfirm,
    webAuthPassword,
    webAuthUsername,
  ]);

  useEffect(() => {
    if (activeTab !== "settings") {
      return;
    }
    loadWebAuthStatus();
  }, [activeTab, loadWebAuthStatus]);

  const dupeProgressStatus = String(dupeCheckSnapshot?.status || "").toLowerCase();
  const dupeCompletedCount = Number(dupeCheckSnapshot?.completedCount || 0);
  const dupeTotalCount = Number(dupeCheckSnapshot?.totalCount || 0);

  return (
    <div className="app-shell">
      <div className="gradient-orb orb-a" />
      <div className="gradient-orb orb-b" />
      <div className={appLayoutClass}>
        <aside className={sidebarClass}>
          <div className={sidebarGroupClass}>
            <button
              className={navButtonClass(activeTab === "input")}
              type="button"
              onClick={() => setActiveTab("input")}
            >
              Input
            </button>
            {hasTrackerData ? (
              <button
                className={navButtonClass(activeTab === "tracker", true)}
                type="button"
                onClick={() => setActiveTab("tracker")}
              >
                Tracker Data
              </button>
            ) : null}
            {hasBlurayData ? (
              <button
                className={navButtonClass(activeTab === "bluray", true)}
                type="button"
                onClick={() => setActiveTab("bluray")}
              >
                Blu-ray.com
              </button>
            ) : null}
            {hasPreview ? (
              <button
                className={navButtonClass(activeTab === "dupes", true)}
                type="button"
                onClick={() => setActiveTab("dupes")}
              >
                Dupe Checking
              </button>
            ) : null}
            {dupeChecked ? (
              <button
                className={navButtonClass(activeTab === "screenshots", true)}
                type="button"
                onClick={() => setActiveTab("screenshots")}
              >
                Screenshots
              </button>
            ) : null}
            {dupeChecked && ["BDMV", "DVD", "HDDVD"].includes(currentDiscType) ? (
              <button
                className={`subtab-button ${activeTab === "menu_images" ? "active" : ""}`}
                type="button"
                onClick={() => setActiveTab("menu_images")}
              >
                Menu Images
              </button>
            ) : null}
            {dupeChecked ? (
              <button
                className={navButtonClass(activeTab === "upload_images", true)}
                type="button"
                onClick={() => setActiveTab("upload_images")}
              >
                Upload Images
              </button>
            ) : null}
            {dupeChecked ? (
              <button
                className={navButtonClass(activeTab === "description_builder", true)}
                type="button"
                onClick={() => setActiveTab("description_builder")}
              >
                Description Builder
              </button>
            ) : null}
            {builderReady ? (
              <button
                className={navButtonClass(activeTab === "upload", true)}
                type="button"
                onClick={() => setActiveTab("upload")}
              >
                Tracker Upload
              </button>
            ) : null}
          </div>
          <div className={sidebarFooterClass}>
            <button
              className={sidebarButtonClass(activeTab === "settings")}
              type="button"
              onClick={() => setActiveTab("settings")}
            >
              <span>Settings</span>
            </button>
            <button
              className={sidebarButtonClass(activeTab === "logging")}
              type="button"
              onClick={() => setActiveTab("logging")}
            >
              <span>Logging</span>
            </button>
            <button
              className={sidebarButtonClass(activeTab === "history")}
              type="button"
              onClick={() => setActiveTab("history")}
            >
              <span>History</span>
            </button>
            <button
              className={cn(sidebarButtonClass(), "mt-0")}
              type="button"
              onClick={handleThemeToggle}
            >
              <span className="mr-0.5 text-base">{getThemeIcon()}</span>
              <span>{getThemeLabel()}</span>
            </button>
            <div className={sidebarAppDetailsClass}>
              <div className="grid min-w-0 gap-0.5">
                {applicationInfo?.version ? (
                  <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap font-semibold text-[var(--text)]">
                    v{applicationInfo.version}
                  </span>
                ) : null}
                <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">
                  © 2026 autobrr
                </span>
              </div>
              <a
                className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md border border-white/10 text-[var(--muted)] transition hover:border-[var(--accent)] hover:text-[var(--accent)]"
                href="https://github.com/autobrr/upbrr"
                target="_blank"
                rel="noreferrer"
                onAuxClick={handleExternalLinkClick}
                onClick={handleExternalLinkClick}
                aria-label="Open autobrr/upbrr on GitHub"
                title="autobrr/upbrr"
              >
                <svg aria-hidden="true" viewBox="0 0 16 16" className="h-4 w-4" fill="currentColor">
                  <path d="M8 0C3.58 0 0 3.67 0 8.2c0 3.62 2.29 6.69 5.47 7.78.4.08.55-.18.55-.4l-.01-1.4c-2.22.5-2.69-1.1-2.69-1.1-.36-.95-.89-1.2-.89-1.2-.73-.51.05-.5.05-.5.81.06 1.24.85 1.24.85.72 1.27 1.89.9 2.35.69.07-.53.28-.9.51-1.1-1.78-.21-3.64-.91-3.64-4.04 0-.89.31-1.62.82-2.19-.08-.21-.36-1.04.08-2.16 0 0 .68-.22 2.2.84A7.37 7.37 0 0 1 8 3.99c.68 0 1.36.09 2 .28 1.52-1.06 2.19-.84 2.19-.84.44 1.12.16 1.95.08 2.16.52.57.82 1.3.82 2.19 0 3.14-1.87 3.83-3.65 4.04.29.25.54.76.54 1.54l-.01 2.22c0 .22.14.48.55.4A8.13 8.13 0 0 0 16 8.2C16 3.67 12.42 0 8 0Z" />
                </svg>
              </a>
            </div>
          </div>
        </aside>

        <main className="content">
          {showPlaylistSelection ? (
            <PlaylistSelectionPage
              path={playlistSelectionPath}
              onBack={() => {
                setShowPlaylistSelection(false);
                setPlaylistPreparationTrackerSnapshot(null);
              }}
              onConfirm={handlePlaylistSelectionComplete}
              preparing={playlistAutoPreparing}
              progressLines={bdinfoProgressLines}
              progressError={playlistPreparationError}
            />
          ) : activeTab === "settings" ? (
            <SettingsPage
              configData={configData}
              settingsLoading={settingsLoading}
              settingsExporting={settingsExporting}
              settingsImporting={settingsImporting}
              settingsDirty={settingsDirty}
              settingsSaved={settingsSaved}
              settingsError={settingsError}
              configOpStatus={configOpStatus}
              dismissConfigOpStatus={dismissConfigOpStatus}
              settingsSection={settingsSection}
              settingsSections={settingsSections}
              trackerSelectionNames={trackerSelectionNames}
              showAdvancedToggle={showAdvancedToggle}
              advancedOpen={advancedOpen}
              setSettingsSection={setSettingsSection}
              setSettingsAdvanced={setSettingsAdvanced}
              loadSettings={loadSettings}
              handleExportSettings={handleExportSettings}
              handleImportConfig={handleImportConfigRequest}
              importConfirmOpen={importConfirmOpen}
              handleImportConfigConfirm={handleImportConfigConfirm}
              handleImportConfigCancel={handleImportConfigCancel}
              handleSaveSettings={handleSaveSettings}
              webAuthAvailable={Boolean(globalThis.go?.guiapp?.App?.GetWebAuthStatus)}
              webAuthStatus={webAuthStatus}
              webAuthLoading={webAuthLoading}
              webAuthCreating={webAuthCreating}
              webAuthUsername={webAuthUsername}
              webAuthPassword={webAuthPassword}
              webAuthConfirm={webAuthConfirm}
              webAuthError={webAuthError}
              setWebAuthUsername={setWebAuthUsername}
              setWebAuthPassword={setWebAuthPassword}
              setWebAuthConfirm={setWebAuthConfirm}
              handleCreateWebAuth={handleCreateWebAuth}
              renderImageHostingSection={renderImageHostingSection}
              renderTrackerSection={renderTrackerSection}
              renderTorrentClientsSection={renderTorrentClientsSection}
              renderField={renderField}
              sectionFieldMeta={sectionFieldMeta}
            />
          ) : activeTab === "logging" ? (
            <LoggingPage
              configData={configData}
              settingsLoading={settingsLoading}
              settingsDirty={settingsDirty}
              settingsSaved={settingsSaved}
              settingsError={settingsError}
              loadSettings={loadSettings}
              handleSaveSettings={handleSaveSettings}
              renderField={renderField}
              updateConfigValue={updateConfigValue}
              sectionFieldMeta={sectionFieldMeta}
            />
          ) : activeTab === "history" ? (
            <HistoryPage onReleaseDeleted={handleHistoryReleaseDeleted} />
          ) : activeTab === "dupes" ? (
            <DupeCheckPage
              path={path}
              dupeLoading={dupeLoading}
              dupeError={dupeError}
              dupeSummary={dupeSummary}
              dupeTrackerStates={dupeCheckSnapshot?.trackers || []}
              dupeTrackerFlags={dupeTrackerFlags}
              dupeIgnore={dupeIgnore}
              ruleSkippedTrackerSet={ruleSkippedTrackerSet}
              ruleSkipReasons={ruleSkipReasons}
              dupeProgressStatus={dupeProgressStatus}
              dupeCompletedCount={dupeCompletedCount}
              dupeTotalCount={dupeTotalCount}
              useFavicons={useFavicons}
              faviconOnly={faviconOnly}
              trackerIconSrcByName={trackerIconSrcByName}
              handleDupeCheck={handleDupeCheck}
              setDupeIgnore={setDupeIgnore}
            />
          ) : activeTab === "screenshots" ? (
            <ScreenshotsPage
              path={path}
              screenshotPlan={screenshots.screenshotPlan}
              screenshotsLoading={screenshots.screenshotsLoading}
              screenshotsError={screenshots.screenshotsError}
              loadScreenshotPlan={screenshots.loadScreenshotPlan}
              handleGenerateScreenshots={handleGenerateScreenshots}
              screenshotConfig={screenshotConfig}
              updateScreenshotConfigValue={updateScreenshotConfigValue}
              loadSettings={loadSettings}
              settingsLoading={settingsLoading}
              applyScreenshotSettings={applyScreenshotSettings}
              settingsDirty={settingsDirty}
              screenshotsSettingsSaving={screenshotsSettingsSaving}
              livePreviewSeconds={livePreviewSeconds}
              setLivePreviewSeconds={setLivePreviewSeconds}
              livePreviewFrame={livePreviewFrame}
              previewDuration={previewDuration}
              previewFrameRate={previewFrameRate}
              clampPreviewSeconds={clampPreviewSeconds}
              stepLivePreview={stepLivePreview}
              runLivePreview={runLivePreview}
              livePreviewLoading={livePreviewLoading}
              liveCaptureLoading={liveCaptureLoading}
              handleCapturePreviewFrame={handleCapturePreviewFrame}
              livePreviewError={livePreviewError}
              livePreviewImage={livePreviewImage}
              setLightboxImage={setLightboxImage}
              setLightboxAlt={setLightboxAlt}
              trackerImageURLs={trackerImageURLs}
              handleDeleteAllTrackerImageURLs={handleDeleteAllTrackerImageURLs}
              handleDeleteTrackerImage={screenshots.handleDeleteTrackerImage}
              existingImages={screenshots.existingImages}
              addFinalSelection={screenshots.addFinalSelection}
              isFinalImageSelected={screenshots.isFinalImageSelected}
              removeFinalSelection={screenshots.removeFinalSelection}
              handleDeleteAllExistingImages={screenshots.handleDeleteAllExistingImages}
              handleDeleteExistingImage={handleDeleteExistingImage}
              existingTrackerImages={screenshots.existingTrackerImages}
              handleDeleteAllTrackerImages={screenshots.handleDeleteAllTrackerImages}
              showFrameSelections={screenshots.showFrameSelections}
              screenshotSelections={screenshots.screenshotSelections}
              updateSelectionTime={screenshots.updateSelectionTime}
              updateSelectionFrame={screenshots.updateSelectionFrame}
              handlePreviewSelection={handlePreviewSelection}
              previewLoadingIndex={screenshots.previewLoadingIndex}
              previewImages={screenshots.previewImages}
              handleDeleteAllPreviewImages={screenshots.handleDeleteAllPreviewImages}
              finalImages={screenshots.finalImages}
              finalDragIndex={finalDragIndex}
              setFinalDragIndex={setFinalDragIndex}
              reorderFinalSelections={screenshots.reorderFinalSelections}
              finalResult={screenshots.finalResult}
              handleDeleteAllFinalImages={screenshots.handleDeleteAllFinalImages}
            />
          ) : activeTab === "menu_images" ? (
            <MenuImagesPage
              path={path}
              overrides={idOverrideState?.overrides || {}}
              nameOverrides={releaseOverrideState?.overrides || {}}
              metadataOverrides={metadataOverrideState?.overrides || {}}
              browseAvailable={browserNativeBrowseAvailable}
              onImportComplete={() => {
                setActiveTab("upload_images");
              }}
            />
          ) : activeTab === "upload_images" ? (
            <UploadImagesPage
              path={path}
              uploadHost={uploadImages.uploadHost}
              setUploadHost={uploadImages.setUploadHost}
              configuredImageHosts={configuredImageHosts}
              resolveImageHostLabel={resolveImageHostLabel}
              uploadImagesLoading={uploadImages.uploadImagesLoading}
              uploadProgress={uploadImages.uploadProgress}
              setAllUploadSelections={uploadImages.setAllUploadSelections}
              handleUploadImages={uploadImages.handleUploadImages}
              uploadImagesError={uploadImages.uploadImagesError}
              uploadImageFailures={uploadImages.uploadImageFailures}
              uploadCandidates={screenshots.uploadCandidates}
              uploadSelections={uploadImages.uploadSelections}
              toggleUploadSelection={uploadImages.toggleUploadSelection}
              setLightboxImage={setLightboxImage}
              setLightboxAlt={setLightboxAlt}
              uploadedRecordByPath={uploadImages.uploadedRecordByPath}
              uploadedImages={uploadImages.uploadedImages}
              uploadedImageRecords={uploadImages.uploadedImageRecords}
              trackerImageLinks={screenshots.trackerImageLinks}
              trackerImageURLs={trackerImageURLs}
              handleDeleteUploadedImage={uploadImages.handleDeleteUploadedImage}
              handleDeleteTrackerImage={screenshots.handleDeleteTrackerImage}
            />
          ) : activeTab === "bluray" ? (
            <BlurayCandidatesPage
              preview={preview}
              selecting={bluraySelecting}
              error={bluraySelectionError}
              onSelect={(releaseID) => void handleSelectBlurayCandidate(releaseID)}
              setLightboxImage={setLightboxImage}
              setLightboxAlt={setLightboxAlt}
            />
          ) : activeTab === "description_builder" ? (
            <DescriptionBuilderPage
              path={path}
              builderPreview={builderPreview}
              builderRawByGroup={builderRawByGroup}
              builderRenderedByGroup={builderRenderedByGroup}
              builderExpandedGroups={builderExpandedGroups}
              builderLoading={builderLoading}
              builderSaving={builderSaving}
              builderRenderLoading={builderRenderLoading}
              builderRefreshing={builderRefreshing}
              builderProgressMessage={builderProgressMessage}
              builderError={builderError}
              builderSaved={builderSaved}
              useFavicons={useFavicons}
              faviconOnly={faviconOnly}
              trackerIconSrcByName={trackerIconSrcByName}
              refreshDescriptionBuilder={refreshDescriptionBuilder}
              setBuilderRawByGroup={setBuilderRawByGroup}
              setBuilderDirtyByGroup={setBuilderDirtyByGroup}
              setBuilderExpandedGroups={setBuilderExpandedGroups}
              resetBuilderDescription={(groupKey) =>
                resetBuilderDescription(
                  groupKey,
                  idOverrideState?.overrides || {},
                  releaseOverrideState?.overrides || {},
                )
              }
              renderBuilderDescription={renderBuilderDescription}
              saveBuilderDescription={saveBuilderDescription}
            />
          ) : activeTab === "upload" ? (
            <TrackerUploadPage
              trackerUploadItems={trackerUploadItems}
              releasePageTrackerSelection={releasePageTrackerSelection}
              dupedTrackerSet={dupedTrackerSet}
              ruleSkipReasons={ruleSkipReasons}
              ruleSkippedTrackerSet={ruleSkippedTrackerSet}
              failedDupeTrackerSet={failedDupeTrackerSet}
              uploadToggles={uploadToggles}
              setUploadToggles={setUploadToggles}
              skipClientInjection={uploadSkipClientInjection}
              setSkipClientInjection={setUploadSkipClientInjection}
              namingOverrides={namingOverrides}
              preview={preview}
              formatLabel={formatLabel}
              uploadRunning={trackerUploadRunning}
              uploadError={trackerUploadError}
              uploadSnapshot={trackerUploadSnapshot}
              dryRunLoading={trackerDryRunLoading}
              dryRunError={trackerDryRunError}
              dryRunProgress={trackerDryRunProgress}
              dryRunPreview={trackerDryRunPreview}
              trackerQuestionnaireAnswers={trackerQuestionnaireAnswers}
              useFavicons={useFavicons}
              faviconOnly={faviconOnly}
              trackerIconSrcByName={trackerIconSrcByName}
              onQuestionnaireAnswerChange={updateTrackerQuestionnaireAnswer}
              onRunDryRun={handleRunTrackerDryRun}
              onStartUpload={handleStartTrackerUpload}
              onCancelUpload={handleCancelTrackerUpload}
              onRetryFailed={handleRetryFailedTrackerUpload}
            />
          ) : activeTab === "tracker" && hasTrackerData ? (
            <TrackerDataPage
              preview={preview}
              renderedDescriptions={renderedDescriptions}
              setRenderedDescriptions={setRenderedDescriptions}
              setLightboxImage={setLightboxImage}
              setLightboxAlt={setLightboxAlt}
              useFavicons={useFavicons}
              faviconOnly={faviconOnly}
              trackerIconSrcByName={trackerIconSrcByName}
            />
          ) : (
            <InputPage
              path={path}
              handleSourcePathChange={handleSourcePathChange}
              sourcePathHistory={sourcePathHistory}
              handleSourcePathHistorySelect={handleSourcePathHistorySelect}
              sourceLookupURL={sourceLookupURL}
              setSourceLookupURL={setSourceLookupURL}
              browseAvailable={browserMode || browserNativeBrowseAvailable}
              handleBrowseFile={handleBrowseFile}
              handleBrowseFolder={handleBrowseFolder}
              handleFetch={handleFetch}
              handleRefresh={handleRefresh}
              handleResetMetadata={handleResetMetadata}
              loading={loading}
              metadataResetting={metadataResetting}
              metadataProgressActive={metadataProgressActive}
              metadataProgressUpdates={metadataProgressUpdates}
              error={error}
              preview={preview}
              trackerUploadItems={trackerUploadItems}
              releasePageTrackerSelection={releasePageTrackerSelection}
              setReleasePageTrackerSelection={setReleasePageTrackerSelection}
              idEdits={idEdits}
              setIdEdits={setIdEdits}
              releaseEdits={releaseEdits}
              setReleaseEdits={setReleaseEdits}
              markReleaseTouched={markReleaseTouched}
              metadataEdits={metadataEdits}
              setMetadataEdits={setMetadataEdits}
              markMetadataTouched={markMetadataTouched}
              idOverrideState={idOverrideState}
              releaseOverrideState={releaseOverrideState}
              showExternalIDInputUI={showExternalIDInputUI}
              refreshDisabled={refreshDisabled}
              selectedProvider={selectedProvider}
              setSelectedProvider={setSelectedProvider}
              setLightboxImage={setLightboxImage}
              setLightboxAlt={setLightboxAlt}
              runDebug={runDebug}
              setRunDebug={setRunDebug}
              runLogLevel={runLogLevel}
              setRunLogLevel={setRunLogLevel}
              runLogLevelTouched={runLogLevelTouched}
              setRunLogLevelTouched={setRunLogLevelTouched}
              useFavicons={useFavicons}
              faviconOnly={faviconOnly}
              trackerIconSrcByName={trackerIconSrcByName}
            />
          )}
        </main>
        <Dialog.Root
          open={Boolean(lightboxImage)}
          onOpenChange={(open) => {
            if (!open) {
              setLightboxImage("");
              setLightboxAlt("");
            }
          }}
        >
          <Dialog.Portal>
            <Dialog.Overlay className="lightbox-overlay" />
            <Dialog.Content className={`lightbox-content ${lightboxFit ? "fit" : "native"}`}>
              <Dialog.Title className="sr-only">{lightboxAlt || "Preview"}</Dialog.Title>
              <div className="lightbox-toolbar">
                <button
                  className="lightbox-toggle"
                  type="button"
                  onClick={() => setLightboxFit((prev) => !prev)}
                >
                  {lightboxFit ? "Actual size" : "Fit to screen"}
                </button>
              </div>
              <img className="lightbox-image" src={lightboxImage} alt={lightboxAlt || "Preview"} />
            </Dialog.Content>
          </Dialog.Portal>
        </Dialog.Root>
        <Dialog.Root
          open={Boolean(hostBrowserMode)}
          onOpenChange={(open) => {
            if (!open) closeHostBrowser();
          }}
        >
          <Dialog.Portal>
            <Dialog.Overlay className="host-browser-overlay" />
            <Dialog.Content className="host-browser-dialog">
              <div className="host-browser-header">
                <div>
                  <Dialog.Title asChild>
                    <h2 className="label">Host browser</h2>
                  </Dialog.Title>
                  <Dialog.Description asChild>
                    <p className="mono host-browser-path">
                      {hostBrowser?.currentPath || "Computer"}
                    </p>
                  </Dialog.Description>
                </div>
                <Dialog.Close asChild>
                  <button className="ghost" type="button">
                    Close
                  </button>
                </Dialog.Close>
              </div>
              <div className="host-browser-toolbar">
                <button
                  className="ghost"
                  type="button"
                  disabled={hostBrowserLoading || !hostBrowser?.parentPath}
                  onClick={() => void browseHostDirectory(hostBrowser?.parentPath || "")}
                >
                  Up
                </button>
                <button
                  className="ghost"
                  type="button"
                  disabled={hostBrowserLoading}
                  onClick={() => void browseHostDirectory("")}
                >
                  Roots
                </button>
                {hostBrowserMode === "folder" && hostBrowser?.currentPath ? (
                  <button
                    className="primary"
                    type="button"
                    disabled={hostBrowserLoading}
                    onClick={() => void selectHostPath(hostBrowser.currentPath, true)}
                  >
                    Select folder
                  </button>
                ) : null}
                <label className="host-browser-search" htmlFor="host-browser-search">
                  <span>Search</span>
                  <input
                    id="host-browser-search"
                    className="host-browser-search__input"
                    value={hostBrowserSearch}
                    onChange={(event) => setHostBrowserSearch(event.target.value)}
                    placeholder="Filter current path"
                    disabled={hostBrowserLoading || !hostBrowser}
                  />
                </label>
              </div>
              {hostBrowserError ? <p className="error">{hostBrowserError}</p> : null}
              {hostBrowserLoading ? <p className="muted">Loading host paths...</p> : null}
              {!hostBrowserLoading && hostBrowser ? (
                <div className="host-browser-list">
                  {hostBrowserEntries.length === 0 ? (
                    <p className="muted host-browser-empty">No matching paths.</p>
                  ) : (
                    hostBrowserEntries.map((entry, index) => (
                      <div
                        key={entry.path}
                        className="host-browser-entry"
                        ref={(element) => {
                          hostBrowserEntryRefs.current[index] = element;
                        }}
                        tabIndex={0}
                        onKeyDown={(event) => handleHostBrowserEntryKeyDown(event, entry, index)}
                        onDoubleClick={() => {
                          if (entry.isDir) {
                            void browseHostDirectory(entry.path);
                            return;
                          }
                          void selectHostPath(entry.path, entry.isDir);
                        }}
                      >
                        <span className="host-browser-entry__name">
                          {entry.isDir ? "[DIR] " : ""}
                          {entry.name}
                        </span>
                        <span className="host-browser-entry__meta">
                          {entry.isDir
                            ? "Folder"
                            : `${Math.round(entry.size / 1024).toLocaleString()} KiB`}
                        </span>
                        <span className="host-browser-entry__actions">
                          {entry.isDir ? (
                            <button
                              className="ghost"
                              type="button"
                              onClick={(event) => {
                                event.stopPropagation();
                                void browseHostDirectory(entry.path);
                              }}
                            >
                              Open
                            </button>
                          ) : null}
                          {(hostBrowserMode === "folder" && entry.isDir) ||
                          (hostBrowserMode === "file" && !entry.isDir) ? (
                            <button
                              className="primary"
                              type="button"
                              onClick={(event) => {
                                event.stopPropagation();
                                void selectHostPath(entry.path, entry.isDir);
                              }}
                            >
                              Select
                            </button>
                          ) : null}
                        </span>
                      </div>
                    ))
                  )}
                </div>
              ) : null}
            </Dialog.Content>
          </Dialog.Portal>
        </Dialog.Root>
      </div>
    </div>
  );
}
