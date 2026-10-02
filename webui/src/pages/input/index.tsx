// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { pageStyle } from "../../components/ui/pageStyle";
import { useEffect, useMemo, useRef, useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import { Button } from "../../components/ui/button";
import { Checkbox, PillCheckbox } from "../../components/ui/checkbox";
import { TrackerIconImage } from "../../components/ui/tracker-icon";
import RenderedDescription from "../../components/RenderedDescription";
import type { TrackerIconCache } from "../../hooks/useTrackerIcons";
import { trackerIconFor } from "../../hooks/useTrackerIcons";
import type { InputFacet } from "../../releaseSession/types";
import { InputCorrectionEditor } from "./InputCorrectionEditor";
import { settingsStyle } from "../../settings/style";
import type {
  DetailBlock,
  DetailItem,
  ExternalIdentityCandidate,
  ExternalIDInfo,
  ProviderDisplay,
  IMDBAKA,
  IMDBEditionDetail,
  IMDBEpisode,
  IMDBPerson,
  IMDBReleaseDate,
  IMDBSeasonSummary,
  MetadataPreview,
  TMDBCompany,
  TMDBCountry,
  TMDBNetwork,
  TrackerUploadItem,
} from "../../types";
import type { SourcePathHistoryEntry } from "../../utils/inputHistory";
import {
  candidatesFromDiagnostics,
  externalIDInfoFromIdentity,
  externalIdentityDraftFromIdentity,
} from "../../utils/canonicalIdentity";
import { emptyExternalIdentity } from "../../utils/canonicalIdentity";
import { formatIMDbID } from "../../utils/providerId";

const compactInputClass =
  "h-8 rounded-md border border-input bg-card px-2.5 text-sm text-card-foreground outline-none transition placeholder:text-muted-foreground focus:border-ring focus:ring-2 focus:ring-ring/30";
const editDropdownClass = "group rounded-[18px] border border-foreground/10 bg-card/80 p-2";
const disclosureSummaryClass =
  "inline-flex min-h-8 w-max max-w-full cursor-pointer list-none items-center gap-[9px] rounded-[10px] px-[11px] py-[7px] font-semibold shadow-sm transition after:content-['▾'] after:transition-transform group-open:after:rotate-180 hover:-translate-y-px hover:shadow-md focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring [&::-webkit-details-marker]:hidden";
const editDropdownSummaryClass = `${disclosureSummaryClass} bg-primary text-primary-foreground`;

const formatProvider = (value: string) => value.toUpperCase();

const formatID = (provider: string, id: number) => {
  if (!id) return "";
  if (provider === "imdb") return formatIMDbID(id);
  return id.toString();
};

const providerOrder = ["tmdb", "imdb", "tvdb", "tvmaze", "mal"] as const;

const filterAndOrderIdentityProviders = (info: ExternalIDInfo[]) => {
  const orderIndex = new Map<string, number>(
    providerOrder.map((provider, index) => [provider, index]),
  );

  return [...info].sort((left, right) => {
    const leftIndex = orderIndex.get(left.Provider) ?? providerOrder.length;
    const rightIndex = orderIndex.get(right.Provider) ?? providerOrder.length;
    if (leftIndex !== rightIndex) return leftIndex - rightIndex;
    return left.Provider.localeCompare(right.Provider);
  });
};

const normalizeKey = (value: string) => value.toLowerCase().replaceAll(/[^a-z0-9]/g, "");

const imdbTypeLabels: Record<string, string> = {
  movie: "Movie",
  tvseries: "TV series",
  tvminiseries: "TV miniseries",
  tvepisode: "TV episode",
  tvmovie: "TV movie",
  short: "Short",
  video: "Video",
  videogame: "Video game",
};

const formatIMDBType = (value: string) => {
  if (!value) return "";
  const key = normalizeKey(value);
  return imdbTypeLabels[key] ?? value;
};

const formatRuntime = (minutes: number) => {
  if (!minutes) return "";
  const hours = Math.floor(minutes / 60);
  const remainder = minutes % 60;
  if (!hours) return `${minutes} min`;
  if (!remainder) return `${hours}h`;
  return `${hours}h ${remainder}m`;
};

const formatRating = (rating: number, count: number) => {
  if (!rating) return "";
  const score = rating.toFixed(1);
  if (count) return `${score} (${count.toLocaleString()} votes)`;
  return score;
};

const formatPlaylistDuration = (seconds: number) => {
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const remainder = Math.floor(seconds % 60);
  if (hours > 0) return `${hours}h ${minutes}m ${remainder}s`;
  if (minutes > 0) return `${minutes}m ${remainder}s`;
  return `${remainder}s`;
};

const formatPlaylistBytes = (bytes: number) => {
  if (!bytes) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  return `${Math.round((bytes / 1024 ** index) * 100) / 100} ${units[index]}`;
};

const formatNumber = (value: number) => (value ? value.toString() : "");

const formatSimilarity = (value: number) => {
  if (!value) return "";
  return `${Math.round(value * 100)}%`;
};

const formatBoolean = (value: boolean) => (value ? "Yes" : "No");

const tmdbLogoBaseURL = "https://image.tmdb.org/t/p/original/";
const tmdbLogoSize = 64;
const malAnimeBaseURL = "https://myanimelist.net/anime/";

const tmdbImageURL = (url: string, size: string) =>
  /\.svg(?:\?|$)/i.test(url)
    ? url
    : url.replace(
        /^https:\/\/image\.tmdb\.org\/t\/p\/(?:original|w\d+(?:_and_h\d+_face)?)\//,
        `https://image.tmdb.org/t/p/${size}/`,
      );

const imdbPosterURL = (url: string, width: number) => {
  if (!url.startsWith("https://m.media-amazon.com/images/M/")) return url;
  return url.replace(/_V1_[^/.]*\.(jpe?g|png)(\?.*)?$/i, `_V1_QL75_UX${width}_.$1$2`);
};

const posterPreviewURL = (preview: ProviderDisplay) => {
  const original = preview.Summary.PosterURL;
  switch (preview.Provider) {
    case "tmdb":
      return tmdbImageURL(original, "w220_and_h330_face");
    case "imdb":
      return imdbPosterURL(original, 190);
    case "tvdb":
      return preview.Details.TVDB.PosterThumbnail || original;
    case "tvmaze":
      return preview.Details.TVmaze.PosterMedium || original;
    case "mal":
      return preview.Details.AniList.CoverMedium || original;
    default:
      return original;
  }
};

const normalizeTMDBLogoURL = (path: string) => {
  const trimmed = path?.trim();
  if (!trimmed) return "";
  if (/^https?:\/\//i.test(trimmed)) return trimmed;
  return `${tmdbLogoBaseURL}${trimmed}`;
};

const formatNameList = (values?: string[] | null) => {
  if (!values || values.length === 0) return "";
  const cleaned = values.map((item) => item?.trim()).filter(Boolean);
  if (cleaned.length === 0) return "";
  return cleaned.join("\n");
};

const formatCommaList = (values?: string[] | null) => {
  if (!values || values.length === 0) return "";
  const cleaned = values.map((item) => item?.trim()).filter(Boolean);
  if (cleaned.length === 0) return "";
  return cleaned.join(", ");
};

// formatAniListScore renders AniList's 0-100 score fields as percentages.
const formatAniListScore = (value: number) => {
  if (!value) return "";
  return `${value}%`;
};

// formatAniListTags hides adult and spoiler tags, sorts by AniList relevance,
// and limits the preview so selected MAL details remain scannable.
const formatAniListTags = (
  values?:
    | {
        Name: string;
        Rank: number;
        IsAdult: boolean;
        IsGeneralSpoiler: boolean;
        IsMediaSpoiler: boolean;
      }[]
    | null,
) => {
  if (!values || values.length === 0) return "";
  const cleaned = values
    .filter((item) => item?.Name && !item.IsAdult && !item.IsGeneralSpoiler && !item.IsMediaSpoiler)
    .sort((left, right) => right.Rank - left.Rank)
    .slice(0, 10)
    .map((item) => (item.Rank ? `${item.Name} (${item.Rank}%)` : item.Name));
  if (cleaned.length === 0) return "";
  return cleaned.join(", ");
};

// formatAniListStudios renders named studio nodes and skips empty placeholders.
const formatAniListStudios = (values?: { Name: string }[] | null) => {
  if (!values || values.length === 0) return "";
  const cleaned = values.map((item) => item?.Name?.trim()).filter(Boolean);
  if (cleaned.length === 0) return "";
  return cleaned.join(", ");
};

// formatAniListExternalLinks preserves both provider labels and URLs because
// AniList links may have one without the other.
const formatAniListExternalLinks = (values?: { Site: string; URL: string }[] | null) => {
  if (!values || values.length === 0) return "";
  const lines = values
    .map((item) => {
      const site = item?.Site?.trim() ?? "";
      const url = item?.URL?.trim() ?? "";
      if (!site && !url) return "";
      if (!site) return url;
      if (!url) return site;
      return `${site} - ${url}`;
    })
    .filter(Boolean)
    .slice(0, 8);
  if (lines.length === 0) return "";
  return lines.join("\n");
};

// formatUnixSeconds renders AniList airing timestamps, which are Unix seconds.
const formatUnixSeconds = (value: number) => {
  if (!value) return "";
  return new Date(value * 1000).toISOString();
};

type TVDBDisplayMode = "original" | "english";

const isEnglishLanguageValue = (value: string) => {
  const normalized = value.trim().toLowerCase().replaceAll("_", "-");
  if (!normalized) return false;
  if (normalized === "en" || normalized === "eng" || normalized === "english") return true;
  return normalized.startsWith("en-");
};

const hasTVDBEnglishDisplay = (preview: ProviderDisplay) => {
  if (preview.Provider !== "tvdb") return false;
  const tvdb = preview.Details.TVDB;
  const originalLanguage = tvdb.OriginalLanguage || preview.Summary.OriginalLanguage;
  if (isEnglishLanguageValue(originalLanguage)) return false;
  if (!tvdb.HasEnglish) return false;
  return Boolean(
    tvdb.NameEnglish ||
    tvdb.OverviewEnglish ||
    tvdb.EpisodeNameEnglish ||
    tvdb.EpisodeOverviewEnglish,
  );
};

const pickTVDBText = (
  mode: TVDBDisplayMode,
  originalValue: string,
  englishValue: string,
  fallbackValue = "",
) => {
  if (mode === "english") {
    return englishValue || originalValue || fallbackValue;
  }
  return originalValue || fallbackValue || englishValue;
};

const formatPeopleList = (values?: IMDBPerson[] | null) => {
  if (!values || values.length === 0) return "";
  const cleaned = values.map((item) => item?.Name?.trim()).filter(Boolean);
  if (cleaned.length === 0) return "";
  return cleaned.join("\n");
};

const formatIMDBAkas = (values?: IMDBAKA[] | null) => {
  if (!values || values.length === 0) return "";
  const lines = values
    .map((item) => {
      const title = item?.Title?.trim() ?? "";
      const country = item?.Country?.trim() ?? "";
      const language = item?.Language?.trim() ?? "";
      const attrs = item?.Attributes?.filter(Boolean) ?? [];
      if (!title && !country && !language) return "";
      let line = title;
      if (country) {
        line = line ? `${line} - ${country}` : country;
      }
      if (language) {
        line = line ? `${line} (${language})` : `(${language})`;
      }
      if (attrs.length > 0) {
        line = `${line} [${attrs.join(", ")}]`;
      }
      return line;
    })
    .filter(Boolean);
  if (lines.length === 0) return "";
  return lines.join("\n");
};

const formatEditionDetails = (values?: Record<string, IMDBEditionDetail> | null) => {
  if (!values) return "";
  const entries = Object.entries(values);
  if (entries.length === 0) return "";
  entries.sort((left, right) => Number(left[0]) - Number(right[0]));
  const lines = entries.map(([, detail]) => {
    const name = detail.DisplayName?.trim() || "";
    const minutes = detail.Minutes || 0;
    const attrs = detail.Attributes?.filter(Boolean) ?? [];
    let line = name;
    if (minutes) {
      line = line ? `${line} (${minutes} min)` : `${minutes} min`;
    }
    if (attrs.length > 0) {
      line = `${line} [${attrs.join(", ")}]`;
    }
    return line;
  });
  const cleaned = lines.filter(Boolean);
  if (cleaned.length === 0) return "";
  return cleaned.join("\n");
};

const formatReleaseDate = (value?: IMDBReleaseDate) => {
  if (!value || !value.Year) return "";
  const month = value.Month ? String(value.Month).padStart(2, "0") : "";
  const day = value.Day ? String(value.Day).padStart(2, "0") : "";
  if (month && day) return `${value.Year}-${month}-${day}`;
  if (month) return `${value.Year}-${month}`;
  return value.Year.toString();
};

const formatEpisodes = (values?: IMDBEpisode[] | null) => {
  if (!values || values.length === 0) return "";
  const lines = values
    .map((item) => {
      const season = item.Season ? `S${item.Season}` : "";
      const episode = item.EpisodeText ? `E${item.EpisodeText}` : "";
      const header = `${season}${episode}`.trim();
      const title = item.Title?.trim() ?? "";
      const date = formatReleaseDate(item.ReleaseDate);
      let line = [header, title].filter(Boolean).join(" - ");
      if (date) {
        line = line ? `${line} (${date})` : date;
      }
      return line;
    })
    .filter(Boolean);
  if (lines.length === 0) return "";
  return lines.join("\n");
};

const formatSeasonsSummary = (values?: IMDBSeasonSummary[] | null) => {
  if (!values || values.length === 0) return "";
  const lines = values
    .map((item) => {
      const year = item.YearRange || formatNumber(item.Year);
      if (!year) return "";
      return `Season ${item.Season}: ${year}`;
    })
    .filter(Boolean);
  if (lines.length === 0) return "";
  return lines.join("\n");
};

const formatTMDBCountries = (values?: TMDBCountry[] | null) => {
  if (!values || values.length === 0) return "";
  const lines = values.map((item) => item?.Name?.trim()).filter(Boolean);
  if (lines.length === 0) return "";
  return lines.join("\n");
};

const buildCompanyBlocks = (values?: TMDBCompany[] | null) => {
  if (!values || values.length === 0) return [];
  const blocks: DetailBlock[] = [];
  for (const item of values) {
    const name = item?.Name?.trim() ?? "";
    const country = item?.OriginCountry?.trim() ?? "";
    const logoURL = normalizeTMDBLogoURL(item?.LogoPath ?? "");
    let text = name;
    if (country) {
      text = text ? `${text} - ${country}` : country;
    }
    if (!text && !logoURL) {
      continue;
    }
    if (logoURL) {
      blocks.push({ imageUrl: logoURL, imageAlt: name || "TMDb logo" });
    }
    if (text) {
      blocks.push({ text });
    }
  }
  return blocks;
};

const buildNetworkBlocks = (values?: TMDBNetwork[] | null) => {
  if (!values || values.length === 0) return [];
  const blocks: DetailBlock[] = [];
  for (const item of values) {
    const name = item?.Name?.trim() ?? "";
    const country = item?.OriginCountry?.trim() ?? "";
    const logoURL = normalizeTMDBLogoURL(item?.LogoPath ?? "");
    let text = name;
    if (country) {
      text = text ? `${text} - ${country}` : country;
    }
    if (!text && !logoURL) {
      continue;
    }
    if (logoURL) {
      blocks.push({ imageUrl: logoURL, imageAlt: name || "TMDb logo" });
    }
    if (text) {
      blocks.push({ text });
    }
  }
  return blocks;
};

const buildPreviewDetails = (
  preview: ProviderDisplay,
  tvdbDisplayMode: TVDBDisplayMode,
): DetailItem[] => {
  const baseID: DetailItem = {
    label: `${formatProvider(preview.Provider)} ID`,
    value: formatID(preview.Provider, preview.ID),
    mono: true,
  };

  if (preview.Provider === "imdb") {
    const imdb = preview.Details.IMDB;
    return [
      baseID,
      { label: "IMDb URL", value: imdb?.IMDbURL ?? "", mono: true },
      { label: "AKA", value: imdb?.AKA ?? "" },
      { label: "Type", value: formatIMDBType(imdb.Type || preview.Summary.MediaType) },
      { label: "Year", value: formatNumber(imdb.Year || preview.Summary.Year) },
      { label: "End year", value: formatNumber(imdb?.EndYear ?? 0) },
      { label: "TV year", value: formatNumber(imdb?.TVYear ?? 0) },
      { label: "Original language", value: imdb?.OriginalLanguage ?? "" },
      { label: "Country", value: imdb.Country || preview.Summary.Country },
      { label: "Country list", value: imdb?.CountryList ?? "" },
      {
        label: "Rating",
        value: formatRating(
          imdb.Rating || preview.Summary.Rating,
          imdb.RatingCount || preview.Summary.RatingCount,
        ),
      },
      { label: "Rating text", value: imdb?.RatingText ?? "" },
      {
        label: "Rating count",
        value: formatNumber(imdb.RatingCount || preview.Summary.RatingCount),
      },
      {
        label: "Runtime",
        value: formatRuntime(imdb.RuntimeMinutes || preview.Summary.RuntimeMinutes),
      },
      { label: "Runtime text", value: imdb?.RuntimeText ?? "" },
      { label: "Editions", value: formatCommaList(imdb?.Editions) },
      { label: "Edition details", value: formatEditionDetails(imdb?.EditionDetails) },
      { label: "Genres", value: imdb.Genres || preview.Summary.Genres },
      { label: "Sound mixes", value: formatNameList(imdb?.SoundMixes) },
      { label: "Directors", value: formatPeopleList(imdb?.Directors) },
      { label: "Creators", value: formatPeopleList(imdb?.Creators) },
      { label: "Writers", value: formatPeopleList(imdb?.Writers) },
      { label: "Stars", value: formatPeopleList(imdb?.Stars) },
      { label: "AKA entries", value: formatIMDBAkas(imdb?.Akas) },
      { label: "Season summary", value: formatSeasonsSummary(imdb?.SeasonsSummary) },
      { label: "Episodes", value: formatEpisodes(imdb?.Episodes) },
      { label: "Cover URL", value: imdb.Cover || preview.Summary.PosterURL, mono: true },
    ].filter((item) => item.value || (item.blocks && item.blocks.length > 0));
  }

  if (preview.Provider === "tmdb") {
    const tmdb = preview.Details.TMDB;
    return [
      baseID,
      { label: "IMDB ID", value: formatID("imdb", tmdb.IMDBID), mono: true },
      { label: "TVDB ID", value: formatNumber(tmdb.TVDBID), mono: true },
      { label: "Original title", value: tmdb.OriginalTitle || preview.Summary.OriginalTitle },
      { label: "Type", value: tmdb.TMDBType || preview.Summary.MediaType },
      { label: "Category", value: tmdb.Category || preview.Summary.Category },
      { label: "Year", value: formatNumber(tmdb.Year || preview.Summary.Year) },
      { label: "Release date", value: tmdb.ReleaseDate || preview.Summary.Date },
      { label: "First air date", value: tmdb.FirstAirDate || preview.Summary.Date },
      { label: "Last air date", value: tmdb.LastAirDate || preview.Summary.EndDate },
      { label: "Runtime", value: formatRuntime(tmdb.Runtime || preview.Summary.RuntimeMinutes) },
      { label: "Genres", value: tmdb.Genres || preview.Summary.Genres },
      { label: "Genre IDs", value: tmdb?.GenreIDs ?? "" },
      { label: "Keywords", value: tmdb.Keywords || preview.Summary.Keywords },
      { label: "YouTube", value: tmdb.YouTube || preview.Summary.TrailerURL },
      { label: "Certification", value: tmdb?.Certification ?? "" },
      { label: "Creators", value: formatNameList(tmdb?.Creators) },
      { label: "Directors", value: formatNameList(tmdb?.Directors) },
      { label: "Cast", value: formatNameList(tmdb?.Cast) },
      { label: "Origin countries", value: formatCommaList(tmdb?.OriginCountry) },
      {
        label: "Production companies",
        value: "",
        blocks: buildCompanyBlocks(tmdb?.ProductionCompanies),
      },
      { label: "Production countries", value: formatTMDBCountries(tmdb?.ProductionCountries) },
      {
        label: "Networks",
        value: "",
        blocks: buildNetworkBlocks(tmdb?.Networks),
      },
      { label: "Poster URL", value: tmdb.Poster || preview.Summary.PosterURL, mono: true },
      { label: "Poster path", value: tmdb?.TMDBPosterPath ?? "", mono: true },
      { label: "Backdrop URL", value: tmdb.Backdrop || preview.Summary.BackdropURL, mono: true },
      { label: "Logo URL", value: tmdb?.Logo ?? "", mono: true },
      { label: "Logo name", value: tmdb?.TMDBLogo ?? "" },
      {
        label: "Original language",
        value: tmdb.OriginalLanguage || preview.Summary.OriginalLanguage,
      },
      { label: "Anime", value: tmdb ? formatBoolean(tmdb.Anime) : "" },
      { label: "MAL ID", value: formatNumber(tmdb?.MALID ?? 0), mono: true },
      { label: "Demographic", value: tmdb?.Demographic ?? "" },
      { label: "Retrieved AKA", value: tmdb?.RetrievedAKA ?? "" },
      { label: "IMDb mismatch", value: tmdb ? formatBoolean(tmdb.IMDbMismatch) : "" },
      { label: "Mismatched IMDb ID", value: formatNumber(tmdb?.MismatchedIMDbID ?? 0), mono: true },
    ].filter((item) => item.value || (item.blocks && item.blocks.length > 0));
  }

  if (preview.Provider === "tvdb") {
    const tvdb = preview.Details.TVDB;
    const displayName = pickTVDBText(
      tvdbDisplayMode,
      tvdb.Name || preview.Summary.Title,
      tvdb.NameEnglish,
      preview.Summary.Title,
    );
    const displayOverview = pickTVDBText(
      tvdbDisplayMode,
      tvdb.Overview || preview.Summary.Overview,
      tvdb.OverviewEnglish,
      preview.Summary.Overview,
    );
    const displayEpisodeName = pickTVDBText(
      tvdbDisplayMode,
      tvdb?.EpisodeName ?? "",
      tvdb?.EpisodeNameEnglish ?? "",
    );
    const displayEpisodeOverview = pickTVDBText(
      tvdbDisplayMode,
      tvdb?.EpisodeOverview ?? "",
      tvdb?.EpisodeOverviewEnglish ?? "",
    );
    const seasonNumber = tvdb?.EpisodeSeason ?? 0;
    const episodeNumber = tvdb?.EpisodeNumber ?? 0;
    const episodeTag =
      seasonNumber > 0 && episodeNumber > 0
        ? `S${String(seasonNumber).padStart(2, "0")}E${String(episodeNumber).padStart(2, "0")}`
        : "";
    return [
      baseID,
      { label: "Name", value: displayName },
      { label: "Type", value: tvdb.Type || preview.Summary.MediaType },
      { label: "Status", value: tvdb?.Status ?? "" },
      { label: "Year", value: formatNumber(tvdb.Year || preview.Summary.Year) },
      { label: "First aired", value: tvdb.FirstAired || preview.Summary.Date },
      { label: "Genres", value: tvdb.Genres || preview.Summary.Genres },
      { label: "Network", value: tvdb?.Network ?? "" },
      { label: "Origin country", value: tvdb.OriginalCountry || preview.Summary.Country },
      {
        label: "Original language",
        value: tvdb.OriginalLanguage || preview.Summary.OriginalLanguage,
      },
      { label: "Aliases", value: formatCommaList(tvdb?.Aliases) },
      { label: "Episode", value: episodeTag },
      { label: "Episode name", value: displayEpisodeName },
      { label: "Episode aired", value: tvdb?.EpisodeAired ?? "" },
      { label: "Episode overview", value: displayEpisodeOverview },
      { label: "Overview", value: displayOverview },
      { label: "Poster URL", value: tvdb.Poster || preview.Summary.PosterURL, mono: true },
    ].filter((item) => item.value || (item.blocks && item.blocks.length > 0));
  }

  if (preview.Provider === "tvmaze") {
    const tvmaze = preview.Details.TVmaze;
    const network = tvmaze?.Network ?? "";
    const webChannel = tvmaze?.WebChannel ?? "";
    const networkText =
      network && tvmaze?.NetworkCountry ? `${network} - ${tvmaze.NetworkCountry}` : network;
    const webChannelText =
      webChannel && tvmaze?.WebCountry ? `${webChannel} - ${tvmaze.WebCountry}` : webChannel;
    return [
      baseID,
      { label: "IMDB ID", value: formatID("imdb", tvmaze.IMDBID), mono: true },
      { label: "TVDB ID", value: formatNumber(tvmaze.TVDBID), mono: true },
      { label: "Name", value: tvmaze.Name || preview.Summary.Title },
      { label: "Type", value: tvmaze.Type || preview.Summary.MediaType },
      { label: "Status", value: tvmaze?.Status ?? "" },
      { label: "Year", value: formatNumber(preview.Summary.Year) },
      { label: "Premiered", value: tvmaze.Premiered || preview.Summary.Date },
      { label: "Ended", value: tvmaze?.Ended ?? "" },
      { label: "Genres", value: tvmaze.Genres || preview.Summary.Genres },
      { label: "Language", value: tvmaze.Language || preview.Summary.OriginalLanguage },
      { label: "Country", value: tvmaze.Country || preview.Summary.Country },
      { label: "Runtime", value: formatRuntime(tvmaze.Runtime || preview.Summary.RuntimeMinutes) },
      { label: "Average runtime", value: formatRuntime(tvmaze?.AverageRuntime ?? 0) },
      {
        label: "Rating",
        value: formatRating(
          tvmaze.Rating || preview.Summary.Rating,
          tvmaze.Weight || preview.Summary.RatingCount,
        ),
      },
      { label: "Score", value: formatNumber(tvmaze.Weight || preview.Summary.RatingCount) },
      { label: "Network", value: networkText },
      { label: "Web channel", value: webChannelText },
      { label: "Official site", value: tvmaze?.OfficialSite ?? "", mono: true },
      { label: "Overview", value: tvmaze.Summary || preview.Summary.Overview },
      { label: "Poster URL", value: tvmaze.Poster || preview.Summary.PosterURL, mono: true },
      { label: "Poster medium", value: tvmaze?.PosterMedium ?? "", mono: true },
      { label: "Backdrop URL", value: tvmaze.Backdrop || preview.Summary.BackdropURL, mono: true },
      { label: "Backdrop medium", value: tvmaze?.BackdropMedium ?? "", mono: true },
      { label: "Network logo", value: tvmaze?.NetworkLogo ?? "", mono: true },
      { label: "Web logo", value: tvmaze?.WebLogo ?? "", mono: true },
    ].filter((item) => item.value || (item.blocks && item.blocks.length > 0));
  }

  if (preview.Provider === "mal") {
    const anilist = preview.Details.AniList;
    const anilistURL =
      anilist?.SiteURL ||
      (anilist?.AniListID ? `https://anilist.co/anime/${anilist.AniListID}` : "");
    return [
      baseID,
      { label: "AniList ID", value: formatNumber(anilist?.AniListID ?? 0), mono: true },
      { label: "MAL URL", value: `${malAnimeBaseURL}${preview.ID}`, mono: true },
      { label: "AniList URL", value: anilistURL, mono: true },
      { label: "English title", value: anilist?.TitleEnglish ?? "" },
      { label: "Romaji title", value: anilist.TitleRomaji || preview.Summary.OriginalTitle },
      { label: "Native title", value: anilist?.TitleNative ?? "" },
      { label: "User preferred title", value: anilist?.TitleUserPreferred ?? "" },
      { label: "Format", value: anilist.Format || preview.Summary.Category },
      { label: "Status", value: anilist?.Status ?? "" },
      { label: "Season", value: anilist?.Season ?? "" },
      { label: "Season year", value: formatNumber(anilist.SeasonYear || preview.Summary.Year) },
      { label: "Start date", value: anilist.StartDate || preview.Summary.Date },
      { label: "End date", value: anilist.EndDate || preview.Summary.EndDate },
      { label: "Episodes", value: formatNumber(anilist?.Episodes ?? 0) },
      {
        label: "Duration",
        value: formatRuntime(anilist.Duration || preview.Summary.RuntimeMinutes),
      },
      {
        label: "Country of origin",
        value: anilist.CountryOfOrigin || preview.Summary.OriginalLanguage,
      },
      { label: "Source", value: anilist?.Source ?? "" },
      { label: "Genres", value: formatCommaList(anilist.Genres) || preview.Summary.Genres },
      { label: "Synonyms", value: formatCommaList(anilist?.Synonyms) },
      { label: "Average score", value: formatAniListScore(anilist?.AverageScore ?? 0) },
      { label: "Mean score", value: formatAniListScore(anilist?.MeanScore ?? 0) },
      {
        label: "Popularity",
        value: formatNumber(anilist.Popularity || preview.Summary.RatingCount),
      },
      { label: "Favourites", value: formatNumber(anilist?.Favourites ?? 0) },
      { label: "Tags", value: formatAniListTags(anilist?.Tags) },
      { label: "Studios", value: formatAniListStudios(anilist?.Studios) },
      {
        label: "Trailer",
        value: anilist?.Trailer?.ID ? `${anilist.Trailer.Site}: ${anilist.Trailer.ID}` : "",
      },
      {
        label: "Next airing",
        value: anilist?.NextAiringEpisode?.Episode
          ? `Episode ${anilist.NextAiringEpisode.Episode} - ${formatUnixSeconds(anilist.NextAiringEpisode.AiringAt)}`
          : "",
      },
      {
        label: "External links",
        value: formatAniListExternalLinks(anilist?.ExternalLinks),
        mono: true,
      },
      { label: "Cover extra large", value: anilist?.CoverExtraLarge ?? "", mono: true },
      { label: "Cover large", value: anilist.CoverLarge || preview.Summary.PosterURL, mono: true },
      { label: "Cover medium", value: anilist?.CoverMedium ?? "", mono: true },
      { label: "Cover color", value: anilist?.CoverColor ?? "", mono: true },
      {
        label: "Banner URL",
        value: anilist.BannerImage || preview.Summary.BackdropURL,
        mono: true,
      },
      { label: "Adult", value: anilist ? formatBoolean(anilist.IsAdult) : "" },
      { label: "Resolver source", value: preview.Provenance },
    ].filter((item) => item.value || (item.blocks && item.blocks.length > 0));
  }

  return [baseID].filter((item) => item.value || (item.blocks && item.blocks.length > 0));
};

const renderDetailValue = (item: DetailItem) => {
  if (item.blocks && item.blocks.length > 0) {
    return (
      <div>
        {item.blocks.map((block, index) => (
          <div key={`${item.label}-${index}`} style={{ marginBottom: "0.35rem" }}>
            {block.imageUrl ? (
              <img
                src={block.imageUrl}
                alt={block.imageAlt || "Logo"}
                loading="lazy"
                style={{
                  width: tmdbLogoSize,
                  height: tmdbLogoSize,
                  objectFit: "contain",
                  display: "block",
                }}
              />
            ) : null}
            {block.text ? <span>{block.text}</span> : null}
          </div>
        ))}
      </div>
    );
  }
  const lines = item.value.split("\n");
  if (lines.length === 1) return item.value;
  return (
    <>
      {lines.map((line, index) => (
        <span key={`${item.label}-${index}`}>
          {line}
          {index < lines.length - 1 ? <br /> : null}
        </span>
      ))}
    </>
  );
};

const PreviewDetailsList = ({ items }: { items: DetailItem[] }) => {
  if (items.length === 0) return null;
  return (
    <div className="mt-[14px] grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-x-[14px] gap-y-[9px] border-t border-foreground/10 pt-[9px]">
      {items.map((item) => (
        <div className="min-w-0" key={item.label}>
          <p className={pageStyle.label}>{item.label}</p>
          <p
            className={`mt-1 mb-0 text-[1.1rem] font-semibold [overflow-wrap:anywhere] ${item.mono ? "font-mono text-[0.95rem]" : ""}`}
          >
            {renderDetailValue(item)}
          </p>
        </div>
      ))}
    </div>
  );
};

type ProviderSelection = {
  sourcePath: string;
  generation: number;
  provider: string;
};

const emptyMetadataPreview: MetadataPreview = {
  SourcePath: "",
  TrackerName: "",
  ReleaseName: "",
  ReleaseNameOverrides: {},
  Release: { SourcePath: "", Generation: 0 },
  Identity: emptyExternalIdentity(),
  Display: { ReleaseName: "", Providers: [] },
  Bluray: null,
  Diagnostics: [],
  TrackerData: [],
  TrackerRuleFailures: {},
};

const emptyInputIntent = (): InputFacet["view"]["intent"] => ({
  sourceLookupURL: "",
  identity: {},
  metadata: {},
  releaseName: {},
  playlist: { Set: false, Selected: [], UseAll: false },
  trackerSourceIDs: {},
  policy: { keepFolder: false, keepImages: false, onlyID: false },
  search: { skip: false, client: "" },
});

type Props = Readonly<{
  facet: InputFacet;
  sourcePathHistory: SourcePathHistoryEntry[];
  handleBrowseFile: () => void;
  handleBrowseFolder: () => void;
  trackerUploadItems: TrackerUploadItem[];
  showExternalIDInputUI: boolean;
  setLightboxImage: Dispatch<SetStateAction<string>>;
  setLightboxAlt: Dispatch<SetStateAction<string>>;
  useFavicons?: boolean;
  faviconOnly?: boolean;
  trackerIconSrcByName: TrackerIconCache;
}>;

/** Presents source selection, preparation progress, prerequisites, and metadata overrides. */
export default function InputPage(props: Props) {
  const {
    facet,
    sourcePathHistory,
    handleBrowseFile,
    handleBrowseFolder,
    trackerUploadItems,
    showExternalIDInputUI,
    setLightboxImage,
    setLightboxAlt,
    useFavicons = true,
    faviconOnly = false,
    trackerIconSrcByName,
  } = props;

  const { view } = facet;
  const path = view.sourceDraft;
  const sourceLookupURL = view.intent.sourceLookupURL;
  const loading = view.status === "running";
  const recovering = view.activeInput.state === "recovering";
  const opaqueRecovering =
    recovering &&
    !view.activeInput.inputID &&
    view.activeInput.recoveryWorkflowIDs.length === 0 &&
    !view.preview;
  const verification =
    view.sourceVerification?.status === "running" ? view.sourceVerification : null;
  const metadataResetting = loading;
  const error = view.error;
  const preview = view.preview || emptyMetadataPreview;
  const isBlurayDisc = (view.release?.Disc?.Type || view.source.discType) === "BDMV";
  const [providerSelection, setProviderSelection] = useState<ProviderSelection>({
    sourcePath: "",
    generation: 0,
    provider: "",
  });
  const releasePageTrackerSelection = useMemo(
    () =>
      Object.fromEntries(
        trackerUploadItems.map((item) => [
          item.name,
          view.selectedTrackers.includes(item.name.trim().toUpperCase()),
        ]),
      ),
    [trackerUploadItems, view.selectedTrackers],
  );
  const setReleasePageTrackerSelection: Dispatch<SetStateAction<Record<string, boolean>>> = (
    action,
  ) => {
    const next = typeof action === "function" ? action(releasePageTrackerSelection) : action;
    facet.chooseTrackers(
      trackerUploadItems.filter((item) => next[item.name]).map((item) => item.name),
    );
  };
  const handleSourcePathChange = facet.updateSourceDraft;
  const setSourceLookupURL: Dispatch<SetStateAction<string>> = (action) => {
    facet.changeSourceLookupURL(typeof action === "function" ? action(sourceLookupURL) : action);
  };
  const handleFetch = () =>
    void facet.prepareSource(
      path,
      view.selectedSource && path.trim() !== view.selectedSource ? emptyInputIntent() : view.intent,
    );
  const handleRefresh = handleFetch;
  const handleResetMetadata = () => void facet.resetSource(path, view.intent);
  const refreshDisabled = loading || !path.trim();

  const [sourcePathHistoryOpen, setSourcePathHistoryOpen] = useState(false);
  const sourcePathHistoryRef = useRef<HTMLDivElement | null>(null);
  const sourcePathHistoryAvailable = sourcePathHistory.length > 0;

  useEffect(() => {
    if (!sourcePathHistoryOpen) {
      return;
    }
    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target;
      if (!(target instanceof Node) || sourcePathHistoryRef.current?.contains(target)) {
        return;
      }
      setSourcePathHistoryOpen(false);
    };
    document.addEventListener("pointerdown", handlePointerDown);
    return () => document.removeEventListener("pointerdown", handlePointerDown);
  }, [sourcePathHistoryOpen]);

  useEffect(() => {
    if (!sourcePathHistoryAvailable) {
      setSourcePathHistoryOpen(false);
    }
  }, [sourcePathHistoryAvailable]);

  const openSourcePathHistory = () => {
    if (sourcePathHistoryAvailable) {
      setSourcePathHistoryOpen(true);
    }
  };

  const selectSourcePathHistory = (entry: SourcePathHistoryEntry) => {
    setSourcePathHistoryOpen(false);
    facet.selectSource(entry.path);
  };

  const identityDraft = useMemo(
    () => externalIdentityDraftFromIdentity(preview.Identity),
    [preview.Identity],
  );
  const externalIDInfo = useMemo(
    () => externalIDInfoFromIdentity(preview.Identity),
    [preview.Identity],
  );
  const providerDisplays = preview.Display.Providers;
  const hasPreview = preview.ReleaseName || externalIDInfo.length > 0;
  const showReleaseDetails = Boolean(
    hasPreview || (path.trim() && (view.status === "ready" || view.status === "error")),
  );
  const hasResolvedPrimaryExternalID = identityDraft.TMDBID > 0 || identityDraft.IMDBID > 0;
  const selectedTrackerCount = useMemo(
    () =>
      trackerUploadItems.reduce(
        (count, tracker) => count + (releasePageTrackerSelection[tracker.name] ? 1 : 0),
        0,
      ),
    [trackerUploadItems, releasePageTrackerSelection],
  );
  const allTrackersSelected =
    trackerUploadItems.length > 0 && selectedTrackerCount === trackerUploadItems.length;

  /**
   * Applies one selection state to every currently configured input tracker.
   *
   * Existing selection entries for trackers outside the visible config list are left untouched.
   */
  const setAllTrackersSelected = (selected: boolean) => {
    setReleasePageTrackerSelection((prev) => {
      const next = { ...prev };
      trackerUploadItems.forEach((tracker) => {
        next[tracker.name] = selected;
      });
      return next;
    });
  };

  const discHint = useMemo(() => {
    const trimmed = path.trim();
    if (!trimmed) return "";
    const normalized = trimmed.replaceAll("\\", "/");
    const upper = normalized.toUpperCase();
    if (/(^|\/)BDMV(\/|$)/.test(upper)) {
      return "Bluray disc folder detected (BDMV).";
    }
    if (/(^|\/)VIDEO_TS(\/|$)/.test(upper)) {
      return "DVD disc folder detected (VIDEO_TS).";
    }
    return "";
  }, [path]);

  const orderedIdentityProviders = useMemo(() => {
    const displays = new Map<string, ProviderDisplay>(
      providerDisplays.map((item) => [item.Provider, item]),
    );
    return filterAndOrderIdentityProviders(externalIDInfo).flatMap((item) => {
      const display = displays.get(item.Provider);
      return display ? [{ ...item, DisplayID: display.DisplayID }] : [];
    });
  }, [externalIDInfo, providerDisplays]);

  const selectedProvider =
    providerSelection.sourcePath === preview.Release.SourcePath &&
    providerSelection.generation === preview.Release.Generation &&
    orderedIdentityProviders.some((item) => item.Provider === providerSelection.provider)
      ? providerSelection.provider
      : (orderedIdentityProviders[0]?.Provider ?? "");

  const selectProvider = (provider: string) => {
    setProviderSelection({
      sourcePath: preview.Release.SourcePath,
      generation: preview.Release.Generation,
      provider,
    });
  };

  const tmdbCandidates = useMemo(
    () => candidatesFromDiagnostics(preview.Diagnostics, "tmdb"),
    [preview.Diagnostics],
  );
  const imdbCandidates = useMemo(
    () => candidatesFromDiagnostics(preview.Diagnostics, "imdb"),
    [preview.Diagnostics],
  );
  const [candidatePreview, setCandidatePreview] = useState<{
    provider: "tmdb" | "imdb";
    candidate: ExternalIdentityCandidate;
  } | null>(null);

  const selectedCandidateID = (provider: "tmdb" | "imdb") => {
    const key = provider === "tmdb" ? "TMDBID" : "IMDBID";
    return Number(view.intent.identity[key] ?? identityDraft[key] ?? 0);
  };

  const applyCandidateID = (provider: "tmdb" | "imdb", candidate: ExternalIdentityCandidate) => {
    if (!candidate?.ID) return;
    const currentSelectedID = selectedCandidateID(provider);
    const key = provider === "tmdb" ? "TMDBID" : "IMDBID";
    if (currentSelectedID === candidate.ID) {
      facet.changeIdentity({ ...view.intent.identity, [key]: 0 });
      if (
        candidatePreview?.provider === provider &&
        candidatePreview.candidate.ID === candidate.ID
      ) {
        setCandidatePreview(null);
      }
      return;
    }
    setCandidatePreview({ provider, candidate });
    facet.changeIdentity({ ...view.intent.identity, [key]: candidate.ID });
  };

  useEffect(() => {
    if (!showExternalIDInputUI) {
      setCandidatePreview(null);
    }
  }, [showExternalIDInputUI]);

  useEffect(() => {
    if (!candidatePreview) return;
    const providerCandidates =
      candidatePreview.provider === "tmdb" ? tmdbCandidates : imdbCandidates;
    const stillExists = providerCandidates.some(
      (candidate) => candidate.ID === candidatePreview.candidate.ID,
    );
    if (!stillExists) {
      setCandidatePreview(null);
    }
  }, [candidatePreview, tmdbCandidates, imdbCandidates]);

  const selectedPreview = useMemo(() => {
    if (!selectedProvider) return null;
    return providerDisplays.find((item) => item.Provider === selectedProvider) || null;
  }, [providerDisplays, selectedProvider]);

  const [tvdbDisplayMode, setTVDBDisplayMode] = useState<TVDBDisplayMode>("original");

  const tvdbToggleEnabled = useMemo(() => {
    if (!selectedPreview) return false;
    return hasTVDBEnglishDisplay(selectedPreview);
  }, [selectedPreview]);

  useEffect(() => {
    if (selectedPreview?.Provider !== "tvdb") {
      setTVDBDisplayMode("original");
      return;
    }
    setTVDBDisplayMode(tvdbToggleEnabled ? "english" : "original");
  }, [selectedPreview, tvdbToggleEnabled]);

  const selectedPreviewTitle = useMemo(() => {
    if (!selectedPreview) return "";
    if (selectedPreview.Provider !== "tvdb") return selectedPreview.Summary.Title;
    const tvdb = selectedPreview.Details.TVDB;
    return pickTVDBText(
      tvdbDisplayMode,
      tvdb.Name || selectedPreview.Summary.Title,
      tvdb.NameEnglish,
      selectedPreview.Summary.Title,
    );
  }, [selectedPreview, tvdbDisplayMode]);

  const selectedPreviewOverview = useMemo(() => {
    if (!selectedPreview) return "";
    if (selectedPreview.Provider !== "tvdb") return selectedPreview.Summary.Overview;
    const tvdb = selectedPreview.Details.TVDB;
    return pickTVDBText(
      tvdbDisplayMode,
      tvdb.Overview || selectedPreview.Summary.Overview,
      tvdb.OverviewEnglish,
      selectedPreview.Summary.Overview,
    );
  }, [selectedPreview, tvdbDisplayMode]);

  const previewDetails = selectedPreview
    ? buildPreviewDetails(selectedPreview, tvdbDisplayMode)
    : [];

  const playlist = view.playlist;
  const playlistGroups = playlist.candidates.reduce<
    Array<{ discID: string; discName: string; candidates: typeof playlist.candidates }>
  >((groups, candidate) => {
    const discID = candidate.discId || "single-disc";
    const existing = groups.find((group) => group.discID === discID);
    if (existing) {
      existing.candidates = [...existing.candidates, candidate];
    } else {
      groups.push({
        discID,
        discName: candidate.discName || "Blu-ray disc",
        candidates: [candidate],
      });
    }
    return groups;
  }, []);
  const selectedPlaylistIDs = new Set(playlist.selected);
  const playlistSelectionComplete =
    playlistGroups.length > 0 &&
    playlistGroups.every((group) =>
      group.candidates.some((candidate) => selectedPlaylistIDs.has(candidate.id)),
    );
  const togglePlaylist = (playlistID: string) => {
    const selected = new Set(playlist.selected);
    if (selected.has(playlistID)) selected.delete(playlistID);
    else selected.add(playlistID);
    facet.choosePlaylists([...selected], false);
  };
  const toggleAllPlaylists = () => {
    if (playlist.useAll) {
      facet.choosePlaylists([], false);
      return;
    }
    facet.choosePlaylists(
      playlist.candidates.map((candidate) => candidate.id),
      true,
    );
  };

  return (
    <div className="flex flex-col gap-4">
      <header className="relative z-[1] max-w-[720px]">
        <p className={pageStyle.eyebrow}>upbrr</p>
        <h1>Build Release Name</h1>
        <p className={pageStyle.subtitle}>
          Build a release name and preview external metadata before you upload.
        </p>
      </header>

      {playlist.required ? (
        <section className={`${pageStyle.panel} mx-auto grid w-full max-w-2xl gap-3`}>
          <div>
            <h2>Select BDMV Playlists</h2>
            <p className="text-muted-foreground mt-1 text-sm">
              Choose playlists for the selected preparation source.
            </p>
          </div>
          {playlist.error ? <p className={pageStyle.error}>{playlist.error}</p> : null}
          {playlist.candidates.length ? (
            <div className="overflow-hidden rounded-md border border-border">
              {playlistGroups.map((group) => (
                <section key={group.discID} aria-label={group.discName}>
                  <h3 className="border-b border-border bg-muted px-3 py-2 text-sm font-semibold text-foreground">
                    {group.discName}
                  </h3>
                  {group.candidates.map((candidate) => {
                    const totalSize = (candidate.items || []).reduce(
                      (sum, item) => sum + item.size,
                      0,
                    );
                    const checkboxID = `playlist-${encodeURIComponent(candidate.id)}`;
                    return (
                      <div
                        key={candidate.id}
                        className="grid gap-1 border-b border-border px-3 py-2 last:border-b-0 hover:bg-muted"
                      >
                        <div className="flex select-none items-center gap-2">
                          <Checkbox
                            id={checkboxID}
                            checked={playlist.selected.includes(candidate.id)}
                            onCheckedChange={() => togglePlaylist(candidate.id)}
                          />
                          <label className="cursor-pointer font-semibold" htmlFor={checkboxID}>
                            {candidate.file}
                          </label>
                        </div>
                        <span className="ml-6 text-xs text-muted-foreground">
                          {formatPlaylistDuration(candidate.duration)} •{" "}
                          {candidate.items?.length || 0} files • {formatPlaylistBytes(totalSize)} •
                          Score: {candidate.score.toFixed(2)}
                        </span>
                      </div>
                    );
                  })}
                </section>
              ))}
            </div>
          ) : null}
          {playlist.candidates.length > 1 ? (
            <div className="flex flex-wrap gap-2">
              <Button type="button" onClick={toggleAllPlaylists}>
                {playlist.useAll ? "Deselect All" : "Select All Playlists"}
              </Button>
              <Button
                type="button"
                onClick={() =>
                  facet.choosePlaylists(
                    playlistGroups.map((group) => group.candidates[0].id),
                    false,
                  )
                }
              >
                Auto-Select Best Per Disc
              </Button>
            </div>
          ) : null}
          <div className="flex justify-end gap-2">
            <Button type="button" onClick={facet.cancelPlaylistSelection}>
              Back
            </Button>
            <Button
              type="button"
              variant="primary"
              disabled={!playlistSelectionComplete || playlist.status === "processing"}
              onClick={() => void facet.confirmPlaylists()}
            >
              Confirm Selection
            </Button>
          </div>
        </section>
      ) : null}

      <section
        className={`relative rounded-[var(--radius)] border border-border bg-card p-[14px] text-card-foreground shadow-[var(--shadow)] ${sourcePathHistoryOpen ? "z-40" : "z-[1]"}`}
      >
        <div className="grid gap-3">
          <div className="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-3 max-[1100px]:grid-cols-1">
            <div className="grid grid-cols-2 gap-3 max-[900px]:grid-cols-1">
              <label
                className="grid gap-1.5 text-sm text-muted-foreground"
                htmlFor="source-lookup-url"
              >
                <span>Site URL override</span>
                <input
                  id="source-lookup-url"
                  className={compactInputClass}
                  value={sourceLookupURL}
                  onChange={(event) => setSourceLookupURL(event.target.value)}
                  placeholder="Paste tracker or media URL for ID lookup"
                />
                <span className="text-xs leading-tight text-muted-foreground">
                  Metadata ID and tracker description/image lookup.
                </span>
              </label>

              <div
                className="grid gap-1.5 text-sm text-muted-foreground"
                ref={sourcePathHistoryRef}
              >
                <label htmlFor="source-path">Source path</label>
                <div className="relative">
                  <input
                    id="source-path"
                    className={compactInputClass}
                    value={path}
                    onChange={(event) => handleSourcePathChange(event.target.value)}
                    onFocus={openSourcePathHistory}
                    onClick={openSourcePathHistory}
                    onKeyDown={(event) => {
                      if (event.key === "Escape") {
                        setSourcePathHistoryOpen(false);
                      }
                    }}
                    placeholder="Select a file or folder"
                    aria-autocomplete="list"
                    aria-expanded={sourcePathHistoryOpen}
                    aria-haspopup="listbox"
                    aria-controls="source-path-history"
                  />
                  {sourcePathHistoryOpen ? (
                    <div
                      id="source-path-history"
                      className="absolute top-[calc(100%+5px)] right-0 left-0 z-30 grid max-h-[220px] gap-1 overflow-auto rounded-lg border border-foreground/10 bg-card p-1.5 shadow-[var(--shadow)]"
                      role="listbox"
                      aria-label="Source path history"
                    >
                      {sourcePathHistory.map((entry) => (
                        <button
                          key={entry.path}
                          className="w-full cursor-pointer rounded-md border-0 bg-transparent px-2 py-[7px] text-left text-foreground hover:bg-foreground/10 focus:bg-foreground/10 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring"
                          type="button"
                          role="option"
                          onMouseDown={(event) => event.preventDefault()}
                          onClick={() => selectSourcePathHistory(entry)}
                        >
                          <span className="font-mono text-[0.95rem]">{entry.path}</span>
                        </button>
                      ))}
                    </div>
                  ) : null}
                </div>
                <span className="text-xs leading-tight text-muted-foreground">
                  {discHint || "File, disc folder, or Season Pack folder."}
                </span>
              </div>
            </div>

            <div className="flex flex-wrap items-center justify-end gap-2 max-[1100px]:justify-start">
              <Button type="button" onClick={handleBrowseFile}>
                Browse file
              </Button>
              <Button type="button" onClick={handleBrowseFolder}>
                Browse folder
              </Button>
              <Button
                variant="primary"
                type="button"
                onClick={handleFetch}
                disabled={loading || (recovering && !opaqueRecovering)}
              >
                {loading ? "Fetching..." : "Fetch metadata"}
              </Button>
              {loading ? (
                <Button type="button" onClick={facet.cancelPreparation}>
                  {verification ? "Cancel verification" : "Cancel fetch"}
                </Button>
              ) : null}
              <Button
                type="button"
                onClick={() => void facet.close()}
                disabled={loading || view.activeInput.state === "empty" || recovering}
              >
                Close input
              </Button>
            </div>
            {recovering && !opaqueRecovering ? (
              <p className="text-muted-foreground col-span-full" role="status">
                Resolve the recovery action above before opening or preparing an input.
              </p>
            ) : null}
            {view.activeInput.recoveryWorkflowIDs.length ? (
              <section className="col-span-full grid gap-2" aria-label="Legacy workflow recovery">
                <p className="text-muted-foreground">
                  Resolve interrupted external effects before opening another input.
                </p>
                <div className="flex flex-wrap gap-2">
                  {view.activeInput.recoveryWorkflowIDs.map((workflowID) => (
                    <Button
                      key={workflowID}
                      type="button"
                      disabled={loading}
                      onClick={() => void facet.recoverLegacyWorkflow(workflowID)}
                    >
                      Recover workflow {workflowID}
                    </Button>
                  ))}
                </div>
              </section>
            ) : null}
            {verification ? (
              <div className="col-span-full grid gap-1" role="status" aria-live="polite">
                <p>{verification.message || "Verifying source content."}</p>
                {verification.totalBytes > 0 ? (
                  <>
                    <progress
                      aria-label="Source verification progress"
                      max={verification.totalBytes}
                      value={verification.completedBytes}
                    />
                    <p className="text-muted-foreground text-sm">
                      {verification.completedBytes.toLocaleString()} of{" "}
                      {verification.totalBytes.toLocaleString()} bytes verified
                    </p>
                  </>
                ) : (
                  <p className="text-muted-foreground text-sm">Calculating source size…</p>
                )}
              </div>
            ) : null}
          </div>
        </div>
        {view.source.discCount > 0 ? (
          <p className="text-muted-foreground" role="status">
            Prepared source: {view.source.discCount} {view.source.discType || "optical"} disc
            {view.source.discCount === 1 ? "" : "s"}
          </p>
        ) : null}
        {error ? (
          <div className="flex flex-wrap items-center gap-2">
            <p className={`error ${pageStyle.error}`}>{error}</p>
            {view.failure?.Code === "confirmation_required" &&
            view.failure.Recovery === "confirm" ? (
              <Button
                type="button"
                variant="primary"
                disabled={loading}
                onClick={() => void facet.confirmBDMVRescan()}
              >
                Confirm Blu-ray rescan
              </Button>
            ) : null}
          </div>
        ) : null}
        {(preview.Diagnostics || [])
          .filter((diagnostic) => diagnostic.Severity === "warning")
          .map((diagnostic) => (
            <p key={`${diagnostic.Code}-${diagnostic.Message}`} className="text-muted-foreground">
              {diagnostic.Message}
            </p>
          ))}
      </section>

      <section className="relative z-[1] flex flex-col gap-[19px]">
        {hasPreview ? (
          <div className="grid grid-cols-[repeat(auto-fit,minmax(240px,1fr))] gap-3 rounded-2xl border border-foreground/10 bg-card/80 p-4">
            <div>
              <p className={pageStyle.label}>Tracker used</p>
              <p className={pageStyle.value}>{preview.TrackerName || "No tracker used"}</p>
            </div>
            <div>
              <p className={pageStyle.label}>Release name</p>
              <p className={pageStyle.value}>{preview.ReleaseName || "No release name yet"}</p>
            </div>
          </div>
        ) : null}

        {hasPreview && showExternalIDInputUI && !hasResolvedPrimaryExternalID ? (
          <div className={pageStyle.panel}>
            <div className={settingsStyle.subgroup}>
              <div className={settingsStyle.title}>External ID candidates</div>
              <p className="text-muted-foreground path-helper">
                Select a candidate to copy it into ID overrides, then refresh metadata.
              </p>
              {tmdbCandidates.length === 0 && imdbCandidates.length === 0 ? (
                <p className="text-muted-foreground">
                  No TMDB/IMDB candidates available for this search.
                </p>
              ) : (
                <div className={settingsStyle.grid}>
                  <div>
                    <p className={pageStyle.label}>TMDB</p>
                    {tmdbCandidates.length === 0 ? (
                      <p className="text-muted-foreground">No TMDB candidates</p>
                    ) : (
                      <div className="flex flex-wrap gap-2">
                        {tmdbCandidates.slice(0, 5).map((candidate) => (
                          <button
                            key={`tmdb-${candidate.ID}`}
                            type="button"
                            className={`ghost ${selectedCandidateID("tmdb") === candidate.ID ? "border-ring bg-accent ring-2 ring-ring/20" : ""}`}
                            onClick={() => applyCandidateID("tmdb", candidate)}
                          >
                            {candidate.Title || "(Untitled)"}
                            {candidate.Year ? ` (${candidate.Year})` : ""}
                            {formatSimilarity(candidate.Similarity)
                              ? ` • ${formatSimilarity(candidate.Similarity)}`
                              : ""}
                          </button>
                        ))}
                      </div>
                    )}
                    {candidatePreview?.provider === "tmdb" ? (
                      <div className={settingsStyle.subgroup}>
                        <p className={pageStyle.label}>Selected TMDB candidate</p>
                        <div className="flex items-start justify-between gap-3 max-[960px]:items-center">
                          <div className="min-w-0 flex-1">
                            <p className={pageStyle.value}>
                              {candidatePreview.candidate.Title || "(Untitled)"}
                              {candidatePreview.candidate.Year
                                ? ` (${candidatePreview.candidate.Year})`
                                : ""}
                            </p>
                            <p className="text-muted-foreground">
                              {candidatePreview.candidate.Category || "Unknown category"}
                              {formatSimilarity(candidatePreview.candidate.Similarity)
                                ? ` • ${formatSimilarity(candidatePreview.candidate.Similarity)}`
                                : ""}
                            </p>
                          </div>
                          {candidatePreview.candidate.PosterURL ? (
                            <button
                              className="cursor-pointer rounded-[10px] border-0 bg-transparent p-0 leading-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-chart-2"
                              type="button"
                              onClick={() => {
                                setLightboxImage(
                                  tmdbImageURL(candidatePreview.candidate.PosterURL, "original"),
                                );
                                setLightboxAlt("TMDB candidate poster");
                              }}
                            >
                              <img
                                className="h-[84px] w-14 min-w-14 rounded-lg border border-foreground/10 object-cover"
                                src={tmdbImageURL(
                                  candidatePreview.candidate.PosterURL,
                                  "w220_and_h330_face",
                                )}
                                alt="TMDB candidate poster"
                                loading="lazy"
                              />
                            </button>
                          ) : null}
                        </div>
                        <p className="text-muted-foreground">
                          {candidatePreview.candidate.Overview || "No overview available."}
                        </p>
                      </div>
                    ) : null}
                  </div>
                  <div>
                    <p className={pageStyle.label}>IMDB</p>
                    {imdbCandidates.length === 0 ? (
                      <p className="text-muted-foreground">No IMDB candidates</p>
                    ) : (
                      <div className="flex flex-wrap gap-2">
                        {imdbCandidates.slice(0, 5).map((candidate) => (
                          <button
                            key={`imdb-${candidate.ID}`}
                            type="button"
                            className={`ghost ${selectedCandidateID("imdb") === candidate.ID ? "border-ring bg-accent ring-2 ring-ring/20" : ""}`}
                            onClick={() => applyCandidateID("imdb", candidate)}
                          >
                            {candidate.Title || "(Untitled)"}
                            {candidate.Year ? ` (${candidate.Year})` : ""}
                            {formatSimilarity(candidate.Similarity)
                              ? ` • ${formatSimilarity(candidate.Similarity)}`
                              : ""}
                          </button>
                        ))}
                      </div>
                    )}
                    {candidatePreview?.provider === "imdb" ? (
                      <div className={settingsStyle.subgroup}>
                        <p className={pageStyle.label}>Selected IMDB candidate</p>
                        <div className="flex items-start justify-between gap-3 max-[960px]:items-center">
                          <div className="min-w-0 flex-1">
                            <p className={pageStyle.value}>
                              {candidatePreview.candidate.Title || "(Untitled)"}
                              {candidatePreview.candidate.Year
                                ? ` (${candidatePreview.candidate.Year})`
                                : ""}
                            </p>
                            <p className="text-muted-foreground">
                              {candidatePreview.candidate.Category || "Unknown category"}
                              {formatSimilarity(candidatePreview.candidate.Similarity)
                                ? ` • ${formatSimilarity(candidatePreview.candidate.Similarity)}`
                                : ""}
                            </p>
                          </div>
                          {candidatePreview.candidate.PosterURL ? (
                            <button
                              className="cursor-pointer rounded-[10px] border-0 bg-transparent p-0 leading-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-chart-2"
                              type="button"
                              onClick={() => {
                                setLightboxImage(candidatePreview.candidate.PosterURL);
                                setLightboxAlt("IMDB candidate poster");
                              }}
                            >
                              <img
                                className="h-[84px] w-14 min-w-14 rounded-lg border border-foreground/10 object-cover"
                                src={imdbPosterURL(candidatePreview.candidate.PosterURL, 190)}
                                alt="IMDB candidate poster"
                                loading="lazy"
                              />
                            </button>
                          ) : null}
                        </div>
                        <p className="text-muted-foreground">
                          {candidatePreview.candidate.Overview || "No overview available."}
                        </p>
                      </div>
                    ) : null}
                  </div>
                </div>
              )}
              <div className="flex justify-end">
                <button
                  className="primary"
                  type="button"
                  onClick={handleRefresh}
                  disabled={refreshDisabled}
                >
                  {loading ? "Refreshing..." : "Refresh metadata"}
                </button>
              </div>
            </div>
          </div>
        ) : null}

        <div className="grid gap-[9px]">
          {hasPreview ? (
            <details className={`tracker-dropdown ${editDropdownClass}`}>
              <summary className={editDropdownSummaryClass}>
                <span>Select Trackers</span>
                <span className="tracker-summary-count inline-flex min-w-12 items-center justify-center rounded-full border border-current px-2 py-0.5 text-[0.76rem] font-bold tracking-[0.03em] text-inherit">
                  {selectedTrackerCount}/{trackerUploadItems.length}
                </span>
              </summary>
              <div className="mt-3 grid gap-[9px]">
                <fieldset className="m-0 grid min-w-0 gap-2 border-0 p-0">
                  <legend className="sr-only">Trackers to upload</legend>
                  {trackerUploadItems.length === 0 ? (
                    <p className="text-muted-foreground">No configured tracker entries found.</p>
                  ) : (
                    <>
                      <div className="mb-2 flex flex-wrap gap-2">
                        <Button
                          type="button"
                          onClick={() => setAllTrackersSelected(true)}
                          disabled={allTrackersSelected}
                        >
                          Select all
                        </Button>
                        <Button
                          type="button"
                          onClick={() => setAllTrackersSelected(false)}
                          disabled={selectedTrackerCount === 0}
                        >
                          Deselect all
                        </Button>
                      </div>
                      <div className="grid w-full grid-cols-[repeat(auto-fill,minmax(min(100%,180px),1fr))] gap-2">
                        {trackerUploadItems.map((tracker) => {
                          const iconSrc = trackerIconFor(trackerIconSrcByName, tracker.name);
                          return (
                            <PillCheckbox
                              aria-label={tracker.name}
                              className="w-full"
                              key={tracker.name}
                              checked={Boolean(releasePageTrackerSelection[tracker.name])}
                              onCheckedChange={(checked) =>
                                setReleasePageTrackerSelection((prev) => ({
                                  ...prev,
                                  [tracker.name]: checked,
                                }))
                              }
                            >
                              <span className="flex items-center gap-1.5">
                                <TrackerIconImage
                                  tracker={tracker.name}
                                  iconSrc={iconSrc}
                                  enabled={useFavicons}
                                />
                                {faviconOnly && useFavicons ? null : tracker.name}
                              </span>
                            </PillCheckbox>
                          );
                        })}
                      </div>
                    </>
                  )}
                </fieldset>
              </div>
            </details>
          ) : null}
          {showReleaseDetails ? (
            <p className="helper max-w-[720px]">
              Review release facts, source options, and selected tracker input.
            </p>
          ) : null}
          {showReleaseDetails && view.correctionDirty ? (
            <p className="text-sm font-medium text-primary" role="status">
              Metadata changes are pending. Use {hasPreview ? "Refresh metadata" : "Retry metadata"}{" "}
              to apply them to the release and dependent metadata.
            </p>
          ) : null}
          {showReleaseDetails ? (
            <details className={editDropdownClass}>
              <summary className={editDropdownSummaryClass}>Edit Release Details</summary>
              <div className="mt-3 grid gap-[9px]">
                <InputCorrectionEditor facet={facet} />
                <div className="flex justify-end">
                  {hasPreview ? (
                    <button
                      className="ghost"
                      type="button"
                      onClick={handleResetMetadata}
                      disabled={loading}
                    >
                      {metadataResetting ? "Resetting..." : "Reset data + refresh"}
                    </button>
                  ) : null}
                  <button
                    className="primary"
                    type="button"
                    onClick={handleRefresh}
                    disabled={refreshDisabled}
                  >
                    {loading
                      ? hasPreview
                        ? "Refreshing..."
                        : "Retrying..."
                      : hasPreview
                        ? "Refresh metadata"
                        : "Retry metadata"}
                  </button>
                </div>
              </div>
            </details>
          ) : null}
        </div>

        {view.preview ? (
          <details className={editDropdownClass} data-mediainfo-preview>
            <summary
              className={`${disclosureSummaryClass} border border-foreground/20 bg-card text-foreground`}
            >
              {isBlurayDisc ? "BDInfo Preview" : "MediaInfo Preview"}
            </summary>
            <div className="mt-3 min-w-0">
              {isBlurayDisc ? (
                view.release?.Disc.Summary ? (
                  <pre className="max-w-full overflow-x-auto whitespace-pre-wrap break-words text-sm text-foreground">
                    {view.release.Disc.Summary}
                  </pre>
                ) : (
                  <p className="text-muted-foreground">
                    No BDInfo summary is available for this source.
                  </p>
                )
              ) : preview.Display.MediaInfoHTML ? (
                <RenderedDescription html={preview.Display.MediaInfoHTML} />
              ) : (
                <p className="text-muted-foreground">
                  No MediaInfo report is available for this source.
                </p>
              )}
            </div>
          </details>
        ) : null}

        <div
          className={`grid grid-cols-[minmax(260px,320px)_minmax(0,1fr)] gap-[19px] transition-[opacity,transform] duration-500 max-[960px]:grid-cols-1 ${hasPreview ? "translate-y-0 opacity-100" : "translate-y-3 opacity-0"}`}
        >
          <div className="rounded-[18px] border border-foreground/10 bg-card/80 p-4">
            <h2>External IDs</h2>
            {orderedIdentityProviders.length === 0 ? (
              <p className="text-muted-foreground">No external metadata details found.</p>
            ) : (
              orderedIdentityProviders.map((item) => (
                <button
                  key={item.Provider}
                  className={`mb-[9px] grid w-full gap-[5px] rounded-[14px] border bg-card/90 p-[11px] text-left text-foreground ${selectedProvider === item.Provider ? "active border-ring ring-2 ring-ring/20" : "border-transparent"}`}
                  type="button"
                  onClick={() => selectProvider(item.Provider)}
                >
                  <span className="text-xs tracking-[0.1em] text-muted-foreground uppercase">
                    {formatProvider(item.Provider)}
                  </span>
                  <span className={pageStyle.value}>{item.DisplayID}</span>
                  <span className="text-[0.8rem] text-muted-foreground">Source: {item.Source}</span>
                </button>
              ))
            )}
          </div>

          <div className="rounded-[18px] border border-foreground/10 bg-card/80 p-4">
            <div className="mb-3 flex items-start justify-between gap-3">
              <div>
                <h2>Preview</h2>
              </div>
              {selectedPreview?.Provider === "tvdb" && tvdbToggleEnabled ? (
                <fieldset className="preview-language-toggle" aria-label="TVDB language display">
                  <button
                    className={`ghost ${tvdbDisplayMode === "original" ? "toggle-active" : ""}`}
                    type="button"
                    onClick={() => setTVDBDisplayMode("original")}
                  >
                    Original
                  </button>
                  <button
                    className={`ghost ${tvdbDisplayMode === "english" ? "toggle-active" : ""}`}
                    type="button"
                    onClick={() => setTVDBDisplayMode("english")}
                  >
                    English
                  </button>
                </fieldset>
              ) : null}
            </div>
            {selectedPreview ? (
              <div className="grid grid-cols-[minmax(0,1fr)_240px] gap-4 max-[960px]:grid-cols-1">
                <div>
                  <p className="mb-1.5 text-[1.4rem] font-semibold">
                    {selectedPreviewTitle || "Untitled"}
                  </p>
                  <p className="mb-3 text-primary-text">
                    {selectedPreview.Summary.Year ? `${selectedPreview.Summary.Year}` : ""}
                  </p>
                  <p className="text-muted-foreground leading-[1.6]">
                    {selectedPreviewOverview || "No overview available."}
                  </p>
                  <PreviewDetailsList items={previewDetails} />
                </div>
                <div className="grid content-start gap-[9px]">
                  {selectedPreview.URL ? (
                    <a
                      className="text-primary-text underline underline-offset-2"
                      href={selectedPreview.URL}
                      target="_blank"
                      rel="noopener noreferrer"
                    >
                      Open {formatProvider(selectedPreview.Provider)}
                    </a>
                  ) : null}
                  {selectedPreview.Summary.PosterURL ? (
                    <button
                      className="cursor-pointer rounded-[14px] border-0 bg-transparent p-0 leading-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                      type="button"
                      aria-label={`Expand ${formatProvider(selectedPreview.Provider)} poster`}
                      onClick={() => {
                        setLightboxImage(selectedPreview.Summary.PosterURL);
                        setLightboxAlt(`${formatProvider(selectedPreview.Provider)} poster`);
                      }}
                    >
                      <img
                        className="h-auto w-full rounded-[14px] border border-foreground/10"
                        src={posterPreviewURL(selectedPreview)}
                        srcSet={
                          selectedPreview.Provider === "imdb"
                            ? [
                                `${imdbPosterURL(selectedPreview.Summary.PosterURL, 190)} 190w`,
                                `${imdbPosterURL(selectedPreview.Summary.PosterURL, 285)} 285w`,
                                `${imdbPosterURL(selectedPreview.Summary.PosterURL, 380)} 380w`,
                              ].join(", ")
                            : undefined
                        }
                        sizes={
                          selectedPreview.Provider === "imdb"
                            ? "(max-width: 960px) 100vw, 240px"
                            : undefined
                        }
                        alt={`${formatProvider(selectedPreview.Provider)} poster`}
                        loading="lazy"
                      />
                    </button>
                  ) : null}
                  {selectedPreview.Summary.BackdropURL &&
                  (selectedPreview.Provider !== "tvmaze" ||
                    selectedPreview.Summary.BackdropURL !== selectedPreview.Summary.PosterURL) ? (
                    <button
                      className="cursor-pointer rounded-[14px] border-0 bg-transparent p-0 leading-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                      type="button"
                      aria-label={`Expand ${formatProvider(selectedPreview.Provider)} backdrop`}
                      onClick={() => {
                        setLightboxImage(selectedPreview.Summary.BackdropURL);
                        setLightboxAlt(`${formatProvider(selectedPreview.Provider)} backdrop`);
                      }}
                    >
                      <img
                        className="w-full rounded-[14px] border border-foreground/10"
                        src={
                          selectedPreview.Provider === "tmdb"
                            ? tmdbImageURL(selectedPreview.Summary.BackdropURL, "w500")
                            : selectedPreview.Provider === "tvmaze"
                              ? selectedPreview.Details.TVmaze.BackdropMedium ||
                                selectedPreview.Summary.BackdropURL
                              : selectedPreview.Summary.BackdropURL
                        }
                        alt={`${formatProvider(selectedPreview.Provider)} backdrop`}
                        loading="lazy"
                      />
                    </button>
                  ) : null}
                  {selectedPreview.Provider === "tmdb" && selectedPreview.Details.TMDB.Logo ? (
                    <button
                      className="cursor-pointer rounded-[14px] border-0 bg-transparent p-0 leading-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                      type="button"
                      aria-label="Expand TMDB title logo"
                      onClick={() => {
                        setLightboxImage(selectedPreview.Details.TMDB.Logo);
                        setLightboxAlt("TMDB title logo");
                      }}
                    >
                      <img
                        className="w-full rounded-[14px] border border-foreground/10"
                        src={tmdbImageURL(selectedPreview.Details.TMDB.Logo, "w300")}
                        alt="TMDB title logo"
                        loading="lazy"
                      />
                    </button>
                  ) : null}
                </div>
              </div>
            ) : (
              <p className="text-muted-foreground">Select an external ID to preview.</p>
            )}
          </div>
        </div>
      </section>
    </div>
  );
}
