// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { KeyboardEvent as ReactKeyboardEvent } from "react";
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
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
import logoUrl from "./assets/logo.png";
import type {
  ConfigMap,
  BrowseDirectoryResponse,
  DescriptionBuilderPreview,
  DVDMenuCaptureSnapshot,
  DupeCheckResult,
  DupeCheckSnapshot,
  DupeCheckSummary,
  ExternalIDInfo,
  ExternalIDOverrides,
  ExternalIDs,
  HistoryEntry,
  HistoryOverview,
  ImageHostPolicyMetadata,
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
import { hasFetchedExternalPreviewData } from "./utils/externalPreview";
import { handleExternalLinkClick } from "./utils/externalLinks";
import { normalizeJobStatus } from "./utils/jobStatus";
import { isMetadataProgressPathMatch } from "./utils/metadataProgress";
import { dupeSkipReason, isRuleSkippedResult } from "./utils/dupeCheck";
import {
  hasFilteredEmptyUploadTrackerSelection as hasFilteredEmptyUploadTrackerSelectionState,
  resolveSelectedUploadTrackers,
} from "./utils/trackerSelection";

// Header/nav styling mirrors autobrr's Header, LeftNav and filter-details tabs.
const headerNavItemClass = (active: boolean) =>
  cn(
    "rounded-2xl border-0 bg-transparent px-3 py-2 text-sm font-medium shadow-none transition-colors duration-200",
    "hover:bg-gray-200 hover:text-gray-900 dark:hover:bg-gray-800 dark:hover:text-white",
    active ? "font-bold text-black dark:text-gray-50" : "text-gray-600 dark:text-gray-500",
  );

const headerIconButtonClass =
  "rounded-full border-0 bg-transparent p-1.5 text-gray-600 shadow-none transition-colors duration-200 hover:bg-gray-200 dark:text-gray-500 dark:hover:bg-gray-800";

const mobileNavItemClass = (active: boolean) =>
  cn(
    "block w-full rounded-md border px-3 py-2 text-left text-base font-medium shadow-sm",
    "border-gray-300 bg-gray-100 dark:border-gray-700 dark:bg-gray-900",
    active ? "text-black dark:text-white" : "text-gray-700 dark:text-gray-200",
  );

const workflowTabClass = (active: boolean) =>
  cn(
    "whitespace-nowrap rounded-none border-0 border-b-2 bg-transparent px-1 py-3 text-sm font-medium shadow-none transition-colors",
    active
      ? "border-blue-600 text-blue-600 dark:border-blue-500 dark:text-white"
      : "border-transparent text-gray-550 hover:text-blue-500 dark:hover:text-white",
  );

// Heroicons outline paths, matching the icons autobrr uses in its header.
const themeGlyphPaths: Record<string, string> = {
  light:
    "M12 3v2.25m6.364.386l-1.591 1.591M21 12h-2.25m-.386 6.364l-1.591-1.591M12 18.75V21m-4.773-4.227l-1.591 1.591M5.25 12H3m4.227-4.773L5.636 5.636M15.75 12a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0z",
  dark: "M21.752 15.002A9.718 9.718 0 0118 15.75c-5.385 0-9.75-4.365-9.75-9.75 0-1.33.266-2.597.748-3.752A9.753 9.753 0 003 11.25C3 16.635 7.365 21 12.75 21a9.753 9.753 0 009.002-5.998z",
  auto: "M9 17.25v1.007a3 3 0 01-.879 2.122L7.5 21h9l-.621-.621A3 3 0 0115 18.257V17.25m6-12V15a2.25 2.25 0 01-2.25 2.25H5.25A2.25 2.25 0 013 15V5.25m18 0A2.25 2.25 0 0018.75 3H5.25A2.25 2.25 0 003 5.25m18 0V12",
};

// More heroicons outline paths for the header (user menu, external link).
const headerGlyphPaths = {
  user: [
    "M15.75 6a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0zM4.501 20.118a7.5 7.5 0 0114.998 0A17.933 17.933 0 0112 21.75c-2.676 0-5.216-.584-7.499-1.632z",
  ],
  cog: [
    "M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.324.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 011.37.49l1.296 2.247a1.125 1.125 0 01-.26 1.431l-1.003.827c-.293.24-.438.613-.431.992a6.759 6.759 0 010 .255c-.007.378.138.75.43.99l1.005.828c.424.35.534.954.26 1.43l-1.298 2.247a1.125 1.125 0 01-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.57 6.57 0 01-.22.128c-.331.183-.581.495-.644.869l-.213 1.28c-.09.543-.56.941-1.11.941h-2.594c-.55 0-1.02-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 01-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 01-1.369-.49l-1.297-2.247a1.125 1.125 0 01.26-1.431l1.004-.827c.292-.24.437-.613.43-.992a6.932 6.932 0 010-.255c.007-.378-.138-.75-.43-.99l-1.004-.828a1.125 1.125 0 01-.26-1.43l1.297-2.247a1.125 1.125 0 011.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.087.22-.128.332-.183.582-.495.644-.869l.214-1.281z",
    "M15 12a3 3 0 11-6 0 3 3 0 016 0z",
  ],
  logout: [
    "M15.75 9V5.25A2.25 2.25 0 0013.5 3h-6a2.25 2.25 0 00-2.25 2.25v13.5A2.25 2.25 0 007.5 21h6a2.25 2.25 0 002.25-2.25V15m3 0l3-3m0 0l-3-3m3 3H9",
  ],
  external: [
    "M13.5 6H5.25A2.25 2.25 0 003 8.25v10.5A2.25 2.25 0 005.25 21h10.5A2.25 2.25 0 0018 18.75V10.5m-10.5 6L21 3m0 0h-5.25M21 3v5.25",
  ],
};

function HeaderGlyph({
  name,
  className,
}: {
  name: keyof typeof headerGlyphPaths;
  className: string;
}) {
  return (
    <svg
      className={className}
      fill="none"
      viewBox="0 0 24 24"
      strokeWidth={1.5}
      stroke="currentColor"
      aria-hidden="true"
    >
      {headerGlyphPaths[name].map((d) => (
        <path key={d.slice(0, 24)} strokeLinecap="round" strokeLinejoin="round" d={d} />
      ))}
    </svg>
  );
}

function ThemeGlyph({ theme }: { theme: string }) {
  return (
    <svg
      className="h-4 w-4"
      fill="none"
      viewBox="0 0 24 24"
      strokeWidth={1.5}
      stroke="currentColor"
      aria-hidden="true"
    >
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        d={themeGlyphPaths[theme] ?? themeGlyphPaths.auto}
      />
    </svg>
  );
}

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
    MALID: 0,
    Category: "",
    SourceTMDB: "",
    SourceIMDB: "",
    SourceTVDB: "",
    SourceTVmaze: "",
    SourceMAL: "",
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

/**
 * Returns every normalized tracker label represented by a duplicate-check row.
 * Grouped rule rows use comma-separated tracker labels, while ordinary rows use
 * a single tracker name; callers choose whether the row is a rule skip, generic
 * skip, dupe hit, or failure before applying the labels.
 */
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

/** Maps backend metadata-cache misses to the same actionable dupe-check guidance. */
const dupeCheckErrorMessage = (message: string) =>
  message.includes("dupe check requires metadata preview")
    ? "Fetch metadata first to cache a preview before checking dupes."
    : message;

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
              trackers: string[],
            ) => Promise<MetadataPreview>;
            ResetMetadata: (
              path: string,
              sourceLookupURL: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              trackers: string[],
            ) => Promise<MetadataPreview>;
            SelectBlurayCandidate: (path: string, releaseID: string) => Promise<MetadataPreview>;
            FetchDescriptionBuilder: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              trackers: string[],
              ignoreDupesFor: string[],
            ) => Promise<DescriptionBuilderPreview>;
            FetchPreparation: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              trackers: string[],
              ignoreDupesFor: string[],
            ) => Promise<PreparationPreview>;
            FetchTrackerDryRun: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
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
              trackers: string[],
            ) => Promise<DupeCheckSummary>;
            StartDupeCheck: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              trackers: string[],
            ) => Promise<string>;
            CancelDupeCheck: (jobID: string) => Promise<void>;
            GetDupeCheckSnapshot: (jobID: string) => Promise<DupeCheckSnapshot>;
            FetchScreenshotPlan: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
            ) => Promise<ScreenshotPlan>;
            GenerateScreenshots: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              selections: ScreenshotSelection[],
              purpose: ScreenshotPurpose,
            ) => Promise<ScreenshotResult>;
            PreviewScreenshotFrame: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              timestampSeconds: number,
            ) => Promise<string>;
            DeleteScreenshot: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              imagePath: string,
            ) => Promise<void>;
            SaveFinalScreenshotSelections: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              images: ScreenshotImage[],
            ) => Promise<void>;
            ImportMenuImages: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              paths: string[],
            ) => Promise<void>;
            /** Starts background capture from prepared metadata and resolves to an opaque job ID. */
            StartDVDMenuCapture: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
            ) => Promise<string>;
            /** Returns the latest state for a background DVD menu capture job. */
            GetDVDMenuCaptureSnapshot: (jobID: string) => Promise<DVDMenuCaptureSnapshot>;
            /** Requests asynchronous cancellation of a background DVD menu capture job. */
            CancelDVDMenuCapture: (jobID: string) => Promise<void>;
            /** Lists persisted manual and automatic menu images for a prepared source path. */
            ListDVDMenuScreenshots: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
            ) => Promise<ScreenshotImage[]>;
            /** Removes one managed menu image and its local records. */
            DeleteDVDMenuScreenshot: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
              imagePath: string,
            ) => Promise<void>;
            ReadScreenshotImage: (path: string) => Promise<string>;
            ListUploadCandidates: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
            ) => Promise<ScreenshotImage[]>;
            ListUploadedImages: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
            ) => Promise<UploadedImageLink[]>;
            UploadImages: (
              path: string,
              overrides: ExternalIDOverrides,
              nameOverrides: ReleaseNameOverrides,
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

const providerOrder = ["tmdb", "imdb", "tvdb", "tvmaze", "mal"] as const;

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
  mal: formatNumber(ids.MALID),
});

