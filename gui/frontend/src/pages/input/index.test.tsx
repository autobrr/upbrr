// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useState } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { MetadataPreview, ReleaseNameEditState, ReleaseNameTouchedState } from "../../types";
import InputPage from "./index";

const preview: MetadataPreview = {
  SourcePath: "C:\\media\\Example.mkv",
  TrackerName: "AITHER",
  ReleaseName: "Example.2026.1080p",
  Warnings: [],
  ReleaseNameOverrides: {},
  ExternalIDs: {
    TMDBID: 0,
    IMDBID: 0,
    TVDBID: 0,
    TVmazeID: 0,
    MALID: 0,
    Category: "TV",
    SourceTMDB: "",
    SourceIMDB: "",
    SourceTVDB: "",
    SourceTVmaze: "",
    SourceMAL: "",
  },
  ExternalIDCandidates: {
    TMDB: [],
    IMDB: [],
    TMDBAutoSelected: false,
    IMDBAutoSelected: false,
  },
  ExternalIDInfo: [],
  ExternalPreview: [],
  TrackerData: [],
};

const releaseEdits: ReleaseNameEditState = {
  category: "",
  type: "",
  source: "",
  resolution: "",
  tag: "",
  service: "",
  edition: "",
  season: "",
  episode: "",
  episodeTitle: "",
  manualYear: "",
  manualDate: "",
  useSeasonEpisode: false,
  noSeason: false,
  noYear: false,
  noAKA: false,
  noTag: false,
  noEdition: false,
  noDub: false,
  noDual: false,
  dualAudio: false,
  region: "",
};

describe("InputPage metadata progress", () => {
  afterEach(() => {
    cleanup();
  });

  it("renders tracker data fallback progress updates", () => {
    render(
      <InputPage
        path="C:\media\Example.mkv"
        handleSourcePathChange={vi.fn()}
        sourcePathHistory={[]}
        handleSourcePathHistorySelect={vi.fn()}
        sourceLookupURL=""
        setSourceLookupURL={vi.fn()}
        browseAvailable={false}
        handleBrowseFile={vi.fn()}
        handleBrowseFolder={vi.fn()}
        handleFetch={vi.fn()}
        handleRefresh={vi.fn()}
        handleResetMetadata={vi.fn()}
        loading={false}
        metadataResetting={false}
        metadataProgressActive={true}
        metadataProgressUpdates={[
          {
            path: "C:\\media\\Example.mkv",
            phase: "tracker-data-fallback",
            message: "Retrying tracker data",
            status: "running",
            level: "info",
            timestamp: "2026-06-29T00:00:00Z",
          },
        ]}
        error=""
        preview={preview}
        trackerUploadItems={[]}
        releasePageTrackerSelection={{}}
        setReleasePageTrackerSelection={vi.fn()}
        idEdits={{ tmdb: "", imdb: "", tvdb: "", tvmaze: "", mal: "" }}
        setIdEdits={vi.fn()}
        markIDTouched={vi.fn()}
        releaseEdits={releaseEdits}
        setReleaseEdits={vi.fn()}
        markReleaseTouched={vi.fn<(key: keyof ReleaseNameTouchedState) => void>()}
        idOverrideState={{ overrides: {}, dirty: false, invalid: false }}
        releaseOverrideState={{ overrides: {}, dirty: false, invalid: false }}
        showExternalIDInputUI={false}
        refreshDisabled={false}
        selectedProvider=""
        setSelectedProvider={vi.fn()}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
        runDebug={false}
        setRunDebug={vi.fn()}
        runLogLevel=""
        setRunLogLevel={vi.fn()}
        runLogLevelTouched={false}
        setRunLogLevelTouched={vi.fn()}
        trackerIconSrcByName={{}}
      />,
    );

    expect(screen.getByText("Retry tracker data")).toBeInTheDocument();
    expect(screen.getByText("Running")).toBeInTheDocument();
  });
});

