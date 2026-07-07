// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import TrackerUploadPage from "./index";
import type { MetadataPreview, TrackerDryRunPreview } from "../../types";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  cleanup();
});

const preview = {
  SourcePath: "C:\\Media\\Example.Movie.mkv",
  TrackerName: "",
  ReleaseName: "Example Movie 2160p WEB-DL DD+ 5.1-GRP",
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
  TrackerData: [],
} satisfies MetadataPreview;

const dryRunPreview: TrackerDryRunPreview = {
  SourcePath: "C:\\Media\\Example.Movie.mkv",
  Trackers: [
    {
      Tracker: "AITHER",
      Status: "ready",
      Message: "",
      ReleaseName: "Example.Movie.2160p.WEB-DL.DDP5.1-GRP",
      OriginalReleaseName: "Example Movie 2160p WEB-DL DD+ 5.1-GRP",
      UploadReleaseName: "Example.Movie.2160p.WEB-DL.DDP5.1-GRP",
      ReleaseNameChanged: true,
      ReleaseNameChangeReason: "tracker naming rules",
      DescriptionGroup: "",
      Description: "",
      Endpoint: "",
      Payload: {},
      Files: [],
      ImageHost: {
        Status: "",
        SelectedHost: "",
        AllowedHosts: [],
        Warnings: [],
        Reuploaded: false,
        Message: "",
      },
    },
  ],
};

