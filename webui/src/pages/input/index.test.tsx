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
import InputPage from "./index";

afterEach(cleanup);

const inputFacet = (): InputFacet => ({
  view: {
    sourceDraft: "C:\\media\\Example.mkv",
    selectedSource: "",
    status: "idle",
    error: "",
    failure: null,
    preparationDirty: false,
    intent: {
      sourceLookupURL: "",
      identity: {},
      metadata: {},
      releaseName: {},
      playlist: { Set: false, Selected: [], UseAll: false },
    },
    selectedTrackers: [],
    preview: null,
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
    fireEvent.click(screen.getByLabelText("No episode title"));
    fireEvent.click(screen.getByLabelText("No distributor"));

    expect(facet.changeReleaseName).toHaveBeenLastCalledWith({
      NoEpisodeTitle: true,
      NoDistributor: true,
    });
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
    expect(facet.changeIdentity).toHaveBeenLastCalledWith({
      TMDBID: 0,
      IMDBID: 0,
      TVDBID: 0,
      TVmazeID: 0,
      MALID: 0,
    });
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
    expect(screen.getByRole("button", { name: "Remove TMDB ID" })).toBeEnabled();

    const reenabledFacet: InputFacet = {
      ...facet,
      view: {
        ...facet.view,
        intent: { ...facet.view.intent, identity: reenabledIDs },
      },
    };
    rerender(<InputPage facet={reenabledFacet} {...pageProps} />);
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
});
