// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { describe, expect, it } from "vitest";
import {
  hasFilteredEmptyUploadTrackerSelection,
  resolveSelectedUploadTrackers,
} from "./trackerSelection";
import type { TrackerUploadItem } from "../types";

describe("resolveSelectedUploadTrackers", () => {
  const trackerUploadItems: TrackerUploadItem[] = [
    { name: "AITHER", config: {} },
    { name: "BLU", config: {} },
  ];

  it("excludes stale upload toggles for trackers deselected on the input page", () => {
    expect(
      resolveSelectedUploadTrackers({
        trackerUploadItems,
        releasePageTrackerSelection: { AITHER: true, BLU: false },
        uploadToggles: { AITHER: true, BLU: true },
        dupedTrackerSet: new Set(),
        ruleSkippedTrackerSet: new Set(),
        failedDupeTrackerSet: new Set(),
      }),
    ).toEqual(["AITHER"]);
  });

  it("excludes blocked selected trackers", () => {
    expect(
      resolveSelectedUploadTrackers({
        trackerUploadItems,
        releasePageTrackerSelection: { AITHER: true, BLU: true },
        uploadToggles: { AITHER: true, BLU: true },
        dupedTrackerSet: new Set(["blu"]),
        ruleSkippedTrackerSet: new Set(),
        failedDupeTrackerSet: new Set(),
      }),
    ).toEqual(["AITHER"]);
  });

  it("detects explicit release selections filtered to no upload trackers", () => {
    expect(
      hasFilteredEmptyUploadTrackerSelection({
        trackerUploadItems,
        releasePageTrackerSelection: { AITHER: true, BLU: true },
        uploadToggles: { AITHER: false, BLU: true },
        dupedTrackerSet: new Set(["blu"]),
        ruleSkippedTrackerSet: new Set(),
        failedDupeTrackerSet: new Set(),
      }),
    ).toBe(true);
  });

  it("does not treat uninitialized toggles or eligible trackers as filtered empty", () => {
    expect(
      hasFilteredEmptyUploadTrackerSelection({
        trackerUploadItems,
        releasePageTrackerSelection: { AITHER: true },
        uploadToggles: {},
        dupedTrackerSet: new Set(),
        ruleSkippedTrackerSet: new Set(),
        failedDupeTrackerSet: new Set(),
      }),
    ).toBe(false);

    expect(
      hasFilteredEmptyUploadTrackerSelection({
        trackerUploadItems,
        releasePageTrackerSelection: { AITHER: true, BLU: true },
        uploadToggles: { AITHER: true, BLU: false },
        dupedTrackerSet: new Set(),
        ruleSkippedTrackerSet: new Set(),
        failedDupeTrackerSet: new Set(),
      }),
    ).toBe(false);
  });
});
