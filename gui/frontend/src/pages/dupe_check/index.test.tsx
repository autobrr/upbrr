// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { DupeCheckSummary } from "../../types";
import DupeCheckPage from "./index";

afterEach(() => {
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
});
