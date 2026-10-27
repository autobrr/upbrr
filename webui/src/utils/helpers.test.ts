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
      ThreeD: false,
      Category: "",
      Type: null,
      Region: undefined,
    };
    expect(normalizeReleaseOverrides(overrides)).toEqual(overrides);
  });
});
