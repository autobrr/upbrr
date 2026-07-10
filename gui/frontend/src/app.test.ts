// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { createElement } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import App, { hasExplicitEmptyReleaseTrackerSelection, ruleBlockingTrackerLabels } from "./app";
import type {
  DescriptionBuilderPreview,
  DVDMenuCaptureSnapshot,
  DupeCheckResult,
  DupeCheckSnapshot,
  DupeEntry,
  DupeMatch,
  HistoryEntry,
  HistoryOverview,
  MetadataPreview,
  ScreenshotPlan,
  TrackerUploadSnapshot,
} from "./types";
import { isRuntimePathCaseInsensitive, updateBrowserCSRFToken } from "./utils/runtime";
import { hasFilteredEmptyUploadTrackerSelection } from "./utils/trackerSelection";

vi.mock("../wailsjs/runtime/runtime", () => ({
  EventsOn: vi.fn(() => () => undefined),
  OnFileDrop: vi.fn(),
  OnFileDropOff: vi.fn(),
}));

const defaultPathCaseInsensitive = isRuntimePathCaseInsensitive();

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  updateBrowserCSRFToken("", defaultPathCaseInsensitive);
  delete (globalThis as typeof globalThis & { go?: any }).go;
});

type FetchMetadata = (
  sourcePath: string,
  sourceLookupURL: string,
  overrides: unknown,
  nameOverrides: unknown,
  trackers: string[],
) => Promise<MetadataPreview>;

type ResetMetadata = FetchMetadata;
type SaveConfig = (config: string) => Promise<void>;
type FetchScreenshotPlan = (sourcePath: string) => Promise<ScreenshotPlan>;
type FetchDescriptionBuilder = (
  sourcePath: string,
  overrides: unknown,
  nameOverrides: unknown,
  trackers: string[],
  ignoreDupesFor: string[],
) => Promise<DescriptionBuilderPreview>;
type FetchPreparation = (
  sourcePath: string,
  overrides: unknown,
  nameOverrides: unknown,
  trackers: string[],
  ignoreDupesFor: string[],
) => Promise<unknown>;
type DetectDiscType = (sourcePath: string) => Promise<string>;
type StartDupeCheck = (...args: unknown[]) => Promise<string>;
type GetDupeCheckSnapshot = (jobID: string) => Promise<DupeCheckSnapshot>;
type StartTrackerUpload = (...args: unknown[]) => Promise<string>;
type RetryFailedTrackerUpload = (jobID: string) => Promise<string>;
type CancelTrackerUpload = (jobID: string) => Promise<void>;
type GetTrackerUploadSnapshot = (jobID: string) => Promise<TrackerUploadSnapshot>;
type ListHistory = () => Promise<HistoryEntry[]>;
type GetHistoryOverview = (sourcePath: string) => Promise<HistoryOverview>;
type DeleteHistoryRelease = (sourcePath: string) => Promise<void>;
type StartDVDMenuCapture = (...args: unknown[]) => Promise<string>;
type GetDVDMenuCaptureSnapshot = (jobID: string) => Promise<DVDMenuCaptureSnapshot>;

const createDeferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
};

