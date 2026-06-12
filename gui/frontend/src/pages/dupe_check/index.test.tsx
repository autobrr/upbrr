// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { DupeCheckSummary } from "../../types";
import DupeCheckPage from "./index";

afterEach(() => {
  vi.unstubAllGlobals();
  cleanup();
});

describe("DupeCheckPage", () => {
  it("hides tracker names when favicon-only mode is enabled", () => {
    const dupeSummary = {
      SourcePath: "C:\\Media\\Watcher.mkv",
      Results: [
        {
          Tracker: "AITHER",
          Raw: [],
          Filtered: [],
          HasDupes: false,
          ContentFail: false,
          Match: {},
          Notes: [],
          Skipped: false,
          SkipReason: "",
          Status: "complete",
          Error: "",
          CheckedAt: "",
        },
      ],
      Notes: [],
    } as unknown as DupeCheckSummary;

    render(
      <DupeCheckPage
        path="C:\\Media\\Watcher.mkv"
        dupeLoading={false}
        dupeError=""
        dupeSummary={dupeSummary}
        trackerUploadItems={[{ name: "AITHER", config: {} }]}
        dupeTrackerFlags={{}}
        dupeIgnore={{}}
        ruleSkippedTrackerSet={new Set()}
        ruleSkipReasons={{}}
        dupeProgressStatus=""
        dupeCompletedCount={0}
        dupeTotalCount={0}
        useFavicons={true}
        faviconOnly={true}
        handleDupeCheck={vi.fn()}
        setDupeIgnore={vi.fn()}
      />,
    );

    expect(screen.queryByText("AITHER")).toBeNull();
  });

  it("uses configured tracker favicon URLs", async () => {
    const getTrackerIcon = vi.fn().mockResolvedValue("");
    vi.stubGlobal("go", { guiapp: { App: { GetTrackerIcon: getTrackerIcon } } });
    const dupeSummary = {
      SourcePath: "C:\\Media\\Watcher.mkv",
      Results: [
        {
          Tracker: "AITHER",
          Raw: [],
          Filtered: [],
          HasDupes: false,
          ContentFail: false,
          Match: {},
          Notes: [],
          Skipped: false,
          SkipReason: "",
          Status: "complete",
          Error: "",
          CheckedAt: "",
        },
      ],
      Notes: [],
    } as unknown as DupeCheckSummary;

    render(
      <DupeCheckPage
        path="C:\\Media\\Watcher.mkv"
        dupeLoading={false}
        dupeError=""
        dupeSummary={dupeSummary}
        trackerUploadItems={[
          {
            name: "AITHER",
            config: { FaviconURL: "https://icons.example/aither.png" },
          },
        ]}
        dupeTrackerFlags={{}}
        dupeIgnore={{}}
        ruleSkippedTrackerSet={new Set()}
        ruleSkipReasons={{}}
        dupeProgressStatus=""
        dupeCompletedCount={0}
        dupeTotalCount={0}
        useFavicons={true}
        faviconOnly={false}
        handleDupeCheck={vi.fn()}
        setDupeIgnore={vi.fn()}
      />,
    );

    await waitFor(() =>
      expect(getTrackerIcon).toHaveBeenCalledWith(
        "icons.example",
        "https://icons.example/aither.png",
      ),
    );
  });

  it("does not fetch favicons for trackers missing from config", () => {
    const getTrackerIcon = vi.fn().mockResolvedValue("");
    vi.stubGlobal("go", { guiapp: { App: { GetTrackerIcon: getTrackerIcon } } });
    const dupeSummary = {
      SourcePath: "C:\\Media\\Watcher.mkv",
      Results: [
        {
          Tracker: "UNCONFIGURED",
          Raw: [],
          Filtered: [],
          HasDupes: false,
          ContentFail: false,
          Match: {},
          Notes: [],
          Skipped: false,
          SkipReason: "",
          Status: "complete",
          Error: "",
          CheckedAt: "",
        },
      ],
      Notes: [],
    } as unknown as DupeCheckSummary;

    render(
      <DupeCheckPage
        path="C:\\Media\\Watcher.mkv"
        dupeLoading={false}
        dupeError=""
        dupeSummary={dupeSummary}
        trackerUploadItems={[]}
        dupeTrackerFlags={{}}
        dupeIgnore={{}}
        ruleSkippedTrackerSet={new Set()}
        ruleSkipReasons={{}}
        dupeProgressStatus=""
        dupeCompletedCount={0}
        dupeTotalCount={0}
        useFavicons={true}
        faviconOnly={true}
        handleDupeCheck={vi.fn()}
        setDupeIgnore={vi.fn()}
      />,
    );

    expect(screen.getAllByText("UNCONFIGURED").length).toBeGreaterThan(0);
    expect(getTrackerIcon).not.toHaveBeenCalled();
  });
});
