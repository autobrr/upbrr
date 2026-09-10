// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { InputFacet } from "../../releaseSession/types";
import type {
  IMDBMetadata,
  MetadataPreview,
  ProviderDisplay,
  ProviderDisplaySummary,
  TMDBMetadata,
} from "../../types";
import { emptyExternalIdentity } from "../../utils/canonicalIdentity";
import { InputCorrectionEditor } from "./InputCorrectionEditor";
import InputPage from "./index";

afterEach(cleanup);

it("shows the used tracker source ID while preserving an edited or cleared draft", () => {
  const base = inputFacet();
  const facet: InputFacet = {
    ...base,
    view: {
      ...base.view,
      selectedTrackers: ["AITHER", "PTP"],
      trackerData: [
        {
          Tracker: "AITHER",
          TrackerID: "123",
          TorrentURL: "",
          InfoHash: "",
          TMDBID: 0,
          IMDBID: 0,
          TVDBID: 0,
          MALID: 0,
          Category: "movie",
          Description: "",
          DescriptionHTML: "",
          ImageURLs: [],
          Filename: "",
          Matched: true,
          UpdatedAt: "",
        },
      ],
    },
  };
  const { rerender } = render(<InputCorrectionEditor facet={facet} />);
  expect(screen.getByLabelText("AITHER source ID")).toHaveValue("123");
  expect(screen.getByLabelText("PTP source ID")).toHaveValue("");
  expect(facet.changeTrackerSourceID).not.toHaveBeenCalled();
  for (const value of ["456", ""]) {
    rerender(
      <InputCorrectionEditor
        facet={{
          ...facet,
          view: {
            ...facet.view,
            intent: { ...facet.view.intent, trackerSourceIDs: { AITHER: value } },
          },
        }}
      />,
    );
    expect(screen.getByLabelText("AITHER source ID")).toHaveValue(value);
  }
});

const inputFacet = (): InputFacet => ({
  view: {
    sourceDraft: "C:\\media\\Example.mkv",
    selectedSource: "",
    status: "idle",
    error: "",
    failure: null,
    preparationDirty: false,
    correctionDirty: false,
    intent: {
      sourceLookupURL: "",
      identity: {},
      metadata: {},
      releaseName: {},
      playlist: { Set: false, Selected: [], UseAll: false },
      trackerSourceIDs: {},
      policy: { keepFolder: false, keepImages: false, onlyID: false },
      search: { skip: false, client: "" },
    },
    corrections: null,
    resetFields: [],
    confirmFields: [],
    trackerInputAnswers: {},
    selectedTrackers: [],
    preview: null,
    release: null,
    readiness: null,
    trackerData: [],
    source: { discCount: 0, discType: "" },
    playlist: {
      status: "idle",
      required: false,
      candidates: [],
      selected: [],
      useAll: false,
      error: "",
    },
  },
  updateSourceDraft: vi.fn(),
  selectSource: vi.fn(),
  changeSourceLookupURL: vi.fn(),
  changeIdentity: vi.fn(),
  changeMetadata: vi.fn(),
  changeReleaseName: vi.fn(),
  resetCorrection: vi.fn(),
  confirmCorrection: vi.fn(),
  changeTrackerInputAnswer: vi.fn(),
  changeTrackerSourceID: vi.fn(),
  changePreparationPolicy: vi.fn(),
  changeClientSearch: vi.fn(),
  chooseTrackers: vi.fn(),
  choosePlaylists: vi.fn(),
  confirmPlaylists: vi.fn(async () => true),
  cancelPlaylistSelection: vi.fn(),
  prepareSource: vi.fn(async () => true),
  resetSource: vi.fn(async () => true),
  prepare: vi.fn(async () => true),
  reset: vi.fn(async () => true),
  confirmBDMVRescan: vi.fn(async () => true),
  selectCandidate: vi.fn(async () => true),
});

const providerSummary = (title: string): ProviderDisplaySummary => ({
  Title: title,
  OriginalTitle: "",
  Year: 2026,
  Overview: `${title} overview`,
  PosterURL: "",
  BackdropURL: "",
  Category: "movie",
  Date: "",
  EndDate: "",
  OriginalLanguage: "en",
  MediaType: "movie",
  RuntimeMinutes: 0,
  Genres: "",
  Keywords: "",
  TrailerURL: "",
  Rating: 0,
  RatingCount: 0,
  Country: "",
});

