// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { describe, expect, it } from "vitest";

import { normalizeOverrides, normalizeReleaseOverrides } from "./helpers";

describe("normalizeOverrides", () => {
  it("keeps zero IDs and drops nullish IDs", () => {
    expect(
      normalizeOverrides({
        TMDBID: 0,
        IMDBID: null,
        TVDBID: undefined,
        TVmazeID: 123,
      }),
    ).toEqual({
      TMDBID: 0,
      TVmazeID: 123,
    });
  });
});

describe("normalizeReleaseOverrides", () => {
  it("keeps false and empty-string override values", () => {
    expect(
      normalizeReleaseOverrides({
        Category: "",
        Type: null,
        Source: undefined,
        NoYear: false,
        UseSeasonEpisode: true,
      }),
    ).toEqual({
      Category: "",
      NoYear: false,
      UseSeasonEpisode: true,
    });
  });
});
