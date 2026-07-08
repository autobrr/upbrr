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