const providerDisplays = (generation: number): ProviderDisplay[] => [
  {
    Provider: "imdb",
    ID: 1_234_567,
    DisplayID: "tt1234567",
    URL: "",
    Provenance: "resolver",
    SummaryAvailable: true,
    Summary: providerSummary(`IMDB generation ${generation}`),
    Details: { IMDB: {} as IMDBMetadata },
  },
  {
    Provider: "tmdb",
    ID: 101,
    DisplayID: "101",
    URL: "",
    Provenance: "resolver",
    SummaryAvailable: true,
    Summary: providerSummary(`TMDB generation ${generation}`),
    Details: { TMDB: {} as TMDBMetadata },
  },
];

const metadataPreview = (generation: number): MetadataPreview => {
  const sourcePath = "C:\\media\\Example.mkv";
  return {
    SourcePath: sourcePath,
    TrackerName: "",
    ReleaseName: "Example.Release.2026.1080p-GRP",
    ReleaseNameOverrides: {},
    Release: { SourcePath: sourcePath, Generation: generation },
    Identity: {
      ...emptyExternalIdentity(sourcePath),
      Generation: generation,
      TMDBID: 101,
      IMDBID: 1_234_567,
      Category: "movie",
      Provenance: { TMDB: "resolver", IMDB: "resolver" },
    },
    Display: {
      ReleaseName: "Example.Release.2026.1080p-GRP",
      Providers: providerDisplays(generation),
    },
    Bluray: null,
    Diagnostics: [],
    TrackerData: [],
    TrackerRuleFailures: {},
  };
};

const readyInputFacet = (generation: number): InputFacet => {
  const base = inputFacet();
  return {
    ...base,
    view: {
      ...base.view,
      selectedSource: "C:\\media\\Example.mkv",
      status: "ready",
      preview: metadataPreview(generation),
    },
  };
};

const preparedRelease = () =>
  ({
    Identity: {
      ...emptyExternalIdentity("C:\\media\\Example.mkv"),
      TMDBID: 101,
      IMDBID: 1_234_567,
      MALID: 51,
      Category: "movie",
    },
    Naming: {
      Type: "encode",
      Source: "BluRay",
      Resolution: "1080p",
      Tag: "GRP",
      Year: 2026,
      Region: "A",
      Title: "Automatic Title",
      AlternateTitle: "Automatic AKA",
      OriginalTitle: "Automatic Original Title",
      Genres: ["Drama"],
      Personal: false,
    },
    Episode: {
      SeasonLabel: "S01",
      EpisodeLabel: "E02",
      Title: "Example Episode",
      DailyDate: "2026-09-09",
    },
    Media: {
      Service: "Example Service",
      Edition: "Director's Cut",
      Region: "A",
      Distributor: "Example Distributor",
      OriginalLanguage: "Japanese",
      AudioLanguages: ["Japanese"],
      SubtitleLanguages: ["English"],
      HardcodedSubtitleLanguages: [],
      Commentary: false,
      WebDV: false,
      StreamOptimized: 0,
      Anime: false,
      HardcodedSubs: false,
      TrackCoverageComplete: false,
      Tracks: [
        {
          ID: "audio:resource-1:1",
          Kind: "audio",
          ResourceID: "resource-1",
          ManifestFingerprint: "a".repeat(64),
          NativeID: "1",
          Ordinal: 1,
          DetectedLanguages: ["Japanese"],
          Languages: ["Japanese"],
          LanguageProvenance: "automatic",
          Default: true,
          Commentary: false,
        },
        {
          ID: "subtitle:resource-1:2",
          Kind: "subtitle",
          ResourceID: "resource-1",
          ManifestFingerprint: "a".repeat(64),
          NativeID: "2",
          Ordinal: 1,
          DetectedLanguages: ["English"],
          Languages: ["English"],
          LanguageProvenance: "automatic",
          Default: true,
          Commentary: false,
        },
      ],
    },
  }) as unknown as NonNullable<InputFacet["view"]["release"]>;