const metadataPreview = (sourcePath: string): MetadataPreview => ({
  SourcePath: sourcePath,
  TrackerName: "AITHER",
  ReleaseName: "Example.Release.2026.1080p",
  Warnings: [],
  ReleaseNameOverrides: {},
  ExternalIDs: {
    TMDBID: 1,
    IMDBID: 0,
    TVDBID: 0,
    TVmazeID: 0,
    MALID: 0,
    Category: "movie",
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
  TrackerData: [],
});

const screenshotPlan = (sourcePath: string): ScreenshotPlan => ({
  SourcePath: sourcePath,
  DiscType: "",
  DurationSeconds: 60,
  FrameRate: 24,
  SuggestedSelections: [],
  ExistingScreenshots: [],
  ExistingTrackerScreenshots: [],
  FinalSelections: [],
  TrackerImageLinks: [],
  PreviewImages: [],
  MetadataTimestamp: "",
  RequiresManualFrames: false,
});

const descriptionBuilderPreview = (sourcePath: string): DescriptionBuilderPreview => ({
  SourcePath: sourcePath,
  Groups: [],
});

const trackerUploadSnapshot = (
  jobID: string,
  status: string,
  overrides: Partial<TrackerUploadSnapshot> = {},
): TrackerUploadSnapshot => ({
  jobID,
  sourcePath: "C:\\media\\Example",
  status,
  currentTask: "",
  currentTaskStatus: status,
  currentMessage: "",
  currentCompletedPieces: 0,
  currentTotalPieces: 0,
  currentPercent: 0,
  currentHashRateMiB: 0,
  trackers: [],
  failedTrackers: [],
  uploadedCount: status === "completed" ? 1 : 0,
  error: "",
  startedAt: "2026-06-17T00:00:00Z",
  finishedAt: "",
  ...overrides,
});

const emptyDupeMatch: DupeMatch = {
  FilenameMatch: "",
  FileCountMatch: 0,
  SizeMatch: "",
  TrumpableID: "",
  MatchedID: "",
  MatchedName: "",
  MatchedLink: "",
  MatchedDownload: "",
  MatchedReason: "",
  SeasonPackExists: false,
  SeasonPackName: "",
  SeasonPackLink: "",
  SeasonPackID: "",
  SeasonPackContainsEpisode: false,
  MatchedEpisodeIDs: [],
};

const dupeEntry = (tracker: string): DupeEntry => ({
  Name: `${tracker} duplicate`,
  SizeBytes: 0,
  SizeKnown: false,
  SizeText: "",
  Files: [],
  FileCount: 0,
  Trumpable: false,
  Link: "",
  Download: "",
  Flags: [],
  ID: "",
  Type: "",
  Res: "",
  Internal: false,
  BDInfo: "",
  Description: "",
});

const dupeResult = (tracker: string, hasDupes: boolean): DupeCheckResult => ({
  Tracker: tracker,
  Raw: [],
  Filtered: hasDupes ? [dupeEntry(tracker)] : [],
  HasDupes: hasDupes,
  ContentFail: false,
  Match: emptyDupeMatch,
  Notes: [],
  Skipped: false,
  SkipReason: "",
  SkipCode: "",
  SkipRules: [],
  Status: "completed",
  Error: "",
  CheckedAt: "2026-06-17T00:00:00Z",
});

const dupeCheckSnapshot = (sourcePath = "C:\\media\\Example"): DupeCheckSnapshot => ({
  jobID: "dupe-job-1",
  sourcePath,
  status: "completed",
  trackers: [],
  completedCount: 2,
  totalCount: 2,
  summary: {
    SourcePath: sourcePath,
    Results: [dupeResult("AITHER", false), dupeResult("BLU", true)],
    Notes: [],
  },
  error: "",
  startedAt: "2026-06-17T00:00:00Z",
  finishedAt: "2026-06-17T00:00:01Z",
});

const dvdMenuCaptureSnapshot = (): DVDMenuCaptureSnapshot => ({
  jobID: "dvd-job-1",
  sourcePath: "C:\\media\\Example",
  status: "completed",
  phase: "complete",
  message: "DVD menu capture finished.",
  discoveredMenus: 1,
  visitedStates: 2,
  visitedButtons: 1,
  capturedCount: 1,
  warningCount: 0,
  result: {
    SourcePath: "C:\\media\\Example",
    Images: [],
    SelectedLanguage: "en",
    Region: 0,
    DiscoveredMenus: 1,
    VisitedStates: 2,
    VisitedButtons: 1,
    MaxItems: 6,
    Complete: true,
    Partial: false,
    Truncated: false,
    Warnings: [],
    Engine: {
      EngineVersion: "phase0a-1",
      SchemaVersion: 1,
      SupportedFeatures: [],
      FFmpegVersion: "8.0",
      FFmpegDVDVideo: true,
      MissingFFmpegOptions: [],
    },
  },
  error: "",
  startedAt: "2026-07-10T00:00:00Z",
  finishedAt: "2026-07-10T00:00:01Z",
});

const historyEntry = (sourcePath: string): HistoryEntry => ({
  SourcePath: sourcePath,
  ReleaseTitle: "Example Release",
  ReleaseSource: "WEB",
  ReleaseResolution: "1080p",
  MetadataUpdatedAt: "2026-06-17T00:00:00Z",
  LatestUploadStatus: "",
  LatestUploadAt: "",
  RuleFailureCount: 0,
});

const historyOverview = (sourcePath: string): HistoryOverview => ({
  SourcePath: sourcePath,
  ReleaseTitle: "Example Release",
  ReleaseSource: "WEB",
  ReleaseResolution: "1080p",
  MetadataUpdatedAt: "2026-06-17T00:00:00Z",
  LatestUploadStatus: "",
  LatestUploadAt: "",
  StatusLabel: "Stored",
  Metadata: {},
  ExternalIDs: metadataPreview(sourcePath).ExternalIDs,
  ExternalMetadata: {},
  ReleaseNameOverrides: {},
  DescriptionOverride: {
    SourcePath: sourcePath,
    GroupKey: "",
    Description: "",
    UpdatedAt: "",
  },
  DescriptionOverrides: [],
  PlaylistSelection: {
    SourcePath: sourcePath,
    SelectedPlaylists: [],
    UseAll: false,
    UpdatedAt: "",
  },
  TrackerMetadata: [],
  TrackerRuleFailures: [],
  Screenshots: [],
  FinalSelections: [],
  UploadedImages: [],
  UploadHistory: [],
});

const installAppBridge = (
  fetchMetadata: FetchMetadata,
  options: {
    resetMetadata?: ResetMetadata;
    saveConfig?: SaveConfig;
    fetchScreenshotPlan?: FetchScreenshotPlan;
    fetchDescriptionBuilder?: FetchDescriptionBuilder;
    fetchPreparation?: FetchPreparation;
    browseFolder?: () => Promise<string>;
    startDupeCheck?: StartDupeCheck;
    getDupeCheckSnapshot?: GetDupeCheckSnapshot;
    detectDiscType?: DetectDiscType;
    startTrackerUpload?: StartTrackerUpload;
    retryFailedTrackerUpload?: RetryFailedTrackerUpload;
    cancelTrackerUpload?: CancelTrackerUpload;
    getTrackerUploadSnapshot?: GetTrackerUploadSnapshot;
    listHistory?: ListHistory;
    getHistoryOverview?: GetHistoryOverview;
    deleteHistoryRelease?: DeleteHistoryRelease;
    startDVDMenuCapture?: StartDVDMenuCapture;
    getDVDMenuCaptureSnapshot?: GetDVDMenuCaptureSnapshot;
  } = {},
) => {
  const storage = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => {
      storage.set(key, value);
    },
    removeItem: (key: string) => {
      storage.delete(key);
    },
  });
  vi.stubGlobal("matchMedia", () => ({ matches: true }));
  (globalThis as typeof globalThis & { go?: any }).go = {
    guiapp: {
      App: {
        GetConfig: async () =>
          JSON.stringify({
            MainSettings: {
              UseFavicons: false,
            },
            Trackers: {
              DefaultTrackers: ["AITHER", "BLU"],
              Trackers: {
                AITHER: { APIKey: "configured" },
                BLU: { APIKey: "configured" },
              },
            },
            ScreenshotHandling: {
              ProcessLimit: 1,
              MaxMenuItems: 6,
            },
          }),
        BrowseFolder: options.browseFolder ?? (async () => "C:\\media\\Example\\BDMV"),
        GetDefaultConfig: async () => JSON.stringify({ Trackers: { Trackers: {} } }),
        FetchMetadata: fetchMetadata,
        DetectDiscType: options.detectDiscType,
        ResetMetadata:
          options.resetMetadata ?? (async (sourcePath: string) => metadataPreview(sourcePath)),
        SaveConfig: options.saveConfig ?? (async () => undefined),
        FetchScreenshotPlan:
          options.fetchScreenshotPlan ?? (async (sourcePath: string) => screenshotPlan(sourcePath)),
        FetchDescriptionBuilder:
          options.fetchDescriptionBuilder ??
          (async (sourcePath: string) => descriptionBuilderPreview(sourcePath)),
        FetchPreparation: options.fetchPreparation ?? (async () => ({})),
        ListUploadedImages: async () => [],
        ListDVDMenuScreenshots: async () => [],
        ReadScreenshotImage: async () => "data:image/png;base64,example",
        StartDVDMenuCapture: options.startDVDMenuCapture ?? (async () => "dvd-job-1"),
        GetDVDMenuCaptureSnapshot:
          options.getDVDMenuCaptureSnapshot ?? (async () => dvdMenuCaptureSnapshot()),
        CancelDVDMenuCapture: async () => undefined,
        DeleteDVDMenuScreenshot: async () => undefined,
        ImportMenuImages: async () => undefined,
        DiscoverPlaylists: async () => [
          {
            file: "00001.mpls",
            duration: 7200,
            size: 0,
            score: 1,
            items: [{ path: "00001.m2ts", size: 1024 }],
          },
        ],
        SavePlaylistSelection: async () => undefined,
        StartDupeCheck: options.startDupeCheck ?? (async () => "dupe-job-1"),
        GetDupeCheckSnapshot: options.getDupeCheckSnapshot ?? (async () => dupeCheckSnapshot()),
        StartTrackerUpload: options.startTrackerUpload ?? (async () => "upload-job-1"),
        RetryFailedTrackerUpload: options.retryFailedTrackerUpload ?? (async () => "upload-job-2"),
        CancelTrackerUpload: options.cancelTrackerUpload ?? (async () => undefined),
        GetTrackerUploadSnapshot:
          options.getTrackerUploadSnapshot ??
          (async (jobID: string) => trackerUploadSnapshot(jobID, "completed")),
        ListHistory: options.listHistory ?? (async () => []),
        GetHistoryOverview:
          options.getHistoryOverview ?? (async (sourcePath: string) => historyOverview(sourcePath)),
        DeleteHistoryRelease: options.deleteHistoryRelease ?? (async () => undefined),
      },
    },
  };
};

