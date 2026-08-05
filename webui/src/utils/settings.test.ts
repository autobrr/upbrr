// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { describe, expect, it } from "vitest";

import { formatLabel, isSkipAutoTorrentEnabled, normalizeDefaultTrackerList } from "./settings";

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

describe("isSkipAutoTorrentEnabled", () => {
  it("reads the exported Metadata.SkipAutoTorrent flag", () => {
    expect(isSkipAutoTorrentEnabled({ Metadata: { SkipAutoTorrent: true } })).toBe(true);
  });

  it("returns false when metadata config is unavailable", () => {
    expect(isSkipAutoTorrentEnabled(null)).toBe(false);
    expect(isSkipAutoTorrentEnabled({ Metadata: null })).toBe(false);
  });

  it("returns false when Metadata is not a config object", () => {
    expect(isSkipAutoTorrentEnabled({ Metadata: [] })).toBe(false);
    expect(isSkipAutoTorrentEnabled({ Metadata: "foo" })).toBe(false);
    expect(isSkipAutoTorrentEnabled({ Metadata: 123 })).toBe(false);
  });

  it("returns false when SkipAutoTorrent is false or missing", () => {
    const configWithUndefined = {
      Metadata: { SkipAutoTorrent: undefined },
    } as unknown as Parameters<typeof isSkipAutoTorrentEnabled>[0];

    expect(isSkipAutoTorrentEnabled({ Metadata: { SkipAutoTorrent: false } })).toBe(false);
    expect(isSkipAutoTorrentEnabled({ Metadata: {} })).toBe(false);
    expect(isSkipAutoTorrentEnabled(configWithUndefined)).toBe(false);
  });
});