describe("InputPage", () => {
  it("keeps typing as a draft and uses explicit preparation intent", () => {
    const facet = inputFacet();
    render(
      <InputPage
        facet={facet}
        sourcePathHistory={[]}
        handleBrowseFile={vi.fn()}
        handleBrowseFolder={vi.fn()}
        trackerUploadItems={[]}
        showExternalIDInputUI={false}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
        trackerIconSrcByName={{}}
      />,
    );
    fireEvent.change(screen.getByLabelText("Source path"), {
      target: { value: "C:\\media\\Other.mkv" },
    });
    expect(facet.updateSourceDraft).toHaveBeenCalledWith("C:\\media\\Other.mkv");
    fireEvent.click(screen.getByRole("button", { name: "Fetch metadata" }));
    expect(facet.prepareSource).toHaveBeenCalledWith("C:\\media\\Example.mkv", facet.view.intent);
  });

  it("does not carry correction intent when fetching a different source", () => {
    const base = readyInputFacet(1);
    const facet: InputFacet = {
      ...base,
      view: {
        ...base.view,
        sourceDraft: "C:\\media\\Different.mkv",
        intent: {
          ...base.view.intent,
          metadata: { Title: "Previous title" },
          trackerSourceIDs: { AITHER: "123" },
          policy: { keepFolder: true, keepImages: true, onlyID: true },
        },
      },
    };
    render(
      <InputPage
        facet={facet}
        sourcePathHistory={[]}
        handleBrowseFile={vi.fn()}
        handleBrowseFolder={vi.fn()}
        trackerUploadItems={[]}
        showExternalIDInputUI={false}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
        trackerIconSrcByName={{}}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Refresh metadata" }));
    expect(facet.prepareSource).toHaveBeenCalledWith("C:\\media\\Different.mkv", {
      sourceLookupURL: "",
      identity: {},
      metadata: {},
      releaseName: {},
      playlist: { Set: false, Selected: [], UseAll: false },
      trackerSourceIDs: {},
      policy: { keepFolder: false, keepImages: false, onlyID: false },
      search: { skip: false, client: "" },
    });
  });

  it("renders BDMV playlist intent and confirms through the input facet", () => {
    const base = inputFacet();
    const facet: InputFacet = {
      ...base,
      view: {
        ...base.view,
        playlist: {
          status: "awaiting_selection",
          required: true,
          candidates: [
            {
              id: "disc-one:00001.mpls",
              discId: "disc-one",
              discName: "Disc 1",
              file: "00001.mpls",
              duration: 120,
              items: [],
              score: 1,
              edition: "",
            },
          ],
          selected: [],
          useAll: false,
          error: "",
        },
      },
    };
    render(
      <InputPage
        facet={facet}
        sourcePathHistory={[]}
        handleBrowseFile={vi.fn()}
        handleBrowseFolder={vi.fn()}
        trackerUploadItems={[]}
        showExternalIDInputUI={false}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
        trackerIconSrcByName={{}}
      />,
    );

    fireEvent.click(screen.getByLabelText("00001.mpls"));
    expect(facet.choosePlaylists).toHaveBeenCalledWith(["disc-one:00001.mpls"], false);

    const selectedFacet: InputFacet = {
      ...facet,
      view: {
        ...facet.view,
        playlist: { ...facet.view.playlist, selected: ["disc-one:00001.mpls"] },
      },
    };
    cleanup();
    render(
      <InputPage
        facet={selectedFacet}
        sourcePathHistory={[]}
        handleBrowseFile={vi.fn()}
        handleBrowseFolder={vi.fn()}
        trackerUploadItems={[]}
        showExternalIDInputUI={false}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
        trackerIconSrcByName={{}}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Confirm Selection" }));
    expect(facet.confirmPlaylists).toHaveBeenCalledOnce();
  });

  it("groups duplicate playlist names and selects opaque IDs for every disc", () => {
    const base = inputFacet();
    const candidates = [
      {
        id: "disc:00001.mpls",
        discId: "disc-one",
        discName: "Disc 1",
        file: "00001.mpls",
        duration: 120,
        items: [],
        score: 2,
        edition: "",
      },
      {
        id: "disc/00001.mpls",
        discId: "disc-two",
        discName: "Disc 2",
        file: "00001.mpls",
        duration: 110,
        items: [],
        score: 1,
        edition: "",
      },
    ];
    const facet: InputFacet = {
      ...base,
      view: {
        ...base.view,
        source: { discCount: 2, discType: "BDMV" },
        playlist: {
          status: "awaiting_selection",
          required: true,
          candidates,
          selected: [candidates[0].id],
          useAll: false,
          error: "",
        },
      },
    };
    render(
      <InputPage
        facet={facet}
        sourcePathHistory={[]}
        handleBrowseFile={vi.fn()}
        handleBrowseFolder={vi.fn()}
        trackerUploadItems={[]}
        showExternalIDInputUI={false}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
        trackerIconSrcByName={{}}
      />,
    );

    expect(screen.getByRole("heading", { name: "Disc 1" })).toBeVisible();
    expect(screen.getByRole("heading", { name: "Disc 2" })).toBeVisible();
    expect(screen.getByText("Prepared source: 2 BDMV discs")).toBeVisible();
    expect(screen.getByRole("button", { name: "Confirm Selection" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Auto-Select Best Per Disc" }));
    expect(facet.choosePlaylists).toHaveBeenCalledWith(
      ["disc:00001.mpls", "disc/00001.mpls"],
      false,
    );
    const playlistSection = screen
      .getByRole("heading", { name: "Select BDMV Playlists" })
      .closest("section");
    if (!playlistSection) throw new Error("playlist section missing");
    const checkboxIDs = within(playlistSection)
      .getAllByRole("checkbox")
      .map((checkbox) => checkbox.getAttribute("id"));
    expect(new Set(checkboxIDs).size).toBe(checkboxIDs.length);
  });

  it("selects the highest-priority metadata source for each prepared generation", () => {
    const firstFacet = readyInputFacet(1);
    const pageProps = {
      sourcePathHistory: [],
      handleBrowseFile: vi.fn(),
      handleBrowseFolder: vi.fn(),
      trackerUploadItems: [],
      showExternalIDInputUI: false,
      setLightboxImage: vi.fn(),
      setLightboxAlt: vi.fn(),
      trackerIconSrcByName: {},
    };
    const { rerender } = render(<InputPage facet={firstFacet} {...pageProps} />);

    expect(screen.getByRole("button", { name: /^TMDB/ })).toHaveClass("active");
    expect(screen.getByText("TMDB generation 1")).toBeVisible();

    fireEvent.click(screen.getByRole("button", { name: /^IMDB/ }));
    expect(screen.getByRole("button", { name: /^IMDB/ })).toHaveClass("active");
    expect(screen.getByText("IMDB generation 1")).toBeVisible();

    rerender(<InputPage facet={readyInputFacet(2)} {...pageProps} />);
    expect(screen.getByRole("button", { name: /^TMDB/ })).toHaveClass("active");
    expect(screen.getByText("TMDB generation 2")).toBeVisible();
  });

  it("forces generated release-name omissions", () => {
    const facet = readyInputFacet(1);
    render(
      <InputPage
        facet={facet}
        sourcePathHistory={[]}
        handleBrowseFile={vi.fn()}
        handleBrowseFolder={vi.fn()}
        trackerUploadItems={[]}
        showExternalIDInputUI={false}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
        trackerIconSrcByName={{}}
      />,
    );

    fireEvent.click(screen.getByText("Edit Release Details"));
    fireEvent.change(screen.getByLabelText("No episode title"), { target: { value: "yes" } });
    fireEvent.change(screen.getByLabelText("No distributor"), { target: { value: "yes" } });

    expect(facet.changeReleaseName).toHaveBeenNthCalledWith(1, { NoEpisodeTitle: true });
    expect(facet.changeReleaseName).toHaveBeenNthCalledWith(2, { NoDistributor: true });
  });

  it("removes each metadata provider without overriding untouched IDs", () => {
    const facet = readyInputFacet(1);
    render(
      <InputPage
        facet={facet}
        sourcePathHistory={[]}
        handleBrowseFile={vi.fn()}
        handleBrowseFolder={vi.fn()}
        trackerUploadItems={[]}
        showExternalIDInputUI={false}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
        trackerIconSrcByName={{}}
      />,
    );

    fireEvent.click(screen.getByText("Edit Release Details"));
    fireEvent.click(screen.getByRole("button", { name: "Remove MAL ID" }));
    expect(facet.changeIdentity).toHaveBeenLastCalledWith({ MALID: 0 });

    for (const provider of ["TMDB", "IMDB", "TVDB", "TVmaze"]) {
      fireEvent.click(screen.getByRole("button", { name: `Remove ${provider} ID` }));
    }
    expect(facet.changeIdentity).toHaveBeenCalledTimes(5);
    expect(facet.changeIdentity).toHaveBeenNthCalledWith(2, { TMDBID: 0 });
    expect(facet.changeIdentity).toHaveBeenNthCalledWith(3, { IMDBID: 0 });
    expect(facet.changeIdentity).toHaveBeenNthCalledWith(4, { TVDBID: 0 });
    expect(facet.changeIdentity).toHaveBeenNthCalledWith(5, { TVmazeID: 0 });
  });

  it("retains restored removals when a provider is re-enabled", () => {
    const base = readyInputFacet(2);
    const removedIDs = {
      TMDBID: 0,
      IMDBID: 0,
      TVDBID: 0,
      TVmazeID: 0,
      MALID: 0,
    };
    const facet: InputFacet = {
      ...base,
      view: {
        ...base.view,
        intent: { ...base.view.intent, identity: removedIDs },
        preview: {
          ...metadataPreview(2),
          Identity: { ...emptyExternalIdentity("C:\\media\\Example.mkv"), Generation: 2 },
          Display: { ReleaseName: "Example.Release.2026.1080p-GRP", Providers: [] },
        },
      },
    };
    const pageProps = {
      sourcePathHistory: [],
      handleBrowseFile: vi.fn(),
      handleBrowseFolder: vi.fn(),
      trackerUploadItems: [],
      showExternalIDInputUI: false,
      setLightboxImage: vi.fn(),
      setLightboxAlt: vi.fn(),
      trackerIconSrcByName: {},
    };
    const { rerender } = render(<InputPage facet={facet} {...pageProps} />);

    fireEvent.click(screen.getByText("Edit Release Details"));
    for (const provider of ["TMDB", "IMDB", "TVDB", "TVmaze", "MAL"]) {
      expect(screen.getByRole("button", { name: `Remove ${provider} ID` })).toBeDisabled();
    }

    fireEvent.change(screen.getByLabelText("TMDB ID"), { target: { value: "550" } });
    const reenabledIDs = { ...removedIDs, TMDBID: 550 };
    expect(facet.changeIdentity).toHaveBeenLastCalledWith(reenabledIDs);

    const reenabledFacet: InputFacet = {
      ...facet,
      view: {
        ...facet.view,
        intent: { ...facet.view.intent, identity: reenabledIDs },
      },
    };
    rerender(<InputPage facet={reenabledFacet} {...pageProps} />);
    expect(screen.getByRole("button", { name: "Remove TMDB ID" })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: "Refresh metadata" }));
    expect(facet.prepareSource).toHaveBeenCalledWith(
      "C:\\media\\Example.mkv",
      reenabledFacet.view.intent,
    );
  });

  for (const preparation of [
    { name: "a failed first preparation", status: "error", error: "Metadata preparation failed." },
    { name: "a first preparation without a metadata match", status: "ready", error: "" },
  ] as const) {
    it(`removes a blank provider before retrying ${preparation.name}`, () => {
      const base = inputFacet();
      const failedFacet: InputFacet = {
        ...base,
        view: {
          ...base.view,
          status: preparation.status,
          error: preparation.error,
        },
      };
      const pageProps = {
        sourcePathHistory: [],
        handleBrowseFile: vi.fn(),
        handleBrowseFolder: vi.fn(),
        trackerUploadItems: [],
        showExternalIDInputUI: false,
        setLightboxImage: vi.fn(),
        setLightboxAlt: vi.fn(),
        trackerIconSrcByName: {},
      };
      const { rerender } = render(<InputPage facet={failedFacet} {...pageProps} />);

      fireEvent.click(screen.getByText("Edit Release Details"));
      fireEvent.click(screen.getByRole("button", { name: "Remove TVDB ID" }));
      expect(failedFacet.changeIdentity).toHaveBeenLastCalledWith({ TVDBID: 0 });

      const retryFacet: InputFacet = {
        ...failedFacet,
        view: {
          ...failedFacet.view,
          intent: { ...failedFacet.view.intent, identity: { TVDBID: 0 } },
        },
      };
      rerender(<InputPage facet={retryFacet} {...pageProps} />);
      fireEvent.click(screen.getByRole("button", { name: "Retry metadata" }));
      expect(failedFacet.prepareSource).toHaveBeenCalledWith(
        "C:\\media\\Example.mkv",
        retryFacet.view.intent,
      );
    });
  }

  it("edits distributor and original-language metadata", () => {
    const facet = readyInputFacet(1);
    render(
      <InputPage
        facet={facet}
        sourcePathHistory={[]}
        handleBrowseFile={vi.fn()}
        handleBrowseFolder={vi.fn()}
        trackerUploadItems={[]}
        showExternalIDInputUI={false}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
        trackerIconSrcByName={{}}
      />,
    );

    fireEvent.click(screen.getByText("Edit Release Details"));
    fireEvent.change(screen.getByLabelText("Distributor"), {
      target: { value: "Example Distributor" },
    });
    expect(facet.changeMetadata).toHaveBeenLastCalledWith({
      Distributor: "Example Distributor",
    });

    fireEvent.change(screen.getByLabelText("Original language"), {
      target: { value: "ja" },
    });
    expect(facet.changeMetadata).toHaveBeenLastCalledWith({ OriginalLanguage: "ja" });
  });

  it("keeps explicit false, empty, and Auto correction intents distinct", () => {
    const base = readyInputFacet(1);
    const facet: InputFacet = {
      ...base,
      view: { ...base.view, release: preparedRelease() },
    };
    render(<InputCorrectionEditor facet={facet} />);

    fireEvent.change(screen.getByLabelText("TMDB ID"), { target: { value: "invalid" } });
    expect(screen.getByLabelText("TMDB ID")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByRole("alert")).toHaveTextContent("Enter digits only.");
    expect(facet.changeIdentity).not.toHaveBeenCalled();

    fireEvent.change(screen.getByLabelText("Hardcoded subtitles"), {
      target: { value: "no" },
    });
    expect(facet.changeMetadata).toHaveBeenCalledWith({ HardcodedSubs: false });

    fireEvent.change(screen.getByLabelText("Original language"), { target: { value: "" } });
    expect(facet.changeMetadata).toHaveBeenCalledWith({ OriginalLanguage: "" });
    fireEvent.click(screen.getByRole("button", { name: "Auto Original language" }));
    expect(facet.resetCorrection).toHaveBeenCalledWith({
      field: "metadata.original_language",
    });

    fireEvent.change(screen.getByLabelText("Audio languages"), {
      target: { value: "English, Brazilian Portuguese, , English" },
    });
    expect(facet.changeMetadata).toHaveBeenCalledWith({
      AudioLanguages: ["English", "Brazilian Portuguese", "English"],
    });
    fireEvent.change(screen.getByLabelText("Subtitle languages"), {
      target: { value: " , " },
    });
    expect(facet.changeMetadata).toHaveBeenCalledWith({ SubtitleLanguages: [] });
  });

  it.each(["movie", "tv"] as const)(
    "locks provider titles and preserves the %s year policy",
    (category) => {
      const base = readyInputFacet(1);
      const release = preparedRelease();
      const facet: InputFacet = {
        ...base,
        view: {
          ...base.view,
          release: { ...release, Identity: { ...release.Identity, Category: category } },
          intent: {
            ...base.view.intent,
            metadata: { Title: "Old manual title", OriginalTitle: "Old original title" },
            releaseName: { ManualYear: 2001 },
          },
        },
      };
      render(<InputCorrectionEditor facet={facet} />);
      for (const [label, value] of [
        ["Title", "Automatic Title"],
        ["Original title", "Automatic Original Title"],
      ]) {
        const field = screen.getByLabelText(label);
        expect(field).toHaveAttribute("readonly");
        expect(field).toBeDisabled();
        expect(field).toHaveValue(value);
        expect(screen.queryByRole("button", { name: `Auto ${label}` })).not.toBeInTheDocument();
        fireEvent.change(field, { target: { value: "Disallowed edit" } });
      }
      expect(facet.changeMetadata).not.toHaveBeenCalled();
      const year = screen.getByLabelText("Manual year");
      if (category === "tv") {
        expect(year).toHaveAttribute("readonly");
        expect(year).toBeDisabled();
        expect(year).toHaveValue(2026);
        expect(screen.queryByRole("button", { name: "Auto Manual year" })).not.toBeInTheDocument();
        fireEvent.change(year, { target: { value: "2002" } });
        expect(facet.changeReleaseName).not.toHaveBeenCalled();
      } else {
        expect(year).not.toHaveAttribute("readonly");
        expect(year).toBeEnabled();
        expect(year).toHaveValue(2001);
        fireEvent.change(year, { target: { value: "2002" } });
        expect(facet.changeReleaseName).toHaveBeenCalledWith({ ManualYear: 2002 });
      }
      expect(screen.getByLabelText("Alternate title")).not.toHaveAttribute("readonly");
    },
  );

  it.each([
    ["movie", " TV ", true],
    ["movie", "television", true],
    ["movie", "series", true],
    ["movie", "episode", true],
    ["tv", "movie", false],
    ["tv", " Film ", false],
    ["tv", "", true],
    ["tv", "unknown", true],
  ] as const)(
    "updates year editing for category change from %s to %s",
    (preparedCategory, draftCategory, locked) => {
      const base = readyInputFacet(1);
      const release = preparedRelease();
      const facet: InputFacet = {
        ...base,
        view: {
          ...base.view,
          release: { ...release, Identity: { ...release.Identity, Category: preparedCategory } },
        },
      };
      const { rerender } = render(<InputCorrectionEditor facet={facet} />);
      fireEvent.change(screen.getByLabelText("Category"), { target: { value: draftCategory } });
      expect(facet.changeReleaseName).toHaveBeenCalledWith({ Category: draftCategory });
      rerender(
        <InputCorrectionEditor
          facet={{
            ...facet,
            view: {
              ...facet.view,
              intent: { ...facet.view.intent, releaseName: { Category: draftCategory } },
            },
          }}
        />,
      );
      if (locked) expect(screen.getByLabelText("Manual year")).toHaveAttribute("readonly");
      else expect(screen.getByLabelText("Manual year")).not.toHaveAttribute("readonly");
    },
  );

  it("renders the complete source-level correction inventory", () => {
    const base = readyInputFacet(1);
    const facet: InputFacet = {
      ...base,
      view: { ...base.view, release: preparedRelease() },
    };
    const { container } = render(<InputCorrectionEditor facet={facet} />);
    const rendered = [...container.querySelectorAll<HTMLElement>("[data-correction-field]")]
      .filter((element) => !element.dataset.trackId)
      .map((element) => element.dataset.correctionField);

    expect(rendered).toEqual([
      "identity.tmdb",
      "identity.imdb",
      "identity.tvdb",
      "identity.tvmaze",
      "identity.mal",
      "release_name.category",
      "release_name.type",
      "release_name.source",
      "release_name.resolution",
      "release_name.tag",
      "release_name.service",
      "release_name.edition",
      "release_name.season",
      "release_name.episode",
      "release_name.episode_title",
      "release_name.manual_year",
      "release_name.manual_date",
      "release_name.region",
      "release_name.use_season_episode",
      "release_name.no_season",
      "release_name.no_year",
      "release_name.no_aka",
      "release_name.no_tag",
      "release_name.no_episode_title",
      "release_name.no_distributor",
      "release_name.no_edition",
      "release_name.no_dub",
      "release_name.no_dual",
      "release_name.dual_audio",
      "metadata.distributor",
      "metadata.original_language",
      "metadata.title",
      "metadata.alternate_title",
      "metadata.original_title",
      "metadata.genres",
      "metadata.audio_languages",
      "metadata.subtitle_languages",
      "metadata.hardcoded_subtitle_languages",
      "metadata.personal_release",
      "metadata.commentary",
      "metadata.web_dv",
      "metadata.stream_optimized",
      "metadata.anime",
      "metadata.hardcoded_subs",
    ]);
  });

  it("renders persisted releases that predate effective media facts", () => {
    const base = readyInputFacet(1);
    const facet: InputFacet = {
      ...base,
      view: {
        ...base.view,
        release: {
          Generation: 1,
          Naming: { ReleaseName: "Example.Release.2026.1080p.GRP" },
          Identity: emptyExternalIdentity("C:\\media\\Example.mkv"),
        } as NonNullable<InputFacet["view"]["release"]>,
      },
    };

    render(<InputCorrectionEditor facet={facet} />);

    expect(screen.getByLabelText("Service")).toHaveValue("");
    expect(screen.getByText("No inspected audio or subtitle tracks.")).toBeInTheDocument();
  });

  it("binds track corrections and source options to their typed facet commands", () => {
    const base = readyInputFacet(1);
    const facet: InputFacet = {
      ...base,
      view: {
        ...base.view,
        release: preparedRelease(),
        selectedTrackers: ["AITHER"],
      },
    };
    render(<InputCorrectionEditor facet={facet} />);

    expect(screen.getByText(/Track coverage is incomplete/)).toBeInTheDocument();
    expect(screen.getByLabelText("Audio track 1 languages")).toBeInTheDocument();
    expect(screen.getByLabelText("Subtitle track 1 languages")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Audio track 1 languages"), {
      target: { value: "Japanese, English" },
    });
    expect(facet.changeMetadata).toHaveBeenCalledWith({
      TrackLanguages: [
        {
          trackId: "audio:resource-1:1",
          languages: ["Japanese", "English"],
          manifestFingerprint: "a".repeat(64),
        },
      ],
    });
    fireEvent.click(screen.getByRole("button", { name: "Auto Audio track 1 languages" }));
    expect(facet.resetCorrection).toHaveBeenCalledWith({
      field: "metadata.track_languages",
      trackId: "audio:resource-1:1",
    });

    expect(screen.getByTestId("input-source-options")).not.toHaveAttribute("open");
    fireEvent.click(screen.getByText("Source options", { exact: true }));
    fireEvent.change(screen.getByLabelText("AITHER source ID"), { target: { value: "123" } });
    expect(facet.changeTrackerSourceID).toHaveBeenCalledWith("AITHER", "123");
    fireEvent.click(screen.getByLabelText("Keep images"));
    expect(facet.changePreparationPolicy).toHaveBeenCalledWith({
      keepFolder: false,
      keepImages: true,
      onlyID: false,
    });
  });

  it("renders backend tracker schemas, readiness, and stale correction evidence", () => {
    const base = readyInputFacet(1);
    const priorBinding = {
      category: "movie",
      providerIds: { tmdbId: 101, imdbId: 0, tvdbId: 0, tvmazeId: 0, malId: 0 },
      sourceFingerprint: "a".repeat(64),
    };
    const currentBinding = {
      ...priorBinding,
      category: "tv",
      providerIds: { ...priorBinding.providerIds, tmdbId: 202 },
      sourceFingerprint: "b".repeat(64),
    };
    const facet: InputFacet = {
      ...base,
      view: {
        ...base.view,
        release: preparedRelease(),
        intent: { ...base.view.intent, metadata: { AlternateTitle: "Saved alternate title" } },
        corrections: {
          revision: 4,
          corrections: {
            version: 1,
            identity: {},
            releaseName: {},
            metadata: { AlternateTitle: "Saved alternate title" },
            staleContentFields: ["metadata.alternate_title"],
            contentBindings: { "metadata.alternate_title": priorBinding },
          },
        },
        readiness: {
          id: "input-readiness-1",
          workflowId: "workflow-1",
          revision: 8,
          release: { SourcePath: "C:\\media\\Example.mkv", Generation: 1 },
          factInstructions: { id: "facts-1", revision: 4 },
          correctionRevision: 4,
          selectedTrackerIds: ["PTP"],
          requirementsFingerprint: "c".repeat(64),
          fields: [
            {
              key: "genres",
              correctionField: "metadata.genres",
              trackerIds: ["PTP"],
              status: "missing",
              disposition: "strict",
              message: "Genres are required.",
            },
          ],
          schemas: [
            {
              Tracker: "PTP",
              Fields: [
                {
                  Key: "no_english_subtitles",
                  Label: "No English subtitles",
                  Kind: "select",
                  Options: ["auto", "yes", "no"],
                  Value: "yes",
                  Placeholder: "",
                  Help: "Use Auto unless explicit intent is needed.",
                  Required: true,
                },
              ],
            },
          ],
          requiredActions: [
            {
              id: "confirm-title",
              kind: "confirm_correction",
              prompt: "Confirm the saved title for the current content.",
              status: "pending",
              workflowRevision: 8,
              createdAt: "2026-09-09T00:00:00Z",
              correctionConfirmation: {
                revision: 4,
                fields: ["metadata.alternate_title"],
                previousBindings: { "metadata.alternate_title": priorBinding },
                currentBinding,
              },
            },
          ],
          status: "blocked",
          createdAt: "2026-09-09T00:00:00Z",
        },
      },
    };
    const { rerender } = render(<InputCorrectionEditor facet={facet} />);

    expect(screen.getByTestId("input-tracker-fields")).not.toHaveAttribute("open");
    expect(screen.getByTestId("input-readiness")).not.toHaveAttribute("open");
    fireEvent.click(screen.getByText("Tracker Input", { exact: true }));
    fireEvent.click(screen.getByText("Input readiness", { exact: true }));
    expect(screen.getByText("Genres are required.", { exact: false })).toBeInTheDocument();
    expect(screen.getByText(/saved for movie.*TMDB 101.*current tv.*TMDB 202/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Confirm saved Alternate title" }));
    expect(facet.confirmCorrection).toHaveBeenCalledWith({ field: "metadata.alternate_title" });
    expect(screen.getByLabelText("PTP No English subtitles")).toHaveValue("yes");
    fireEvent.change(screen.getByLabelText("PTP No English subtitles"), {
      target: { value: "auto" },
    });
    expect(facet.changeTrackerInputAnswer).toHaveBeenCalledWith(
      "PTP",
      "no_english_subtitles",
      null,
    );
    rerender(
      <InputCorrectionEditor
        facet={{
          ...facet,
          view: {
            ...facet.view,
            trackerInputAnswers: { PTP: { no_english_subtitles: null } },
          },
        }}
      />,
    );
    expect(screen.getByLabelText("PTP No English subtitles")).toHaveValue("auto");
  });
});