describe("InputPage external ID preview", () => {
  afterEach(() => {
    cleanup();
  });

  it("renders an explicit unavailable preview for selected MAL IDs", () => {
    render(
      <InputPage
        path="C:\\media\\Example.mkv"
        handleSourcePathChange={vi.fn()}
        sourcePathHistory={[]}
        handleSourcePathHistorySelect={vi.fn()}
        sourceLookupURL=""
        setSourceLookupURL={vi.fn()}
        browseAvailable={false}
        handleBrowseFile={vi.fn()}
        handleBrowseFolder={vi.fn()}
        handleFetch={vi.fn()}
        handleRefresh={vi.fn()}
        handleResetMetadata={vi.fn()}
        loading={false}
        metadataResetting={false}
        metadataProgressActive={false}
        metadataProgressUpdates={[]}
        error=""
        preview={{
          ...preview,
          ExternalIDInfo: [{ Provider: "mal", ID: 5114, Source: "tmdb" }],
          ExternalPreview: [],
        }}
        trackerUploadItems={[]}
        releasePageTrackerSelection={{}}
        setReleasePageTrackerSelection={vi.fn()}
        idEdits={{ tmdb: "", imdb: "", tvdb: "", tvmaze: "", mal: "" }}
        setIdEdits={vi.fn()}
        markIDTouched={vi.fn()}
        releaseEdits={releaseEdits}
        setReleaseEdits={vi.fn()}
        markReleaseTouched={vi.fn<(key: keyof ReleaseNameTouchedState) => void>()}
        idOverrideState={{ overrides: {}, dirty: false, invalid: false }}
        releaseOverrideState={{ overrides: {}, dirty: false, invalid: false }}
        showExternalIDInputUI={false}
        refreshDisabled={false}
        selectedProvider="mal"
        setSelectedProvider={vi.fn()}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
        runDebug={false}
        setRunDebug={vi.fn()}
        runLogLevel=""
        setRunLogLevel={vi.fn()}
        runLogLevelTouched={false}
        setRunLogLevelTouched={vi.fn()}
        trackerIconSrcByName={{}}
      />,
    );

    expect(screen.getByText("MAL metadata preview unavailable.")).toBeInTheDocument();
    expect(screen.getByText("MAL URL")).toBeInTheDocument();
    expect(screen.getByText("https://myanimelist.net/anime/5114")).toBeInTheDocument();
    expect(screen.getByText("Unavailable")).toBeInTheDocument();
  });

  it("renders rich AniList metadata for selected MAL IDs", () => {
    render(
      <InputPage
        path="C:\\media\\Example.mkv"
        handleSourcePathChange={vi.fn()}
        sourcePathHistory={[]}
        handleSourcePathHistorySelect={vi.fn()}
        sourceLookupURL=""
        setSourceLookupURL={vi.fn()}
        browseAvailable={false}
        handleBrowseFile={vi.fn()}
        handleBrowseFolder={vi.fn()}
        handleFetch={vi.fn()}
        handleRefresh={vi.fn()}
        handleResetMetadata={vi.fn()}
        loading={false}
        metadataResetting={false}
        metadataProgressActive={false}
        metadataProgressUpdates={[]}
        error=""
        preview={{
          ...preview,
          ExternalIDInfo: [{ Provider: "mal", ID: 5114, Source: "tmdb" }],
          ExternalPreview: [
            {
              Provider: "mal",
              ID: 5114,
              Source: "tmdb",
              Title: "Example Anime",
              Year: 2026,
              Overview: "AniList overview",
              PosterURL: "https://img.example/anilist-cover.jpg",
              BackdropURL: "https://img.example/anilist-banner.jpg",
              Category: "TV",
              OriginalTitle: "Example Anime Romaji",
              ReleaseDate: "2026-04-01",
              FirstAirDate: "2026-04-01",
              LastAirDate: "",
              OriginalLanguage: "JP",
              TMDBType: "TV",
              Runtime: 24,
              Genres: "Action, Drama",
              Keywords: "",
              YouTube: "",
              IMDBType: "",
              Rating: 8.2,
              RatingCount: 12345,
              RuntimeMinutes: 0,
              Country: "",
              Premiered: "",
              IMDBID: 0,
              TVDBID: 0,
              AniList: {
                AniListID: 1,
                MALID: 5114,
                SiteURL: "https://anilist.co/anime/1",
                TitleRomaji: "Example Anime Romaji",
                TitleEnglish: "Example Anime",
                TitleNative: "例アニメ",
                TitleUserPreferred: "Example Anime",
                Description: "AniList overview",
                Format: "TV",
                Status: "FINISHED",
                StartDate: "2026-04-01",
                EndDate: "2026-06-24",
                Season: "SPRING",
                SeasonYear: 2026,
                Episodes: 12,
                Duration: 24,
                CountryOfOrigin: "JP",
                Source: "MANGA",
                CoverExtraLarge: "https://img.example/anilist-cover.jpg",
                CoverLarge: "",
                CoverMedium: "",
                CoverColor: "#abcdef",
                BannerImage: "https://img.example/anilist-banner.jpg",
                Genres: ["Action", "Drama"],
                Synonyms: ["Example Alt"],
                AverageScore: 82,
                MeanScore: 80,
                Popularity: 12345,
                Favourites: 678,
                IsAdult: false,
                Tags: [
                  {
                    Name: "Visible Low Rank",
                    Rank: 10,
                    Category: "Theme",
                    IsAdult: false,
                    IsGeneralSpoiler: false,
                    IsMediaSpoiler: false,
                  },
                  {
                    Name: "Visible High Rank",
                    Rank: 90,
                    Category: "Theme",
                    IsAdult: false,
                    IsGeneralSpoiler: false,
                    IsMediaSpoiler: false,
                  },
                  {
                    Name: "Hidden Adult",
                    Rank: 100,
                    Category: "Theme",
                    IsAdult: true,
                    IsGeneralSpoiler: false,
                    IsMediaSpoiler: false,
                  },
                  {
                    Name: "Hidden Spoiler",
                    Rank: 95,
                    Category: "Theme",
                    IsAdult: false,
                    IsGeneralSpoiler: true,
                    IsMediaSpoiler: false,
                  },
                ],
                Studios: [
                  { ID: 2, Name: "Example Studio", SiteURL: "https://anilist.co/studio/2" },
                ],
                Trailer: { ID: "abc123", Site: "youtube", Thumbnail: "" },
                NextAiringEpisode: { AiringAt: 0, TimeUntilAiring: 0, Episode: 0 },
                ExternalLinks: [
                  {
                    Site: "Official Site",
                    URL: "https://example.invalid",
                    Type: "INFO",
                    Language: "en",
                  },
                ],
              },
            },
          ],
        }}
        trackerUploadItems={[]}
        releasePageTrackerSelection={{}}
        setReleasePageTrackerSelection={vi.fn()}
        idEdits={{ tmdb: "", imdb: "", tvdb: "", tvmaze: "", mal: "" }}
        setIdEdits={vi.fn()}
        markIDTouched={vi.fn()}
        releaseEdits={releaseEdits}
        setReleaseEdits={vi.fn()}
        markReleaseTouched={vi.fn<(key: keyof ReleaseNameTouchedState) => void>()}
        idOverrideState={{ overrides: {}, dirty: false, invalid: false }}
        releaseOverrideState={{ overrides: {}, dirty: false, invalid: false }}
        showExternalIDInputUI={false}
        refreshDisabled={false}
        selectedProvider="mal"
        setSelectedProvider={vi.fn()}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
        runDebug={false}
        setRunDebug={vi.fn()}
        runLogLevel=""
        setRunLogLevel={vi.fn()}
        runLogLevelTouched={false}
        setRunLogLevelTouched={vi.fn()}
        trackerIconSrcByName={{}}
      />,
    );

    expect(screen.getAllByText("Example Anime").length).toBeGreaterThan(0);
    expect(screen.getByRole("img", { name: "Poster" })).toHaveAttribute(
      "src",
      "https://img.example/anilist-cover.jpg",
    );
    expect(screen.getByRole("img", { name: "Backdrop" })).toHaveAttribute(
      "src",
      "https://img.example/anilist-banner.jpg",
    );
    expect(screen.getByText("AniList ID")).toBeInTheDocument();
    expect(screen.getByText("MAL URL")).toBeInTheDocument();
    expect(screen.getByText("https://myanimelist.net/anime/5114")).toBeInTheDocument();
    expect(screen.getByText("AniList URL")).toBeInTheDocument();
    expect(screen.getByText("https://anilist.co/anime/1")).toBeInTheDocument();
    expect(screen.getByText("Status")).toBeInTheDocument();
    expect(screen.getByText("FINISHED")).toBeInTheDocument();
    expect(screen.getByText("Start date")).toBeInTheDocument();
    expect(screen.getByText("2026-04-01")).toBeInTheDocument();
    expect(screen.getByText("End date")).toBeInTheDocument();
    expect(screen.getByText("2026-06-24")).toBeInTheDocument();
    expect(screen.getByText("Genres")).toBeInTheDocument();
    expect(screen.getByText("Action, Drama")).toBeInTheDocument();
    expect(screen.getByText("Average score")).toBeInTheDocument();
    expect(screen.getByText("82%")).toBeInTheDocument();
    expect(screen.getByText("Tags")).toBeInTheDocument();
    expect(screen.getByText("Visible High Rank (90%), Visible Low Rank (10%)")).toBeInTheDocument();
    expect(screen.queryByText(/Hidden Adult/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Hidden Spoiler/)).not.toBeInTheDocument();
    expect(screen.getByText("Studios")).toBeInTheDocument();
    expect(screen.getByText("Example Studio")).toBeInTheDocument();
    expect(screen.getByText("External links")).toBeInTheDocument();
    expect(screen.getByText("Official Site - https://example.invalid")).toBeInTheDocument();
    expect(screen.getByText("Cover extra large")).toBeInTheDocument();
    expect(screen.getByText("Banner URL")).toBeInTheDocument();
  });
});