describe("TrackerUploadPage", () => {
  const baseProps = {
    trackerUploadItems: [{ name: "AITHER", config: {} }],
    releasePageTrackerSelection: { AITHER: true },
    dupedTrackerSet: new Set<string>(),
    ruleSkipReasons: {},
    ruleSkippedTrackerSet: new Set<string>(),
    failedDupeTrackerSet: new Set<string>(),
    uploadToggles: { AITHER: true },
    setUploadToggles: vi.fn(),
    skipClientInjection: false,
    setSkipClientInjection: vi.fn(),
    namingOverrides: [],
    preview,
    formatLabel: (value: string) => value,
    uploadRunning: false,
    uploadError: "",
    uploadSnapshot: null,
    dryRunLoading: false,
    dryRunError: "",
    dryRunProgress: null,
    dryRunPreview,
    trackerQuestionnaireAnswers: {},
    trackerIconSrcByName: {},
    onQuestionnaireAnswerChange: vi.fn(),
    onRunDryRun: vi.fn(),
    onStartUpload: vi.fn(),
    onCancelUpload: vi.fn(),
    onRetryFailed: vi.fn(),
  };

  it("shows tracker-specific dry-run naming changes on the tracker tile", () => {
    render(<TrackerUploadPage {...baseProps} />);

    expect(screen.getByText("Name changed")).toBeTruthy();
    expect(screen.getByText(/Original:/).textContent).toContain(
      "Example Movie 2160p WEB-DL DD+ 5.1-GRP",
    );
    expect(screen.getByText(/Upload:/).textContent).toContain(
      "Example.Movie.2160p.WEB-DL.DDP5.1-GRP",
    );
  });

  it("does not show stale dry-run naming changes for disabled trackers", () => {
    render(<TrackerUploadPage {...baseProps} uploadToggles={{ AITHER: false }} />);

    expect(screen.queryByText("Name changed")).toBeNull();
    expect(screen.queryByText(/Upload:/)).toBeNull();
  });

  it("renders cached tracker icons without fetching from the page", () => {
    const getTrackerIcon = vi.fn().mockResolvedValue("");
    vi.stubGlobal("go", { guiapp: { App: { GetTrackerIcon: getTrackerIcon } } });

    const { container } = render(
      <TrackerUploadPage
        {...baseProps}
        trackerIconSrcByName={{ aither: "data:image/png;base64,iVBORw0KGgo=" }}
      />,
    );

    expect(container.querySelector("img")).toBeInstanceOf(HTMLImageElement);
    expect(getTrackerIcon).not.toHaveBeenCalled();
  });

  it("uses abbreviation fallback for blocked tracker icons without cached icons", () => {
    render(<TrackerUploadPage {...baseProps} failedDupeTrackerSet={new Set(["aither"])} />);

    expect(screen.getByText("AIT")).toBeTruthy();
  });

  it("hides full tracker names when favicon-only mode is enabled", () => {
    render(<TrackerUploadPage {...baseProps} faviconOnly={true} useFavicons={true} />);

    expect(screen.queryByText("AITHER")).toBeNull();
    expect(screen.getAllByText("AIT").length).toBeGreaterThan(0);
  });

  it("does not lose a pending live dry-run refresh when the timer is cleaned up", () => {
    vi.useFakeTimers();
    const firstRun = vi.fn();
    const secondRun = vi.fn();
    const { rerender } = render(<TrackerUploadPage {...baseProps} onRunDryRun={firstRun} />);

    rerender(
      <TrackerUploadPage
        {...baseProps}
        namingOverrides={[["AITHER", "Example custom"]]}
        onRunDryRun={firstRun}
      />,
    );
    rerender(
      <TrackerUploadPage
        {...baseProps}
        namingOverrides={[["AITHER", "Example custom"]]}
        onRunDryRun={secondRun}
      />,
    );

    act(() => {
      vi.advanceTimersByTime(250);
    });

    expect(firstRun).not.toHaveBeenCalled();
    expect(secondRun).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });

  it("defers live dry-run refresh while dry-run or upload work is running", () => {
    vi.useFakeTimers();
    const onRunDryRun = vi.fn();
    const { rerender } = render(<TrackerUploadPage {...baseProps} onRunDryRun={onRunDryRun} />);

    rerender(
      <TrackerUploadPage
        {...baseProps}
        dryRunLoading={true}
        namingOverrides={[["AITHER", "Example custom"]]}
        onRunDryRun={onRunDryRun}
      />,
    );
    act(() => {
      vi.advanceTimersByTime(500);
    });
    expect(onRunDryRun).not.toHaveBeenCalled();

    rerender(
      <TrackerUploadPage
        {...baseProps}
        namingOverrides={[["AITHER", "Example custom"]]}
        onRunDryRun={onRunDryRun}
      />,
    );
    act(() => {
      vi.advanceTimersByTime(250);
    });

    expect(onRunDryRun).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });

  it("redacts debug dry-run endpoints, payloads, and local file paths", () => {
    render(
      <TrackerUploadPage
        {...baseProps}
        dryRunPreview={{
          ...dryRunPreview,
          Trackers: [
            {
              ...dryRunPreview.Trackers[0],
              DebugSections: [
                {
                  Title: "Tracker request",
                  Endpoint:
                    "https://tracker.example/upload?api_key=debug-value&name=Example.Release.2026.1080p-GRP",
                  Files: [
                    {
                      Field: "torrent",
                      Path: "C:\\path\\to\\Example.Release.2026.1080p-GRP.torrent",
                      Present: true,
                    },
                  ],
                  Payload: {
                    api_key: "debug-value",
                    description: "line 1\nline 2",
                    name: "Example.Release.2026.1080p-GRP",
                  },
                },
              ],
            },
          ],
        }}
      />,
    );

    expect(
      screen.getByText(
        "https://tracker.example/upload?api_key=[REDACTED]&name=Example.Release.2026.1080p-GRP",
      ),
    ).toBeTruthy();
    expect(screen.getByText("[local path]")).toBeTruthy();
    expect(screen.getByText("[13 bytes, 2 lines omitted]")).toBeTruthy();
    expect(screen.getAllByText("[REDACTED]").length).toBeGreaterThanOrEqual(1);
  });

  it("renders blocked debug dry-run top-level payload and files", () => {
    render(
      <TrackerUploadPage
        {...baseProps}
        dryRunPreview={{
          ...dryRunPreview,
          Trackers: [
            {
              ...dryRunPreview.Trackers[0],
              Status: "blocked",
              Message: "Manual confirmation required",
              Payload: {
                upload_name: "top-level-payload",
              },
              Files: [
                {
                  Field: "torrent",
                  Path: ".upbrr/tmp/top-level.torrent",
                  Present: true,
                },
              ],
              DebugSections: [
                {
                  Title: "Debug payload",
                  Endpoint: "",
                  Files: [
                    {
                      Field: "torrent",
                      Path: ".upbrr/tmp/debug.torrent",
                      Present: true,
                    },
                  ],
                  Payload: {
                    upload_name: "debug-payload",
                  },
                },
              ],
            },
          ],
        }}
      />,
    );

    expect(screen.getByText("blocked")).toBeTruthy();
    expect(screen.getByText("Manual confirmation required")).toBeTruthy();
    expect(screen.getByText("top-level-payload")).toBeTruthy();
    expect(screen.getByText(".upbrr/tmp/top-level.torrent")).toBeTruthy();
    expect(screen.getByText("debug-payload")).toBeTruthy();
    expect(screen.getByText(".upbrr/tmp/debug.torrent")).toBeTruthy();
  });
});
