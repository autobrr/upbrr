// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { describe, expect, it } from "vitest";

import fixture from "../../pkg/api/testdata/prepared_release_display.json";
import type { PreparedReleaseDisplay } from "./types";

describe("prepared release display contract", () => {
  it("consumes the canonical five-provider fixture as an exhaustive typed union", () => {
    const display = fixture as PreparedReleaseDisplay;
    expect(display.Providers.map((provider) => provider.Provider)).toEqual([
      "tmdb",
      "imdb",
      "tvdb",
      "tvmaze",
      "mal",
    ]);
    for (const provider of display.Providers) {
      switch (provider.Provider) {
        case "tmdb":
          expect(provider.Details.TMDB.TMDBID).toBe(provider.ID);
          break;
        case "imdb":
          expect(provider.Details.IMDB.IMDBID).toBe(provider.ID);
          break;
        case "tvdb":
          expect(provider.Details.TVDB.TVDBID).toBe(provider.ID);
          break;
        case "tvmaze":
          expect(provider.Details.TVmaze.TVmazeID).toBe(provider.ID);
          break;
        case "mal":
          expect(provider.Details.AniList.MALID).toBe(provider.ID);
          break;
      }
    }
  });
});
