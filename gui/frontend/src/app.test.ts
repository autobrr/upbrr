// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { createElement } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import App, { hasExplicitEmptyReleaseTrackerSelection, ruleBlockingTrackerLabels } from "./app";
import type {
  DescriptionBuilderPreview,
  DupeCheckResult,
  DupeCheckSnapshot,
  DupeEntry,
  DupeMatch,
  MetadataPreview,
  ScreenshotImage,
  ScreenshotPlan,
  ScreenshotPurpose,
  ScreenshotResult,
  ScreenshotSelection,
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
  metadataOverrides: unknown,
  trackers: string[],
) => Promise<MetadataPreview>;

type ResetMetadata = FetchMetadata;
type SaveConfig = (config: string) => Promise<void>;
type FetchScreenshotPlan = (
  sourcePath: string,
  overrides: unknown,
  nameOverrides: unknown,
  metadataOverrides: unknown,
) => Promise<ScreenshotPlan>;
type SaveFinalScreenshotSelections = (
  sourcePath: string,
  overrides: unknown,
  nameOverrides: unknown,
  metadataOverrides: unknown,
  images: ScreenshotImage[],
) => Promise<void>;
type GenerateScreenshots = (
  sourcePath: string,
  overrides: unknown,
  nameOverrides: unknown,
  metadataOverrides: unknown,
  selections: ScreenshotSelection[],
  purpose: ScreenshotPurpose,
) => Promise<ScreenshotResult>;
type PreviewScreenshotFrame = (
  sourcePath: string,
  overrides: unknown,
  nameOverrides: unknown,
  metadataOverrides: unknown,
  timestampSeconds: number,
) => Promise<string>;
type ListUploadCandidates = (
  sourcePath: string,
  overrides: unknown,
  nameOverrides: unknown,
  metadataOverrides: unknown,
) => Promise<ScreenshotImage[]>;
type ReadScreenshotImage = (imagePath: string) => Promise<string>;
type FetchDescriptionBuilder = (
  sourcePath: string,
  overrides: unknown,
  nameOverrides: unknown,
  metadataOverrides: unknown,
  trackers: string[],
  ignoreDupesFor: string[],
) => Promise<DescriptionBuilderPreview>;
type FetchPreparation = (
  sourcePath: string,
  overrides: unknown,
  nameOverrides: unknown,
  metadataOverrides: unknown,
  trackers: string[],
  ignoreDupesFor: string[],
) => Promise<unknown>;
type DetectDiscType = (sourcePath: string) => Promise<string>;
type StartTrackerUpload = (...args: unknown[]) => Promise<string>;
type RetryFailedTrackerUpload = (jobID: string) => Promise<string>;
type CancelTrackerUpload = (jobID: string) => Promise<void>;
type GetTrackerUploadSnapshot = (jobID: string) => Promise<TrackerUploadSnapshot>;

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
    Category: "movie",
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

const installAppBridge = (
  fetchMetadata: FetchMetadata,
  options: {
    resetMetadata?: ResetMetadata;
    saveConfig?: SaveConfig;
    fetchScreenshotPlan?: FetchScreenshotPlan;
    saveFinalScreenshotSelections?: SaveFinalScreenshotSelections;
    generateScreenshots?: GenerateScreenshots;
    previewScreenshotFrame?: PreviewScreenshotFrame;
    listUploadCandidates?: ListUploadCandidates;
    readScreenshotImage?: ReadScreenshotImage;
    fetchDescriptionBuilder?: FetchDescriptionBuilder;
    fetchPreparation?: FetchPreparation;
    browseFolder?: () => Promise<string>;
    getDupeCheckSnapshot?: () => Promise<DupeCheckSnapshot>;
    detectDiscType?: DetectDiscType;
    startTrackerUpload?: StartTrackerUpload;
    retryFailedTrackerUpload?: RetryFailedTrackerUpload;
    cancelTrackerUpload?: CancelTrackerUpload;
    getTrackerUploadSnapshot?: GetTrackerUploadSnapshot;
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
        GenerateScreenshots:
          options.generateScreenshots ??
          (async (
            sourcePath: string,
            _overrides: unknown,
            _nameOverrides: unknown,
            _metadataOverrides: unknown,
            _selections: ScreenshotSelection[],
            purpose: ScreenshotPurpose,
          ) => ({
            SourcePath: sourcePath,
            Purpose: purpose,
            Images: [],
            Tonemapped: false,
            UsedLibplacebo: false,
            Errors: [],
          })),
        PreviewScreenshotFrame:
          options.previewScreenshotFrame ?? (async () => "data:image/png;base64,PREVIEW=="),
        SaveFinalScreenshotSelections:
          options.saveFinalScreenshotSelections ?? (async () => undefined),
        ReadScreenshotImage: options.readScreenshotImage ?? (async () => "data:image/png;base64,"),
        ListUploadCandidates: options.listUploadCandidates ?? (async () => []),
        FetchDescriptionBuilder:
          options.fetchDescriptionBuilder ??
          (async (sourcePath: string) => descriptionBuilderPreview(sourcePath)),
        FetchPreparation: options.fetchPreparation ?? (async () => ({})),
        ListUploadedImages: async () => [],
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
        StartDupeCheck: async () => "dupe-job-1",
        GetDupeCheckSnapshot: options.getDupeCheckSnapshot ?? (async () => dupeCheckSnapshot()),
        StartTrackerUpload: options.startTrackerUpload ?? (async () => "upload-job-1"),
        RetryFailedTrackerUpload: options.retryFailedTrackerUpload ?? (async () => "upload-job-2"),
        CancelTrackerUpload: options.cancelTrackerUpload ?? (async () => undefined),
        GetTrackerUploadSnapshot:
          options.getTrackerUploadSnapshot ??
          (async (jobID: string) => trackerUploadSnapshot(jobID, "completed")),
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
        ruleSkippedTrackerSet: new Set(),
        failedDupeTrackerSet: new Set(),
      }),
    ).toBe(true);
  });
});

