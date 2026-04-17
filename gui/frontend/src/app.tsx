// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { EventsOn, isBrowserMode, isBrowserNativeBrowseAvailable } from "./utils/runtime";
import DescriptionBuilderPage from "./pages/description_builder";
import DupeCheckPage from "./pages/dupe_check";
import InputPage from "./pages/input";
import HistoryPage from "./pages/history/index";
import LoggingPage from "./pages/logging";
import PlaylistSelectionPage from "./pages/playlist_selection";
import ScreenshotsPage from "./pages/screenshots";
import SettingsPage from "./pages/settings";
import TrackerDataPage from "./pages/tracker_data";
import TrackerUploadPage from "./pages/tracker_upload";
import UploadImagesPage from "./pages/upload_images";
import { useSettingsState } from "./hooks/useSettingsState";
import { useScreenshots } from "./hooks/useScreenshots";
import { useUploadImages } from "./hooks/useUploadImages";
import type {
  ConfigMap,
  DescriptionBuilderPreview,
  DupeCheckSnapshot,
  DupeCheckSummary,
  ExternalIDInfo,
  ExternalIDOverrides,
  ExternalIDs,
  HistoryEntry,
  HistoryOverview,
  MetadataPreview,
  MetadataProgressUpdate,
  PreparationDescription,
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
  TrackerDryRunPreview,
  TrackerUploadSnapshot,
  UploadedImageLink
} from "./types";
import { formatLabel, normalizeDefaultTrackerList } from "./utils/settings";

const emptyDupeSummary: DupeCheckSummary = {
  SourcePath: "",
  Results: [],
  Notes: []
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
    SourceTVmaze: ""
  },
  ExternalIDCandidates: {
    TMDB: [],
    IMDB: [],
    TMDBAutoSelected: false,
    IMDBAutoSelected: false
  },
  ExternalIDInfo: [],
  ExternalPreview: [],
  TrackerData: []
};

const emptyPreparation: PreparationPreview = {
  SourcePath: "",
  Descriptions: []
};

const emptyTrackerDryRun: TrackerDryRunPreview = {
  SourcePath: "",
  Trackers: []
};

const cloneQuestionnaireAnswers = (input: Record<string, Record<string, string>>) =>
  Object.fromEntries(
    Object.entries(input).map(([tracker, values]) => [
      tracker,
      Object.fromEntries(Object.entries(values || {}).map(([key, value]) => [key, String(value ?? "")]))
    ])
  );

const buildQuestionnaireAnswerDefaults = (
  questionnaire: TrackerQuestionnaire | null | undefined,
  existing: Record<string, string> | undefined
) => {
  const next: Record<string, string> = { ...(existing || {}) };
  (questionnaire?.Fields || []).forEach((field) => {
    if (next[field.Key] === undefined) {
      next[field.Key] = field.Value || "";
    }
  });
  return next;
};

const splitTrackerLabel = (value: string) => value
  .split(",")
  .map((entry) => entry.toLowerCase().trim())
  .filter((entry) => entry.length > 0);

const emptyDescriptionBuilder: DescriptionBuilderPreview = {
  SourcePath: "",
  Groups: []
};

const upsertBuilderGroup = (
  preview: DescriptionBuilderPreview,
  nextGroup: DescriptionBuilderPreview["Groups"][number]
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
    Groups: nextGroups
  };
};

const bdinfoProgressEvent = "bdinfo:progress";
const metadataProgressEvent = "metadata:progress";
const dupeCheckEventPrefix = "dupe:job:";
const trackerUploadEventPrefix = "upload:job:";
const runLogLevels = ["error", "warn", "info", "debug", "trace"] as const;

