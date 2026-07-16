// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { InputFacet } from "../../releaseSession/types";
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
      releaseName: {},
      playlist: { Set: false, Selected: [], UseAll: false },
    },
    selectedTrackers: [],
    progress: { correlationID: "", status: "idle", message: "", steps: [] },
    preview: null,
    trackerData: [],
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
          candidates: [{ file: "00001.mpls", duration: 120, items: [], score: 1, edition: "" }],
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
    expect(facet.choosePlaylists).toHaveBeenCalledWith(["00001.mpls"], false);

    const selectedFacet: InputFacet = {
      ...facet,
      view: { ...facet.view, playlist: { ...facet.view.playlist, selected: ["00001.mpls"] } },
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

  it("renders ordered session progress without a hard-coded phase catalog", () => {
    const base = inputFacet();
    const facet: InputFacet = {
      ...base,
      view: {
        ...base.view,
        status: "running",
        progress: {
          correlationID: "attempt-1",
          status: "running",
          message: "Future work is active.",
          steps: [
            {
              phase: "future_phase",
              order: 9999,
              label: "Future preparation phase",
              message: "Future phase detail.",
              status: "running",
              timestamp: "2026-07-16T00:00:00Z",
            },
          ],
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

    expect(screen.getByText("Future preparation phase")).toBeVisible();
    expect(screen.getByText("Future phase detail.")).toBeVisible();
  });

  it("hides preparation progress after preparation completes", () => {
    const base = inputFacet();
    const facet: InputFacet = {
      ...base,
      view: {
        ...base.view,
        status: "ready",
        progress: {
          correlationID: "attempt-1",
          status: "ready",
          message: "Metadata preparation complete.",
          steps: [
            {
              phase: "source_inspection",
              order: 100,
              label: "Inspect source",
              message: "Source inspected.",
              status: "completed",
              timestamp: "2026-07-16T00:00:00Z",
            },
          ],
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

    expect(screen.queryByText("Preparation progress")).not.toBeInTheDocument();
    expect(screen.queryByText("Inspect source")).not.toBeInTheDocument();
  });
});
