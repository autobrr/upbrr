// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { DupeCheckSummary } from "../../types";
import DupeCheckPage from "./index";

afterEach(() => {
  vi.unstubAllGlobals();
  cleanup();
});

const dupeSummaryFor = (trackers: string[], notes: string[] = []) =>
  ({
    SourcePath: "C:\\Media\\Watcher.mkv",
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
  } = {},
) =>
  render(
    <DupeCheckPage
      path="C:\\Media\\Watcher.mkv"
      dupeLoading={false}
      dupeError=""
      dupeSummary={dupeSummary}
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
});
