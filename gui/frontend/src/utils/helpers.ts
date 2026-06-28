// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ExternalIDOverrides, MetadataOverrides, ReleaseNameOverrides } from "../types";

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
  if (overrides.NoTag !== null && overrides.NoTag !== undefined) {
    payload.NoTag = overrides.NoTag;
  }
  if (overrides.NoEdition !== null && overrides.NoEdition !== undefined) {
    payload.NoEdition = overrides.NoEdition;
  }
  if (overrides.NoDub !== null && overrides.NoDub !== undefined) {
    payload.NoDub = overrides.NoDub;
  }
  if (overrides.NoDual !== null && overrides.NoDual !== undefined) {
    payload.NoDual = overrides.NoDual;
  }
  if (overrides.DualAudio !== null && overrides.DualAudio !== undefined) {
    payload.DualAudio = overrides.DualAudio;
  }
  if (overrides.Region !== null && overrides.Region !== undefined) {
    payload.Region = overrides.Region;
  }
  return payload;
};

/**
 * Normalizes metadata overrides by dropping nullish fields while preserving explicit false values.
 */
export const normalizeMetadataOverrides = (overrides: MetadataOverrides): MetadataOverrides => {
  const payload: MetadataOverrides = {};
  if (overrides.Distributor !== null && overrides.Distributor !== undefined) {
    payload.Distributor = overrides.Distributor;
  }
  if (overrides.OriginalLanguage !== null && overrides.OriginalLanguage !== undefined) {
    payload.OriginalLanguage = overrides.OriginalLanguage;
  }
  if (overrides.PersonalRelease !== null && overrides.PersonalRelease !== undefined) {
    payload.PersonalRelease = overrides.PersonalRelease;
  }
  if (overrides.Commentary !== null && overrides.Commentary !== undefined) {
    payload.Commentary = overrides.Commentary;
  }
  if (overrides.WebDV !== null && overrides.WebDV !== undefined) {
    payload.WebDV = overrides.WebDV;
  }
  if (overrides.StreamOptimized !== null && overrides.StreamOptimized !== undefined) {
    payload.StreamOptimized = overrides.StreamOptimized;
  }
  if (overrides.Anime !== null && overrides.Anime !== undefined) {
    payload.Anime = overrides.Anime;
  }
  return payload;
};
