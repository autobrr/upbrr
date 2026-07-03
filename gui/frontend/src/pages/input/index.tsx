// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useMemo, useRef, useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import { Button } from "../../components/ui/button";
import { PillCheckbox } from "../../components/ui/checkbox";
import { Switch } from "../../components/ui/switch";
import { TrackerIconImage } from "../../components/ui/tracker-icon";
import type { TrackerIconCache } from "../../hooks/useTrackerIcons";
import { trackerIconFor } from "../../hooks/useTrackerIcons";
import type {
  DetailBlock,
  DetailItem,
  ExternalIDCandidate,
  ExternalIDInfo,
  ExternalPreview,
  IMDBAKA,
  IMDBEditionDetail,
  IMDBEpisode,
  IMDBPerson,
  IMDBReleaseDate,
  IMDBSeasonSummary,
  MetadataPreview,
  MetadataProgressUpdate,
  ReleaseNameEditState,
  ReleaseNameTouchedState,
  TMDBCompany,
  TMDBCountry,
  TMDBNetwork,
  TrackerUploadItem,
} from "../../types";
import type { SourcePathHistoryEntry } from "../../utils/inputHistory";

const compactInputClass =
  "h-8 rounded-md border border-white/10 bg-slate-950/45 px-2.5 text-sm text-[var(--text)] outline-none transition placeholder:text-[var(--muted)] focus:border-[var(--accent-2)] focus:ring-2 focus:ring-[rgba(53,194,193,0.18)]";

const formatProvider = (value: string) => value.toUpperCase();

const formatID = (provider: string, id: number) => {
  if (!id) return "";
  if (provider === "imdb") return `tt${id.toString().padStart(7, "0")}`;
  return id.toString();
};

const providerOrder = ["tmdb", "imdb", "tvdb", "tvmaze"] as const;

