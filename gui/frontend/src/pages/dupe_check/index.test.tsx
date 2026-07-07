// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { DupeCheckResult, DupeCheckSummary, DupeCheckTrackerState } from "../../types";
import DupeCheckPage from "./index";

afterEach(() => {
  vi.unstubAllGlobals();
  cleanup();
});

const dupeSummaryFor = (trackers: string[], notes: string[] = []) =>
  ({
    SourcePath: "C:\\Media\\Example.Movie.mkv",
    Results: trackers.map((tracker) => ({
      Tracker: tracker,
      Raw: [],
      Filtered: [],
      HasDupes: false,
      ContentFail: false,
      Match: {},
      Notes: notes,
      Skipped: notes.length > 0,
      SkipReason: "",
      Status: "complete",
      Error: "",
      CheckedAt: "",
    })),
    Notes: [],
  }) as unknown as DupeCheckSummary;

const renderPage = (
  dupeSummary: DupeCheckSummary,
  options: {
    faviconOnly?: boolean;
    trackerIconSrcByName?: Record<string, string>;
    dupeTrackerStates?: DupeCheckTrackerState[];
  } = {},
) =>
  render(
    <DupeCheckPage
      path="C:\\Media\\Example.Movie.mkv"
      dupeLoading={false}
      dupeError=""
      dupeSummary={dupeSummary}
      dupeTrackerStates={options.dupeTrackerStates ?? []}
      dupeTrackerFlags={{}}
      dupeIgnore={{}}
      ruleSkippedTrackerSet={new Set()}
      ruleSkipReasons={{}}
      dupeProgressStatus=""
      dupeCompletedCount={0}
      dupeTotalCount={0}
      useFavicons={true}
      faviconOnly={options.faviconOnly ?? false}
      trackerIconSrcByName={options.trackerIconSrcByName ?? {}}
      handleDupeCheck={vi.fn()}
      setDupeIgnore={vi.fn()}
    />,
  );

const ruleSkippedResult = (tracker: string, reason: string) =>
  ({
    Tracker: tracker,
    Raw: [],
    Filtered: [],
    HasDupes: false,
    ContentFail: false,
    Match: {},
    Notes: [],
    Skipped: true,
    SkipReason: reason,
    SkipCode: "",
    SkipRules: [],
    Status: "skipped",
    Error: "",
    CheckedAt: "",
  }) as unknown as DupeCheckResult;

const completedResult = (tracker: string) =>
  ({
    ...ruleSkippedResult(tracker, ""),
    Skipped: false,
    Status: "completed",
  }) as DupeCheckResult;

const trackerStateFor = (result: DupeCheckResult): DupeCheckTrackerState => ({
  tracker: result.Tracker,
  status: result.Status,
  message: result.SkipReason || "no dupes found",
  result,
  startedAt: "",
  finishedAt: "",
});