const progressUpdatePrefixes = new Set([
  "scanning",
  "initialize",
  "playlist",
  "clipinfo",
  "stream"
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

declare global {
  var go:
    | {
        guiapp?: {
          App?: {
            BrowsePath: () => Promise<string>;
            BrowseFile: () => Promise<string>;
            BrowseFolder: () => Promise<string>;
            DetectDiscType: (path: string) => Promise<string>;
            FetchMetadata: (path: string, sourceLookupURL: string, overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides, trackers: string[]) => Promise<MetadataPreview>;
            ResetMetadata: (path: string, sourceLookupURL: string, overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides, trackers: string[]) => Promise<MetadataPreview>;
            FetchDescriptionBuilder: (path: string, overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides, trackers: string[], ignoreDupesFor: string[]) => Promise<DescriptionBuilderPreview>;
            FetchPreparation: (path: string, overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides, trackers: string[], ignoreDupesFor: string[]) => Promise<PreparationPreview>;
            FetchTrackerDryRun: (path: string, overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides, trackers: string[], ignoreRuleFailures: boolean, ignoreDupesFor: string[], questionnaireAnswers: Record<string, Record<string, string>>, descriptionGroups: DescriptionBuilderPreview["Groups"], debug: boolean, runLogLevel: string) => Promise<TrackerDryRunPreview>;
            CheckDupes: (path: string, overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides, trackers: string[]) => Promise<DupeCheckSummary>;
            StartDupeCheck: (path: string, overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides, trackers: string[]) => Promise<string>;
            CancelDupeCheck: (jobID: string) => Promise<void>;
            GetDupeCheckSnapshot: (jobID: string) => Promise<DupeCheckSnapshot>;
            FetchScreenshotPlan: (path: string, overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides) => Promise<ScreenshotPlan>;
            GenerateScreenshots: (path: string, overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides, selections: ScreenshotSelection[], purpose: ScreenshotPurpose) => Promise<ScreenshotResult>;
            PreviewScreenshotFrame: (path: string, overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides, timestampSeconds: number) => Promise<string>;
            DeleteScreenshot: (path: string, overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides, imagePath: string) => Promise<void>;
            SaveFinalScreenshotSelections: (path: string, overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides, images: ScreenshotImage[]) => Promise<void>;
            ReadScreenshotImage: (path: string) => Promise<string>;
            ListUploadCandidates: (path: string, overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides) => Promise<ScreenshotImage[]>;
            ListUploadedImages: (path: string, overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides) => Promise<UploadedImageLink[]>;
            UploadImages: (path: string, overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides, host: string, images: ScreenshotImage[]) => Promise<UploadedImageLink[]>;
            DeleteUploadedImage: (path: string, imagePath: string, host: string) => Promise<void>;
            RenderDescription: (raw: string) => Promise<string>;
            SaveDescriptionOverride: (path: string, groupKey: string, raw: string, trackers: string[], overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides) => Promise<DescriptionBuilderPreview["Groups"][number]>;
            DiscoverPlaylists: (path: string) => Promise<any[]>;
            SavePlaylistSelection: (path: string, playlists: string[], useAll: boolean) => Promise<void>;
            LoadPlaylistSelection: (path: string) => Promise<any>;
            GetConfig: () => Promise<string>;
            GetDefaultConfig: () => Promise<string>;
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
            ListHistory: () => Promise<HistoryEntry[]>;
            GetHistoryOverview: (sourcePath: string) => Promise<HistoryOverview>;
            DeleteHistoryRelease: (sourcePath: string) => Promise<void>;
            StartTrackerUpload: (path: string, overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides, trackers: string[], ignoreRuleFailures: boolean, ignoreDupesFor: string[], questionnaireAnswers: Record<string, Record<string, string>>, descriptionGroups: DescriptionBuilderPreview["Groups"], debug: boolean, runLogLevel: string) => Promise<string>;
            CancelTrackerUpload: (jobID: string) => Promise<void>;
            RetryFailedTrackerUpload: (jobID: string) => Promise<string>;
            GetTrackerUploadSnapshot: (jobID: string) => Promise<TrackerUploadSnapshot>;
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
    providerOrder.map((provider, index) => [provider, index])
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
  tvmaze: formatNumber(ids.TVmazeID)
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
  region: overrides?.Region ?? ""
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
  useSeasonEpisode: overrides?.UseSeasonEpisode !== undefined && overrides?.UseSeasonEpisode !== null,
  noSeason: overrides?.NoSeason !== undefined && overrides?.NoSeason !== null,
  noYear: overrides?.NoYear !== undefined && overrides?.NoYear !== null,
  noAKA: overrides?.NoAKA !== undefined && overrides?.NoAKA !== null,
  noTag: overrides?.NoTag !== undefined && overrides?.NoTag !== null,
  noEdition: overrides?.NoEdition !== undefined && overrides?.NoEdition !== null,
  noDub: overrides?.NoDub !== undefined && overrides?.NoDub !== null,
  noDual: overrides?.NoDual !== undefined && overrides?.NoDual !== null,
  dualAudio: overrides?.DualAudio !== undefined && overrides?.DualAudio !== null,
  region: overrides?.Region !== undefined && overrides?.Region !== null
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


export default function App() {
  const browserNativeBrowseAvailable = !isBrowserMode() || isBrowserNativeBrowseAvailable();
  const [path, setPath] = useState("");
  const [sourceLookupURL, setSourceLookupURL] = useState("");
  const [loading, setLoading] = useState(false);
  const [metadataResetting, setMetadataResetting] = useState(false);
  const [error, setError] = useState("");
  const [preview, setPreview] = useState<MetadataPreview>(emptyPreview);
  const [idEdits, setIdEdits] = useState(() => buildIDEditState(emptyPreview.ExternalIDs));
  const [releaseEdits, setReleaseEdits] = useState(() => buildReleaseEditState(emptyPreview.ReleaseNameOverrides));
  const [releaseTouched, setReleaseTouched] = useState(() => buildReleaseTouchedState(emptyPreview.ReleaseNameOverrides));
  const [showExternalIDInputUI, setShowExternalIDInputUI] = useState(true);
  const [selectedProvider, setSelectedProvider] = useState<string>("");
  const [activeTab, setActiveTab] = useState("input");
  const [theme, setTheme] = useState<ThemeMode>("auto");
  const [renderedDescriptions, setRenderedDescriptions] = useState<Record<string, boolean>>({});
  const [lightboxImage, setLightboxImage] = useState<string>("");
  const [lightboxAlt, setLightboxAlt] = useState<string>("");
  const [lightboxFit, setLightboxFit] = useState<boolean>(true);
  const [showPlaylistSelection, setShowPlaylistSelection] = useState(false);
  const [playlistSelectionPath, setPlaylistSelectionPath] = useState("");
  const [playlistAutoPreparing, setPlaylistAutoPreparing] = useState(false);
  const [playlistPreparationError, setPlaylistPreparationError] = useState("");
  const [bdinfoProgressLines, setBdinfoProgressLines] = useState<string[]>([]);
  const [metadataProgressTarget, setMetadataProgressTarget] = useState("");
  const [metadataProgressActive, setMetadataProgressActive] = useState(false);
  const [metadataProgressUpdates, setMetadataProgressUpdates] = useState<MetadataProgressUpdate[]>([]);
  const [dupeSummary, setDupeSummary] = useState<DupeCheckSummary>(emptyDupeSummary);
  const [dupeLoading, setDupeLoading] = useState(false);
  const [dupeError, setDupeError] = useState("");
  const [dupeChecked, setDupeChecked] = useState(false);
  const [dupeCheckJobID, setDupeCheckJobID] = useState("");
  const [dupeCheckSnapshot, setDupeCheckSnapshot] = useState<DupeCheckSnapshot | null>(null);
  const [dupeIgnore, setDupeIgnore] = useState<Record<string, boolean>>({});
  const [dupeTrackerFlags, setDupeTrackerFlags] = useState<Record<string, boolean>>({});
  const [prepPreview, setPrepPreview] = useState<PreparationPreview>(emptyPreparation);
  const [prepLoading, setPrepLoading] = useState(false);
  const [prepError, setPrepError] = useState("");
  const [builderPreview, setBuilderPreview] = useState<DescriptionBuilderPreview>(emptyDescriptionBuilder);
  const [builderRawByGroup, setBuilderRawByGroup] = useState<Record<string, string>>({});
  const [builderRenderedByGroup, setBuilderRenderedByGroup] = useState<Record<string, string>>({});
  const [builderExpandedGroups, setBuilderExpandedGroups] = useState<Record<string, boolean>>({});
  const [builderLoading, setBuilderLoading] = useState(false);
  const [builderError, setBuilderError] = useState("");
  const [builderDirtyByGroup, setBuilderDirtyByGroup] = useState<Record<string, boolean>>({});
  const [builderRenderLoading, setBuilderRenderLoading] = useState(false);
  const [builderSaved, setBuilderSaved] = useState("");
  const [builderSaving, setBuilderSaving] = useState(false);
  const [builderAutoRequestKey, setBuilderAutoRequestKey] = useState("");
  const [uploadToggles, setUploadToggles] = useState<Record<string, boolean>>({});
  const [overrideRuleBlocks, setOverrideRuleBlocks] = useState(false);
  const [trackerUploadRunning, setTrackerUploadRunning] = useState(false);
  const [trackerUploadError, setTrackerUploadError] = useState("");
  const [trackerUploadJobID, setTrackerUploadJobID] = useState("");
  const [trackerUploadSnapshot, setTrackerUploadSnapshot] = useState<TrackerUploadSnapshot | null>(null);
  const [trackerDryRunLoading, setTrackerDryRunLoading] = useState(false);
  const [trackerDryRunError, setTrackerDryRunError] = useState("");
  const [trackerDryRunPreview, setTrackerDryRunPreview] = useState<TrackerDryRunPreview>(emptyTrackerDryRun);
  const [trackerQuestionnaireAnswers, setTrackerQuestionnaireAnswers] = useState<Record<string, Record<string, string>>>({});
  const [releasePageTrackerSelection, setReleasePageTrackerSelection] = useState<Record<string, boolean>>({});
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
  const configOpTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const builderDirty = useMemo(
    () => Object.values(builderDirtyByGroup).some(Boolean),
    [builderDirtyByGroup]
  );
  const builderReady = useMemo(
    () => {
      const normalizedPath = path.trim();
      if (!normalizedPath) {
        return false;
      }
      return builderPreview.SourcePath === normalizedPath && builderPreview.Groups !== undefined;
    },
    [builderPreview.SourcePath, builderPreview.Groups, path]
  );

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
    renderMapSection,
    renderField,
    sectionFieldMeta,
    updateConfigValue,
    updateScreenshotConfigValue,
    configuredImageHosts,
    screenshotConfig,
    buildSavePayload,
    clearSettingsStatus,
    markSettingsSaved,
    setSettingsSavedMessage,
    setSettingsErrorMessage,
    resolveImageHostLabel,
    trackerSelectionNames
  } = useSettingsState({ activeTab });

  const configuredRunLogLevel = useMemo(() => {
    const loggingSection = ((configData as ConfigMap | null)?.Logging ?? null) as ConfigMap | null;
    const rawLevel = String(loggingSection?.Level ?? "info").toLowerCase().trim();
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
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setLightboxImage("");
        setLightboxAlt("");
      }
    };
    globalThis.addEventListener("keydown", handleKeyDown);
    return () => globalThis.removeEventListener("keydown", handleKeyDown);
  }, [lightboxImage]);

  useEffect(() => {
    if (!playlistAutoPreparing) {
      return;
    }
    const off = EventsOn(bdinfoProgressEvent, (payload: any) => {
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
  }, [playlistAutoPreparing]);

  useEffect(() => {
    const normalizePath = (value: string) => value.trim().replaceAll("\\", "/").toLowerCase();
    const off = EventsOn(metadataProgressEvent, (payload: any) => {
      if (!metadataProgressActive) {
        return;
      }
      const eventPath = typeof payload?.path === "string" ? payload.path : "";
      if (metadataProgressTarget && normalizePath(eventPath) !== normalizePath(metadataProgressTarget)) {
        return;
      }

      const update: MetadataProgressUpdate = {
        path: eventPath,
        phase: typeof payload?.phase === "string" ? payload.phase : "",
        message: typeof payload?.message === "string" ? payload.message : "",
        status: typeof payload?.status === "string" ? payload.status : "",
        level: typeof payload?.level === "string" ? payload.level : "info",
        timestamp: typeof payload?.timestamp === "string" ? payload.timestamp : ""
      };

      if (update.phase === "complete" && update.status === "completed") {
        setMetadataProgressActive(false);
        setMetadataProgressTarget("");
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
  }, [metadataProgressActive, metadataProgressTarget]);

  useEffect(() => {
    type ComparisonElement = HTMLElement & { __uaComparisonInit?: boolean };
    const comparisons = Array.from(
      document.querySelectorAll<HTMLElement>(
        ".tracker-description.rendered .comparison"
      )
    );
    const cleanups: Array<() => void> = [];

    comparisons.forEach((comparison) => {
      const element = comparison as ComparisonElement;
      if (element.__uaComparisonInit) {
        return;
      }
      element.__uaComparisonInit = true;

      const details = comparison.querySelector<HTMLDetailsElement>(
        ".comparison__details"
      );
      const summary = comparison.querySelector<HTMLElement>(
        ".comparison__details > summary"
      );
      const rows = Array.from(
        comparison.querySelectorAll<HTMLElement>(".comparison__row")
      );
      if (!details || rows.length === 0) {
        return;
      }

      const maxColumns = rows.reduce(
        (max, row) => Math.max(max, row.children.length),
        0
      );
      if (maxColumns <= 1) {
        return;
      }

      let current = 0;

      const applyColumn = (next: number) => {
        const clamped = Math.min(Math.max(next, 1), maxColumns);
        if (clamped === current) {
          return;
        }
        current = clamped;
        rows.forEach((row) => {
          const columns = Array.from(row.children) as HTMLElement[];
          columns.forEach((cell, index) => {
            const isActive = index + 1 === current;
            cell.classList.toggle(
              "comparison__image-container--hidden",
              !isActive
            );
            const image = cell.querySelector<HTMLElement>(
              ".comparison__image"
            );
            if (image) {
              image.classList.toggle("comparison__image--hidden", !isActive);
            }
          });
        });
      };

      const handleKeyDown = (event: KeyboardEvent) => {
        if (!details.open && !comparison.classList.contains("comparison--open")) {
          return;
        }
        if (event.key === "ArrowLeft") {
          event.preventDefault();
          const next = current - 1 < 1 ? maxColumns : current - 1;
          applyColumn(next);
          return;
        }
        if (event.key === "ArrowRight") {
          event.preventDefault();
          const next = current + 1 > maxColumns ? 1 : current + 1;
          applyColumn(next);
          return;
        }
        if (event.key === "Escape") {
          event.preventDefault();
          details.open = false;
          comparison.classList.remove("comparison--open");
          return;
        }
        const digit = Number.parseInt(event.key, 10);
        if (!Number.isNaN(digit) && digit >= 1 && digit <= maxColumns) {
          event.preventDefault();
          applyColumn(digit);
        }
      };

      const handleMouseMove = (event: MouseEvent) => {
        if (
          (!details.open && !comparison.classList.contains("comparison--open")) ||
          maxColumns <= 1
        ) {
          return;
        }
        const width = globalThis.innerWidth || 1;
        const ratio = event.clientX / width;
        const next = Math.min(
          maxColumns,
          Math.max(1, Math.ceil(ratio * maxColumns))
        );
        applyColumn(next);
      };

      const handleToggle = () => {
        if (details.open || comparison.classList.contains("comparison--open")) {
          applyColumn(current);
          globalThis.addEventListener("keydown", handleKeyDown);
          globalThis.addEventListener("mousemove", handleMouseMove);
        } else {
          globalThis.removeEventListener("keydown", handleKeyDown);
          globalThis.removeEventListener("mousemove", handleMouseMove);
        }
      };

      const handleSummaryClick = (event: MouseEvent) => {
        event.preventDefault();
        details.open = !details.open;
        comparison.classList.toggle("comparison--open", details.open);
        handleToggle();
      };

      details.addEventListener("toggle", handleToggle);
      if (summary) {
        summary.addEventListener("click", handleSummaryClick);
      }
      applyColumn(1);
      if (details.open) {
        handleToggle();
      }

      cleanups.push(() => {
        details.removeEventListener("toggle", handleToggle);
        if (summary) {
          summary.removeEventListener("click", handleSummaryClick);
        }
        globalThis.removeEventListener("keydown", handleKeyDown);
        globalThis.removeEventListener("mousemove", handleMouseMove);
        element.__uaComparisonInit = false;
      });
    });

    return () => {
      cleanups.forEach((cleanup) => cleanup());
    };
  }, [renderedDescriptions, preview.TrackerData, prepPreview.Descriptions, builderRenderedByGroup, builderPreview.Groups, activeTab]);

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

  const hasTrackerData = preview.TrackerData && preview.TrackerData.length > 0;
  const hasPreview = Boolean(preview.SourcePath);

  useEffect(() => {
    setDupeIgnore((prev) => {
      if (!dupeSummary.Results || dupeSummary.Results.length === 0) {
        return prev;
      }
      let changed = false;
      const next = { ...prev };
      dupeSummary.Results.forEach((result) => {
        const tracker = result.Tracker;
        if (!tracker) return;
        if (next[tracker] === undefined) {
          next[tracker] = false;
          changed = true;
        }
      });
      return changed ? next : prev;
    });
  }, [dupeSummary]);

  useEffect(() => {
    if (!dupeSummary.Results || dupeSummary.Results.length === 0) {
      setDupeTrackerFlags({});
      return;
    }
    const next: Record<string, boolean> = {};
    dupeSummary.Results.forEach((result) => {
      const tracker = result.Tracker;
      if (!tracker) return;
      const ignored = dupeIgnore[tracker] ?? false;
      const skipped = Boolean(result.Skipped);
      next[tracker] = Boolean(result.HasDupes) && !ignored && !skipped;
    });
    setDupeTrackerFlags(next);
  }, [dupeSummary, dupeIgnore]);

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
    (dupeSummary.Results || []).forEach((result) => {
      if (!result.Tracker || !result.Skipped) return;
      const normalized = result.Tracker.toLowerCase().trim();
      if (normalized) next.add(normalized);
      splitTrackerLabel(result.Tracker).forEach((tracker) => next.add(tracker));
    });
    return next;
  }, [dupeSummary]);

  const failedDupeTrackerSet = useMemo(() => {
    const next = new Set<string>();
    (dupeSummary.Results || []).forEach((result) => {
      if (!result.Tracker) return;
      const status = String(result.Status || "").toLowerCase().trim();
      const hasError = Boolean(String(result.Error || "").trim());
      if (status !== "failed" && !hasError) return;
      const normalized = result.Tracker.toLowerCase().trim();
      if (normalized) next.add(normalized);
      splitTrackerLabel(result.Tracker).forEach((tracker) => next.add(tracker));
    });
    return next;
  }, [dupeSummary]);

  const ruleSkipReasons = useMemo(() => {
    const next: Record<string, string> = {};
    (dupeSummary.Results || []).forEach((result) => {
      if (!result.Tracker || !result.Skipped) return;
      const normalized = result.Tracker.toLowerCase().trim();
      if (!normalized) return;
      const reason = (result.SkipReason || result.Notes?.join(" ") || "rule check failed").trim();
      next[normalized] = reason || "rule check failed";
      splitTrackerLabel(result.Tracker).forEach((tracker) => {
        next[tracker] = reason || "rule check failed";
      });
    });
    return next;
  }, [dupeSummary]);

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
  const filterPrepDescriptions = (descriptions: PreparationDescription[]) => {
    if (dupedTrackerSet.size === 0 && ruleSkippedTrackerSet.size === 0) return descriptions;
    const filtered: PreparationDescription[] = [];
    descriptions.forEach((entry) => {
      const trackers = (entry.Trackers || []).filter(
        (tracker) => {
          const normalized = String(tracker).toLowerCase().trim();
          return !dupedTrackerSet.has(normalized) && !ruleSkippedTrackerSet.has(normalized);
        }
      );
      if (trackers.length === 0) return;
      filtered.push({ ...entry, Trackers: trackers });
    });
    return filtered;
  };



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
      ([, value]) => value && typeof value === "object" && !Array.isArray(value)
    ) as Array<[string, ConfigMap]>;
    const visibleTrackerSet = new Set(trackerSelectionNames);

    return entries
      .filter(([name]) => visibleTrackerSet.has(name))
      .map(([name, config]) => ({ name, config }))
      .sort((left, right) => left.name.localeCompare(right.name));
  }, [configData, trackerSelectionNames]);

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
      tvmaze: parseIDInput("tvmaze", idEdits.tvmaze)
    };

    const invalid = Object.values(parsed).includes(null);
    const overrides: ExternalIDOverrides = {
      TMDBID: parsed.tmdb !== null && parsed.tmdb !== preview.ExternalIDs.TMDBID ? parsed.tmdb : null,
      IMDBID: parsed.imdb !== null && parsed.imdb !== preview.ExternalIDs.IMDBID ? parsed.imdb : null,
      TVDBID: parsed.tvdb !== null && parsed.tvdb !== preview.ExternalIDs.TVDBID ? parsed.tvdb : null,
      TVmazeID: parsed.tvmaze !== null && parsed.tvmaze !== preview.ExternalIDs.TVmazeID ? parsed.tvmaze : null
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
    const stored = (preview.ReleaseNameOverrides && typeof preview.ReleaseNameOverrides === 'object') ? preview.ReleaseNameOverrides : {};
    let invalid = false;

    const readTrimmed = (value: string | null | undefined) => (value || "").trim();
    const stringDirty = (touched: boolean, current: string | null | undefined, storedValue?: string | null) => {
      if (!touched) return false;
      if (storedValue === undefined || storedValue === null) return true;
      return readTrimmed(current) !== readTrimmed(storedValue);
    };
    const boolDirty = (touched: boolean, current: boolean | null | undefined, storedValue?: boolean | null) => {
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
    if (releaseTouched.episodeTitle) overrides.EpisodeTitle = readTrimmed(releaseEdits.episodeTitle);

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
      boolDirty(releaseTouched.useSeasonEpisode, releaseEdits.useSeasonEpisode, stored.UseSeasonEpisode) ||
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
    screenshotsEnabled,
    setScreenshotsEnabled,
    screenshotsSettingsSaving,
    setScreenshotsSettingsSaving,
    setScreenshotsError,
    loadScreenshotPlan,
    readScreenshotImage,
    setExistingImages,
    resetScreenshotState: resetScreenshots,
    handleDeleteTrackerImageURL,
  } = screenshots;

  // Upload images workflow hook
  const uploadImages = useUploadImages({
    path,
    idOverrideState,
    releaseOverrideState,
    uploadCandidates: screenshots.uploadCandidates,
    configuredImageHosts,
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
      screenshots.uploadCandidates.map((item) => item.image.Path).filter((path): path is string => Boolean(path))
    );
  }, [screenshots.uploadCandidates]);

  const resetScreenshotState = useCallback(() => {
    resetScreenshots();
    resetUploadState();
    setUploadToggles({});
    setOverrideRuleBlocks(false);
    setFinalDragIndex(null);
    setLiveCaptureLoading(false);
  }, [resetScreenshots, resetUploadState]);



  // Helper functions for screenshot management (not in the hook)
  const handleDeleteExistingImage = (image: ScreenshotImage) => {
    if (!image.Path) return;
    const deletedPath = image.Path;
    screenshots.setExistingImages((prev) => prev.filter((entry) => entry.image.Path !== deletedPath));
    if (screenshots.finalImagesRef.current.length > 0) {
      screenshots.saveFinalSelections(
        screenshots.finalImagesRef.current.filter((entry) => entry.image.Path !== deletedPath)
      );
    }
  };

  const mergeFinalSelections = (current: ScreenshotPreviewImage[], additions: ScreenshotPreviewImage[]) => {
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
    if (screenshotConfig && typeof screenshotConfig.Screens === "number" && screenshotConfig.Screens > 0) {
      return screenshotConfig.Screens;
    }
    if (screenshots.screenshotPlan && Array.isArray(screenshots.screenshotPlan.SuggestedSelections)) {
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
      return !manual.some((manualEntry) => Math.abs(normalizeSelectionTimestamp(manualEntry) - ts) <= tolerance);
    });

    const needed = Math.max(0, targetCount - manual.length);
    const auto = filtered.slice(0, needed).map((entry) => ({
      ...entry,
      Source: "auto"
    }));

    return [...manual, ...auto];
  };

  const namingOverrides = useMemo(() => {
    const stored = (preview.ReleaseNameOverrides && typeof preview.ReleaseNameOverrides === "object")
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

  const applyPreviewResult = (result: MetadataPreview) => {
    setPreview(result);
    setActiveTab("input");
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
    setPrepPreview(emptyPreparation);
    setPrepError("");
    setBuilderPreview(emptyDescriptionBuilder);
    setBuilderRawByGroup({});
    setBuilderRenderedByGroup({});
    setBuilderExpandedGroups({});
    setBuilderError("");
    setBuilderDirtyByGroup({});
    setBuilderSaved("");
    setBuilderAutoRequestKey("");
    resetScreenshotState();
  };

  const runBrowse = async (mode: "file" | "folder") => {
    setError("");
    const app = globalThis.go?.guiapp?.App;
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
  const handlePathSelected = async (selectedPath: string, mode: "file" | "folder" = "folder") => {
    setPath(selectedPath);
    setShowExternalIDInputUI(true);
    setPlaylistPreparationError("");
    setBdinfoProgressLines([]);
    setPlaylistAutoPreparing(false);

    if (mode === "file") {
      setShowPlaylistSelection(false);
      setPlaylistSelectionPath("");
      setActiveTab("input");
      return;
    }

    const discType = await detectDiscType(selectedPath);
    if (discType !== "BDMV") {
      setShowPlaylistSelection(false);
      setPlaylistSelectionPath("");
      setActiveTab("input");
      return;
    }

    // Smart path detection: check if path already contains BDMV or PLAYLIST
    const upperPath = selectedPath.toUpperCase();
    let bdmvPath = selectedPath;

    if (!upperPath.includes("\\BDMV") && !upperPath.includes("/BDMV")) {
      // Try BDMV subfolder first
      bdmvPath = `${selectedPath}/BDMV`;
    }

    // Set the path for playlist discovery (component will discover the playlists)
    setPlaylistSelectionPath(bdmvPath);
    setShowPlaylistSelection(true);
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
    try {
      await fetcher(path.trim(), {}, {}, getSelectedTrackers(), []);
      return true;
    } catch (err) {
      setPlaylistPreparationError(String(err));
      return false;
    }
  };

  const handlePlaylistSelectionComplete = async () => {
    setPlaylistPreparationError("");
    setBdinfoProgressLines([]);
    setPlaylistAutoPreparing(true);
    const completed = await runPlaylistBDInfo();
    setPlaylistAutoPreparing(false);
    if (completed) {
      setShowPlaylistSelection(false);
      setPlaylistSelectionPath("");
      setActiveTab("input");
    }
  };

  const runFetch = async (
    overrides: ExternalIDOverrides,
    nameOverrides: ReleaseNameOverrides,
    hideExternalIDInputUIOnSuccess = false
  ) => {
    setError("");
    setDupeChecked(false);
    setDupeSummary(emptyDupeSummary);
    setPrepPreview(emptyPreparation);
    setPrepError("");
    setBuilderPreview(emptyDescriptionBuilder);
    setBuilderRawByGroup({});
    setBuilderRenderedByGroup({});
    setBuilderExpandedGroups({});
    setBuilderDirtyByGroup({});
    const fetcher = globalThis.go?.guiapp?.App?.FetchMetadata;
    if (!fetcher) {
      setError("Fetch metadata is unavailable in this build.");
      return;
    }
    if (!path.trim()) {
      setError("Please select a file or folder.");
      return;
    }
    const targetPath = path.trim();
    setMetadataProgressTarget(targetPath);
    setMetadataProgressUpdates([]);
    setMetadataProgressActive(true);
    setLoading(true);
    try {
      const result = await fetcher(
        targetPath,
        sourceLookupURL.trim(),
        normalizeOverrides(overrides),
        normalizeReleaseOverrides(nameOverrides),
        getSelectedTrackers()
      );
      applyPreviewResult(result);
      setShowExternalIDInputUI(!hideExternalIDInputUIOnSuccess);
    } catch (err) {
      setError(String(err));
    } finally {
      setMetadataProgressActive(false);
      setLoading(false);
    }
  };

  const handleFetch = async () => {
    await runFetch({}, {}, false);
  };

  const clearEditAttributesState = () => {
    setIdEdits(buildIDEditState(emptyPreview.ExternalIDs));
    setReleaseEdits(buildReleaseEditState({}));
    setReleaseTouched(buildReleaseTouchedState({}));
  };

  const handleRefresh = async () => {
    if ((!idOverrideState?.dirty && !releaseOverrideState?.dirty && !sourceLookupURL.trim()) || idOverrideState?.invalid || releaseOverrideState?.invalid) {
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
    if (!globalThis.confirm("Remove cached metadata and temporary files for this content, then refetch metadata?")) {
      return;
    }
    const targetPath = path.trim();
    clearEditAttributesState();
    setMetadataProgressTarget(targetPath);
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
        getSelectedTrackers()
      );
      applyPreviewResult(result);
      setShowExternalIDInputUI(true);
    } catch (err) {
      setError(String(err));
    } finally {
      setMetadataProgressActive(false);
      setMetadataResetting(false);
      setLoading(false);
    }
  };

  const runPreparation = async (overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides) => {
    setPrepError("");
    const fetcher = globalThis.go?.guiapp?.App?.FetchPreparation;
    if (!fetcher) {
      setPrepError("Preparation preview is unavailable in this build.");
      return false;
    }
    if (!path.trim()) {
      setPrepError("Please select a file or folder.");
      return false;
    }
    setPrepLoading(true);
    try {
      const result = await fetcher(
        path.trim(),
        normalizeOverrides(overrides),
        normalizeReleaseOverrides(nameOverrides),
        Object.entries(releasePageTrackerSelection)
          .filter(([, selected]) => selected)
          .map(([name]) => name),
        ignoredDupeTrackers
      );
      const filteredDescriptions = filterPrepDescriptions(result.Descriptions || []);
      setPrepPreview({ ...result, Descriptions: filteredDescriptions });
      return true;
    } catch (err) {
      setPrepError(String(err));
      return false;
    } finally {
      setPrepLoading(false);
    }
  };

  const runDescriptionBuilder = useCallback(async (overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides) => {
    setBuilderError("");
    setBuilderSaved("");
    const fetcher = globalThis.go?.guiapp?.App?.FetchDescriptionBuilder;
    if (!fetcher) {
      setBuilderError("Description builder is unavailable in this build.");
      return;
    }
    if (!path.trim()) {
      setBuilderError("Please select a file or folder.");
      return;
    }
    setBuilderLoading(true);
    try {
      const result = await fetcher(
        path.trim(),
        normalizeOverrides(overrides),
        normalizeReleaseOverrides(nameOverrides),
        Object.entries(releasePageTrackerSelection)
          .filter(([, selected]) => selected)
          .map(([name]) => name),
        ignoredDupeTrackers
      );
      setBuilderPreview(result);
      setBuilderRawByGroup(
        Object.fromEntries(
          (result.Groups || []).map((group) => [group.GroupKey, group.RawDescription || ""])
        )
      );
      setBuilderRenderedByGroup(
        Object.fromEntries(
          (result.Groups || []).map((group) => [group.GroupKey, group.RawDescriptionHTML || ""])
        )
      );
      setBuilderExpandedGroups((prev) => {
        const next: Record<string, boolean> = {};
        (result.Groups || []).forEach((group) => {
          next[group.GroupKey] = prev[group.GroupKey] ?? false;
        });
        return next;
      });
      setBuilderDirtyByGroup({});
    } catch (err) {
      setBuilderError(String(err));
    } finally {
      setBuilderLoading(false);
    }
  }, [path, releasePageTrackerSelection, ignoredDupeTrackers]);

  const resetBuilderDescription = async (groupKey: string, overrides: ExternalIDOverrides, nameOverrides: ReleaseNameOverrides) => {
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
        normalizeReleaseOverrides(nameOverrides)
      );
      setBuilderPreview((prev) => upsertBuilderGroup(prev, updatedGroup));
      setBuilderRawByGroup((prev) => ({ ...prev, [groupKey]: updatedGroup.RawDescription || "" }));
      setBuilderRenderedByGroup((prev) => ({ ...prev, [groupKey]: updatedGroup.RawDescriptionHTML || "" }));
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
        normalizeReleaseOverrides(releaseOverrideState?.overrides || {})
      );
      const nextPreview = upsertBuilderGroup(builderPreview, updatedGroup);
      const shouldRefreshDryRun = path.trim() === String(trackerDryRunPreview.SourcePath || "").trim() && (trackerDryRunPreview.Trackers || []).length > 0;

      setBuilderPreview(nextPreview);
      setBuilderRawByGroup((prev) => ({ ...prev, [groupKey]: updatedGroup.RawDescription || "" }));
      setBuilderRenderedByGroup((prev) => ({ ...prev, [groupKey]: updatedGroup.RawDescriptionHTML || "" }));
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



  const runScreenshotCapture = async (selections: ScreenshotSelection[], purpose: ScreenshotPurpose) => {
    const runner = globalThis.go?.guiapp?.App?.GenerateScreenshots;
    if (!runner) {
      throw new Error("Screenshot capture is unavailable in this build.");
    }
    return runner(
      path.trim(),
      normalizeOverrides(idOverrideState?.overrides || {}),
      normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
      selections,
      purpose
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
    await fetcher(
      path.trim(),
      sourceLookupURL.trim(),
      normalizeOverrides(idOverrideState?.overrides || {}),
      normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
      getSelectedTrackers()
    );
  };

  const previewFrameRate = useMemo(() => {
    const rate = screenshots.screenshotPlan?.FrameRate || 0;
    return rate > 0 ? rate : 24;
  }, [screenshots.screenshotPlan]);

  const previewDuration = useMemo(() => screenshots.screenshotPlan?.DurationSeconds || 0, [screenshots.screenshotPlan]);

  const clampPreviewSeconds = useCallback((value: number) => {
    if (!Number.isFinite(value)) return 0;
    if (previewDuration > 0) {
      return Math.min(Math.max(value, 0), previewDuration);
    }
    return Math.max(value, 0);
  }, [previewDuration]);

  const livePreviewFrame = useMemo(() => {
    if (previewFrameRate <= 0) return 0;
    const seconds = clampPreviewSeconds(livePreviewSeconds);
    const frame = Math.round(seconds * previewFrameRate);
    return Number.isFinite(frame) ? frame : 0;
  }, [livePreviewSeconds, previewFrameRate, clampPreviewSeconds]);

  const runLivePreviewAt = async (timestampSeconds: number) => {
    setLivePreviewError("");
    if (!screenshotsEnabled) {
      setLivePreviewError("Enable screenshot capture to generate previews.");
      return;
    }
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
        timestamp
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
    if (!screenshotsEnabled) {
      screenshots.setScreenshotsError("Enable screenshot capture to generate previews.");
      return;
    }
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
    if (!screenshotsEnabled) {
      screenshots.setScreenshotsError("Enable screenshot capture to save previews.");
      return;
    }
    if (!path.trim()) {
      screenshots.setScreenshotsError("Please select a file or folder.");
      return;
    }

    const timestamp = clampPreviewSeconds(livePreviewSeconds);
    const baseSelections = screenshots.screenshotSelections.length > 0
      ? screenshots.screenshotSelections
      : (screenshots.screenshotPlan?.SuggestedSelections || []);
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

    const closest = candidates.reduce((best, entry) => {
      const currentDiff = Math.abs(resolveTimestamp(entry) - timestamp);
      if (!best) return entry;
      const bestDiff = Math.abs(resolveTimestamp(best) - timestamp);
      if (currentDiff < bestDiff) return entry;
      return best;
    }, undefined as ScreenshotSelection | undefined);

    if (!closest) {
      screenshots.setScreenshotsError("No screenshot selections available.");
      return;
    }

    const frame = Math.max(0, Math.round(timestamp * previewFrameRate));
    const selection: ScreenshotSelection = {
      Index: closest.Index,
      TimestampSeconds: timestamp,
      Frame: frame,
      Source: "manual"
    };

    const updatedSelections = baseSelections.map((entry) =>
      entry.Index === selection.Index ? selection : entry
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
    const resolvedSelection = manualSelection
      || (reindexed.targetIndex >= 0 ? reindexed.selections[reindexed.targetIndex] : undefined);
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
    if (!screenshotsEnabled) {
      screenshots.setScreenshotsError("Enable screenshot capture to generate screenshots.");
      return;
    }
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
    try {
      const jobID = await starter(
        path.trim(),
        normalizeOverrides(idOverrideState?.overrides || {}),
        normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
        selectedTrackers
      );
      setDupeCheckJobID(jobID);
      if (snapshotLoader) {
        const snapshot = await snapshotLoader(jobID);
        setDupeCheckSnapshot(snapshot);
        setDupeSummary(snapshot.summary || emptyDupeSummary);
      }
    } catch (err) {
      const message = String(err);
      setDupeChecked(false);
      setPrepPreview(emptyPreparation);
      if (message.includes("dupe check requires metadata preview")) {
        setDupeError("Fetch metadata first to cache a preview before checking dupes.");
      } else {
        setDupeError(message);
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
      const snapshot = payload as DupeCheckSnapshot;
      setDupeCheckSnapshot(snapshot);
      setDupeSummary(snapshot.summary || emptyDupeSummary);

      const normalized = String(snapshot.status || "").toLowerCase().trim();
      const running = normalized === "queued" || normalized === "running";
      setDupeLoading(running);

      if (normalized === "completed") {
        setDupeChecked(true);
        setDupeError("");
      } else if (normalized === "completed_with_errors") {
        setDupeChecked(true);
        setDupeError(snapshot.error || "One or more tracker dupe checks failed.");
      } else if (normalized === "failed" || normalized === "canceled") {
        setDupeChecked(false);
        setPrepPreview(emptyPreparation);
        setDupeError(snapshot.error || "Dupe check failed.");
      }
    });

    return () => {
      if (typeof off === "function") {
        off();
      }
    };
  }, [dupeCheckJobID]);

  useEffect(() => {
    setDupeChecked(false);
    setDupeCheckJobID("");
    setDupeCheckSnapshot(null);
    setDupeSummary(emptyDupeSummary);
    setDupeIgnore({});
    setDupeTrackerFlags({});
    setPrepPreview(emptyPreparation);
    setPrepError("");
    setBuilderPreview(emptyDescriptionBuilder);
    setBuilderRawByGroup({});
    setBuilderRenderedByGroup({});
    setBuilderExpandedGroups({});
    setBuilderError("");
    setBuilderDirtyByGroup({});
    setBuilderSaved("");
    setBuilderAutoRequestKey("");
    setOverrideRuleBlocks(false);
    setTrackerUploadRunning(false);
    setTrackerUploadError("");
    setTrackerUploadJobID("");
    setTrackerUploadSnapshot(null);
    setTrackerDryRunLoading(false);
    setTrackerDryRunError("");
    setTrackerDryRunPreview(emptyTrackerDryRun);
    setMetadataProgressTarget("");
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
      release: normalizeReleaseOverrides(releaseOverrideState?.overrides || {})
    });
    if (builderAutoRequestKey === requestKey) return;
    setBuilderAutoRequestKey(requestKey);
    runDescriptionBuilder(idOverrideState?.overrides || {}, releaseOverrideState?.overrides || {});
  }, [activeTab, dupeChecked, builderLoading, builderSaving, builderDirty, path, idOverrideState, releaseOverrideState, builderAutoRequestKey, runDescriptionBuilder]);

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
  }, [activeTab, dupeChecked, screenshots.screenshotPlan, screenshots.screenshotsLoading, loadScreenshotPlan]);

  useEffect(() => {
    if (activeTab !== "upload_images") return;
    if (!dupeChecked) return;
    if (screenshots.screenshotPlan || screenshots.screenshotsLoading) return;
    loadScreenshotPlan();
  }, [activeTab, dupeChecked, screenshots.screenshotPlan, screenshots.screenshotsLoading, loadScreenshotPlan]);

  useEffect(() => {
    if (activeTab !== "upload_images") return;
    if (!path.trim()) return;
    const loadUploadCandidates = async () => {
      try {
        const candidates = await globalThis.go?.guiapp?.App?.ListUploadCandidates(
          path.trim(),
          normalizeOverrides(idOverrideState?.overrides || {}),
          normalizeReleaseOverrides(releaseOverrideState?.overrides || {})
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
          })
        );
        setExistingImages(previews.filter((entry): entry is ScreenshotPreviewImage => Boolean(entry)));
        await refreshUploadedImages();
      } catch (err) {
        console.error("Failed to load upload candidates:", err);
      }
    };
    loadUploadCandidates();
  }, [activeTab, path, idOverrideState, releaseOverrideState, setExistingImages, readScreenshotImage, refreshUploadedImages]);

  useEffect(() => {
    if (trackerUploadItems.length === 0) return;
    setUploadToggles((prev) => {
      const next = { ...prev };
      trackerUploadItems.forEach((item) => {
        const normalized = item.name.toLowerCase();
        if (failedDupeTrackerSet.has(normalized)) {
          next[item.name] = false;
          return;
        }
        if ((dupedTrackerSet.has(normalized) || ruleSkippedTrackerSet.has(normalized)) && !overrideRuleBlocks) {
          next[item.name] = false;
          return;
        }
        if (next[item.name] === undefined) {
          // Prioritize release page selection, then fall back to defaults
          if (releasePageTrackerSelection[item.name] !== undefined) {
            next[item.name] = releasePageTrackerSelection[item.name];
          } else {
            next[item.name] = defaultTrackerSet.has(normalized);
          }
        }
      });
      return next;
    });
  }, [trackerUploadItems, defaultTrackerSet, dupedTrackerSet, ruleSkippedTrackerSet, failedDupeTrackerSet, releasePageTrackerSelection, overrideRuleBlocks]);

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
  }, [path]);

  // NOTE: releasePageTrackerSelection is memory-only state tracking which trackers
  // the user has selected for the current release on the input page.
  // During final upload handling, use the trackers selected here combined with
  // dupe-check filtering and any special rules (e.g., banned groups).

  // Helper to get selected tracker names
  const getSelectedTrackers = () => {
    return Object.entries(releasePageTrackerSelection)
      .filter(([, selected]) => selected)
      .map(([name]) => name);
  };

  const getSelectedUploadTrackers = useCallback(() => {
    const validTrackers = new Set(trackerUploadItems.map((item) => item.name));
    return Object.entries(uploadToggles)
      .filter(([name, enabled]) => {
        if (!enabled) return false;
        if (!validTrackers.has(name)) return false;
        const normalized = name.toLowerCase().trim();
        if (!normalized) return false;
        if (dupedTrackerSet.has(normalized) && !overrideRuleBlocks) return false;
        if (ruleSkippedTrackerSet.has(normalized) && !overrideRuleBlocks) return false;
        if (failedDupeTrackerSet.has(normalized)) return false;
        return true;
      })
      .map(([name]) => name);
  }, [uploadToggles, trackerUploadItems, dupedTrackerSet, ruleSkippedTrackerSet, failedDupeTrackerSet, overrideRuleBlocks]);

  const updateTrackerQuestionnaireAnswer = useCallback((tracker: string, key: string, value: string) => {
    setTrackerQuestionnaireAnswers((prev) => {
      const trackerKey = tracker.toUpperCase().trim();
      return {
        ...prev,
        [trackerKey]: {
          ...(prev[trackerKey] || {}),
          [key]: value
        }
      };
    });
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
        (entry) => String(entry?.Tracker || "").toLowerCase().trim() === tracker.toLowerCase().trim()
      );
      const questionnaire = dryRunEntry?.Questionnaire;
      if (!questionnaire?.Fields?.length) {
        return;
      }
      const trackerAnswers = buildQuestionnaireAnswerDefaults(
        questionnaire,
        trackerQuestionnaireAnswers[tracker.toUpperCase().trim()]
      );
      questionnaire.Fields.forEach((field) => {
        if (field.Required && !String(trackerAnswers[field.Key] || "").trim()) {
          missingRequiredFields.push(`${tracker}: ${field.Label || field.Key}`);
        }
      });
    });
    if (missingRequiredFields.length > 0) {
      setTrackerUploadError(`Complete required questionnaire fields before uploading: ${missingRequiredFields.join(", ")}`);
      return;
    }
    setTrackerUploadRunning(true);
    setTrackerUploadSnapshot(null);
    try {
      const jobID = await starter(
        path.trim(),
        normalizeOverrides(idOverrideState?.overrides || {}),
        normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
        selectedTrackers,
        overrideRuleBlocks,
        ignoredDupeTrackers,
        cloneQuestionnaireAnswers(trackerQuestionnaireAnswers),
        builderPreview.Groups || [],
        runDebug,
        runLogLevel
      );
      setTrackerUploadJobID(jobID);
      if (snapshotLoader) {
        const snapshot = await snapshotLoader(jobID);
        setTrackerUploadSnapshot(snapshot);
      }
    } catch (err) {
      setTrackerUploadRunning(false);
      setTrackerUploadError(String(err));
    }
    }, [path, idOverrideState, releaseOverrideState, getSelectedUploadTrackers, overrideRuleBlocks, ignoredDupeTrackers, trackerDryRunPreview, trackerQuestionnaireAnswers, builderPreview, runDebug, runLogLevel]);

  const runTrackerDryRun = useCallback(async (descriptionGroups: DescriptionBuilderPreview["Groups"], surfaceError = true) => {
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
    try {
      const result = await fetcher(
        path.trim(),
        normalizeOverrides(idOverrideState?.overrides || {}),
        normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
        selectedTrackers,
        overrideRuleBlocks,
        ignoredDupeTrackers,
        cloneQuestionnaireAnswers(trackerQuestionnaireAnswers),
        descriptionGroups,
        runDebug,
        runLogLevel
      );
      setTrackerDryRunPreview(result || emptyTrackerDryRun);
      setTrackerQuestionnaireAnswers((prev) => {
        const next = cloneQuestionnaireAnswers(prev);
        (result?.Trackers || []).forEach((entry) => {
          const trackerKey = String(entry?.Tracker || "").toUpperCase().trim();
          if (!trackerKey) {
            return;
          }
          next[trackerKey] = buildQuestionnaireAnswerDefaults(entry.Questionnaire, next[trackerKey]);
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
  }, [path, idOverrideState, releaseOverrideState, getSelectedUploadTrackers, overrideRuleBlocks, ignoredDupeTrackers, trackerQuestionnaireAnswers, runDebug, runLogLevel]);

  const handleRunTrackerDryRun = useCallback(async () => {
    await runTrackerDryRun(builderPreview.Groups || []);
  }, [builderPreview, runTrackerDryRun]);

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
    try {
      const nextJobID = await retry(trackerUploadJobID);
      setTrackerUploadJobID(nextJobID);
      if (snapshotLoader) {
        const snapshot = await snapshotLoader(nextJobID);
        setTrackerUploadSnapshot(snapshot);
      }
    } catch (err) {
      setTrackerUploadRunning(false);
      setTrackerUploadError(String(err));
    }
  }, [trackerUploadJobID]);

  useEffect(() => {
    if (!trackerUploadJobID) {
      return;
    }

    const eventName = `${trackerUploadEventPrefix}${trackerUploadJobID}`;
    const off = EventsOn(eventName, (payload: any) => {
      if (payload?.jobID !== trackerUploadJobID) {
        return;
      }
      const snapshot = payload as TrackerUploadSnapshot;
      setTrackerUploadSnapshot(snapshot);
      const normalized = String(snapshot.status || "").toLowerCase();
      const running = normalized === "queued" || normalized === "running";
      setTrackerUploadRunning(running);
      if (normalized === "completed") {
        setTrackerUploadError("");
      } else if (normalized === "completed_with_errors" || normalized === "canceled") {
        setTrackerUploadError(snapshot.error || "Upload finished with errors.");
      }
    });

    return () => {
      if (typeof off === "function") {
        off();
      }
    };
  }, [trackerUploadJobID]);

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
      showConfigOpStatus({ type: "error", title: "Export Failed", message: "Settings export is unavailable in this build." });
      return;
    }

    setSettingsExporting(true);
    try {
      const exportedPath = await exportConfig();
      if (exportedPath?.trim()) {
        showConfigOpStatus({ type: "success", title: "Configuration Exported", message: `Saved to ${exportedPath}` });
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
      showConfigOpStatus({ type: "error", title: "Import Failed", message: "Config import is unavailable in this build." });
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

  const dupeProgressStatus = String(dupeCheckSnapshot?.status || "").toLowerCase();
  const dupeCompletedCount = Number(dupeCheckSnapshot?.completedCount || 0);
  const dupeTotalCount = Number(dupeCheckSnapshot?.totalCount || 0);


  return (
    <div className="app-shell">
      <div className="gradient-orb orb-a" />
      <div className="gradient-orb orb-b" />
      <div className="app-layout">
        <aside className="side-panel">
          <div className="side-panel__tabs">
            <button
              className={`tab-button ${activeTab === "input" ? "active" : ""}`}
              type="button"
              onClick={() => setActiveTab("input")}
            >
              Input
            </button>
            {hasTrackerData ? (
              <button
                className={`subtab-button ${activeTab === "tracker" ? "active" : ""}`}
                type="button"
                onClick={() => setActiveTab("tracker")}
              >
                Tracker Data
              </button>
            ) : null}
            {hasPreview ? (
              <button
                className={`subtab-button ${activeTab === "dupes" ? "active" : ""}`}
                type="button"
                onClick={() => setActiveTab("dupes")}
              >
                Dupe Checking
              </button>
            ) : null}
            {dupeChecked ? (
              <button
                className={`subtab-button ${activeTab === "screenshots" ? "active" : ""}`}
                type="button"
                onClick={() => setActiveTab("screenshots")}
              >
                Screenshots
              </button>
            ) : null}
            {dupeChecked ? (
              <button
                className={`subtab-button ${activeTab === "upload_images" ? "active" : ""}`}
                type="button"
                onClick={() => setActiveTab("upload_images")}
              >
                Upload Images
              </button>
            ) : null}
            {dupeChecked ? (
              <button
                className={`subtab-button ${activeTab === "description_builder" ? "active" : ""}`}
                type="button"
                onClick={() => setActiveTab("description_builder")}
              >
                Description Builder
              </button>
            ) : null}
            {builderReady ? (
              <button
                className={`subtab-button ${activeTab === "upload" ? "active" : ""}`}
                type="button"
                onClick={() => setActiveTab("upload")}
              >
                Tracker Upload
              </button>
            ) : null}
          </div>
          <div className="side-panel__footer">
            <button
              className={`settings-button ${activeTab === "settings" ? "active" : ""}`}
              type="button"
              onClick={() => setActiveTab("settings")}
            >
              <span>Settings</span>
            </button>
            <button
              className={`settings-button ${activeTab === "logging" ? "active" : ""}`}
              type="button"
              onClick={() => setActiveTab("logging")}
            >
              <span>Logging</span>
            </button>
            <button
              className={`settings-button ${activeTab === "history" ? "active" : ""}`}
              type="button"
              onClick={() => setActiveTab("history")}
            >
              <span>History</span>
            </button>
            <button
              className="settings-button settings-button--theme"
              type="button"
              onClick={handleThemeToggle}
            >
              <span className="theme-toggle">{getThemeIcon()}</span>
              <span>{getThemeLabel()}</span>
            </button>
          </div>
        </aside>

        <main className="content">
          {showPlaylistSelection ? (
            <PlaylistSelectionPage
              path={playlistSelectionPath}
              onBack={() => setShowPlaylistSelection(false)}
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
              renderImageHostingSection={renderImageHostingSection}
              renderTrackerSection={renderTrackerSection}
              renderMapSection={renderMapSection}
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
            <HistoryPage />
          ) : activeTab === "dupes" ? (
            <DupeCheckPage
              path={path}
              dupeLoading={dupeLoading}
              dupeError={dupeError}
              dupeSummary={dupeSummary}
              dupeTrackerFlags={dupeTrackerFlags}
              dupeIgnore={dupeIgnore}
              ruleSkippedTrackerSet={ruleSkippedTrackerSet}
              ruleSkipReasons={ruleSkipReasons}
              dupeProgressStatus={dupeProgressStatus}
              dupeCompletedCount={dupeCompletedCount}
              dupeTotalCount={dupeTotalCount}
              handleDupeCheck={handleDupeCheck}
              setDupeIgnore={setDupeIgnore}
            />
          ) : activeTab === "screenshots" ? (
            <ScreenshotsPage
              path={path}
              screenshotPlan={screenshots.screenshotPlan}
              screenshotsLoading={screenshots.screenshotsLoading}
              screenshotsError={screenshots.screenshotsError}
              screenshotsEnabled={screenshotsEnabled}
              setScreenshotsEnabled={setScreenshotsEnabled}
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
              uploadCandidates={screenshots.uploadCandidates}
              uploadSelections={uploadImages.uploadSelections}
              toggleUploadSelection={uploadImages.toggleUploadSelection}
              setLightboxImage={setLightboxImage}
              setLightboxAlt={setLightboxAlt}
              uploadedRecordByPath={uploadImages.uploadedRecordByPath}
              uploadedImages={uploadImages.uploadedImages}
              uploadedImageRecords={uploadImages.uploadedImageRecords}
              trackerImageLinks={screenshots.trackerImageLinks}
              handleDeleteUploadedImage={uploadImages.handleDeleteUploadedImage}
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
              builderError={builderError}
              builderSaved={builderSaved}
              setBuilderRawByGroup={setBuilderRawByGroup}
              setBuilderDirtyByGroup={setBuilderDirtyByGroup}
              setBuilderExpandedGroups={setBuilderExpandedGroups}
              resetBuilderDescription={(groupKey) =>
                resetBuilderDescription(groupKey, idOverrideState?.overrides || {}, releaseOverrideState?.overrides || {})
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
              overrideRuleBlocks={overrideRuleBlocks}
              setOverrideRuleBlocks={setOverrideRuleBlocks}
              uploadToggles={uploadToggles}
              setUploadToggles={setUploadToggles}
              namingOverrides={namingOverrides}
              preview={preview}
              formatLabel={formatLabel}
              uploadRunning={trackerUploadRunning}
              uploadError={trackerUploadError}
              uploadSnapshot={trackerUploadSnapshot}
              dryRunLoading={trackerDryRunLoading}
              dryRunError={trackerDryRunError}
              dryRunPreview={trackerDryRunPreview}
              trackerQuestionnaireAnswers={trackerQuestionnaireAnswers}
              onQuestionnaireAnswerChange={updateTrackerQuestionnaireAnswer}
              onRunDryRun={handleRunTrackerDryRun}
              onStartUpload={handleStartTrackerUpload}
              onCancelUpload={handleCancelTrackerUpload}
              onRetryFailed={handleRetryFailedTrackerUpload}
            />
          ) : activeTab === "input" ? (
            <InputPage
              path={path}
              setPath={setPath}
              sourceLookupURL={sourceLookupURL}
            setSourceLookupURL={setSourceLookupURL}
            browseAvailable={browserNativeBrowseAvailable}
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
            />
          ) : (
            <TrackerDataPage
              preview={preview}
              renderedDescriptions={renderedDescriptions}
              setRenderedDescriptions={setRenderedDescriptions}
              setLightboxImage={setLightboxImage}
              setLightboxAlt={setLightboxAlt}
            />
          )}
        </main>
        {lightboxImage ? (
          <div
            className="lightbox-overlay"
            role="dialog"
            aria-modal="true"
            onClick={() => {
              setLightboxImage("");
              setLightboxAlt("");
            }}
          >
            <div
              className={`lightbox-content ${lightboxFit ? "fit" : "native"}`}
              onClick={(event) => event.stopPropagation()}
            >
              <div className="lightbox-toolbar">
                <button
                  className="lightbox-toggle"
                  type="button"
                  onClick={() => setLightboxFit((prev) => !prev)}
                >
                  {lightboxFit ? "Actual size" : "Fit to screen"}
                </button>
              </div>
              <img src={lightboxImage} alt={lightboxAlt || "Preview"} />
            </div>
          </div>
        ) : null}
      </div>
    </div>
  );
}