const filterAndOrderExternalIDs = (info: ExternalIDInfo[]) => {
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

const formatNumber = (value: number) => (value ? value.toString() : "");

const formatSimilarity = (value: number) => {
  if (!value) return "";
  return `${Math.round(value * 100)}%`;
};

const formatBoolean = (value: boolean) => (value ? "Yes" : "No");

const tmdbLogoBaseURL = "https://image.tmdb.org/t/p/original/";
const tmdbLogoSize = 64;

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

type TVDBDisplayMode = "original" | "english";

const isEnglishLanguageValue = (value: string) => {
  const normalized = value.trim().toLowerCase().replaceAll("_", "-");
  if (!normalized) return false;
  if (normalized === "en" || normalized === "eng" || normalized === "english") return true;
  return normalized.startsWith("en-");
};

const hasTVDBEnglishDisplay = (preview: ExternalPreview) => {
  if (preview.Provider !== "tvdb") return false;
  const tvdb = preview.TVDB;
  if (!tvdb) return false;
  const originalLanguage = tvdb.OriginalLanguage || preview.OriginalLanguage;
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
  preview: ExternalPreview,
  tvdbDisplayMode: TVDBDisplayMode,
): DetailItem[] => {
  const baseID: DetailItem = {
    label: `${formatProvider(preview.Provider)} ID`,
    value: formatID(preview.Provider, preview.ID),
    mono: true,
  };

  if (preview.Provider === "imdb") {
    const imdb = preview.IMDB;
    return [
      baseID,
      { label: "IMDb URL", value: imdb?.IMDbURL ?? "", mono: true },
      { label: "AKA", value: imdb?.AKA ?? "" },
      { label: "Type", value: formatIMDBType(imdb?.Type ?? preview.IMDBType) },
      { label: "Year", value: formatNumber(imdb?.Year ?? preview.Year) },
      { label: "End year", value: formatNumber(imdb?.EndYear ?? 0) },
      { label: "TV year", value: formatNumber(imdb?.TVYear ?? 0) },
      { label: "Original language", value: imdb?.OriginalLanguage ?? "" },
      { label: "Country", value: imdb?.Country ?? preview.Country },
      { label: "Country list", value: imdb?.CountryList ?? "" },
      {
        label: "Rating",
        value: formatRating(
          imdb?.Rating ?? preview.Rating,
          imdb?.RatingCount ?? preview.RatingCount,
        ),
      },
      { label: "Rating text", value: imdb?.RatingText ?? "" },
      { label: "Rating count", value: formatNumber(imdb?.RatingCount ?? preview.RatingCount) },
      { label: "Runtime", value: formatRuntime(imdb?.RuntimeMinutes ?? preview.RuntimeMinutes) },
      { label: "Runtime text", value: imdb?.RuntimeText ?? "" },
      { label: "Editions", value: formatCommaList(imdb?.Editions) },
      { label: "Edition details", value: formatEditionDetails(imdb?.EditionDetails) },
      { label: "Genres", value: imdb?.Genres ?? preview.Genres },
      { label: "Sound mixes", value: formatNameList(imdb?.SoundMixes) },
      { label: "Directors", value: formatPeopleList(imdb?.Directors) },
      { label: "Creators", value: formatPeopleList(imdb?.Creators) },
      { label: "Writers", value: formatPeopleList(imdb?.Writers) },
      { label: "Stars", value: formatPeopleList(imdb?.Stars) },
      { label: "AKA entries", value: formatIMDBAkas(imdb?.Akas) },
      { label: "Season summary", value: formatSeasonsSummary(imdb?.SeasonsSummary) },
      { label: "Episodes", value: formatEpisodes(imdb?.Episodes) },
      { label: "Cover URL", value: imdb?.Cover ?? preview.PosterURL, mono: true },
    ].filter((item) => item.value || (item.blocks && item.blocks.length > 0));
  }

  if (preview.Provider === "tmdb") {
    const tmdb = preview.TMDB;
    return [
      baseID,
      { label: "IMDB ID", value: formatID("imdb", tmdb?.IMDBID ?? preview.IMDBID), mono: true },
      { label: "TVDB ID", value: formatNumber(tmdb?.TVDBID ?? preview.TVDBID), mono: true },
      { label: "Original title", value: tmdb?.OriginalTitle ?? preview.OriginalTitle },
      { label: "Type", value: tmdb?.TMDBType ?? preview.TMDBType },
      { label: "Category", value: tmdb?.Category ?? preview.Category },
      { label: "Year", value: formatNumber(tmdb?.Year ?? preview.Year) },
      { label: "Release date", value: tmdb?.ReleaseDate ?? preview.ReleaseDate },
      { label: "First air date", value: tmdb?.FirstAirDate ?? preview.FirstAirDate },
      { label: "Last air date", value: tmdb?.LastAirDate ?? preview.LastAirDate },
      { label: "Runtime", value: formatRuntime(tmdb?.Runtime ?? preview.Runtime) },
      { label: "Genres", value: tmdb?.Genres ?? preview.Genres },
      { label: "Genre IDs", value: tmdb?.GenreIDs ?? "" },
      { label: "Keywords", value: tmdb?.Keywords ?? preview.Keywords },
      { label: "YouTube", value: tmdb?.YouTube ?? preview.YouTube },
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
      { label: "Poster URL", value: tmdb?.Poster ?? preview.PosterURL, mono: true },
      { label: "Poster path", value: tmdb?.TMDBPosterPath ?? "", mono: true },
      { label: "Backdrop URL", value: tmdb?.Backdrop ?? preview.BackdropURL, mono: true },
      { label: "Logo URL", value: tmdb?.Logo ?? "", mono: true },
      { label: "Logo name", value: tmdb?.TMDBLogo ?? "" },
      { label: "Original language", value: tmdb?.OriginalLanguage ?? preview.OriginalLanguage },
      { label: "Anime", value: tmdb ? formatBoolean(tmdb.Anime) : "" },
      { label: "MAL ID", value: formatNumber(tmdb?.MALID ?? 0), mono: true },
      { label: "Demographic", value: tmdb?.Demographic ?? "" },
      { label: "Retrieved AKA", value: tmdb?.RetrievedAKA ?? "" },
      { label: "IMDb mismatch", value: tmdb ? formatBoolean(tmdb.IMDbMismatch) : "" },
      { label: "Mismatched IMDb ID", value: formatNumber(tmdb?.MismatchedIMDbID ?? 0), mono: true },
    ].filter((item) => item.value || (item.blocks && item.blocks.length > 0));
  }

  if (preview.Provider === "tvdb") {
    const tvdb = preview.TVDB;
    const displayName = pickTVDBText(
      tvdbDisplayMode,
      tvdb?.Name ?? preview.Title,
      tvdb?.NameEnglish ?? "",
      preview.Title,
    );
    const displayOverview = pickTVDBText(
      tvdbDisplayMode,
      tvdb?.Overview ?? preview.Overview,
      tvdb?.OverviewEnglish ?? "",
      preview.Overview,
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
      { label: "Type", value: tvdb?.Type ?? preview.TMDBType },
      { label: "Status", value: tvdb?.Status ?? "" },
      { label: "Year", value: formatNumber(tvdb?.Year ?? preview.Year) },
      { label: "First aired", value: tvdb?.FirstAired ?? preview.FirstAirDate },
      { label: "Genres", value: tvdb?.Genres ?? preview.Genres },
      { label: "Network", value: tvdb?.Network ?? "" },
      { label: "Origin country", value: tvdb?.OriginalCountry ?? preview.Country },
      { label: "Original language", value: tvdb?.OriginalLanguage ?? preview.OriginalLanguage },
      { label: "Aliases", value: formatCommaList(tvdb?.Aliases) },
      { label: "Episode", value: episodeTag },
      { label: "Episode name", value: displayEpisodeName },
      { label: "Episode aired", value: tvdb?.EpisodeAired ?? "" },
      { label: "Episode overview", value: displayEpisodeOverview },
      { label: "Overview", value: displayOverview },
      { label: "Poster URL", value: tvdb?.Poster ?? preview.PosterURL, mono: true },
    ].filter((item) => item.value || (item.blocks && item.blocks.length > 0));
  }

  if (preview.Provider === "tvmaze") {
    const tvmaze = preview.TVmaze;
    const network = tvmaze?.Network ?? "";
    const webChannel = tvmaze?.WebChannel ?? "";
    const networkText =
      network && tvmaze?.NetworkCountry ? `${network} - ${tvmaze.NetworkCountry}` : network;
    const webChannelText =
      webChannel && tvmaze?.WebCountry ? `${webChannel} - ${tvmaze.WebCountry}` : webChannel;
    return [
      baseID,
      { label: "IMDB ID", value: formatID("imdb", tvmaze?.IMDBID ?? preview.IMDBID), mono: true },
      { label: "TVDB ID", value: formatNumber(tvmaze?.TVDBID ?? preview.TVDBID), mono: true },
      { label: "Name", value: tvmaze?.Name ?? preview.Title },
      { label: "Type", value: tvmaze?.Type ?? preview.TMDBType },
      { label: "Status", value: tvmaze?.Status ?? "" },
      { label: "Year", value: formatNumber(preview.Year) },
      { label: "Premiered", value: tvmaze?.Premiered ?? preview.Premiered },
      { label: "Ended", value: tvmaze?.Ended ?? "" },
      { label: "Genres", value: tvmaze?.Genres ?? preview.Genres },
      { label: "Language", value: tvmaze?.Language ?? preview.OriginalLanguage },
      { label: "Country", value: tvmaze?.Country ?? preview.Country },
      { label: "Runtime", value: formatRuntime(tvmaze?.Runtime ?? preview.Runtime) },
      { label: "Average runtime", value: formatRuntime(tvmaze?.AverageRuntime ?? 0) },
      {
        label: "Rating",
        value: formatRating(
          tvmaze?.Rating ?? preview.Rating,
          tvmaze?.Weight ?? preview.RatingCount,
        ),
      },
      { label: "Score", value: formatNumber(tvmaze?.Weight ?? preview.RatingCount) },
      { label: "Network", value: networkText },
      { label: "Web channel", value: webChannelText },
      { label: "Official site", value: tvmaze?.OfficialSite ?? "", mono: true },
      { label: "Overview", value: tvmaze?.Summary ?? preview.Overview },
      { label: "Poster URL", value: tvmaze?.Poster ?? preview.PosterURL, mono: true },
      { label: "Poster medium", value: tvmaze?.PosterMedium ?? "", mono: true },
      { label: "Backdrop URL", value: tvmaze?.Backdrop ?? preview.BackdropURL, mono: true },
      { label: "Backdrop medium", value: tvmaze?.BackdropMedium ?? "", mono: true },
      { label: "Network logo", value: tvmaze?.NetworkLogo ?? "", mono: true },
      { label: "Web logo", value: tvmaze?.WebLogo ?? "", mono: true },
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

type OverrideState<T> = {
  overrides: T;
  dirty: boolean;
  invalid: boolean;
};

type IDEdits = {
  tmdb: string;
  imdb: string;
  tvdb: string;
  tvmaze: string;
};

type Props = Readonly<{
  path: string;
  handleSourcePathChange: (path: string) => void;
  sourcePathHistory: SourcePathHistoryEntry[];
  handleSourcePathHistorySelect: (entry: SourcePathHistoryEntry) => void;
  sourceLookupURL: string;
  setSourceLookupURL: Dispatch<SetStateAction<string>>;
  browseAvailable: boolean;
  handleBrowseFile: () => void;
  handleBrowseFolder: () => void;
  handleFetch: () => void;
  handleRefresh: () => void;
  handleResetMetadata: () => void;
  loading: boolean;
  metadataResetting: boolean;
  metadataProgressActive: boolean;
  metadataProgressUpdates: MetadataProgressUpdate[];
  error: string;
  preview: MetadataPreview;
  trackerUploadItems: TrackerUploadItem[];
  releasePageTrackerSelection: Record<string, boolean>;
  setReleasePageTrackerSelection: Dispatch<SetStateAction<Record<string, boolean>>>;
  idEdits: IDEdits;
  setIdEdits: Dispatch<SetStateAction<IDEdits>>;
  releaseEdits: ReleaseNameEditState;
  setReleaseEdits: Dispatch<SetStateAction<ReleaseNameEditState>>;
  markReleaseTouched: (key: keyof ReleaseNameTouchedState) => void;
  idOverrideState: OverrideState<Record<string, unknown>>;
  releaseOverrideState: OverrideState<Record<string, unknown>>;
  showExternalIDInputUI: boolean;
  refreshDisabled: boolean;
  selectedProvider: string;
  setSelectedProvider: Dispatch<SetStateAction<string>>;
  setLightboxImage: Dispatch<SetStateAction<string>>;
  setLightboxAlt: Dispatch<SetStateAction<string>>;
  runDebug: boolean;
  setRunDebug: Dispatch<SetStateAction<boolean>>;
  runLogLevel: string;
  setRunLogLevel: Dispatch<SetStateAction<string>>;
  runLogLevelTouched: boolean;
  setRunLogLevelTouched: Dispatch<SetStateAction<boolean>>;
  useFavicons?: boolean;
  faviconOnly?: boolean;
  trackerIconSrcByName: TrackerIconCache;
}>;

export default function InputPage(props: Props) {
  const {
    path,
    handleSourcePathChange,
    sourcePathHistory,
    handleSourcePathHistorySelect,
    sourceLookupURL,
    setSourceLookupURL,
    browseAvailable,
    handleBrowseFile,
    handleBrowseFolder,
    handleFetch,
    handleRefresh,
    handleResetMetadata,
    loading,
    metadataResetting,
    metadataProgressActive,
    metadataProgressUpdates,
    error,
    preview,
    trackerUploadItems,
    releasePageTrackerSelection,
    setReleasePageTrackerSelection,
    idEdits,
    setIdEdits,
    releaseEdits,
    setReleaseEdits,
    markReleaseTouched,
    idOverrideState,
    releaseOverrideState,
    showExternalIDInputUI,
    refreshDisabled,
    selectedProvider,
    setSelectedProvider,
    setLightboxImage,
    setLightboxAlt,
    runDebug,
    setRunDebug,
    runLogLevel,
    setRunLogLevel,
    runLogLevelTouched,
    setRunLogLevelTouched,
    useFavicons = true,
    faviconOnly = false,
    trackerIconSrcByName,
  } = props;

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
    handleSourcePathHistorySelect(entry);
  };

  const hasPreview =
    preview.ReleaseName || (preview.ExternalIDInfo && preview.ExternalIDInfo.length > 0);
  const isTVEpisodePreview = (preview.ExternalIDs?.Category || "").trim().toUpperCase() === "TV";
  const hasResolvedPrimaryExternalID =
    (preview.ExternalIDs?.TMDBID || 0) > 0 || (preview.ExternalIDs?.IMDBID || 0) > 0;
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

  const orderedExternalIDs = useMemo(
    () => filterAndOrderExternalIDs(preview.ExternalIDInfo || []),
    [preview.ExternalIDInfo],
  );

  const tmdbCandidates = useMemo(
    () => preview.ExternalIDCandidates?.TMDB || [],
    [preview.ExternalIDCandidates?.TMDB],
  );
  const imdbCandidates = useMemo(
    () => preview.ExternalIDCandidates?.IMDB || [],
    [preview.ExternalIDCandidates?.IMDB],
  );
  const [candidatePreview, setCandidatePreview] = useState<{
    provider: "tmdb" | "imdb";
    candidate: ExternalIDCandidate;
  } | null>(null);

  const selectedCandidateID = (provider: "tmdb" | "imdb") => {
    if (provider === "tmdb") {
      const value = idEdits.tmdb.trim();
      if (!value || !/^\d+$/.test(value)) return 0;
      return Number(value);
    }
    const normalized = idEdits.imdb.trim().replace(/^tt/i, "");
    if (!normalized || !/^\d+$/.test(normalized)) return 0;
    return Number(normalized);
  };

  const applyCandidateID = (provider: "tmdb" | "imdb", candidate: ExternalIDCandidate) => {
    if (!candidate?.ID) return;
    const currentSelectedID = selectedCandidateID(provider);
    if (currentSelectedID === candidate.ID) {
      if (provider === "tmdb") {
        setIdEdits((prev) => ({ ...prev, tmdb: "" }));
      } else {
        setIdEdits((prev) => ({ ...prev, imdb: "" }));
      }
      if (
        candidatePreview?.provider === provider &&
        candidatePreview.candidate.ID === candidate.ID
      ) {
        setCandidatePreview(null);
      }
      return;
    }
    setCandidatePreview({ provider, candidate });
    if (provider === "tmdb") {
      setIdEdits((prev) => ({ ...prev, tmdb: candidate.ID.toString() }));
      return;
    }
    setIdEdits((prev) => ({ ...prev, imdb: `tt${candidate.ID.toString().padStart(7, "0")}` }));
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
    return (
      (preview.ExternalPreview || []).find((item) => item.Provider === selectedProvider) || null
    );
  }, [preview.ExternalPreview, selectedProvider]);

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
    if (selectedPreview.Provider !== "tvdb") return selectedPreview.Title;
    const tvdb = selectedPreview.TVDB;
    return pickTVDBText(
      tvdbDisplayMode,
      tvdb?.Name ?? selectedPreview.Title,
      tvdb?.NameEnglish ?? "",
      selectedPreview.Title,
    );
  }, [selectedPreview, tvdbDisplayMode]);

  const selectedPreviewOverview = useMemo(() => {
    if (!selectedPreview) return "";
    if (selectedPreview.Provider !== "tvdb") return selectedPreview.Overview;
    const tvdb = selectedPreview.TVDB;
    return pickTVDBText(
      tvdbDisplayMode,
      tvdb?.Overview ?? selectedPreview.Overview,
      tvdb?.OverviewEnglish ?? "",
      selectedPreview.Overview,
    );
  }, [selectedPreview, tvdbDisplayMode]);

  const previewDetails = selectedPreview
    ? buildPreviewDetails(selectedPreview, tvdbDisplayMode)
    : [];

  // Keep this aligned with the backend FetchMetadataPreview phase emissions so
  // progress updates advance without skipping backend work, including fallback retries.
  const metadataPhaseOrder = [
    { key: "prepare", label: "Prepare metadata" },
    { key: "tracker-data", label: "Get tracker data" },
    { key: "tracker-data-fallback", label: "Retry tracker data" },
    { key: "mediainfo-ids", label: "Apply MediaInfo IDs" },
    { key: "arr", label: "Apply Sonarr/Radarr data" },
    { key: "external-ids", label: "Resolve external IDs" },
    { key: "media-details", label: "Apply media details" },
    { key: "tracker-claims", label: "Check tracker claims" },
    { key: "complete", label: "Complete" },
  ];

  const latestMetadataPhase = useMemo(() => {
    const next = new Map<string, MetadataProgressUpdate>();
    metadataProgressUpdates.forEach((entry) => {
      if (!entry?.phase) {
        return;
      }
      next.set(entry.phase, entry);
    });
    return next;
  }, [metadataProgressUpdates]);

  const metadataStatusLabel = (status: string) => {
    if (status === "running") return "Running";
    if (status === "completed") return "Done";
    if (status === "failed") return "Failed";
    return "Pending";
  };

  return (
    <div className="content-stack">
      <header className="hero">
        <p className="eyebrow">upbrr</p>
        <h1>Build Release Name</h1>
        <p className="subtitle">
          Build a release name and preview external metadata before you upload.
        </p>
      </header>

      <section
        className={`panel input-source-panel${
          sourcePathHistoryOpen ? " input-source-panel--history-open" : ""
        }`}
      >
        <div className="grid gap-3">
          <div className="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-3 max-[1100px]:grid-cols-1">
            <div className="grid grid-cols-2 gap-3 max-[900px]:grid-cols-1">
              <label
                className="grid gap-1.5 text-sm text-[var(--muted)]"
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
                <span className="text-xs leading-tight text-[var(--muted)]">
                  Metadata ID and tracker description/image lookup.
                </span>
              </label>

              <div className="grid gap-1.5 text-sm text-[var(--muted)]" ref={sourcePathHistoryRef}>
                <label htmlFor="source-path">Source path</label>
                <div className="source-path-input-shell source-path-input-shell--drop-target">
                  <input
                    id="source-path"
                    className={`${compactInputClass} source-path-input`}
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
                      className="source-path-history"
                      role="listbox"
                      aria-label="Source path history"
                    >
                      {sourcePathHistory.map((entry) => (
                        <button
                          key={entry.path}
                          className="source-path-history__item"
                          type="button"
                          role="option"
                          onMouseDown={(event) => event.preventDefault()}
                          onClick={() => selectSourcePathHistory(entry)}
                        >
                          <span className="mono">{entry.path}</span>
                        </button>
                      ))}
                    </div>
                  ) : null}
                </div>
                <span className="text-xs leading-tight text-[var(--muted)]">
                  {discHint || "File, disc folder, or Season Pack folder."}
                </span>
              </div>
            </div>

            <div className="flex flex-wrap items-center justify-end gap-2 max-[1100px]:justify-start">
              {browseAvailable ? (
                <>
                  <Button type="button" onClick={handleBrowseFile}>
                    Browse file
                  </Button>
                  <Button type="button" onClick={handleBrowseFolder}>
                    Browse folder
                  </Button>
                </>
              ) : null}
              <Button variant="primary" type="button" onClick={handleFetch} disabled={loading}>
                {loading ? "Fetching..." : "Fetch metadata"}
              </Button>
            </div>
          </div>

          {!browseAvailable ? (
            <p className="m-0 text-xs text-[var(--muted)]">
              Native browse is only available from localhost. Remote sessions must enter the server
              path manually.
            </p>
          ) : null}

          <div className="flex flex-wrap items-center gap-3 rounded-lg border border-white/10 bg-white/[0.035] px-3 py-2">
            <span className="text-sm font-semibold text-[var(--text)]">Run options</span>
            <div className="inline-flex items-center gap-2 text-sm text-[var(--text)]">
              <Switch
                aria-label="Enable debug run"
                checked={runDebug}
                onChange={(event) => setRunDebug(event.target.checked)}
              />
              <span>Debug run</span>
            </div>
            <label
              className="inline-flex items-center gap-2 text-sm text-[var(--muted)]"
              htmlFor="run-log-level"
            >
              <span>Log level</span>
              <select
                id="run-log-level"
                className={compactInputClass}
                value={runLogLevel}
                onChange={(event) => {
                  setRunLogLevel(event.target.value);
                  setRunLogLevelTouched(true);
                }}
              >
                {["error", "warn", "info", "debug", "trace"].map((level) => (
                  <option key={level} value={level}>
                    {level}
                  </option>
                ))}
              </select>
            </label>
            {runLogLevelTouched ? (
              <Button type="button" onClick={() => setRunLogLevelTouched(false)}>
                Reset log level
              </Button>
            ) : null}
          </div>
        </div>
        {error ? <p className="error">{error}</p> : null}
        {(preview.Warnings || []).map((warning) => (
          <p key={warning} className="muted">
            {warning}
          </p>
        ))}
        {metadataProgressActive || metadataProgressUpdates.length > 0 ? (
          <div className="metadata-progress">
            <p className="label">Metadata progress</p>
            <div className="metadata-progress__list">
              {metadataPhaseOrder.map((phase) => {
                const state = latestMetadataPhase.get(phase.key);
                const status = state?.status || "pending";
                return (
                  <div
                    key={phase.key}
                    className={`metadata-progress__item metadata-progress__item--${status}`}
                  >
                    <span className="metadata-progress__phase">{phase.label}</span>
                    <span className="metadata-progress__status">{metadataStatusLabel(status)}</span>
                  </div>
                );
              })}
            </div>
          </div>
        ) : null}
      </section>

      <section className="results">
        {hasPreview ? (
          <div className="summary">
            <div>
              <p className="label">Tracker used</p>
              <p className="value">{preview.TrackerName || "No tracker used"}</p>
            </div>
            <div>
              <p className="label">Release name</p>
              <p className="value">{preview.ReleaseName || "No release name yet"}</p>
            </div>
          </div>
        ) : null}

        {hasPreview && showExternalIDInputUI && !hasResolvedPrimaryExternalID ? (
          <div className="panel">
            <div className="settings-subgroup">
              <div className="settings-subgroup__title">External ID candidates</div>
              <p className="muted path-helper">
                Select a candidate to copy it into ID overrides, then refresh metadata.
              </p>
              {tmdbCandidates.length === 0 && imdbCandidates.length === 0 ? (
                <p className="muted">No TMDB/IMDB candidates available for this search.</p>
              ) : (
                <div className="settings-grid">
                  <div>
                    <p className="label">TMDB</p>
                    {tmdbCandidates.length === 0 ? (
                      <p className="muted">No TMDB candidates</p>
                    ) : (
                      <div className="tracker-pills">
                        {tmdbCandidates.slice(0, 5).map((candidate) => (
                          <button
                            key={`tmdb-${candidate.ID}`}
                            type="button"
                            className={`ghost candidate-selector ${selectedCandidateID("tmdb") === candidate.ID ? "active" : ""}`}
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
                      <div className="settings-subgroup candidate-preview">
                        <p className="label">Selected TMDB candidate</p>
                        <div className="candidate-preview__header">
                          <div className="candidate-preview__text">
                            <p className="value">
                              {candidatePreview.candidate.Title || "(Untitled)"}
                              {candidatePreview.candidate.Year
                                ? ` (${candidatePreview.candidate.Year})`
                                : ""}
                            </p>
                            <p className="muted">
                              {candidatePreview.candidate.Category || "Unknown category"}
                              {formatSimilarity(candidatePreview.candidate.Similarity)
                                ? ` • ${formatSimilarity(candidatePreview.candidate.Similarity)}`
                                : ""}
                            </p>
                          </div>
                          {candidatePreview.candidate.PosterURL ? (
                            <button
                              className="candidate-preview__poster-button"
                              type="button"
                              onClick={() => {
                                setLightboxImage(candidatePreview.candidate.PosterURL);
                                setLightboxAlt("TMDB candidate poster");
                              }}
                            >
                              <img
                                className="candidate-preview__poster"
                                src={candidatePreview.candidate.PosterURL}
                                alt="TMDB candidate poster"
                                loading="lazy"
                              />
                            </button>
                          ) : null}
                        </div>
                        <p className="muted">
                          {candidatePreview.candidate.Overview || "No overview available."}
                        </p>
                      </div>
                    ) : null}
                  </div>
                  <div>
                    <p className="label">IMDB</p>
                    {imdbCandidates.length === 0 ? (
                      <p className="muted">No IMDB candidates</p>
                    ) : (
                      <div className="tracker-pills">
                        {imdbCandidates.slice(0, 5).map((candidate) => (
                          <button
                            key={`imdb-${candidate.ID}`}
                            type="button"
                            className={`ghost candidate-selector ${selectedCandidateID("imdb") === candidate.ID ? "active" : ""}`}
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
                      <div className="settings-subgroup candidate-preview">
                        <p className="label">Selected IMDB candidate</p>
                        <div className="candidate-preview__header">
                          <div className="candidate-preview__text">
                            <p className="value">
                              {candidatePreview.candidate.Title || "(Untitled)"}
                              {candidatePreview.candidate.Year
                                ? ` (${candidatePreview.candidate.Year})`
                                : ""}
                            </p>
                            <p className="muted">
                              {candidatePreview.candidate.Category || "Unknown category"}
                              {formatSimilarity(candidatePreview.candidate.Similarity)
                                ? ` • ${formatSimilarity(candidatePreview.candidate.Similarity)}`
                                : ""}
                            </p>
                          </div>
                          {candidatePreview.candidate.PosterURL ? (
                            <button
                              className="candidate-preview__poster-button"
                              type="button"
                              onClick={() => {
                                setLightboxImage(candidatePreview.candidate.PosterURL);
                                setLightboxAlt("IMDB candidate poster");
                              }}
                            >
                              <img
                                className="candidate-preview__poster"
                                src={candidatePreview.candidate.PosterURL}
                                alt="IMDB candidate poster"
                                loading="lazy"
                              />
                            </button>
                          ) : null}
                        </div>
                        <p className="muted">
                          {candidatePreview.candidate.Overview || "No overview available."}
                        </p>
                      </div>
                    ) : null}
                  </div>
                </div>
              )}
              <div className="edit-actions">
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

        <div className="edit-controls">
          {hasPreview ? (
            <details className="edit-dropdown tracker-dropdown">
              <summary>
                <span>Select Trackers</span>
                <span className="tracker-summary-count">
                  {selectedTrackerCount}/{trackerUploadItems.length}
                </span>
              </summary>
              <div className="edit-dropdown__body">
                <div className="tracker-selection-container">
                  {trackerUploadItems.length === 0 ? (
                    <p className="muted">No configured tracker entries found.</p>
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
                      <div className="tracker-pills">
                        {trackerUploadItems.map((tracker) => {
                          const iconSrc = trackerIconFor(trackerIconSrcByName, tracker.name);
                          return (
                            <PillCheckbox
                              aria-label={tracker.name}
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
                </div>
              </div>
            </details>
          ) : null}
          {hasPreview ? (
            <p className="helper edit-helper">Edit external IDs and Release Name attributes.</p>
          ) : null}
          {hasPreview ? (
            <details className="edit-dropdown">
              <summary>Edit Release Details</summary>
              <div className="edit-dropdown__body">
                <div className="settings-subgroup">
                  <div className="settings-subgroup__title">External IDs</div>
                  <div className="id-editor settings-grid">
                    <div className="settings-field">
                      <label htmlFor="external-tmdb-id">TMDB ID</label>
                      <input
                        id="external-tmdb-id"
                        value={idEdits.tmdb}
                        onChange={(event) =>
                          setIdEdits((prev) => ({ ...prev, tmdb: event.target.value }))
                        }
                        placeholder="e.g. 550"
                      />
                    </div>
                    <div className="settings-field">
                      <label htmlFor="external-imdb-id">IMDB ID</label>
                      <input
                        id="external-imdb-id"
                        value={idEdits.imdb}
                        onChange={(event) =>
                          setIdEdits((prev) => ({ ...prev, imdb: event.target.value }))
                        }
                        placeholder="e.g. tt0137523"
                      />
                    </div>
                    <div className="settings-field">
                      <label htmlFor="external-tvdb-id">TVDB ID</label>
                      <input
                        id="external-tvdb-id"
                        value={idEdits.tvdb}
                        onChange={(event) =>
                          setIdEdits((prev) => ({ ...prev, tvdb: event.target.value }))
                        }
                        placeholder="e.g. 80379"
                      />
                    </div>
                    <div className="settings-field">
                      <label htmlFor="external-tvmaze-id">TVmaze ID</label>
                      <input
                        id="external-tvmaze-id"
                        value={idEdits.tvmaze}
                        onChange={(event) =>
                          setIdEdits((prev) => ({ ...prev, tvmaze: event.target.value }))
                        }
                        placeholder="e.g. 82"
                      />
                    </div>
                  </div>
                </div>
                <div className="settings-subgroup">
                  <div className="settings-subgroup__title">Release name overrides</div>
                  <div className="settings-grid">
                    <div className="settings-field">
                      <label htmlFor="release-category">Category</label>
                      <input
                        id="release-category"
                        value={releaseEdits?.category || ""}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, category: event.target.value }));
                          markReleaseTouched("category");
                        }}
                        placeholder="movie or tv"
                      />
                    </div>
                    <div className="settings-field">
                      <label htmlFor="release-type">Type</label>
                      <input
                        id="release-type"
                        value={releaseEdits?.type || ""}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, type: event.target.value }));
                          markReleaseTouched("type");
                        }}
                        placeholder="remux, encode, webdl"
                      />
                    </div>
                    <div className="settings-field">
                      <label htmlFor="release-source">Source</label>
                      <input
                        id="release-source"
                        value={releaseEdits?.source || ""}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, source: event.target.value }));
                          markReleaseTouched("source");
                        }}
                        placeholder="BluRay, WEB, DVD"
                      />
                    </div>
                    <div className="settings-field">
                      <label htmlFor="release-resolution">Resolution</label>
                      <input
                        id="release-resolution"
                        value={releaseEdits?.resolution || ""}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, resolution: event.target.value }));
                          markReleaseTouched("resolution");
                        }}
                        placeholder="2160p"
                      />
                    </div>
                    <div className="settings-field">
                      <label htmlFor="release-tag">Tag</label>
                      <input
                        id="release-tag"
                        value={releaseEdits?.tag || ""}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, tag: event.target.value }));
                          markReleaseTouched("tag");
                        }}
                        placeholder="GROUP"
                      />
                    </div>
                    <div className="settings-field">
                      <label htmlFor="release-service">Service</label>
                      <input
                        id="release-service"
                        value={releaseEdits?.service || ""}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, service: event.target.value }));
                          markReleaseTouched("service");
                        }}
                        placeholder="Netflix"
                      />
                    </div>
                    <div className="settings-field">
                      <label htmlFor="release-edition">Edition</label>
                      <input
                        id="release-edition"
                        value={releaseEdits?.edition || ""}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, edition: event.target.value }));
                          markReleaseTouched("edition");
                        }}
                        placeholder="Director's Cut"
                      />
                    </div>
                    <div className="settings-field">
                      <label htmlFor="release-region">Region</label>
                      <input
                        id="release-region"
                        value={releaseEdits?.region || ""}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, region: event.target.value }));
                          markReleaseTouched("region");
                        }}
                        placeholder="A, B, C"
                      />
                    </div>
                    <div className="settings-field">
                      <label htmlFor="release-season">Season</label>
                      <input
                        id="release-season"
                        value={releaseEdits?.season || ""}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, season: event.target.value }));
                          markReleaseTouched("season");
                        }}
                        placeholder="S01"
                      />
                    </div>
                    <div className="settings-field">
                      <label htmlFor="release-episode">Episode</label>
                      <input
                        id="release-episode"
                        value={releaseEdits?.episode || ""}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, episode: event.target.value }));
                          markReleaseTouched("episode");
                        }}
                        placeholder="E02"
                      />
                    </div>
                    <div className="settings-field">
                      <label htmlFor="release-episode-title">Episode title</label>
                      <input
                        id="release-episode-title"
                        value={releaseEdits?.episodeTitle || ""}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({
                            ...prev,
                            episodeTitle: event.target.value,
                          }));
                          markReleaseTouched("episodeTitle");
                        }}
                        placeholder="Pilot"
                      />
                    </div>
                    <div className="settings-field">
                      <label htmlFor="release-manual-year">Manual year</label>
                      <input
                        id="release-manual-year"
                        type="number"
                        value={releaseEdits?.manualYear || ""}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, manualYear: event.target.value }));
                          markReleaseTouched("manualYear");
                        }}
                        placeholder="2024"
                      />
                    </div>
                    <div className="settings-field">
                      <label htmlFor="release-manual-date">Manual date</label>
                      <input
                        id="release-manual-date"
                        value={releaseEdits?.manualDate || ""}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, manualDate: event.target.value }));
                          markReleaseTouched("manualDate");
                        }}
                        placeholder="YYYY-MM-DD"
                      />
                    </div>
                    {isTVEpisodePreview ? (
                      <div className="settings-toggle">
                        <span>Use season/episode instead</span>
                        <Switch
                          aria-label="Use season/episode instead"
                          checked={Boolean(releaseEdits?.useSeasonEpisode)}
                          onChange={(event) => {
                            setReleaseEdits((prev) => ({
                              ...prev,
                              useSeasonEpisode: event.target.checked,
                            }));
                            markReleaseTouched("useSeasonEpisode");
                          }}
                        />
                      </div>
                    ) : null}
                  </div>
                </div>
                <div className="settings-subgroup">
                  <div className="settings-subgroup__title">Flags</div>
                  <div className="settings-grid">
                    <div className="settings-toggle">
                      <span>No season</span>
                      <Switch
                        aria-label="No season"
                        checked={Boolean(releaseEdits?.noSeason)}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, noSeason: event.target.checked }));
                          markReleaseTouched("noSeason");
                        }}
                      />
                    </div>
                    <div className="settings-toggle">
                      <span>No year</span>
                      <Switch
                        aria-label="No year"
                        checked={Boolean(releaseEdits?.noYear)}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, noYear: event.target.checked }));
                          markReleaseTouched("noYear");
                        }}
                      />
                    </div>
                    <div className="settings-toggle">
                      <span>No AKA</span>
                      <Switch
                        aria-label="No AKA"
                        checked={Boolean(releaseEdits?.noAKA)}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, noAKA: event.target.checked }));
                          markReleaseTouched("noAKA");
                        }}
                      />
                    </div>
                    <div className="settings-toggle">
                      <span>No tag</span>
                      <Switch
                        aria-label="No tag"
                        checked={Boolean(releaseEdits?.noTag)}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, noTag: event.target.checked }));
                          markReleaseTouched("noTag");
                        }}
                      />
                    </div>
                    <div className="settings-toggle">
                      <span>No edition</span>
                      <Switch
                        aria-label="No edition"
                        checked={Boolean(releaseEdits?.noEdition)}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, noEdition: event.target.checked }));
                          markReleaseTouched("noEdition");
                        }}
                      />
                    </div>
                    <div className="settings-toggle">
                      <span>No dub</span>
                      <Switch
                        aria-label="No dub"
                        checked={Boolean(releaseEdits?.noDub)}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, noDub: event.target.checked }));
                          markReleaseTouched("noDub");
                        }}
                      />
                    </div>
                    <div className="settings-toggle">
                      <span>No dual-audio</span>
                      <Switch
                        aria-label="No dual-audio"
                        checked={Boolean(releaseEdits?.noDual)}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, noDual: event.target.checked }));
                          markReleaseTouched("noDual");
                        }}
                      />
                    </div>
                    <div className="settings-toggle">
                      <span>Force dual-audio</span>
                      <Switch
                        aria-label="Force dual-audio"
                        checked={Boolean(releaseEdits?.dualAudio)}
                        onChange={(event) => {
                          setReleaseEdits((prev) => ({ ...prev, dualAudio: event.target.checked }));
                          markReleaseTouched("dualAudio");
                        }}
                      />
                    </div>
                  </div>
                </div>
                {idOverrideState?.invalid ? (
                  <p className="error">
                    Enter numeric IDs only. IMDb supports an optional tt prefix.
                  </p>
                ) : null}
                {releaseOverrideState?.invalid ? (
                  <p className="error">
                    Manual year must be numeric and manual date must be YYYY-MM-DD.
                  </p>
                ) : null}
                <div className="edit-actions">
                  <button
                    className="ghost"
                    type="button"
                    onClick={handleResetMetadata}
                    disabled={loading}
                  >
                    {metadataResetting ? "Resetting..." : "Reset data + refresh"}
                  </button>
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
            </details>
          ) : null}
        </div>

        <div className={`details ${hasPreview ? "loaded" : ""}`}>
          <div className="id-list">
            <h2>External IDs</h2>
            {orderedExternalIDs.length === 0 ? (
              <p className="muted">No external metadata details found.</p>
            ) : (
              orderedExternalIDs.map((item) => (
                <button
                  key={item.Provider}
                  className={`id-card ${selectedProvider === item.Provider ? "active" : ""}`}
                  type="button"
                  onClick={() => setSelectedProvider(item.Provider)}
                >
                  <span className="id-label">{formatProvider(item.Provider)}</span>
                  <span className="id-value">{formatID(item.Provider, item.ID)}</span>
                  <span className="id-source">Source: {item.Source}</span>
                </button>
              ))
            )}
          </div>

          <div className="preview-panel">
            <div className="preview-header">
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
              <div className="preview-content">
                <div className="preview-text">
                  <p className="title">{selectedPreviewTitle || "Untitled"}</p>
                  <p className="meta">{selectedPreview.Year ? `${selectedPreview.Year}` : ""}</p>
                  <p className="overview">{selectedPreviewOverview || "No overview available."}</p>
                  {previewDetails.length > 0 ? (
                    <div className="preview-details">
                      {previewDetails.map((item) => (
                        <div className="preview-detail" key={item.label}>
                          <p className="label">{item.label}</p>
                          <p className={`value preview-detail__value ${item.mono ? "mono" : ""}`}>
                            {renderDetailValue(item)}
                          </p>
                        </div>
                      ))}
                    </div>
                  ) : null}
                </div>
                <div className="preview-images">
                  {selectedPreview.PosterURL ? (
                    <img src={selectedPreview.PosterURL} alt="Poster" loading="lazy" />
                  ) : null}
                  {selectedPreview.BackdropURL ? (
                    <img src={selectedPreview.BackdropURL} alt="Backdrop" loading="lazy" />
                  ) : null}
                </div>
              </div>
            ) : (
              <p className="muted">Select an external ID to preview.</p>
            )}
          </div>
        </div>
      </section>
    </div>
  );
}
