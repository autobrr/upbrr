// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { describe, expect, it } from "vitest";

import { formatLabel, normalizeDefaultTrackerList } from "./settings";

describe("formatLabel", () => {
  it("keeps underscore labels as space-separated words", () => {
    expect(formatLabel("preferred_trackers")).toBe("preferred trackers");
  });

  it("splits camel-case labels", () => {
    expect(formatLabel("TMDBAPIKey")).toBe("TMDBAPI Key");
    expect(formatLabel("minimumSeedTime")).toBe("minimum Seed Time");
  });
});

describe("normalizeDefaultTrackerList", () => {
  it("normalizes comma-separated tracker strings", () => {
    expect(normalizeDefaultTrackerList("HDB, AITHER,, BTN ")).toEqual(["HDB", "AITHER", "BTN"]);
  });

  it("normalizes array values and drops blanks", () => {
    expect(normalizeDefaultTrackerList([" HDB ", "", null, "BTN"])).toEqual(["HDB", "BTN"]);
  });

  it("returns an empty list for unsupported values", () => {
    expect(normalizeDefaultTrackerList({ enabled: true })).toEqual([]);
  });
});
