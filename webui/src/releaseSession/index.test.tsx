// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { JobRegistryProvider } from "../jobRegistry";
import type { JobRegistryTransport } from "../jobRegistry";
import type {
  DescriptionBuilderPreview,
  DVDMenuCaptureResult,
  MetadataPreview,
  OwnerJobSnapshot,
  PlaylistInfo,
  ScreenshotPlan,
} from "../types";
import { emptyExternalIdentity } from "../utils/canonicalIdentity";
import {
  ReleaseSessionProvider,
  routeAccess,
  trackerWorkflowRequirements,
  useReleaseSession,
} from ".";
import type { PreparationCommand, ReleaseSessionPorts } from "./ports";

const preview = (sourcePath: string, generation: number): MetadataPreview => ({
  SourcePath: sourcePath,
  TrackerName: "AITHER",
  ReleaseName: "Example.Release.2026.1080p-GRP",
  ReleaseNameOverrides: {},
  Release: { SourcePath: sourcePath, Generation: generation },
  Identity: { ...emptyExternalIdentity(sourcePath), Generation: generation },
  Display: { ReleaseName: "Example.Release.2026.1080p-GRP", Providers: [] },
  Bluray: null,
  Diagnostics: [],
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

const dvdCaptureResult = (sourcePath: string): DVDMenuCaptureResult => ({
  SourcePath: sourcePath,
  Images: [],
  SelectedLanguage: "en",
  Region: 0,
  DiscoveredMenus: 0,
  VisitedStates: 0,
  VisitedButtons: 0,
  MaxItems: 6,
  Complete: true,
  Partial: false,
  Truncated: false,
  Warnings: [],
  Engine: {
    EngineVersion: "test",
    SchemaVersion: 1,
    SupportedFeatures: [],
    FFmpegVersion: "test",
    FFmpegDVDVideo: true,
    MissingFFmpegOptions: [],
  },
});

const completedDupeJob = (
  sourcePath: string,
  generation: number,
  trackers: readonly string[] = ["AITHER"],
): Extract<OwnerJobSnapshot, { kind: "duplicate_check" }> => ({
  kind: "duplicate_check",
  jobID: "dupe-job",
  correlationID: "dupe-correlation",
  release: { SourcePath: sourcePath, Generation: generation },
  status: "completed",
  startedAt: "2026-07-15T00:00:00Z",
  finishedAt: "2026-07-15T00:00:01Z",
  dupe: {
    jobID: "dupe-job",
    correlationID: "dupe-correlation",
    release: { SourcePath: sourcePath, Generation: generation },
    runtimeGeneration: 1,
    status: "completed",
    trackers: [],
    completedCount: trackers.length,
    totalCount: trackers.length,
    summary: {
      SourcePath: sourcePath,
      Results: [],
      Notes: [],
      Eligibility: {
        Release: { SourcePath: sourcePath, Generation: generation },
        Trackers: trackers.map((tracker) => ({ Tracker: tracker, Eligible: true, Reasons: [] })),
        EligibleTrackers: [...trackers],
      },
    },
    startedAt: "2026-07-15T00:00:00Z",
    finishedAt: "2026-07-15T00:00:01Z",
  },
});

const runningDupeJob = (
  sourcePath: string,
  generation: number,
  trackers: readonly string[] = ["AITHER"],
): Extract<OwnerJobSnapshot, { kind: "duplicate_check" }> => {
  const completed = completedDupeJob(sourcePath, generation, trackers);
  return {
    ...completed,
    status: "running",
    finishedAt: "",
    dupe: {
      ...completed.dupe,
      status: "running",
      trackers: trackers.map((tracker) => ({
        tracker,
        status: "running",
        message: "checking tracker auth",
        result: {} as never,
        startedAt: completed.startedAt,
        finishedAt: "",
      })),
      completedCount: 0,
      summary: {
        ...completed.dupe.summary,
        Eligibility: {
          Release: completed.release,
          Trackers: [],
          EligibleTrackers: [],
        },
      },
      finishedAt: "",
    },
  };
};

const createDeferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
};

const defaultJobTransport = (): JobRegistryTransport => ({
  list: async () => [],
  subscribe: () => () => undefined,
  startDupe: async () => "dupe-job",
  startUpload: async () => "upload-job",
  retryUpload: async () => "retry-job",
  cancelDupe: async () => undefined,
  cancelUpload: async () => undefined,
});

type PortOverrides = Readonly<{
  prepare?: ReleaseSessionPorts["preparation"]["execute"];
  detectDiscType?: ReleaseSessionPorts["preparation"]["detectDiscType"];
  discoverPlaylists?: ReleaseSessionPorts["preparation"]["discoverPlaylists"];
  screenshotsLoad?: ReleaseSessionPorts["screenshots"]["load"];
  screenshotsGenerate?: ReleaseSessionPorts["screenshots"]["generate"];
  screenshotsSaveFinal?: ReleaseSessionPorts["screenshots"]["saveFinal"];
  uploadedImagesListCandidates?: ReleaseSessionPorts["uploadedImages"]["listCandidates"];
  uploadedImagesListUploaded?: ReleaseSessionPorts["uploadedImages"]["listUploaded"];
  uploadedImagesUpload?: ReleaseSessionPorts["uploadedImages"]["upload"];
  descriptionsLoad?: ReleaseSessionPorts["descriptions"]["load"];
  menuCapture?: ReleaseSessionPorts["menuImages"]["capture"];
  dryRun?: ReleaseSessionPorts["upload"]["dryRun"];
  review?: ReleaseSessionPorts["upload"]["review"];
}>;

const portsFor = (overrides: PortOverrides = {}): ReleaseSessionPorts => {
  const execute = overrides.prepare ?? (async (command) => preview(command.sourcePath, 1));
  return {
    preparation: {
      detectDiscType: overrides.detectDiscType ?? (async () => ""),
      discoverPlaylists: overrides.discoverPlaylists ?? (async () => []),
      execute,
    },
    screenshots: {
      load: overrides.screenshotsLoad ?? (async (release) => screenshotPlan(release.SourcePath)),
      generate:
        overrides.screenshotsGenerate ??
        (async (release, _selections, purpose) => ({
          SourcePath: release.SourcePath,
          Purpose: purpose,
          Images: [],
          Tonemapped: false,
          UsedLibplacebo: false,
          Errors: [],
        })),
      previewFrame: async () => "data:image/png;base64,example",
      remove: async () => undefined,
      removeTrackerURL: async () => undefined,
      saveFinal: overrides.screenshotsSaveFinal ?? (async () => undefined),
      readImage: async () => "data:image/png;base64,example",
    },
    menuImages: {
      list: async () => [],
      readImage: async () => "data:image/png;base64,example",
      importPaths: async () => undefined,
      capture: overrides.menuCapture ?? (async (release) => dvdCaptureResult(release.SourcePath)),
      remove: async () => undefined,
    },
    uploadedImages: {
      listCandidates: overrides.uploadedImagesListCandidates ?? (async () => []),
      readImage: async () => "",
      listUploaded: overrides.uploadedImagesListUploaded ?? (async () => []),
      upload: overrides.uploadedImagesUpload ?? (async () => ({ Links: [], Failures: [] })),
      remove: async () => undefined,
    },
    descriptions: {
      load:
        overrides.descriptionsLoad ??
        (async (release): Promise<DescriptionBuilderPreview> => ({
          SourcePath: release.SourcePath,
          Groups: [],
          ContentFailures: [],
        })),
      render: async (raw) => raw,
      save: async (_release, groupKey, raw, trackers) => ({
        GroupKey: groupKey,
        Trackers: [...trackers],
        Description: raw,
        DescriptionHTML: raw,
        RawDescription: raw,
        RawDescriptionHTML: raw,
        HasOverride: Boolean(raw),
        ImageHost: {
          Status: "",
          Message: "",
          Warnings: [],
          Reuploaded: false,
          SelectedHost: "",
          AllowedHosts: [],
        },
      }),
    },
    upload: {
      dryRun:
        overrides.dryRun ??
        (async (command) => ({ SourcePath: command.release.SourcePath, Trackers: [] })),
      review:
        overrides.review ??
        (async (command) => ({
          Review: {
            SourcePath: command.release.SourcePath,
            Trackers: [],
            Eligibility: {
              Release: command.release,
              Trackers: [],
              EligibleTrackers: [...command.trackers],
            },
          },
          Token: "review-token",
        })),
    },
  };
};

const wrapperFor = (
  ports: ReleaseSessionPorts,
  jobTransport: JobRegistryTransport = defaultJobTransport(),
) =>
  function Wrapper({ children }: Readonly<{ children: ReactNode }>) {
    return (
      <JobRegistryProvider ownerKey="test-owner" transport={jobTransport}>
        <ReleaseSessionProvider ports={ports}>{children}</ReleaseSessionProvider>
      </JobRegistryProvider>
    );
  };

const selectAndPrepare = async (
  result: { current: ReturnType<typeof useReleaseSession> },
  sourcePath: string,
) => {
  act(() => result.current.input.updateSourceDraft(sourcePath));
  act(() => result.current.input.selectSource(sourcePath));
  act(() => result.current.upload.chooseTrackers(["AITHER"]));
  await act(() => result.current.input.prepare());
};

describe("tracker workflow capabilities", () => {
  const catalog = {
    entries: [
      {
        name: "BTN",
        family: "standalone",
        baseURL: "https://btn.example.invalid",
        uploadContentMode: "none" as const,
        fields: [],
        configured: true,
      },
      {
        name: "ANT",
        family: "standalone",
        baseURL: "https://ant.example.invalid",
        uploadContentMode: "screenshots" as const,
        fields: [],
        configured: true,
      },
      {
        name: "AITHER",
        family: "unit3d",
        baseURL: "https://aither.example.invalid",
        uploadContentMode: "description" as const,
        fields: [],
        configured: true,
      },
    ],
    unsupported: [],
  };

  it("derives none, screenshot, mixed, and conservative unknown requirements", () => {
    expect(trackerWorkflowRequirements(["BTN"], catalog)).toEqual({
      needsImages: false,
      needsDescriptions: false,
    });
    expect(trackerWorkflowRequirements(["ANT"], catalog)).toEqual({
      needsImages: true,
      needsDescriptions: false,
    });
    expect(trackerWorkflowRequirements(["BTN", "AITHER"], catalog)).toEqual({
      needsImages: true,
      needsDescriptions: true,
    });
    expect(trackerWorkflowRequirements(["UNKNOWN"], catalog)).toEqual({
      needsImages: true,
      needsDescriptions: true,
    });
  });

  it("opens only applicable pages and blocks retained content failures", () => {
    const none = routeAccess(
      true,
      false,
      true,
      trackerWorkflowRequirements(["BTN"], catalog),
      true,
      false,
      false,
    );
    expect(none.screenshots.available).toBe(false);
    expect(none.descriptions.available).toBe(false);
    expect(none.upload.available).toBe(true);

    const screenshots = routeAccess(
      true,
      false,
      true,
      trackerWorkflowRequirements(["ANT"], catalog),
      true,
      true,
      false,
    );
    expect(screenshots.screenshots.available).toBe(true);
    expect(screenshots.descriptions.available).toBe(false);
    expect(screenshots.upload.available).toBe(false);
    expect(screenshots.upload.reason).toContain("screenshot preparation");

    const description = routeAccess(
      true,
      false,
      true,
      trackerWorkflowRequirements(["AITHER"], catalog),
      false,
      false,
      true,
    );
    expect(description.screenshots.available).toBe(true);
    expect(description.descriptions.available).toBe(true);
    expect(description.upload.available).toBe(false);
    expect(description.upload.reason).toContain("description preparation");
  });
});

describe("useReleaseSession", () => {
  it("keeps manual path typing as an unselected draft", () => {
    const { result } = renderHook(useReleaseSession, { wrapper: wrapperFor(portsFor()) });

    act(() => result.current.input.updateSourceDraft("C:\\media\\Example"));

    expect(result.current.input.view.sourceDraft).toBe("C:\\media\\Example");
    expect(result.current.identity.view.sourcePath).toBe("");
    expect(result.current.identity.view.release).toBeNull();
  });

  it.each([
    "C:\\media\\Example.Release.2026.mkv",
    "C:\\media\\Example Season",
    "C:\\media\\Example DVD\\VIDEO_TS",
    "C:\\media\\Example Blu-ray",
    "C:\\media\\Example Blu-ray\\BDMV",
  ])("allows duplicate navigation for a prepared source shape: %s", async (sourcePath) => {
    const { result } = renderHook(useReleaseSession, { wrapper: wrapperFor(portsFor()) });
    act(() => result.current.input.selectSource(sourcePath));
    await act(() =>
      result.current.input.prepareSource(sourcePath, {
        ...result.current.input.view.intent,
        playlist: /Blu-ray/.test(sourcePath)
          ? { Set: true, Selected: ["00001.mpls"], UseAll: false }
          : result.current.input.view.intent.playlist,
      }),
    );

    expect(result.current.navigation.view.access.duplicates).toEqual({
      available: true,
      reason: "",
    });
  });

  it("discovers BDMV playlists and prepares with one direct playlist instruction", async () => {
    const candidates: PlaylistInfo[] = [
      { file: "00001.mpls", duration: 120, items: [], score: 1, edition: "" },
      { file: "00002.mpls", duration: 240, items: [], score: 2, edition: "" },
    ];
    const prepare = vi.fn(async (command: PreparationCommand) => {
      command.onProgress({
        correlationID: command.correlationID,
        phase: "bdinfo",
        order: 350,
        label: "Analyze Blu-ray playlists",
        message: "Blu-ray analysis complete.",
        status: "completed",
        timestamp: "2026-07-16T00:00:00Z",
      });
      return preview(command.sourcePath, 1);
    });
    const discoverPlaylists = vi.fn(async () => candidates);
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          prepare,
          detectDiscType: async () => "BDMV",
          discoverPlaylists,
        }),
      ),
    });

    act(() => result.current.input.selectSource("C:\\media\\Example Disc"));
    act(() => result.current.input.chooseTrackers(["GRP"]));
    await act(() => result.current.input.prepare());

    expect(prepare).not.toHaveBeenCalled();
    expect(discoverPlaylists).toHaveBeenCalledWith(
      "C:\\media\\Example Disc",
      expect.any(AbortSignal),
    );
    expect(result.current.input.view.playlist.required).toBe(true);
    expect(result.current.input.view.playlist.candidates.map((item) => item.file)).toEqual([
      "00002.mpls",
      "00001.mpls",
    ]);
    expect(result.current.input.view.status).toBe("awaiting_input");
    expect(result.current.input.view.playlist.status).toBe("awaiting_selection");
    expect(
      result.current.input.view.progress.steps.map((step) => [step.phase, step.status]),
    ).toEqual([
      ["disc_detection", "completed"],
      ["playlist_discovery", "completed"],
      ["playlist_selection", "awaiting_input"],
    ]);
    const correlationID = result.current.input.view.progress.correlationID;

    act(() => result.current.input.choosePlaylists(["00002.mpls"], false));
    await act(() => result.current.input.confirmPlaylists());

    expect(prepare).toHaveBeenCalledWith(
      expect.objectContaining({
        sourcePath: "C:\\media\\Example Disc",
        operation: "prepare",
        controls: { confirmBDMVRescan: false },
        intent: expect.objectContaining({
          playlist: { Set: true, Selected: ["00002.mpls"], UseAll: false },
        }),
      }),
    );
    expect(result.current.identity.view.release).toEqual({
      SourcePath: "C:\\media\\Example Disc",
      Generation: 1,
    });
    expect(result.current.input.view.progress.correlationID).toBe(correlationID);
    expect(result.current.input.view.progress.status).toBe("ready");
    expect(result.current.input.view.progress.steps).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ phase: "playlist_selection", status: "completed" }),
        expect.objectContaining({ phase: "bdinfo", status: "completed" }),
      ]),
    );
    expect(result.current.input.view.playlist).toMatchObject({
      status: "complete",
      required: false,
      selected: ["00002.mpls"],
    });
  });

  it("retries the same preparation with explicit BDMV rescan permission", async () => {
    const prepare = vi.fn(async (command: PreparationCommand) => {
      if (!command.controls.confirmBDMVRescan) {
        throw Object.assign(new Error("Confirmation required."), {
          failure: {
            Code: "confirmation_required",
            Operation: "preparation",
            Message: "Blu-ray playlist changes require confirmation before rescanning.",
            Recovery: "confirm",
          },
        });
      }
      return preview(command.sourcePath, 1);
    });
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ prepare })),
    });
    const sourcePath = "C:\\media\\Example Disc";
    const directIntent = {
      ...result.current.input.view.intent,
      playlist: { Set: true, Selected: ["00001.mpls"], UseAll: false },
    };

    act(() => result.current.input.selectSource(sourcePath));
    await act(() => result.current.input.prepareSource(sourcePath, directIntent));

    expect(result.current.input.view.failure?.Recovery).toBe("confirm");
    await act(() => result.current.input.confirmBDMVRescan());

    expect(prepare).toHaveBeenCalledTimes(2);
    expect(prepare.mock.calls.map((call) => call[0].controls)).toEqual([
      { confirmBDMVRescan: false },
      { confirmBDMVRescan: true },
    ]);
    expect(result.current.identity.view.release).toEqual({ SourcePath: sourcePath, Generation: 1 });
  });

  it("binds exact generations and invalidates dependent facets on N+1", async () => {
    let generation = 0;
    const ports = portsFor({
      prepare: async (command) => preview(command.sourcePath, ++generation),
    });
    const { result } = renderHook(useReleaseSession, { wrapper: wrapperFor(ports) });

    await selectAndPrepare(result, "C:\\media\\Example");
    expect(result.current.identity.view.release).toEqual({
      SourcePath: "C:\\media\\Example",
      Generation: 1,
    });
    const firstRevision = result.current.screenshots.view.revision;

    await act(() => result.current.input.prepare());
    expect(result.current.identity.view.release?.Generation).toBe(2);
    expect(result.current.screenshots.view.revision).toBeGreaterThan(firstRevision);
    expect(result.current.screenshots.view.staleReason).toBe("Prepared generation changed.");
  });

  it("aborts and suppresses stale preparation completion after source replacement", async () => {
    const first = createDeferred<MetadataPreview>();
    let firstSignal: AbortSignal | undefined;
    const prepare = vi.fn(async (command: PreparationCommand) => {
      if (command.sourcePath.endsWith("First")) {
        firstSignal = command.signal;
        return first.promise;
      }
      return preview(command.sourcePath, 1);
    });
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ prepare })),
    });

    act(() => result.current.input.selectSource("C:\\media\\First"));
    act(() => result.current.upload.chooseTrackers(["AITHER"]));
    let firstCommand!: Promise<boolean>;
    act(() => {
      firstCommand = result.current.input.prepare();
    });
    await waitFor(() => expect(prepare).toHaveBeenCalledTimes(1));

    act(() => result.current.input.selectSource("C:\\media\\Second"));
    expect(firstSignal?.aborted).toBe(true);
    act(() => result.current.upload.chooseTrackers(["AITHER"]));
    await act(() => result.current.input.prepare());

    first.resolve(preview("C:\\media\\First", 1));
    await act(() => firstCommand);
    expect(result.current.identity.view.sourcePath).toBe("C:\\media\\Second");
  });

  it("rejects stale progress and completion from an older same-source attempt", async () => {
    const first = createDeferred<MetadataPreview>();
    let firstCommand: PreparationCommand | null = null;
    let calls = 0;
    const execute = vi.fn(async (command: PreparationCommand) => {
      calls += 1;
      if (calls === 1) {
        firstCommand = command;
        return first.promise;
      }
      command.onProgress({
        correlationID: command.correlationID,
        phase: "preview_projection",
        order: 1200,
        label: "Build metadata preview",
        message: "Current attempt.",
        status: "completed",
        timestamp: "2026-07-16T00:00:01Z",
      });
      return preview(command.sourcePath, 2);
    });
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor({ prepare: execute })),
    });
    const sourcePath = "C:\\media\\Example";
    act(() => result.current.input.selectSource(sourcePath));

    let firstResult!: Promise<boolean>;
    act(() => {
      firstResult = result.current.input.prepare();
    });
    await waitFor(() => expect(execute).toHaveBeenCalledTimes(1));
    await act(() => result.current.input.prepare());

    const staleCommand = firstCommand as PreparationCommand | null;
    if (!staleCommand) throw new Error("first preparation command was not captured");
    staleCommand.onProgress({
      correlationID: staleCommand.correlationID,
      phase: "stale_phase",
      order: 1,
      label: "Stale phase",
      message: "Stale attempt.",
      status: "completed",
      timestamp: "2026-07-16T00:00:00Z",
    });
    first.resolve(preview(sourcePath, 1));
    await act(() => firstResult);

    expect(result.current.identity.view.release).toEqual({ SourcePath: sourcePath, Generation: 2 });
    expect(
      result.current.input.view.progress.steps.some((step) => step.phase === "stale_phase"),
    ).toBe(false);
  });

  it("carries workflow drafts across same-source generations and clears them for another source", async () => {
    const { result } = renderHook(useReleaseSession, { wrapper: wrapperFor(portsFor()) });
    await selectAndPrepare(result, "C:\\media\\Example");
    act(() => result.current.upload.chooseTrackers(["AITHER", "BLU"]));
    act(() => result.current.upload.changeOptions({ noSeed: true, runLogLevel: "debug" }));
    act(() => result.current.upload.answerQuestionnaire("AITHER", "season", "1"));

    await act(() => result.current.input.prepare());
    expect(result.current.upload.view.selectedTrackers).toEqual(["AITHER", "BLU"]);
    expect(result.current.upload.view.options.noSeed).toBe(true);
    expect(result.current.upload.view.options.runLogLevel).toBe("debug");
    expect(result.current.upload.view.questionnaireAnswers.AITHER).toEqual({ season: "1" });

    act(() => result.current.input.selectSource("C:\\media\\Other"));
    expect(result.current.upload.view.selectedTrackers).toEqual([]);
    expect(result.current.upload.view.options.noSeed).toBe(false);
    expect(result.current.upload.view.options.runLogLevel).toBe("info");
    expect(result.current.upload.view.questionnaireAnswers).toEqual({});
  });

  it("keeps independent facets concurrent and suppresses stale media completion", async () => {
    const screenshot = createDeferred<ScreenshotPlan>();
    const descriptionsLoad = vi.fn(async (release) => ({
      SourcePath: release.SourcePath,
      Groups: [],
      ContentFailures: [],
    }));
    const jobTransport: JobRegistryTransport = {
      ...defaultJobTransport(),
      list: async () => [completedDupeJob("C:\\media\\Example", 1)],
    };
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          screenshotsLoad: async () => screenshot.promise,
          descriptionsLoad,
        }),
        jobTransport,
      ),
    });
    await selectAndPrepare(result, "C:\\media\\Example");
    await waitFor(() => expect(result.current.duplicates.view.status).toBe("ready"));

    let screenshotCommand!: Promise<boolean>;
    act(() => {
      screenshotCommand = result.current.screenshots.load();
    });
    await act(() => result.current.descriptions.load());
    expect(descriptionsLoad).toHaveBeenCalledTimes(1);

    await act(() => result.current.input.prepare());
    screenshot.resolve(screenshotPlan("C:\\media\\Example"));
    await act(() => screenshotCommand);
    expect(result.current.screenshots.view.plan).toBeNull();
    expect(result.current.screenshots.view.staleReason).toBe("Prepared generation changed.");
  });

  it("persists generated final screenshots through the canonical session", async () => {
    const sourcePath = "C:\\media\\Example";
    const existing = {
      Index: 0,
      TimestampSeconds: 5,
      Path: "C:\\tmp\\existing.png",
      Purpose: "final" as const,
      Width: 1920,
      Height: 1080,
      SizeBytes: 1024,
    };
    const generated = {
      ...existing,
      Index: 1,
      TimestampSeconds: 10,
      Path: "C:\\tmp\\generated.png",
    };
    const loadedPlan: ScreenshotPlan = {
      ...screenshotPlan(sourcePath),
      SuggestedSelections: [{ Index: 1, TimestampSeconds: 10, Frame: 240, Source: "auto" }],
      FinalSelections: [existing],
    };
    const saveFinal = vi.fn(async () => undefined);
    const generate = vi.fn(async () => ({
      SourcePath: sourcePath,
      Purpose: "final" as const,
      Images: [generated],
      Tonemapped: false,
      UsedLibplacebo: false,
      Errors: [],
    }));
    const jobTransport: JobRegistryTransport = {
      ...defaultJobTransport(),
      list: async () => [completedDupeJob(sourcePath, 1)],
    };
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          screenshotsLoad: async () => loadedPlan,
          screenshotsGenerate: generate,
          screenshotsSaveFinal: saveFinal,
        }),
        jobTransport,
      ),
    });
    await selectAndPrepare(result, sourcePath);
    await waitFor(() =>
      expect(result.current.navigation.view.access.screenshots.available).toBe(true),
    );
    await act(() => result.current.screenshots.load());
    await act(() => result.current.screenshots.generate("final"));

    expect(generate).toHaveBeenCalledWith(
      { SourcePath: sourcePath, Generation: 1 },
      [{ Index: 1, TimestampSeconds: 10, Frame: 240, Source: "auto" }],
      "final",
      expect.any(AbortSignal),
    );
    expect(saveFinal).toHaveBeenCalledWith(
      { SourcePath: sourcePath, Generation: 1 },
      [existing, generated],
      expect.any(AbortSignal),
    );
    expect(result.current.screenshots.view.finalSelectionPaths).toEqual([
      existing.Path,
      generated.Path,
    ]);

    await act(() => result.current.screenshots.reorderFinal(1, 0));
    expect(saveFinal).toHaveBeenLastCalledWith(
      { SourcePath: sourcePath, Generation: 1 },
      [generated, existing],
      expect.any(AbortSignal),
    );
    expect(result.current.screenshots.view.finalSelectionPaths).toEqual([
      generated.Path,
      existing.Path,
    ]);
  });

  it("cancels DVD menu capture as an abortable session media operation", async () => {
    const capture = createDeferred<DVDMenuCaptureResult>();
    let captureSignal: AbortSignal | undefined;
    const jobTransport: JobRegistryTransport = {
      ...defaultJobTransport(),
      list: async () => [completedDupeJob("C:\\media\\Example", 1)],
    };
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          menuCapture: async (_release, signal) => {
            captureSignal = signal;
            return capture.promise;
          },
        }),
        jobTransport,
      ),
    });
    await selectAndPrepare(result, "C:\\media\\Example");
    await waitFor(() =>
      expect(result.current.navigation.view.access.menuImages.available).toBe(true),
    );

    let command!: Promise<boolean>;
    act(() => {
      command = result.current.menuImages.capture();
    });
    await waitFor(() => expect(captureSignal).toBeDefined());
    act(() => result.current.menuImages.cancelCapture());

    expect(captureSignal?.aborted).toBe(true);
    expect(result.current.menuImages.view.status).toBe("idle");
    expect(result.current.menuImages.view.staleReason).toBe("Operation canceled.");
    capture.resolve(dvdCaptureResult("C:\\media\\Example"));
    await act(() => command);
    expect(result.current.menuImages.view.capture).toBeNull();
  });

  it("does not hide dry run behind description save and marks authority stale after edits", async () => {
    const dryRun = vi.fn(async (command) => ({
      SourcePath: command.release.SourcePath,
      Trackers: [],
    }));
    const descriptionPreview: DescriptionBuilderPreview = {
      SourcePath: "C:\\media\\Example",
      ContentFailures: [],
      Groups: [
        {
          GroupKey: "unit3d",
          Trackers: ["AITHER"],
          Description: "generated",
          DescriptionHTML: "generated",
          RawDescription: "generated",
          RawDescriptionHTML: "generated",
          HasOverride: false,
          ImageHost: {
            Status: "",
            Message: "",
            Warnings: [],
            Reuploaded: false,
            SelectedHost: "",
            AllowedHosts: [],
          },
        },
      ],
    };
    const jobTransport: JobRegistryTransport = {
      ...defaultJobTransport(),
      list: async () => [completedDupeJob("C:\\media\\Example", 1)],
    };
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({ descriptionsLoad: async () => descriptionPreview, dryRun }),
        jobTransport,
      ),
    });
    await selectAndPrepare(result, "C:\\media\\Example");
    await waitFor(() => expect(result.current.duplicates.view.status).toBe("ready"));
    await act(() => result.current.descriptions.load());

    act(() => result.current.descriptions.edit("unit3d", "edited"));
    await act(() => result.current.descriptions.save("unit3d"));
    expect(dryRun).not.toHaveBeenCalled();

    await act(() => result.current.upload.runDryRun());
    expect(result.current.upload.view.dryRunStatus).toBe("ready");
    expect(dryRun).toHaveBeenCalledOnce();
    expect(dryRun.mock.calls[0]?.[0].dupeJobID).toBe("dupe-job");
    expect(dryRun.mock.calls[0]?.[0]).not.toHaveProperty("summary");
    expect(dryRun.mock.calls[0]?.[0]).not.toHaveProperty("results");
    act(() => result.current.upload.answerQuestionnaire("AITHER", "season", "2"));
    expect(result.current.upload.view.dryRun).not.toBeNull();
    expect(result.current.upload.view.dryRunStaleReason).toBe("Questionnaire answers changed.");
    expect(await result.current.upload.review()).toBe(false);

    act(() => result.current.upload.chooseTrackers(["AITHER", "BLU"]));
    expect(await result.current.upload.runDryRun()).toBe(false);
    expect(dryRun).toHaveBeenCalledOnce();
  });

  it("preserves an accepted Job start when the source session is replaced", async () => {
    const start = createDeferred<string>();
    const jobTransport: JobRegistryTransport = {
      ...defaultJobTransport(),
      list: async () => [completedDupeJob("C:\\media\\Example", 1)],
      startDupe: vi.fn(async () => start.promise),
    };
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor(), jobTransport),
    });
    await selectAndPrepare(result, "C:\\media\\Example");

    let command!: Promise<boolean>;
    act(() => {
      command = result.current.duplicates.run();
    });
    await waitFor(() => expect(jobTransport.startDupe).toHaveBeenCalledTimes(1));
    expect(result.current.duplicates.view.status).toBe("running");
    expect(result.current.duplicates.view.snapshot).toBeNull();
    act(() => result.current.input.selectSource("C:\\media\\Other"));
    start.resolve("dupe-job");

    await expect(command).resolves.toBe(true);
    expect(result.current.identity.view.sourcePath).toBe("C:\\media\\Other");
  });

  it("exposes running duplicate auth progress before terminal eligibility exists", async () => {
    const jobTransport: JobRegistryTransport = {
      ...defaultJobTransport(),
      list: async () => [runningDupeJob("C:\\media\\Example", 1)],
    };
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor(), jobTransport),
    });

    await selectAndPrepare(result, "C:\\media\\Example");
    await waitFor(() => expect(result.current.duplicates.view.status).toBe("running"));

    expect(result.current.duplicates.view.snapshot?.totalCount).toBe(1);
    expect(result.current.duplicates.view.snapshot?.trackers[0]?.message).toBe(
      "checking tracker auth",
    );
    expect(result.current.duplicates.view.eligibility?.Trackers).toEqual([]);
  });

  it("tracks selected and completed image uploads through the session facet", async () => {
    const images: Awaited<ReturnType<ReleaseSessionPorts["uploadedImages"]["listCandidates"]>> = [
      {
        Index: 0,
        TimestampSeconds: 1,
        Path: "C:\\managed\\one.png",
        Purpose: "final",
        Width: 1920,
        Height: 1080,
        SizeBytes: 1,
      },
      {
        Index: 1,
        TimestampSeconds: 2,
        Path: "C:\\managed\\two.png",
        Purpose: "final",
        Width: 1920,
        Height: 1080,
        SizeBytes: 1,
      },
    ];
    const uploadResult =
      createDeferred<Awaited<ReturnType<ReleaseSessionPorts["uploadedImages"]["upload"]>>>();
    let uploaded: Awaited<ReturnType<ReleaseSessionPorts["uploadedImages"]["listUploaded"]>> = [];
    const jobTransport: JobRegistryTransport = {
      ...defaultJobTransport(),
      list: async () => [completedDupeJob("C:\\media\\Example", 1)],
    };
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(
        portsFor({
          uploadedImagesListCandidates: async () => images,
          uploadedImagesListUploaded: async () => uploaded,
          uploadedImagesUpload: async (uploadCommand) => {
            uploadCommand.onProgress({
              correlationID: uploadCommand.correlationID,
              attemptID: "example|global",
              host: "example",
              usageScope: "global",
              trackers: ["AITHER"],
              fallback: false,
              completed: 1,
              total: 2,
              succeeded: 1,
              failed: 0,
              reused: 0,
              status: "running",
              message: "Uploading images.",
              timestamp: "2026-07-16T00:00:00Z",
            });
            const value = await uploadResult.promise;
            uploadCommand.onProgress({
              correlationID: uploadCommand.correlationID,
              attemptID: "example|global",
              host: "example",
              usageScope: "global",
              trackers: ["AITHER"],
              fallback: false,
              completed: 2,
              total: 2,
              succeeded: 2,
              failed: 0,
              reused: 0,
              status: "completed",
              message: "Host upload complete.",
              timestamp: "2026-07-16T00:00:01Z",
            });
            uploaded = value.Links;
            return value;
          },
        }),
        jobTransport,
      ),
    });
    await selectAndPrepare(result, "C:\\media\\Example");
    await waitFor(() =>
      expect(result.current.navigation.view.access.uploadedImages.available).toBe(true),
    );
    await act(() => result.current.uploadedImages.load());
    act(() => result.current.uploadedImages.chooseHost("example"));

    let command!: Promise<boolean>;
    act(() => {
      command = result.current.uploadedImages.upload();
    });
    await waitFor(() => expect(result.current.uploadedImages.view.status).toBe("running"));
    expect(result.current.uploadedImages.view.progress.attempts).toEqual([
      expect.objectContaining({ attemptID: "example|global", completed: 1, total: 2 }),
    ]);

    uploadResult.resolve({
      Links: images.map((image) => ({
        SourcePath: "C:\\media\\Example",
        ImagePath: image.Path,
        Host: "example",
        UsageScope: "global",
        ImgURL: `https://example.invalid/${image.Index}.png`,
        RawURL: `https://example.invalid/${image.Index}.png`,
        WebURL: `https://example.invalid/${image.Index}.png`,
        SizeBytes: image.SizeBytes,
        UploadedAt: "2026-07-16T00:00:00Z",
      })),
      Failures: [],
    });
    await act(() => command);

    expect(result.current.uploadedImages.view.progress.attempts).toEqual([
      expect.objectContaining({
        attemptID: "example|global",
        completed: 2,
        total: 2,
        status: "completed",
      }),
    ]);
    expect(result.current.uploadedImages.view.status).toBe("ready");
  });

  it("preserves explicit-empty tracker intent and blocks duplicate start", async () => {
    const jobTransport: JobRegistryTransport = {
      ...defaultJobTransport(),
      startDupe: vi.fn(async () => "dupe-job"),
    };
    const { result } = renderHook(useReleaseSession, {
      wrapper: wrapperFor(portsFor(), jobTransport),
    });
    act(() => result.current.input.selectSource("C:\\media\\Example"));
    await act(() => result.current.input.prepare());

    expect(result.current.navigation.view.access.duplicates.available).toBe(true);

    let started = true;
    await act(async () => {
      started = await result.current.duplicates.run();
    });
    expect(started).toBe(false);
    expect(jobTransport.startDupe).not.toHaveBeenCalled();
    expect(result.current.duplicates.view.error).toBe(
      "Select at least one tracker to run duplicate checking.",
    );

    act(() => result.current.duplicates.chooseTrackers(["AITHER"]));
    expect(result.current.input.view.preparationDirty).toBe(false);
    await act(async () => {
      started = await result.current.duplicates.run();
    });
    expect(started).toBe(true);
    expect(jobTransport.startDupe).toHaveBeenCalledWith(
      {
        release: { SourcePath: "C:\\media\\Example", Generation: 1 },
        trackers: ["AITHER"],
      },
      expect.any(String),
    );
  });
});