type IDProviderKey = keyof ReturnType<typeof buildIDEditState>;

const buildIDTouchedState = (): Record<IDProviderKey, boolean> => ({
  tmdb: false,
  imdb: false,
  tvdb: false,
  tvmaze: false,
  mal: false,
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

type AppProps = {
  webUsername?: string;
  onWebLogout?: () => void;
};

export default function App({ webUsername, onWebLogout }: AppProps = {}) {
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
  const [imageAssetsRevision, setImageAssetsRevision] = useState(0);
  const [sourceLookupURL, setSourceLookupURL] = useState("");
  const [loading, setLoading] = useState(false);
  const [metadataResetting, setMetadataResetting] = useState(false);
  const [error, setError] = useState("");
  const [preview, setPreview] = useState<MetadataPreview>(emptyPreview);
  const [idEdits, setIdEdits] = useState(() => buildIDEditState(emptyPreview.ExternalIDs));
  const [idTouched, setIdTouched] = useState<Record<IDProviderKey, boolean>>(() =>
    buildIDTouchedState(),
  );
  const [releaseEdits, setReleaseEdits] = useState(() =>
    buildReleaseEditState(emptyPreview.ReleaseNameOverrides),
  );
  const [releaseTouched, setReleaseTouched] = useState(() =>
    buildReleaseTouchedState(emptyPreview.ReleaseNameOverrides),
  );
  const [showExternalIDInputUI, setShowExternalIDInputUI] = useState(true);
  const [selectedProvider, setSelectedProvider] = useState<string>("");
  const [activeTab, setActiveTab] = useState("input");
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
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
  const discTypeRequestTokenRef = useRef(0);
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
  /**
   * Tracks upload toggles disabled by dupe hits so a later Ignore override can
   * restore only automatic disables, not user-disabled upload targets.
   */
  const autoDisabledUploadTrackersRef = useRef<Set<string>>(new Set());
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
  const maxMenuItems = useMemo(() => {
    const value = screenshotConfig?.MaxMenuItems;
    return typeof value === "number" && Number.isFinite(value) && value > 0 ? Math.trunc(value) : 6;
  }, [screenshotConfig]);

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
    discTypeRequestTokenRef.current += 1;
    setPath(value);
    setSourcePathMode(undefined);
    setCurrentDiscType("");
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

  // Rule failures stay terminal even in debug/dry-run. Keep this set separate
  // from generic skipped duplicate-check results so the UI can show the exact
  // skip reason without labeling config/handler skips as rule failures.
  const ruleSkippedTrackerSet = useMemo(() => {
    const next = new Set<string>();
    dupeEffectiveResults.forEach((result) => {
      if (!result.Tracker || !isRuleSkippedResult(result)) return;
      ruleBlockingTrackerLabels(result).forEach((tracker) => next.add(tracker));
    });
    return next;
  }, [dupeEffectiveResults]);

  // Any skipped duplicate-check result blocks upload eligibility; rule skips are
  // also included here but their display copy comes from ruleSkipReasons.
  const skippedDupeTrackerSet = useMemo(() => {
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

  // Generic skipped reasons cover handler/config skips such as missing API keys.
  // Rule-specific labels are derived separately to avoid misleading copy.
  const skippedDupeReasons = useMemo(() => {
    const next: Record<string, string> = {};
    dupeEffectiveResults.forEach((result) => {
      if (!result.Tracker || !result.Skipped) return;
      const reason = dupeSkipReason(result) || "Skipped";
      ruleBlockingTrackerLabels(result).forEach((tracker) => {
        next[tracker] = reason;
      });
    });
    return next;
  }, [dupeEffectiveResults]);

  const ruleSkipReasons = useMemo(() => {
    const next: Record<string, string> = {};
    dupeEffectiveResults.forEach((result) => {
      if (!result.Tracker || !isRuleSkippedResult(result)) return;
      const reason = dupeSkipReason(result);
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
      mal: parseIDInput("mal", idEdits.mal),
    };

    const invalid = Object.values(parsed).includes(null);
    const overrides: ExternalIDOverrides = {
      TMDBID: parsed.tmdb !== null && idTouched.tmdb ? parsed.tmdb : null,
      IMDBID: parsed.imdb !== null && idTouched.imdb ? parsed.imdb : null,
      TVDBID: parsed.tvdb !== null && idTouched.tvdb ? parsed.tvdb : null,
      TVmazeID: parsed.tvmaze !== null && idTouched.tvmaze ? parsed.tvmaze : null,
      MALID: parsed.mal !== null && idTouched.mal ? parsed.mal : null,
    };
    const dirty = Object.values(overrides).some((value) => value !== null);

    return { overrides, dirty, invalid };
  }, [idEdits, idTouched]);

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

  // Screenshot workflow hook (now idOverrideState/releaseOverrideState are defined)
  const screenshots = useScreenshots({
    path,
    idOverrideState,
    releaseOverrideState,
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
   * Callers starting a fresh dupe check can disable dupe filters while keeping
   * manual upload tracker toggles.
   */
  const resolveUploadTrackerEligibilityForPath = useCallback(
    (sourcePath: string, options: { applyDupeFilters?: boolean } = {}) => {
      const targetPath = sourcePath.trim();
      const currentPath = path.trim();
      const dupeSourcePath = String(
        dupeSummary.SourcePath || dupeCheckSnapshot?.sourcePath || "",
      ).trim();
      const pathCaseInsensitive = isRuntimePathCaseInsensitive();
      const applyDupeFilters = options.applyDupeFilters ?? true;
      const dupeSourceMatchesTarget =
        applyDupeFilters &&
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
        const ignoreAutoDisabledToggle =
          !applyDupeFilters && autoDisabledUploadTrackersRef.current.has(normalized);
        if (
          uploadTogglesMatchTarget &&
          hasOwnSelection(uploadToggles, item.name) &&
          !ignoreAutoDisabledToggle
        ) {
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
      const scopedSkippedDupeTrackerSet = dupeSourceMatchesTarget
        ? skippedDupeTrackerSet
        : emptyTrackerSet;
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
        skippedDupeTrackerSet: scopedSkippedDupeTrackerSet,
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
            skippedDupeTrackerSet: scopedSkippedDupeTrackerSet,
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
      skippedDupeTrackerSet,
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
    handleUploadImages: uploadSelectedImages,
    handleDeleteUploadedImage: deleteUploadedImage,
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

  const markImageAssetsChanged = useCallback(() => {
    setImageAssetsRevision((revision) => revision + 1);
  }, []);

  const handleUploadImagesWithRevision = useCallback(
    async (selected: ScreenshotPreviewImage[]) => {
      await uploadSelectedImages(selected);
      markImageAssetsChanged();
    },
    [markImageAssetsChanged, uploadSelectedImages],
  );

  const handleDeleteUploadedImageWithRevision = useCallback(
    async (imagePath: string, host: string) => {
      await deleteUploadedImage(imagePath, host);
      markImageAssetsChanged();
    },
    [deleteUploadedImage, markImageAssetsChanged],
  );

  const resetScreenshotState = useCallback(() => {
    resetScreenshots();
    resetUploadState();
    setUploadToggles({});
    setFinalDragIndex(null);
    setLiveCaptureLoading(false);
  }, [resetScreenshots, resetUploadState]);

  /**
   * Clears release-specific workflow state after the loaded release is no longer current.
   *
   * Input tracker selections are intentionally preserved so the next metadata fetch keeps the
   * user's configured/default tracker choices.
   */
  const resetFreshWorkflowState = useCallback(
    (nextActiveTab = "input") => {
      discTypeRequestTokenRef.current += 1;
      setPath("");
      setSourcePathMode(undefined);
      setCurrentDiscType("");
      setImageAssetsRevision(0);
      setSourceLookupURL("");
      setLoading(false);
      setMetadataResetting(false);
      setError("");
      setPreview(emptyPreview);
      setIdEdits(buildIDEditState(emptyPreview.ExternalIDs));
      setIdTouched(buildIDTouchedState());
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
      autoDisabledUploadTrackersRef.current.clear();
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
    (!idOverrideState?.dirty && !releaseOverrideState?.dirty && !sourceLookupURL.trim()) ||
    idOverrideState?.invalid ||
    releaseOverrideState?.invalid;

  const normalizeOverrides = (overrides: ExternalIDOverrides) => {
    const payload: ExternalIDOverrides = {};
    if (overrides.TMDBID !== null && overrides.TMDBID !== undefined) {
      payload.TMDBID = overrides.TMDBID;
    }
    if (overrides.IMDBID !== null && overrides.IMDBID !== undefined) {
      payload.IMDBID = overrides.IMDBID;
    }
    if (overrides.TVDBID !== null && overrides.TVDBID !== undefined) {
      payload.TVDBID = overrides.TVDBID;
    }
    if (overrides.TVmazeID !== null && overrides.TVmazeID !== undefined) {
      payload.TVmazeID = overrides.TVmazeID;
    }
    if (overrides.MALID !== null && overrides.MALID !== undefined) {
      payload.MALID = overrides.MALID;
    }
    return payload;
  };

  const normalizeReleaseOverrides = (overrides: ReleaseNameOverrides) => {
    const payload: ReleaseNameOverrides = {};
    if (overrides.Category !== null && overrides.Category !== undefined) {
      payload.Category = overrides.Category;
    }
    if (overrides.Type !== null && overrides.Type !== undefined) {
      payload.Type = overrides.Type;
    }
    if (overrides.Source !== null && overrides.Source !== undefined) {
      payload.Source = overrides.Source;
    }
    if (overrides.Resolution !== null && overrides.Resolution !== undefined) {
      payload.Resolution = overrides.Resolution;
    }
    if (overrides.Tag !== null && overrides.Tag !== undefined) {
      payload.Tag = overrides.Tag;
    }
    if (overrides.Service !== null && overrides.Service !== undefined) {
      payload.Service = overrides.Service;
    }
    if (overrides.Edition !== null && overrides.Edition !== undefined) {
      payload.Edition = overrides.Edition;
    }
    if (overrides.Season !== null && overrides.Season !== undefined) {
      payload.Season = overrides.Season;
    }
    if (overrides.Episode !== null && overrides.Episode !== undefined) {
      payload.Episode = overrides.Episode;
    }
    if (overrides.EpisodeTitle !== null && overrides.EpisodeTitle !== undefined) {
      payload.EpisodeTitle = overrides.EpisodeTitle;
    }
    if (overrides.ManualYear !== null && overrides.ManualYear !== undefined) {
      payload.ManualYear = overrides.ManualYear;
    }
    if (overrides.ManualDate !== null && overrides.ManualDate !== undefined) {
      payload.ManualDate = overrides.ManualDate;
    }
    if (overrides.UseSeasonEpisode !== null && overrides.UseSeasonEpisode !== undefined) {
      payload.UseSeasonEpisode = overrides.UseSeasonEpisode;
    }
    if (overrides.NoSeason !== null && overrides.NoSeason !== undefined) {
      payload.NoSeason = overrides.NoSeason;
    }
    if (overrides.NoYear !== null && overrides.NoYear !== undefined) {
      payload.NoYear = overrides.NoYear;
    }
    if (overrides.NoAKA !== null && overrides.NoAKA !== undefined) {
      payload.NoAKA = overrides.NoAKA;
    }
    if (overrides.NoTag !== null && overrides.NoTag !== undefined) {
      payload.NoTag = overrides.NoTag;
    }
    if (overrides.NoEdition !== null && overrides.NoEdition !== undefined) {
      payload.NoEdition = overrides.NoEdition;
    }
    if (overrides.NoDub !== null && overrides.NoDub !== undefined) {
      payload.NoDub = overrides.NoDub;
    }
    if (overrides.NoDual !== null && overrides.NoDual !== undefined) {
      payload.NoDual = overrides.NoDual;
    }
    if (overrides.DualAudio !== null && overrides.DualAudio !== undefined) {
      payload.DualAudio = overrides.DualAudio;
    }
    if (overrides.Region !== null && overrides.Region !== undefined) {
      payload.Region = overrides.Region;
    }
    return payload;
  };

  const applyPreviewResult = (
    result: MetadataPreview,
    options: { switchToInput?: boolean; preserveIDTouches?: boolean } = {},
  ) => {
    const { switchToInput = true, preserveIDTouches = false } = options;
    setPreview(result);
    if (switchToInput) {
      setActiveTab("input");
    }
    setIdEdits(buildIDEditState(result.ExternalIDs));
    if (!preserveIDTouches) {
      setIdTouched(buildIDTouchedState());
    }
    setReleaseEdits(buildReleaseEditState(result.ReleaseNameOverrides || {}));
    setReleaseTouched(buildReleaseTouchedState(result.ReleaseNameOverrides || {}));
    const fetchedProviders = new Set(
      (result.ExternalPreview || [])
        .filter(hasFetchedExternalPreviewData)
        .map((item) => item.Provider),
    );
    const orderedIDs = filterAndOrderExternalIDs(result.ExternalIDInfo || []).filter((item) =>
      fetchedProviders.has(item.Provider),
    );
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

  const updateCurrentDiscType = async (selectedPath: string): Promise<string | null> => {
    const requestToken = ++discTypeRequestTokenRef.current;
    const discType = await detectDiscType(selectedPath);
    if (requestToken !== discTypeRequestTokenRef.current) {
      return null;
    }
    setCurrentDiscType(discType);
    return discType;
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
    const discType = await updateCurrentDiscType(trimmedPath);
    if (discType === null) {
      return null;
    }
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
      await fetcher(path.trim(), {}, {}, selectedTrackers, []);
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
      await updateCurrentDiscType(targetPath);
      const result = await fetcher(
        targetPath,
        sourceLookupURL.trim(),
        normalizeOverrides(overrides),
        normalizeReleaseOverrides(nameOverrides),
        selectedTrackers,
      );
      applyPreviewResult(result, {
        switchToInput: options.switchToInput,
        preserveIDTouches: Object.keys(normalizeOverrides(overrides)).length > 0,
      });
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
    await runFetch({}, {}, false);
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
    await runFetch({}, {}, false, {
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
    setIdTouched(buildIDTouchedState());
    setReleaseEdits(buildReleaseEditState({}));
    setReleaseTouched(buildReleaseTouchedState({}));
  };

  const handleRefresh = async () => {
    if (
      (!idOverrideState?.dirty && !releaseOverrideState?.dirty && !sourceLookupURL.trim()) ||
      idOverrideState?.invalid ||
      releaseOverrideState?.invalid
    ) {
      return;
    }
    await runFetch(idOverrideState?.overrides || {}, releaseOverrideState?.overrides || {}, true);
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
      const result = await resetter(targetPath, sourceLookupURL.trim(), {}, {}, selectedTrackers);
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
      setDupeError(snapshot.error ? dupeCheckErrorMessage(snapshot.error) : "Dupe check failed.");
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
    const trackerEligibility = resolveUploadTrackerEligibilityForPath(path, {
      applyDupeFilters: false,
    });
    const selectedTrackers = trackerEligibility.selectedTrackers.slice();
    if (selectedTrackers.length === 0) {
      setDupeError("Select at least one tracker before checking dupes.");
      return;
    }
    setDupeLoading(true);
    let jobID = "";
    try {
      jobID = await starter(
        path.trim(),
        normalizeOverrides(idOverrideState?.overrides || {}),
        normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
        selectedTrackers,
      );
    } catch (err) {
      const message = String(err);
      // Starter failures do not create a durable job, so stop polling without
      // discarding the last completed summary.
      setDupeLoading(false);
      setDupeCheckJobID("");
      setDupeError(dupeCheckErrorMessage(message));
      return;
    }
    setDupeChecked(false);
    setDupeSummary(emptyDupeSummary);
    setDupeCheckSnapshot(null);
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
    setCurrentDiscType("");
    setImageAssetsRevision(0);
    setDupeChecked(false);
    setDupeCheckJobID("");
    setDupeCheckSnapshot(null);
    setDupeSummary(emptyDupeSummary);
    setDupeIgnore({});
    setDupeTrackerFlags({});
    autoDisabledUploadTrackersRef.current.clear();
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
      imageAssetsRevision,
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
    builderAutoRequestKey,
    imageAssetsRevision,
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
    setExistingImages,
    readScreenshotImage,
    refreshUploadedImages,
    imageAssetsRevision,
  ]);

  /**
   * Synchronizes upload toggles with tracker selection and dupe eligibility.
   *
   * Dupe hits temporarily turn upload targets off. When the user ignores those
   * dupes, the previous selected/default state is restored; hard failures and
   * rule skips stay disabled until the blocking result changes.
   */
  useEffect(() => {
    if (trackerUploadItems.length === 0) return;
    const next = { ...uploadToggles };
    const autoDisabled = new Set(autoDisabledUploadTrackersRef.current);
    let changed = false;
    const setToggle = (name: string, value: boolean) => {
      if (next[name] !== value) {
        next[name] = value;
        changed = true;
      }
    };
    const deleteToggle = (name: string) => {
      if (Object.prototype.hasOwnProperty.call(next, name)) {
        delete next[name];
        changed = true;
      }
    };

    trackerUploadItems.forEach((item) => {
      const normalized = item.name.toLowerCase().trim();
      const hasReleaseSelection = Object.prototype.hasOwnProperty.call(
        releasePageTrackerSelection,
        item.name,
      );
      if (hasReleaseSelection && !releasePageTrackerSelection[item.name]) {
        autoDisabled.delete(normalized);
        deleteToggle(item.name);
        return;
      }
      if (dupeFiltersMatchCurrentPath && failedDupeTrackerSet.has(normalized)) {
        autoDisabled.delete(normalized);
        setToggle(item.name, false);
        return;
      }
      if (dupeFiltersMatchCurrentPath && dupedTrackerSet.has(normalized)) {
        if (next[item.name] !== false) {
          autoDisabled.add(normalized);
        }
        setToggle(item.name, false);
        return;
      }
      if (dupeFiltersMatchCurrentPath && ruleSkippedTrackerSet.has(normalized)) {
        autoDisabled.delete(normalized);
        setToggle(item.name, false);
        return;
      }
      if (dupeFiltersMatchCurrentPath && skippedDupeTrackerSet.has(normalized)) {
        autoDisabled.delete(normalized);
        setToggle(item.name, false);
        return;
      }
      if (autoDisabled.has(normalized)) {
        setToggle(
          item.name,
          hasReleaseSelection
            ? releasePageTrackerSelection[item.name]
            : defaultTrackerSet.has(normalized),
        );
        autoDisabled.delete(normalized);
        return;
      }
      if (next[item.name] === undefined) {
        // Prioritize release page selection, then fall back to defaults
        if (hasReleaseSelection) {
          setToggle(item.name, releasePageTrackerSelection[item.name]);
        } else {
          setToggle(item.name, defaultTrackerSet.has(normalized));
        }
      }
    });
    const trackerNames = new Set(trackerUploadItems.map((item) => item.name));
    Object.keys(next).forEach((name) => {
      if (!trackerNames.has(name)) {
        autoDisabled.delete(name.toLowerCase().trim());
        deleteToggle(name);
        return;
      }
      if (
        Object.prototype.hasOwnProperty.call(releasePageTrackerSelection, name) &&
        !releasePageTrackerSelection[name]
      ) {
        autoDisabled.delete(name.toLowerCase().trim());
        deleteToggle(name);
      }
    });
    autoDisabledUploadTrackersRef.current = autoDisabled;
    if (changed) {
      setUploadToggles(next);
    }
  }, [
    trackerUploadItems,
    defaultTrackerSet,
    dupeFiltersMatchCurrentPath,
    dupedTrackerSet,
    ruleSkippedTrackerSet,
    skippedDupeTrackerSet,
    failedDupeTrackerSet,
    releasePageTrackerSelection,
    uploadToggles,
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
    setTrackerUploadSnapshot(null);
    let jobID = "";
    try {
      jobID = await starter(
        path.trim(),
        normalizeOverrides(idOverrideState?.overrides || {}),
        normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
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
    getSelectedUploadTrackers,
    ignoredDupeTrackers,
    trackerDryRunPreview,
    trackerQuestionnaireAnswers,
    builderPreview,
    runDebug,
    uploadSkipClientInjection,
    runLogLevel,
    applyTrackerUploadSnapshot,
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

  const workflowTabs = [
    { id: "tracker", label: "Tracker Data", visible: Boolean(hasTrackerData) },
    { id: "bluray", label: "Blu-ray.com", visible: hasBlurayData },
    { id: "dupes", label: "Dupe Checking", visible: hasPreview },
    { id: "screenshots", label: "Screenshots", visible: dupeChecked },
    {
      id: "menu_images",
      label: "Menu Images",
      visible: dupeChecked && ["BDMV", "DVD", "HDDVD"].includes(currentDiscType),
    },
    { id: "upload_images", label: "Upload Images", visible: dupeChecked },
    { id: "description_builder", label: "Description Builder", visible: dupeChecked },
    { id: "upload", label: "Tracker Upload", visible: builderReady },
  ].filter((tab) => tab.visible);

  const headerNavItems = [
    { id: "input", label: "Input" },
    { id: "logging", label: "Logging" },
    { id: "history", label: "History" },
  ];

  // Settings lives in the user menu (web) or the gear button (desktop),
  // but stays a first-class destination in the mobile menu.
  const mobileNavExtras = [{ id: "settings", label: "Settings" }];

  const selectTab = (tab: string) => {
    setActiveTab(tab);
    setMobileNavOpen(false);
  };

  return (
    <div className="app-shell">
      <div className="flex min-h-screen flex-col">
        <nav className="bg-gradient-to-b from-gray-100 dark:from-gray-925">
          <div className="mx-auto w-full max-w-screen-xl sm:px-6 lg:px-8">
            <div className="border-b border-gray-300 dark:border-gray-775">
              <div className="flex h-16 items-center justify-between px-4 sm:px-0">
                <div className="flex items-center">
                  <button
                    className="flex shrink-0 items-center rounded-full border-0 bg-transparent p-0 shadow-none"
                    type="button"
                    onClick={() => selectTab("input")}
                    aria-label="upbrr — go to Input"
                  >
                    <img src={logoUrl} alt="upbrr" className="h-10 w-10" />
                  </button>
                  <div className="hidden sm:ml-3 sm:block">
                    <div className="flex items-baseline space-x-4">
                      {headerNavItems.map((item) => (
                        <button
                          key={item.id}
                          className={headerNavItemClass(activeTab === item.id)}
                          type="button"
                          onClick={() => selectTab(item.id)}
                        >
                          {item.label}
                        </button>
                      ))}
                      <a
                        className={cn(
                          headerNavItemClass(false),
                          "flex items-center justify-center no-underline",
                        )}
                        href="https://github.com/autobrr/upbrr"
                        target="_blank"
                        rel="noreferrer"
                        onAuxClick={handleExternalLinkClick}
                        onClick={handleExternalLinkClick}
                      >
                        GitHub
                        <HeaderGlyph name="external" className="ml-1 inline h-5 w-5" />
                      </a>
                    </div>
                  </div>
                </div>
                <div className="hidden sm:block">
                  <div className="ml-4 flex items-center sm:ml-6">
                    <button
                      className={headerIconButtonClass}
                      type="button"
                      onClick={handleThemeToggle}
                      title={`Theme: ${getThemeLabel()}`}
                    >
                      <ThemeGlyph theme={theme} />
                      <span className="sr-only">Toggle theme ({getThemeLabel()})</span>
                    </button>
                    {!webUsername ? (
                      <button
                        className={cn(headerIconButtonClass, "ml-1")}
                        type="button"
                        onClick={() => selectTab("settings")}
                        title="Settings"
                      >
                        <HeaderGlyph name="cog" className="h-4 w-4" />
                        <span className="sr-only">Settings</span>
                      </button>
                    ) : null}
                    {webUsername ? (
                      <DropdownMenu.Root>
                        <DropdownMenu.Trigger asChild>
                          <button
                            className={cn(
                              "ml-2 flex max-w-xs items-center rounded-full border-0 bg-transparent px-3 py-2 text-sm font-medium shadow-none",
                              "text-gray-600 transition duration-200 hover:bg-gray-200 hover:text-gray-900",
                              "dark:text-gray-500 dark:hover:bg-gray-800 dark:hover:text-white",
                              "data-[state=open]:bg-gray-200 data-[state=open]:text-gray-900",
                              "dark:data-[state=open]:bg-gray-800 dark:data-[state=open]:text-white",
                            )}
                            type="button"
                          >
                            <span className="sr-only">Open user menu for </span>
                            {webUsername}
                            <HeaderGlyph name="user" className="ml-1 inline h-5 w-5" />
                          </button>
                        </DropdownMenu.Trigger>
                        <DropdownMenu.Portal>
                          <DropdownMenu.Content
                            align="end"
                            sideOffset={8}
                            className="z-10 w-48 divide-y divide-gray-100 rounded-md border border-gray-250 bg-white shadow-lg dark:divide-gray-750 dark:border-gray-775 dark:bg-gray-800"
                          >
                            <DropdownMenu.Item asChild>
                              <button
                                className="flex w-full items-center rounded-none rounded-t-md border-0 bg-transparent px-2 py-2 text-left text-sm text-gray-900 shadow-none outline-none transition data-[highlighted]:bg-gray-100 dark:text-gray-200 dark:data-[highlighted]:bg-gray-600"
                                type="button"
                                onClick={() => selectTab("settings")}
                              >
                                <HeaderGlyph
                                  name="cog"
                                  className="mr-1 h-5 w-5 text-gray-700 dark:text-gray-400"
                                />
                                Settings
                              </button>
                            </DropdownMenu.Item>
                            <DropdownMenu.Item asChild>
                              <button
                                className="flex w-full items-center rounded-none rounded-b-md border-0 bg-transparent px-2 py-2 text-left text-sm text-gray-900 shadow-none outline-none transition data-[highlighted]:bg-gray-100 dark:text-gray-200 dark:data-[highlighted]:bg-gray-600"
                                type="button"
                                onClick={onWebLogout}
                              >
                                <HeaderGlyph
                                  name="logout"
                                  className="mr-1 h-5 w-5 text-gray-700 dark:text-gray-400"
                                />
                                Log out
                              </button>
                            </DropdownMenu.Item>
                          </DropdownMenu.Content>
                        </DropdownMenu.Portal>
                      </DropdownMenu.Root>
                    ) : null}
                  </div>
                </div>
                <div className="-mr-2 flex sm:hidden">
                  <button
                    className="inline-flex items-center justify-center rounded-md border-0 bg-gray-200 p-2 text-gray-600 shadow-none hover:bg-gray-700 hover:text-white dark:bg-gray-800 dark:text-gray-400"
                    type="button"
                    onClick={() => setMobileNavOpen((open) => !open)}
                    aria-expanded={mobileNavOpen}
                  >
                    <span className="sr-only">Open main menu</span>
                    <svg
                      className="block h-6 w-6"
                      fill="none"
                      viewBox="0 0 24 24"
                      strokeWidth={1.5}
                      stroke="currentColor"
                      aria-hidden="true"
                    >
                      <path
                        strokeLinecap="round"
                        strokeLinejoin="round"
                        d={
                          mobileNavOpen
                            ? "M6 18L18 6M6 6l12 12"
                            : "M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5"
                        }
                      />
                    </svg>
                  </button>
                </div>
              </div>
            </div>
          </div>
          {mobileNavOpen ? (
            <div className="space-y-1 border-b border-gray-300 px-2 pb-3 pt-2 dark:border-gray-775 sm:hidden">
              {[...headerNavItems, ...mobileNavExtras].map((item) => (
                <button
                  key={item.id}
                  className={mobileNavItemClass(activeTab === item.id)}
                  type="button"
                  onClick={() => selectTab(item.id)}
                >
                  {item.label}
                </button>
              ))}
              {workflowTabs.map((tab) => (
                <button
                  key={tab.id}
                  className={mobileNavItemClass(activeTab === tab.id)}
                  type="button"
                  onClick={() => selectTab(tab.id)}
                >
                  {tab.label}
                </button>
              ))}
              <button
                className={mobileNavItemClass(false)}
                type="button"
                onClick={handleThemeToggle}
              >
                <span className="inline-flex items-center gap-2">
                  <ThemeGlyph theme={theme} />
                  Theme: {getThemeLabel()}
                </span>
              </button>
              {webUsername && onWebLogout ? (
                <button className={mobileNavItemClass(false)} type="button" onClick={onWebLogout}>
                  Log out ({webUsername})
                </button>
              ) : null}
            </div>
          ) : null}
        </nav>

        <main className="content">
          {!showPlaylistSelection && workflowTabs.length > 0 ? (
            <nav
              className="-mb-2 flex gap-4 overflow-x-auto border-b border-gray-250 dark:border-gray-775"
              aria-label="Workflow"
            >
              {workflowTabs.map((tab) => (
                <button
                  key={tab.id}
                  className={workflowTabClass(activeTab === tab.id)}
                  type="button"
                  onClick={() => selectTab(tab.id)}
                >
                  {tab.label}
                </button>
              ))}
            </nav>
          ) : null}
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
              skippedDupeReasons={skippedDupeReasons}
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
              currentDiscType={currentDiscType}
              maxMenuItems={maxMenuItems}
              browseAvailable={browserNativeBrowseAvailable}
              onImagesChanged={markImageAssetsChanged}
              onContinue={() => setActiveTab("upload_images")}
              setLightboxImage={setLightboxImage}
              setLightboxAlt={setLightboxAlt}
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
              handleUploadImages={handleUploadImagesWithRevision}
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
              handleDeleteUploadedImage={handleDeleteUploadedImageWithRevision}
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
              skippedDupeReasons={skippedDupeReasons}
              skippedDupeTrackerSet={skippedDupeTrackerSet}
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
              markIDTouched={(key) =>
                setIdTouched((previous) => ({
                  ...previous,
                  [key]: true,
                }))
              }
              releaseEdits={releaseEdits}
              setReleaseEdits={setReleaseEdits}
              markReleaseTouched={markReleaseTouched}
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