const openTrackerUploadPage = async (
  fetchMetadata: FetchMetadata,
  options: Parameters<typeof installAppBridge>[1] = {},
) => {
  installAppBridge(fetchMetadata, options);

  render(createElement(App));

  fireEvent.change(screen.getByLabelText("Source path"), {
    target: { value: "C:\\media\\Example" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
  await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));
  await screen.findByText("2/2");

  fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
  fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
  await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

  fireEvent.click(screen.getByRole("button", { name: "Description Builder" }));
  await screen.findByRole("button", { name: "Tracker Upload" });
  fireEvent.click(screen.getByRole("button", { name: "Tracker Upload" }));
  await screen.findByRole("heading", { name: "Upload Targets" });
};

describe("hasExplicitEmptyReleaseTrackerSelection", () => {
  it("does not treat pre-selection state as deselect-all", () => {
    expect(hasExplicitEmptyReleaseTrackerSelection([], {})).toBe(false);
    expect(hasExplicitEmptyReleaseTrackerSelection([{ name: "AITHER" }], {})).toBe(false);
  });

  it("treats initialized all-false tracker state as explicit empty selection", () => {
    expect(
      hasExplicitEmptyReleaseTrackerSelection([{ name: "AITHER" }, { name: "BLU" }], {
        AITHER: false,
        BLU: false,
      }),
    ).toBe(true);
  });

  it("keeps nonempty tracker selections available", () => {
    expect(
      hasExplicitEmptyReleaseTrackerSelection([{ name: "AITHER" }, { name: "BLU" }], {
        AITHER: true,
        BLU: false,
      }),
    ).toBe(false);
  });
});

describe("external ID edits", () => {
  it("sends touched equal-value IDs plus edited and cleared MAL IDs", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath, _lookup, overrides) => {
      const idOverrides = overrides as { TMDBID?: number; MALID?: number };
      const result = metadataPreview(sourcePath);
      return {
        ...result,
        ExternalIDs: {
          ...result.ExternalIDs,
          TMDBID: idOverrides.TMDBID ?? result.ExternalIDs.TMDBID,
          MALID: idOverrides.MALID ?? result.ExternalIDs.MALID,
        },
      };
    });
    installAppBridge(fetchMetadata);

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByText("Edit Release Details"));
    const tmdbInput = screen.getByLabelText("TMDB ID");
    fireEvent.change(tmdbInput, { target: { value: "2" } });
    fireEvent.change(tmdbInput, { target: { value: "1" } });
    const malInput = screen.getByLabelText("MAL ID");
    fireEvent.change(malInput, { target: { value: "5114" } });

    const refreshButton = screen.getByRole("button", { name: "Refresh metadata" });
    await waitFor(() => expect(refreshButton).not.toBeDisabled());
    fireEvent.click(refreshButton);
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(2));
    expect(fetchMetadata.mock.calls[1][2]).toEqual({ TMDBID: 1, MALID: 5114 });

    await waitFor(() => expect(screen.getByLabelText("MAL ID")).toHaveValue("5114"));
    fireEvent.change(screen.getByLabelText("MAL ID"), { target: { value: "" } });

    await waitFor(() => expect(refreshButton).not.toBeDisabled());
    fireEvent.click(refreshButton);
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(3));
    expect(fetchMetadata.mock.calls[2][2]).toEqual({ TMDBID: 1, MALID: 0 });
  });
});

