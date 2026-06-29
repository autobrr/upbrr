// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { describe, expect, it } from "vitest";

import {
  normalizeMetadataOverrides,
  normalizeOverrides,
  normalizeReleaseOverrides,
} from "./helpers";

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
  it("keeps every release override field", () => {
    expect(
      normalizeReleaseOverrides({
        Category: "",
        Type: "remux",
        Source: "BluRay",
        Resolution: "2160p",
        Tag: "-GROUP",
        Service: "NF",
        Edition: "Director's Cut",
        Season: "S01",
        Episode: "E02",
        EpisodeTitle: "Pilot",
        ManualYear: 0,
        ManualDate: "",
        UseSeasonEpisode: false,
        NoSeason: false,
        NoYear: false,
        NoAKA: false,
        NoTag: false,
        NoEdition: false,
        NoDub: false,
        NoDual: false,
        DualAudio: true,
        Region: "",
      }),
    ).toEqual({
      Category: "",
      Type: "remux",
      Source: "BluRay",
      Resolution: "2160p",
      Tag: "-GROUP",
      Service: "NF",
      Edition: "Director's Cut",
      Season: "S01",
      Episode: "E02",
      EpisodeTitle: "Pilot",
      ManualYear: 0,
      ManualDate: "",
      UseSeasonEpisode: false,
      NoSeason: false,
      NoYear: false,
      NoAKA: false,
      NoTag: false,
      NoEdition: false,
      NoDub: false,
      NoDual: false,
      DualAudio: true,
      Region: "",
    });
  });

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

describe("normalizeMetadataOverrides", () => {
  it("keeps every metadata override field and preserves false values", () => {
    expect(
      normalizeMetadataOverrides({
        Distributor: "",
        OriginalLanguage: "ja",
        PersonalRelease: false,
        Commentary: true,
        WebDV: false,
        StreamOptimized: true,
        Anime: false,
        Clear: ["Anime"],
      }),
    ).toEqual({
      Distributor: "",
      OriginalLanguage: "ja",
      PersonalRelease: false,
      Commentary: true,
      WebDV: false,
      StreamOptimized: true,
      Anime: false,
      Clear: ["Anime"],
    });
  });

  it("drops nullish metadata override values", () => {
    expect(
      normalizeMetadataOverrides({
        Distributor: null,
        OriginalLanguage: undefined,
        PersonalRelease: false,
      }),
    ).toEqual({
      PersonalRelease: false,
    });
  });
});
