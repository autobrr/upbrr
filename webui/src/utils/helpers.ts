// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ExternalIDOverrides, ReleaseNameOverrides } from "../types";

/** Normalize external ID overrides while preserving zero as an explicit clear. */
export const normalizeOverrides = (overrides: ExternalIDOverrides): ExternalIDOverrides => {
  const payload: ExternalIDOverrides = {};
  if (overrides.TMDBID !== null && overrides.TMDBID !== undefined) {
    payload.TMDBID = overrides.TMDBID;
  }
  if (overrides.IMDBID !== null && overrides.IMDBID !== undefined) {
    payload.IMDBID = overrides.IMDBID;
  }
  if (overrides.TVDBID !== null && overrides.TVDBID !== undefined) {
    payload.TVDBID = overrides.TVDBID;
  }
  if (overrides.TVmazeID !== null && overrides.TVmazeID !== undefined) {
    payload.TVmazeID = overrides.TVmazeID;
  }
  if (overrides.MALID !== null && overrides.MALID !== undefined) {
    payload.MALID = overrides.MALID;
  }
  return payload;
};

/** Normalize release name overrides without dropping false, empty-string, or nullish field intent. */
export const normalizeReleaseOverrides = (
  overrides: ReleaseNameOverrides,
): ReleaseNameOverrides => {
  return { ...overrides };
};