describe("ruleBlockingTrackerLabels", () => {
  const labelsFor = (result: Partial<DupeCheckResult> & Pick<DupeCheckResult, "Tracker">) =>
    Array.from(
      ruleBlockingTrackerLabels({
        ...dupeResult(result.Tracker, false),
        Skipped: true,
        ...result,
      }),
    ).sort();

  it("blocks skipped trackers", () => {
    expect(labelsFor({ Tracker: "CZT" })).toEqual(["czt"]);
    expect(labelsFor({ Tracker: "HDB", SkipCode: "legacy_code" })).toEqual(["hdb"]);
  });

  it("blocks each split tracker label", () => {
    expect(labelsFor({ Tracker: "CZT, HDB", SkipCode: "legacy_code" })).toEqual([
      "czt",
      "czt, hdb",
      "hdb",
    ]);
    expect(labelsFor({ Tracker: ", CZT,, ", SkipCode: "legacy_code" })).toEqual([", czt,,", "czt"]);
  });
});

describe("hasFilteredEmptyUploadTrackerSelection", () => {
  const trackerUploadItems = [
    { name: "AITHER", config: {} },
    { name: "BLU", config: {} },
  ];

  it("detects selected input trackers filtered out of upload eligibility", () => {
    expect(
      hasFilteredEmptyUploadTrackerSelection({
        trackerUploadItems,
        releasePageTrackerSelection: {
          AITHER: false,
          BLU: true,
        },
        uploadToggles: { AITHER: true, BLU: true },
        dupedTrackerSet: new Set(["blu"]),
        skippedDupeTrackerSet: new Set(),
        ruleSkippedTrackerSet: new Set(),
        failedDupeTrackerSet: new Set(),
      }),
    ).toBe(true);
  });

  it("preserves missing-key startup and nonempty eligible selections", () => {
    expect(
      hasFilteredEmptyUploadTrackerSelection({
        trackerUploadItems,
        releasePageTrackerSelection: {},
        uploadToggles: { AITHER: true, BLU: true },
        dupedTrackerSet: new Set(),
        skippedDupeTrackerSet: new Set(),
        ruleSkippedTrackerSet: new Set(),
        failedDupeTrackerSet: new Set(),
      }),
    ).toBe(false);
    expect(
      hasFilteredEmptyUploadTrackerSelection({
        trackerUploadItems,
        releasePageTrackerSelection: {
          AITHER: true,
        },
        uploadToggles: { AITHER: true, BLU: true },
        dupedTrackerSet: new Set(),
        skippedDupeTrackerSet: new Set(),
        ruleSkippedTrackerSet: new Set(),
        failedDupeTrackerSet: new Set(),
      }),
    ).toBe(false);
  });

  it("does not treat disabled upload toggles as missing startup state", () => {
    expect(
      hasFilteredEmptyUploadTrackerSelection({
        trackerUploadItems,
        releasePageTrackerSelection: {
          AITHER: true,
          BLU: false,
        },
        uploadToggles: { AITHER: false, BLU: true },
        dupedTrackerSet: new Set(),
        skippedDupeTrackerSet: new Set(),
        ruleSkippedTrackerSet: new Set(),
        failedDupeTrackerSet: new Set(),
      }),
    ).toBe(true);
  });
});

