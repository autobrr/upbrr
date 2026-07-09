// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { describe, expect, it } from "vitest";

import type { ReleaseNameOverrides } from "../types";

import { normalizeOverrides, normalizeReleaseOverrides } from "./helpers";

describe("normalizeOverrides", () => {
  it("keeps zero IDs and drops nullish IDs", () => {
    expect(
      normalizeOverrides({
        TMDBID: 0,
        IMDBID: null,
        TVDBID: undefined,
        TVmazeID: 123,
        MALID: 0,
      }),
    ).toEqual({
      TMDBID: 0,
      TVmazeID: 123,
      MALID: 0,
    });
  });
});

describe("normalizeReleaseOverrides", () => {
  it("keeps every override field including nullish, false, and empty-string values", () => {
    const overrides: ReleaseNameOverrides = {
      Category: "",
      Type: null,
      Source: "BluRay",
      Resolution: "1080p",
      Tag: "-GRP",
      Service: "AMZN",
      Edition: "Director's Cut",
      Season: "01",
      Episode: "02",
      EpisodeTitle: "Pilot",
      ManualYear: 2026,
      ManualDate: "2026-01-02",
      UseSeasonEpisode: true,
      NoSeason: false,
      NoYear: false,
      NoAKA: false,
      NoTag: false,
      NoEdition: false,
      NoDub: false,
      NoDual: false,
      DualAudio: false,
      Region: undefined,
    };
    const allFields: Record<keyof ReleaseNameOverrides, true> = {
      Category: true,
      Type: true,
      Source: true,
      Resolution: true,
      Tag: true,
      Service: true,
      Edition: true,
      Season: true,
      Episode: true,
      EpisodeTitle: true,
      ManualYear: true,
      ManualDate: true,
      UseSeasonEpisode: true,
      NoSeason: true,
      NoYear: true,
      NoAKA: true,
      NoTag: true,
      NoEdition: true,
      NoDub: true,
      NoDual: true,
      DualAudio: true,
      Region: true,
    };

    expect(Object.keys(overrides).sort()).toEqual(Object.keys(allFields).sort());
    expect(normalizeReleaseOverrides(overrides)).toEqual(overrides);
  });
});