describe("metadata tracker payloads", () => {
  it("sends metadata overrides from edit controls", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    installAppBridge(fetchMetadata);

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByText("Edit Release Details"));
    fireEvent.change(screen.getByLabelText("Distributor"), {
      target: { value: "Criterion" },
    });
    fireEvent.change(screen.getByLabelText("Original language"), {
      target: { value: "ja" },
    });
    fireEvent.change(screen.getByLabelText("Anime"), {
      target: { value: "false" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));

    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(2));
    expect(fetchMetadata.mock.calls[1][4]).toEqual({
      Distributor: "Criterion",
      OriginalLanguage: "ja",
      Anime: false,
    });
  });

  it("omits cleared metadata text overrides from edit controls", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    installAppBridge(fetchMetadata);

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByText("Edit Release Details"));
    fireEvent.change(screen.getByLabelText("Distributor"), {
      target: { value: "   " },
    });
    fireEvent.change(screen.getByLabelText("Original language"), {
      target: { value: "   " },
    });
    fireEvent.change(screen.getByLabelText("Anime"), {
      target: { value: "false" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));

    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(2));
    expect(fetchMetadata.mock.calls[1][4]).toEqual({
      Anime: false,
    });
  });

  it("saves screenshot selections with the overrides used to load the plan", async () => {
    const existingImage: ScreenshotImage = {
      Index: 0,
      TimestampSeconds: 10,
      Path: "C:\\media\\Example\\screen-001.png",
      Width: 1920,
      Height: 1080,
      SizeBytes: 1024,
    };
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const fetchScreenshotPlan = vi.fn<FetchScreenshotPlan>(async (sourcePath) => ({
      ...screenshotPlan(sourcePath),
      ExistingScreenshots: [existingImage],
    }));
    const saveFinalScreenshotSelections = vi.fn<SaveFinalScreenshotSelections>(
      async () => undefined,
    );
    const readScreenshotImage = vi.fn<ReadScreenshotImage>(
      async () => "data:image/png;base64,AA==",
    );
    installAppBridge(fetchMetadata, {
      fetchScreenshotPlan,
      saveFinalScreenshotSelections,
      readScreenshotImage,
    });

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByText("Edit Release Details"));
    fireEvent.change(screen.getByLabelText("TMDB ID"), { target: { value: "12345" } });
    fireEvent.change(screen.getByLabelText("Category"), { target: { value: "movie" } });
    fireEvent.change(screen.getByLabelText("Distributor"), {
      target: { value: "Loaded Distributor" },
    });

    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Screenshots" }));

    await waitFor(() => expect(fetchScreenshotPlan).toHaveBeenCalledTimes(1));
    expect(fetchScreenshotPlan.mock.calls[0][1]).toEqual({ TMDBID: 12345 });
    expect(fetchScreenshotPlan.mock.calls[0][2]).toEqual({ Category: "movie" });
    expect(fetchScreenshotPlan.mock.calls[0][3]).toEqual({
      Distributor: "Loaded Distributor",
    });
    await screen.findByRole("button", { name: "Add to final" });

    fireEvent.click(screen.getByRole("button", { name: "Input" }));
    fireEvent.click(screen.getByText("Edit Release Details"));
    fireEvent.change(screen.getByLabelText("TMDB ID"), { target: { value: "67890" } });
    fireEvent.change(screen.getByLabelText("Category"), { target: { value: "tv" } });
    fireEvent.change(screen.getByLabelText("Distributor"), {
      target: { value: "Drifted Distributor" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Screenshots" }));
    fireEvent.click(await screen.findByRole("button", { name: "Add to final" }));

    await waitFor(() => expect(saveFinalScreenshotSelections).toHaveBeenCalledTimes(1));
    expect(saveFinalScreenshotSelections.mock.calls[0][1]).toEqual({ TMDBID: 12345 });
    expect(saveFinalScreenshotSelections.mock.calls[0][2]).toEqual({ Category: "movie" });
    expect(saveFinalScreenshotSelections.mock.calls[0][3]).toEqual({
      Distributor: "Loaded Distributor",
    });
  });

  it("previews and captures screenshots with the overrides used to load the plan", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    const fetchScreenshotPlan = vi.fn<FetchScreenshotPlan>(async (sourcePath) => ({
      ...screenshotPlan(sourcePath),
      SuggestedSelections: [
        {
          Index: 0,
          TimestampSeconds: 10,
          Frame: 240,
          Source: "auto",
        },
      ],
    }));
    const previewScreenshotFrame = vi.fn<PreviewScreenshotFrame>(
      async () => "data:image/png;base64,PREVIEW==",
    );
    const generateScreenshots = vi.fn<GenerateScreenshots>(
      async (sourcePath, _overrides, _nameOverrides, _metadataOverrides, _selections, purpose) => ({
        SourcePath: sourcePath,
        Purpose: purpose,
        Images: [],
        Tonemapped: false,
        UsedLibplacebo: false,
        Errors: [],
      }),
    );
    const listUploadCandidates = vi.fn<ListUploadCandidates>(async () => []);
    installAppBridge(fetchMetadata, {
      fetchScreenshotPlan,
      previewScreenshotFrame,
      generateScreenshots,
      listUploadCandidates,
    });

    render(createElement(App));

    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Example" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByText("Edit Release Details"));
    fireEvent.change(screen.getByLabelText("TMDB ID"), { target: { value: "12345" } });
    fireEvent.change(screen.getByLabelText("Category"), { target: { value: "movie" } });
    fireEvent.change(screen.getByLabelText("Distributor"), {
      target: { value: "Loaded Distributor" },
    });

    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Screenshots" }));
    await waitFor(() => expect(fetchScreenshotPlan).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByRole("button", { name: "Input" }));
    fireEvent.click(screen.getByText("Edit Release Details"));
    fireEvent.change(screen.getByLabelText("TMDB ID"), { target: { value: "67890" } });
    fireEvent.change(screen.getByLabelText("Category"), { target: { value: "tv" } });
    fireEvent.change(screen.getByLabelText("Distributor"), {
      target: { value: "Drifted Distributor" },
    });

    fireEvent.click(screen.getByRole("button", { name: "Screenshots" }));
    fireEvent.click(await screen.findByRole("button", { name: "Run preview" }));
    await waitFor(() => expect(previewScreenshotFrame).toHaveBeenCalledTimes(1));
    expect(previewScreenshotFrame.mock.calls[0][1]).toEqual({ TMDBID: 12345 });
    expect(previewScreenshotFrame.mock.calls[0][2]).toEqual({ Category: "movie" });
    expect(previewScreenshotFrame.mock.calls[0][3]).toEqual({
      Distributor: "Loaded Distributor",
    });

    fireEvent.click(screen.getByRole("button", { name: "Generate screenshots" }));
    await waitFor(() => expect(generateScreenshots).toHaveBeenCalledTimes(1));
    expect(generateScreenshots.mock.calls[0][1]).toEqual({ TMDBID: 12345 });
    expect(generateScreenshots.mock.calls[0][2]).toEqual({ Category: "movie" });
    expect(generateScreenshots.mock.calls[0][3]).toEqual({
      Distributor: "Loaded Distributor",
    });

    fireEvent.click(screen.getByRole("button", { name: "Upload Images" }));
    await waitFor(() => expect(listUploadCandidates).toHaveBeenCalledTimes(1));
    expect(listUploadCandidates.mock.calls[0][1]).toEqual({ TMDBID: 12345 });
    expect(listUploadCandidates.mock.calls[0][2]).toEqual({ Category: "movie" });
    expect(listUploadCandidates.mock.calls[0][3]).toEqual({
      Distributor: "Loaded Distributor",
    });
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
    expect(fetchMetadata.mock.calls[1][5]).toEqual(["AITHER", "BLU"]);

    fireEvent.click(await screen.findByRole("button", { name: "Dupe Checking" }));
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));

    await waitFor(() => expect(screen.getByText("1 blocked.")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Input" }));
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));

    await waitFor(() => expect(fetchMetadata).toHaveBeenCalledTimes(3));
    expect(fetchMetadata.mock.calls[2][5]).toEqual(["AITHER"]);
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
    expect(fetchMetadata.mock.calls[1][5]).toEqual(["AITHER", "BLU"]);
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
    expect(fetchMetadata.mock.calls[1][5]).toEqual(["AITHER", "BLU"]);

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
    expect(fetchMetadata.mock.calls[2][5]).toEqual(["BLU"]);
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
    expect(fetchPreparation.mock.calls[0][4]).toEqual(["AITHER"]);
    expect(fetchPreparation.mock.calls[0][5]).toEqual([]);
  });

  it("sends edited overrides when preparing selected playlists", async () => {
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

    fireEvent.click(screen.getByText("Edit Release Details"));
    fireEvent.change(screen.getByLabelText("TMDB ID"), { target: { value: "12345" } });
    fireEvent.change(screen.getByLabelText("Source"), { target: { value: "BluRay" } });
    fireEvent.change(screen.getByLabelText("Distributor"), { target: { value: "Criterion" } });
    fireEvent.change(screen.getByLabelText("Anime"), { target: { value: "false" } });
    fireEvent.click(screen.getByRole("button", { name: "Browse folder" }));
    fireEvent.click(await screen.findByRole("button", { name: "Confirm Selection" }));

    await waitFor(() => expect(fetchPreparation).toHaveBeenCalledTimes(1));
    expect(fetchPreparation.mock.calls[0][1]).toEqual({ TMDBID: 12345 });
    expect(fetchPreparation.mock.calls[0][2]).toEqual({ Source: "BluRay" });
    expect(fetchPreparation.mock.calls[0][3]).toEqual({
      Distributor: "Criterion",
      Anime: false,
    });
    expect(fetchPreparation.mock.calls[0][4]).toEqual(["AITHER", "BLU"]);
    expect(fetchPreparation.mock.calls[0][5]).toEqual([]);
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
    expect(fetchPreparation.mock.calls[0][4]).toEqual(["AITHER", "BLU"]);
    expect(fetchPreparation.mock.calls[0][5]).toEqual([]);
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
    expect(fetchPreparation.mock.calls[0][4]).toEqual(["AITHER"]);
    expect(fetchPreparation.mock.calls[0][5]).toEqual([]);
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

  it("clears previous upload job state before a failed replacement start resolves", async () => {
    const fetchMetadata = vi.fn<FetchMetadata>(async (sourcePath) => metadataPreview(sourcePath));
    let rejectSecondStart: (reason: Error) => void = () => undefined;
    const startTrackerUpload = vi
      .fn<StartTrackerUpload>()
      .mockResolvedValueOnce("upload-job-1")
      .mockImplementationOnce(
        () =>
          new Promise<string>((_resolve, reject) => {
            rejectSecondStart = reject;
          }),
      );
    const getTrackerUploadSnapshot = vi.fn<GetTrackerUploadSnapshot>().mockResolvedValueOnce(
      trackerUploadSnapshot("upload-job-1", "failed", {
        failedTrackers: ["AITHER"],
        error: "upload failed",
        finishedAt: "2026-06-17T00:00:01Z",
      }),
    );
    await openTrackerUploadPage(fetchMetadata, { startTrackerUpload, getTrackerUploadSnapshot });
    fireEvent.click(screen.getByRole("button", { name: "Start Upload" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Retry Failed" })).toBeEnabled());

    vi.useFakeTimers();
    fireEvent.click(screen.getByRole("button", { name: "Start Upload" }));

    expect(startTrackerUpload).toHaveBeenCalledTimes(2);
    await act(async () => {
      vi.advanceTimersByTime(1100);
      await Promise.resolve();
    });
    expect(getTrackerUploadSnapshot).toHaveBeenCalledTimes(1);
    await act(async () => {
      rejectSecondStart(new Error("start failed"));
    });
    expect(screen.getByText("Error: start failed")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Retry Failed" })).toBeDisabled();
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