describe("metadata tracker payloads", () => {
  it("gates Disc Menus by detected disc type", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    installAppBridge(fetchMetadata, { detectDiscType: async () => "" });

    render(createElement(App));
    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example.mkv" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledOnce());
    await screen.findByText("2/2");
    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    expect(screen.queryByRole("button", { name: "Menu Images" })).not.toBeInTheDocument();
  });

  it("ignores a fetch disc detection after the source path changes", async () => {
    const detection = createDeferred<string>();
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const detectDiscType = vi.fn<DetectDiscType>(() => detection.promise);
    installAppBridge(fetchMetadata, { detectDiscType });

    render(createElement(App));
    const sourcePath = screen.getByLabelText("Source path");
    fireEvent.change(sourcePath, { target: { value: "C:\\media\\First" } });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(detectDiscType).toHaveBeenCalledWith("C:\\media\\First"));

    fireEvent.change(sourcePath, { target: { value: "C:\\media\\Second.mkv" } });
    await act(async () => detection.resolve("BDMV"));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledOnce());
    await screen.findByText("2/2");
    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    expect(screen.queryByRole("button", { name: "Menu Images" })).not.toBeInTheDocument();
  });

  it("ignores an older browse disc detection after a newer path selection", async () => {
    const firstDetection = createDeferred<string>();
    const secondDetection = createDeferred<string>();
    const firstPath = "C:\\media\\First";
    const secondPath = "C:\\media\\Second";
    const browseFolder = vi
      .fn<() => Promise<string>>()
      .mockResolvedValueOnce(firstPath)
      .mockResolvedValueOnce(secondPath);
    const detectDiscType = vi.fn<DetectDiscType>((sourcePath) => {
      if (sourcePath === firstPath) {
        return firstDetection.promise;
      }
      if (sourcePath === secondPath) {
        return secondDetection.promise;
      }
      return Promise.resolve("");
    });
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    installAppBridge(fetchMetadata, { browseFolder, detectDiscType });

    render(createElement(App));
    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example.mkv" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledOnce());
    await screen.findByText("2/2");
    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Input" }));

    fireEvent.click(screen.getByRole("button", { name: "Browse folder" }));
    await waitFor(() => expect(detectDiscType).toHaveBeenCalledWith(firstPath));
    fireEvent.click(screen.getByRole("button", { name: "Browse folder" }));
    await waitFor(() => expect(detectDiscType).toHaveBeenCalledWith(secondPath));

    await act(async () => secondDetection.resolve(""));
    await act(async () => firstDetection.resolve("BDMV"));

    expect(screen.getByLabelText("Source path")).toHaveValue(secondPath);
    expect(screen.queryByRole("button", { name: "Menu Images" })).not.toBeInTheDocument();
  });

  it("invalidates Description Builder after DVD menu capture without auto-navigation", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const fetchDescriptionBuilder = vi.fn<FetchDescriptionBuilder>(async (sourcePath) =>
      descriptionBuilderPreview(sourcePath),
    );
    installAppBridge(fetchMetadata, {
      detectDiscType: async () => "DVD",
      fetchDescriptionBuilder,
      startDVDMenuCapture: async () => "dvd-job-1",
      getDVDMenuCaptureSnapshot: async () => dvdMenuCaptureSnapshot(),
    });

    render(createElement(App));
    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledOnce());
    await screen.findByText("2/2");
    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Description Builder" }));
    await waitFor(() => expect(fetchDescriptionBuilder).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole("button", { name: "Menu Images" }));
    fireEvent.click(screen.getByRole("button", { name: "Capture DVD menus" }));
    expect(await screen.findByText("Captured 1 DVD menu image.")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Menu Images" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Description Builder" }));
    await waitFor(() => expect(fetchDescriptionBuilder).toHaveBeenCalledTimes(2));
  });

  it("excludes dupe-blocked upload targets from metadata fetches", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    installAppBridge(fetchMetadata);

    render(createElement(App));

    const sourcePath = screen.getByLabelText("Source path");
    fireEvent.change(sourcePath, { target: { value: "C:\\media\\Example" } });

    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));

    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));
    await screen.findByText("2/2");

    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));

    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(2));
    expect(fetchMetadata.mock.calls[1][4]).toEqual(["AITHER", "BLU"]);

    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));

    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Input" }));
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));

    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(3));
    expect(fetchMetadata.mock.calls[2][4]).toEqual(["AITHER"]);
  });

  it("restores dupe-ignored trackers for description builder requests", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const fetchDescriptionBuilder = vi.fn<FetchDescriptionBuilder>(async (sourcePath) =>
      descriptionBuilderPreview(sourcePath),
    );
    installAppBridge(fetchMetadata, { fetchDescriptionBuilder });

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));
    await screen.findByText("2/2");

    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("switch", { name: "Ignore dupes for BLU" }));
    await waitFor(() => expect(screen.getByText("Available for upload: 2")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Description Builder" }));

    await waitFor(() => expect(fetchDescriptionBuilder).toHaveBeenCalledTimes(1));
    expect(fetchDescriptionBuilder.mock.calls[0][3]).toEqual(["AITHER", "BLU"]);
    expect(fetchDescriptionBuilder.mock.calls[0][4]).toEqual(["blu"]);
  });

  it("does not apply dupe blocks from a previous path to metadata fetches", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    installAppBridge(fetchMetadata);

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));
    await screen.findByText("2/2");

    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Input" }));
    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Other" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));

    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(2));
    expect(fetchMetadata.mock.calls[1][0]).toBe("C:\\media\\Other");
    expect(fetchMetadata.mock.calls[1][4]).toEqual(["AITHER", "BLU"]);
  });

  it("keeps current upload disables when stale dupe state came from another path", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    installAppBridge(fetchMetadata);

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));
    await screen.findByText("2/2");

    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Input" }));
    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Other" },
    });
    await waitFor(() =>
      expect(screen.getByLabelText("Source path")).toHaveValue("C:\\media\\Other"),
    );
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(2));
    expect(fetchMetadata.mock.calls[1][4]).toEqual(["AITHER", "BLU"]);

    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    fireEvent.click(await screen.findByRole("button", { name: "Description Builder" }));
    fireEvent.click(await screen.findByRole("button", { name: "Tracker Upload" }));
    await screen.findByRole("heading", { name: "Upload Targets" });
    const aitherUploadSwitch = screen.getByRole("switch", { name: "Enable upload for AITHER" });
    expect(aitherUploadSwitch).toHaveAttribute("aria-checked", "true");
    fireEvent.click(aitherUploadSwitch);
    await waitFor(() => expect(aitherUploadSwitch).toHaveAttribute("aria-checked", "false"));

    fireEvent.click(screen.getByRole("button", { name: "Input" }));
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));

    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(3));
    expect(fetchMetadata.mock.calls[2][0]).toBe("C:\\media\\Other");
    expect(fetchMetadata.mock.calls[2][4]).toEqual(["BLU"]);
  });

  it("keeps input tracker selections after deleting the current history release", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const deletedPath = "C:\\media\\Example";
    const listHistory = vi
      .fn<ListHistory>()
      .mockResolvedValueOnce([historyEntry(deletedPath)])
      .mockResolvedValue([]);
    const deleteHistoryRelease = vi.fn<DeleteHistoryRelease>(async () => undefined);
    const confirm = vi.fn(() => true);
    vi.stubGlobal("confirm", confirm);
    installAppBridge(fetchMetadata, {
      listHistory,
      getHistoryOverview: async (sourcePath) => historyOverview(sourcePath),
      deleteHistoryRelease,
    });

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: deletedPath },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));
    await screen.findByText("2/2");

    fireEvent.click(screen.getByRole("button", { name: "History" }));
    await screen.findByRole("button", { name: /Example Release/ });
    fireEvent.click(await screen.findByRole("button", { name: "Remove from database" }));
    await waitFor(() => expect(deleteHistoryRelease).toHaveBeenCalledWith(deletedPath));

    fireEvent.click(screen.getByRole("button", { name: "Input" }));
    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Other" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));

    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(2));
    expect(fetchMetadata.mock.calls[1][4]).toEqual(["AITHER", "BLU"]);
    await screen.findByText("2/2");
  });

  it("blocks metadata fetch when all selected trackers are filtered out", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    installAppBridge(fetchMetadata);

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));
    await screen.findByText("2/2");

    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Input" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "AITHER" }));
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));

    await waitFor(() =>
      expect(
        screen.getByText("Select at least one tracker before fetching metadata."),
      ).toBeInTheDocument(),
    );
    expect(fetchMetadata).toHaveBeenCalledTimes(1);
  });

  it("uses structured SkipRules for upload eligibility", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const fetchPreparation = vi.fn<FetchPreparation>(async () => ({}));
    installAppBridge(fetchMetadata, {
      fetchPreparation,
      getDupeCheckSnapshot: async () => ({
        ...dupeCheckSnapshot(),
        summary: {
          SourcePath: "C:\\media\\Example",
          Results: [
            dupeResult("AITHER", false),
            {
              ...dupeResult("BLU", false),
              Skipped: true,
              Status: "skipped",
              SkipRules: ["required_metadata"],
            },
          ],
          Notes: [],
        },
      }),
    });

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));
    await screen.findByText("2/2");

    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Input" }));
    fireEvent.click(screen.getByRole("button", { name: "Browse folder" }));
    fireEvent.click(await screen.findByRole("button", { name: "Confirm Selection" }));

    await waitFor(() => expect(fetchPreparation).toHaveBeenCalledTimes(1));
    expect(fetchPreparation.mock.calls[0][3]).toEqual(["AITHER"]);
  });

  it("blocks metadata reset when all selected trackers are filtered out", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const resetMetadata = vi.fn<ResetMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const confirm = vi.fn(() => true);
    installAppBridge(fetchMetadata, { resetMetadata });
    vi.stubGlobal("confirm", confirm);

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));
    await screen.findByText("2/2");

    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Input" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "AITHER" }));
    fireEvent.click(screen.getByRole("button", { name: "Reset data + refresh" }));

    await waitFor(() =>
      expect(
        screen.getByText("Select at least one tracker before resetting metadata."),
      ).toBeInTheDocument(),
    );
    expect(confirm).not.toHaveBeenCalled();
    expect(resetMetadata).not.toHaveBeenCalled();
  });

  it("skips warm metadata fetch when all selected trackers are filtered out", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const saveConfig = vi.fn<SaveConfig>(async () => undefined);
    installAppBridge(fetchMetadata, { saveConfig });

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));
    await screen.findByText("2/2");

    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Input" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "AITHER" }));
    fireEvent.click(screen.getByRole("button", { name: "Screenshots" }));
    fireEvent.click(screen.getByText("Screenshot settings"));
    fireEvent.change(screen.getByLabelText("FFmpeg concurrency"), { target: { value: "2" } });
    fireEvent.click(screen.getByRole("button", { name: "Apply settings" }));

    await waitFor(() => expect(saveConfig).toHaveBeenCalledTimes(1));
    expect(fetchMetadata).toHaveBeenCalledTimes(1);
  });

  it("uses upload-eligible trackers when preparing selected playlists", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const fetchPreparation = vi.fn<FetchPreparation>(async () => ({}));
    installAppBridge(fetchMetadata, { fetchPreparation });

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));
    await screen.findByText("2/2");

    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Input" }));
    fireEvent.click(screen.getByRole("button", { name: "Browse folder" }));

    fireEvent.click(await screen.findByRole("button", { name: "Confirm Selection" }));

    await waitFor(() => expect(fetchPreparation).toHaveBeenCalledTimes(1));
    expect(fetchPreparation.mock.calls[0][3]).toEqual(["AITHER"]);
    expect(fetchPreparation.mock.calls[0][4]).toEqual([]);
  });

  it("does not apply previous path dupe blocks when preparing selected playlists", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const fetchPreparation = vi.fn<FetchPreparation>(async () => ({}));
    installAppBridge(fetchMetadata, {
      browseFolder: async () => "C:\\media\\Other\\BDMV",
      fetchPreparation,
    });

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));
    await screen.findByText("2/2");

    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Input" }));
    fireEvent.click(screen.getByRole("button", { name: "Browse folder" }));
    fireEvent.click(await screen.findByRole("button", { name: "Confirm Selection" }));

    await waitFor(() => expect(fetchPreparation).toHaveBeenCalledTimes(1));
    expect(fetchPreparation.mock.calls[0][3]).toEqual(["AITHER", "BLU"]);
    expect(fetchPreparation.mock.calls[0][4]).toEqual([]);
  });

  it.each([
    {
      name: "root to lowercase child",
      dupeSourcePath: "C:\\media\\Example",
      browsePath: "C:\\media\\Example\\bdmv",
    },
    {
      name: "lowercase child to root",
      dupeSourcePath: "C:\\media\\Example\\bdmv",
      browsePath: "C:\\media\\Example",
      detectDiscType: async () => "BDMV",
    },
    {
      name: "trailing slash root to lowercase child",
      dupeSourcePath: "C:\\media\\Example\\",
      browsePath: "C:\\media\\Example\\bdmv\\",
    },
  ])("matches lowercase BDMV context for playlist preparation: $name", async (testCase) => {
    updateBrowserCSRFToken("", false);
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const fetchPreparation = vi.fn<FetchPreparation>(async () => ({}));
    installAppBridge(fetchMetadata, {
      browseFolder: async () => testCase.browsePath,
      getDupeCheckSnapshot: async () => dupeCheckSnapshot(testCase.dupeSourcePath),
      detectDiscType: testCase.detectDiscType,
      fetchPreparation,
    });

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: testCase.dupeSourcePath },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));
    await screen.findByText("2/2");

    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Input" }));
    fireEvent.click(screen.getByRole("button", { name: "Browse folder" }));
    fireEvent.click(await screen.findByRole("button", { name: "Confirm Selection" }));

    await waitFor(() => expect(fetchPreparation).toHaveBeenCalledTimes(1));
    expect(fetchPreparation.mock.calls[0][3]).toEqual(["AITHER"]);
    expect(fetchPreparation.mock.calls[0][4]).toEqual([]);
  });
});

