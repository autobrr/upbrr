// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import TrackerUploadPage from "./index";
import type { MetadataPreview, TrackerDryRunPreview } from "../../types";

afterEach(() => {
  vi.useRealTimers();
  cleanup();
});

const preview = {
  SourcePath: "C:\\Media\\Watcher.mkv",
  TrackerName: "",
  ReleaseName: "Watcher 2160p WEB-DL DD+ 5.1-FLUX",
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
  SourcePath: "C:\\Media\\Watcher.mkv",
  Trackers: [
    {
      Tracker: "AITHER",
      Status: "ready",
      Message: "",
      ReleaseName: "Watcher.2160p.WEB-DL.DDP5.1-FLUX",
      OriginalReleaseName: "Watcher 2160p WEB-DL DD+ 5.1-FLUX",
      UploadReleaseName: "Watcher.2160p.WEB-DL.DDP5.1-FLUX",
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
      "Watcher 2160p WEB-DL DD+ 5.1-FLUX",
    );
    expect(screen.getByText(/Upload:/).textContent).toContain("Watcher.2160p.WEB-DL.DDP5.1-FLUX");
  });

  it("does not show stale dry-run naming changes for disabled trackers", () => {
    render(<TrackerUploadPage {...baseProps} uploadToggles={{ AITHER: false }} />);

    expect(screen.queryByText("Name changed")).toBeNull();
    expect(screen.queryByText(/Upload:/)).toBeNull();
  });

  it("does not lose a pending live dry-run refresh when the timer is cleaned up", () => {
    vi.useFakeTimers();
    const firstRun = vi.fn();
    const secondRun = vi.fn();
    const { rerender } = render(<TrackerUploadPage {...baseProps} onRunDryRun={firstRun} />);

    rerender(
      <TrackerUploadPage
        {...baseProps}
        namingOverrides={[["AITHER", "Watcher custom"]]}
        onRunDryRun={firstRun}
      />,
    );
    rerender(
      <TrackerUploadPage
        {...baseProps}
        namingOverrides={[["AITHER", "Watcher custom"]]}
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
        namingOverrides={[["AITHER", "Watcher custom"]]}
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
        namingOverrides={[["AITHER", "Watcher custom"]]}
        onRunDryRun={onRunDryRun}
      />,
    );
    act(() => {
      vi.advanceTimersByTime(250);
    });

    expect(onRunDryRun).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });
});