describe("InputPage tracker selection", () => {
  afterEach(() => {
    cleanup();
  });

  it("selects and deselects all configured trackers", () => {
    function Harness() {
      const [releasePageTrackerSelection, setReleasePageTrackerSelection] = useState<
        Record<string, boolean>
      >({
        AITHER: true,
        BLU: false,
      });

      return (
        <InputPage
          path="C:\\media\\Example.mkv"
          handleSourcePathChange={vi.fn()}
          sourcePathHistory={[]}
          handleSourcePathHistorySelect={vi.fn()}
          sourceLookupURL=""
          setSourceLookupURL={vi.fn()}
          browseAvailable={false}
          handleBrowseFile={vi.fn()}
          handleBrowseFolder={vi.fn()}
          handleFetch={vi.fn()}
          handleRefresh={vi.fn()}
          handleResetMetadata={vi.fn()}
          loading={false}
          metadataResetting={false}
          metadataProgressActive={false}
          metadataProgressUpdates={[]}
          error=""
          preview={preview}
          trackerUploadItems={[
            { name: "AITHER", config: {} },
            { name: "BLU", config: {} },
          ]}
          releasePageTrackerSelection={releasePageTrackerSelection}
          setReleasePageTrackerSelection={setReleasePageTrackerSelection}
          idEdits={{ tmdb: "", imdb: "", tvdb: "", tvmaze: "", mal: "" }}
          setIdEdits={vi.fn()}
          markIDTouched={vi.fn()}
          releaseEdits={releaseEdits}
          setReleaseEdits={vi.fn()}
          markReleaseTouched={vi.fn<(key: keyof ReleaseNameTouchedState) => void>()}
          idOverrideState={{ overrides: {}, dirty: false, invalid: false }}
          releaseOverrideState={{ overrides: {}, dirty: false, invalid: false }}
          showExternalIDInputUI={false}
          refreshDisabled={false}
          selectedProvider=""
          setSelectedProvider={vi.fn()}
          setLightboxImage={vi.fn()}
          setLightboxAlt={vi.fn()}
          runDebug={false}
          setRunDebug={vi.fn()}
          runLogLevel=""
          setRunLogLevel={vi.fn()}
          runLogLevelTouched={false}
          setRunLogLevelTouched={vi.fn()}
          trackerIconSrcByName={{}}
        />
      );
    }

    render(<Harness />);

    expect(screen.getByText("1/2")).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "AITHER" })).toHaveAttribute(
      "aria-checked",
      "true",
    );
    expect(screen.getByRole("checkbox", { name: "BLU" })).toHaveAttribute("aria-checked", "false");

    fireEvent.click(screen.getByRole("button", { name: "Select all" }));

    expect(screen.getByText("2/2")).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "AITHER" })).toHaveAttribute(
      "aria-checked",
      "true",
    );
    expect(screen.getByRole("checkbox", { name: "BLU" })).toHaveAttribute("aria-checked", "true");

    fireEvent.click(screen.getByRole("button", { name: "Deselect all" }));

    expect(screen.getByText("0/2")).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "AITHER" })).toHaveAttribute(
      "aria-checked",
      "false",
    );
    expect(screen.getByRole("checkbox", { name: "BLU" })).toHaveAttribute("aria-checked", "false");
  });
});