describe("dupe check job tracking", () => {
  it("honors fresh upload tracker toggles when starting dupe checks", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const startDupeCheck = vi.fn<StartDupeCheck>(async () => "dupe-job-1");
    installAppBridge(fetchMetadata, { startDupeCheck });

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));
    await screen.findByText("2/2");

    const bluTrackerCheckbox = screen.getByRole("checkbox", { name: "BLU" });
    expect(bluTrackerCheckbox).toHaveAttribute("aria-checked", "true");
    fireEvent.click(bluTrackerCheckbox);
    await waitFor(() => expect(bluTrackerCheckbox).toHaveAttribute("aria-checked", "false"));

    fireEvent.click(screen.getByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(startDupeCheck).toHaveBeenCalledTimes(1));

    expect(startDupeCheck.mock.calls[0][3]).toEqual(["AITHER"]);
  });

  it("reruns dupe checks without filtering previous dupe results", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const startDupeCheck = vi.fn<StartDupeCheck>(async () => "dupe-job-1");
    installAppBridge(fetchMetadata, { startDupeCheck });

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));
    await screen.findByText("2/2");

    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(startDupeCheck).toHaveBeenCalledTimes(2));

    expect(startDupeCheck.mock.calls[1][3]).toEqual(["AITHER", "BLU"]);
  });

  it("keeps previous dupe results when job creation fails", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const startDupeCheck = vi
      .fn<StartDupeCheck>()
      .mockResolvedValueOnce("dupe-job-1")
      .mockRejectedValueOnce(new Error("dupe check requires metadata preview"));
    installAppBridge(fetchMetadata, { startDupeCheck });

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));
    await screen.findByText("2/2");

    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));

    await waitFor(() => expect(startDupeCheck).toHaveBeenCalledTimes(2));
    await screen.findByText("Fetch metadata first to cache a preview before checking dupes.");
    expect(screen.getByText("1 blocked.")).toBeInTheDocument();
  });

  it("shows metadata preview guidance when dupe job creation is blocked", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const startDupeCheck = vi.fn<StartDupeCheck>(async () => {
      throw new Error("dupe check requires metadata preview");
    });
    installAppBridge(fetchMetadata, { startDupeCheck });

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));
    await screen.findByText("2/2");

    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));

    await waitFor(() => expect(startDupeCheck).toHaveBeenCalledTimes(1));
    await screen.findByText("Fetch metadata first to cache a preview before checking dupes.");
  });

  it("shows metadata preview guidance when an existing dupe job reports a cache miss", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const getDupeCheckSnapshot = vi.fn<GetDupeCheckSnapshot>(async () => ({
      ...dupeCheckSnapshot(),
      status: "failed",
      summary: { SourcePath: "C:\\media\\Example", Results: [], Notes: [] },
      error: "core: dupe check requires metadata preview",
      finishedAt: "2026-06-17T00:00:01Z",
    }));
    installAppBridge(fetchMetadata, { getDupeCheckSnapshot });

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));
    await screen.findByText("2/2");

    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));

    await waitFor(() => expect(getDupeCheckSnapshot).toHaveBeenCalledWith("dupe-job-1"));
    await screen.findByText("Fetch metadata first to cache a preview before checking dupes.");
  });
});

