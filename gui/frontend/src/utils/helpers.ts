// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ExternalIDOverrides, ReleaseNameOverrides } from "../types";

/**
 * Normalize external ID overrides by filtering out null/undefined values
 */
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
  return payload;
};

/**
 * Normalize release name overrides by filtering out null/undefined values
 */
export const normalizeReleaseOverrides = (
  overrides: ReleaseNameOverrides,
): ReleaseNameOverrides => {
  const payload: ReleaseNameOverrides = {};
  if (overrides.Category !== null && overrides.Category !== undefined) {
    payload.Category = overrides.Category;
  }
  if (overrides.Type !== null && overrides.Type !== undefined) {
    payload.Type = overrides.Type;
  }
  if (overrides.Source !== null && overrides.Source !== undefined) {
    payload.Source = overrides.Source;
  }
  if (overrides.Resolution !== null && overrides.Resolution !== undefined) {
    payload.Resolution = overrides.Resolution;
  }
  if (overrides.Tag !== null && overrides.Tag !== undefined) {
    payload.Tag = overrides.Tag;
  }
  if (overrides.Service !== null && overrides.Service !== undefined) {
    payload.Service = overrides.Service;
  }
  if (overrides.Edition !== null && overrides.Edition !== undefined) {
    payload.Edition = overrides.Edition;
  }
  if (overrides.Season !== null && overrides.Season !== undefined) {
    payload.Season = overrides.Season;
  }
  if (overrides.Episode !== null && overrides.Episode !== undefined) {
    payload.Episode = overrides.Episode;
  }
  if (overrides.EpisodeTitle !== null && overrides.EpisodeTitle !== undefined) {
    payload.EpisodeTitle = overrides.EpisodeTitle;
  }
  if (overrides.ManualYear !== null && overrides.ManualYear !== undefined) {
    payload.ManualYear = overrides.ManualYear;
  }
  if (overrides.ManualDate !== null && overrides.ManualDate !== undefined) {
    payload.ManualDate = overrides.ManualDate;
  }
  if (overrides.UseSeasonEpisode !== null && overrides.UseSeasonEpisode !== undefined) {
    payload.UseSeasonEpisode = overrides.UseSeasonEpisode;
  }
  if (overrides.NoSeason !== null && overrides.NoSeason !== undefined) {
    payload.NoSeason = overrides.NoSeason;
  }
  if (overrides.NoYear !== null && overrides.NoYear !== undefined) {
    payload.NoYear = overrides.NoYear;
  }
  if (overrides.NoAKA !== null && overrides.NoAKA !== undefined) {
    payload.NoAKA = overrides.NoAKA;
  }
  return payload;
};