describe("DupeCheckPage", () => {
  it("hides full tracker names in favicon-only mode when cached icons are present", () => {
    renderPage(dupeSummaryFor(["AITHER"]), {
      faviconOnly: true,
      trackerIconSrcByName: { aither: "data:image/png;base64,iVBORw0KGgo=" },
    });

    expect(screen.queryByText("AITHER")).toBeNull();
  });

  it("falls back to tracker abbreviation in favicon-only mode without cached icons", () => {
    renderPage(dupeSummaryFor(["UNCONFIGURED"]), { faviconOnly: true });

    expect(screen.queryByText("UNCONFIGURED")).toBeNull();
    expect(screen.getAllByText("UNC").length).toBeGreaterThan(0);
  });

  it("does not attempt to fetch icons from the dupe page", () => {
    const getTrackerIcon = vi.fn().mockResolvedValue("");
    vi.stubGlobal("go", { guiapp: { App: { GetTrackerIcon: getTrackerIcon } } });

    renderPage(dupeSummaryFor(["AITHER"]));

    expect(screen.getAllByText("AITHER").length).toBeGreaterThan(0);
    expect(getTrackerIcon).not.toHaveBeenCalled();
  });

  it("uses abbreviation fallback on in-client dupe rows in favicon-only mode", () => {
    renderPage(
      dupeSummaryFor(["AITHER", "BLUTOPIA"], ["pathed torrent match found; skipping dupe search"]),
      { faviconOnly: true },
    );

    expect(screen.getAllByText("In client")).toHaveLength(2);
    expect(screen.queryByText("AITHER")).toBeNull();
    expect(screen.queryByText("BLUTOPIA")).toBeNull();
    expect(screen.getAllByText("AIT").length).toBeGreaterThan(0);
    expect(screen.getAllByText("BLU").length).toBeGreaterThan(0);
  });

  it("renders each tracker favicon on combined in-client rows", () => {
    const { container } = renderPage(
      dupeSummaryFor(["AITHER, BLUTOPIA"], ["pathed torrent match found; skipping dupe search"]),
      {
        faviconOnly: true,
        trackerIconSrcByName: {
          aither: "data:image/png;base64,iVBORw0KGgo=",
          blutopia: "data:image/png;base64,iVBORw0KGgo=",
        },
      },
    );

    expect(screen.getAllByText("In client")).toHaveLength(1);
    expect(screen.getByLabelText("AITHER, BLUTOPIA")).toBeTruthy();
    expect(container.querySelectorAll("img")).toHaveLength(2);
  });

  it("counts split tracker labels on grouped rule-failure rows", () => {
    renderPage({
      SourcePath: "C:\\Media\\Example.Movie.mkv",
      Results: [
        ruleSkippedResult("AITHER, DP", "rule check failed: missing language data"),
        completedResult("ANT"),
      ],
      Notes: [],
    });

    expect(screen.getByText("Available for upload: 1")).toBeInTheDocument();
    expect(screen.getByText("2 blocked.")).toBeInTheDocument();
    expect(screen.getByText("AITHER, DP")).toBeInTheDocument();
    expect(screen.getAllByText("AIT").length).toBeGreaterThan(0);
    expect(screen.getAllByText("DP").length).toBeGreaterThan(0);
  });

  it("shows non-rule skipped reasons", () => {
    renderPage({
      SourcePath: "C:\\Media\\Example.Movie.mkv",
      Results: [
        {
          ...completedResult("NBL"),
          Status: "skipped",
          Skipped: true,
          SkipReason: "missing api_key for tracker",
          Notes: ["skip: missing api_key for tracker"],
        },
        completedResult("ANT"),
      ],
      Notes: [],
    });

    expect(screen.getByText("Available for upload: 1")).toBeInTheDocument();
    expect(screen.getByText("1 blocked.")).toBeInTheDocument();
    expect(screen.getByText("Skipped")).toBeInTheDocument();
    expect(screen.getByText("missing api_key for tracker")).toBeInTheDocument();
    expect(screen.queryByText("Rule failed")).toBeNull();
  });

  it("prefers per-tracker snapshot results over grouped rule-failure summary rows", () => {
    renderPage(
      {
        SourcePath: "C:\\Media\\Example.Movie.mkv",
        Results: [
          ruleSkippedResult(
            "AITHER, DP",
            "rule check failed: missing MediaInfo Unique ID; missing language data",
          ),
          completedResult("ANT"),
        ],
        Notes: [],
      },
      {
        dupeTrackerStates: [
          trackerStateFor(
            ruleSkippedResult("AITHER", "rule check failed: missing MediaInfo Unique ID"),
          ),
          trackerStateFor(ruleSkippedResult("DP", "rule check failed: missing language data")),
          trackerStateFor(completedResult("ANT")),
        ],
      },
    );

    expect(screen.getByText("Available for upload: 1")).toBeInTheDocument();
    expect(screen.getByText("2 blocked.")).toBeInTheDocument();
    expect(screen.queryByText("AITHER, DP")).toBeNull();
    expect(screen.getAllByText("AITHER").length).toBeGreaterThan(0);
    expect(screen.getAllByText("DP").length).toBeGreaterThan(0);
    expect(screen.getByText("rule check failed: missing MediaInfo Unique ID")).toBeInTheDocument();
    expect(screen.getByText("rule check failed: missing language data")).toBeInTheDocument();
  });
});