describe("tracker upload job tracking", () => {
  it("keeps start upload tracking alive when bootstrap snapshot loading fails", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const startTrackerUpload = vi.fn<StartTrackerUpload>(async () => "upload-job-1");
    const getTrackerUploadSnapshot = vi
      .fn<GetTrackerUploadSnapshot>()
      .mockRejectedValueOnce(new Error("bootstrap failed"))
      .mockResolvedValueOnce(trackerUploadSnapshot("upload-job-1", "running"));
    await openTrackerUploadPage(fetchMetadata, { startTrackerUpload, getTrackerUploadSnapshot });

    fireEvent.click(screen.getByRole("button", { name: "Start Upload" }));

    await waitFor(() => expect(startTrackerUpload).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(getTrackerUploadSnapshot).toHaveBeenCalledWith("upload-job-1"));
    await waitFor(() => expect(screen.getByRole("button", { name: "Cancel" })).toBeEnabled());
    await waitFor(() => expect(getTrackerUploadSnapshot).toHaveBeenCalledTimes(2), {
      timeout: 1500,
    });
    expect(getTrackerUploadSnapshot.mock.calls[1][0]).toBe("upload-job-1");
  });

  it("preserves start upload creation failures", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const startTrackerUpload = vi.fn<StartTrackerUpload>(async () => {
      throw new Error("start failed");
    });
    const getTrackerUploadSnapshot = vi.fn<GetTrackerUploadSnapshot>();
    await openTrackerUploadPage(fetchMetadata, { startTrackerUpload, getTrackerUploadSnapshot });

    fireEvent.click(screen.getByRole("button", { name: "Start Upload" }));

    await waitFor(() => expect(screen.getByText("Error: start failed")).toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
    expect(getTrackerUploadSnapshot).not.toHaveBeenCalled();
  });

  it("keeps retry upload tracking alive when replacement bootstrap snapshot loading fails", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const startTrackerUpload = vi.fn<StartTrackerUpload>(async () => "upload-job-1");
    const retryFailedTrackerUpload = vi.fn<RetryFailedTrackerUpload>(async () => "upload-job-2");
    const getTrackerUploadSnapshot = vi
      .fn<GetTrackerUploadSnapshot>()
      .mockResolvedValueOnce(
        trackerUploadSnapshot("upload-job-1", "failed", {
          failedTrackers: ["AITHER"],
          error: "upload failed",
          finishedAt: "2026-06-17T00:00:01Z",
        }),
      )
      .mockRejectedValueOnce(new Error("retry bootstrap failed"))
      .mockResolvedValueOnce(trackerUploadSnapshot("upload-job-2", "running"));
    await openTrackerUploadPage(fetchMetadata, {
      startTrackerUpload,
      retryFailedTrackerUpload,
      getTrackerUploadSnapshot,
    });
    fireEvent.click(screen.getByRole("button", { name: "Start Upload" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Retry Failed" })).toBeEnabled());

    fireEvent.click(screen.getByRole("button", { name: "Retry Failed" }));

    await waitFor(() => expect(retryFailedTrackerUpload).toHaveBeenCalledWith("upload-job-1"));
    await waitFor(() => expect(getTrackerUploadSnapshot).toHaveBeenCalledWith("upload-job-2"));
    await waitFor(() => expect(screen.getByRole("button", { name: "Cancel" })).toBeEnabled());
    await waitFor(() => expect(getTrackerUploadSnapshot).toHaveBeenCalledTimes(3), {
      timeout: 1500,
    });
    expect(getTrackerUploadSnapshot.mock.calls[2][0]).toBe("upload-job-2");
  });

  it("preserves retry creation failures", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const startTrackerUpload = vi.fn<StartTrackerUpload>(async () => "upload-job-1");
    const retryFailedTrackerUpload = vi.fn<RetryFailedTrackerUpload>(async () => {
      throw new Error("retry failed");
    });
    const getTrackerUploadSnapshot = vi.fn<GetTrackerUploadSnapshot>().mockResolvedValueOnce(
      trackerUploadSnapshot("upload-job-1", "failed", {
        failedTrackers: ["AITHER"],
        error: "upload failed",
        finishedAt: "2026-06-17T00:00:01Z",
      }),
    );
    await openTrackerUploadPage(fetchMetadata, {
      startTrackerUpload,
      retryFailedTrackerUpload,
      getTrackerUploadSnapshot,
    });
    fireEvent.click(screen.getByRole("button", { name: "Start Upload" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Retry Failed" })).toBeEnabled());

    fireEvent.click(screen.getByRole("button", { name: "Retry Failed" }));

    await waitFor(() => expect(screen.getByText("Error: retry failed")).toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
    expect(getTrackerUploadSnapshot).toHaveBeenCalledTimes(1);
  });
});
