// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { InputFacet } from "../../releaseSession/types";
import TrackerDataPage from "./index";

afterEach(cleanup);

describe("TrackerDataPage", () => {
  it("renders the input facet's immutable tracker-data view", () => {
    const facet = {
      view: {
        sourceDraft: "",
        selectedSource: "",
        status: "idle",
        error: "",
        preparationDirty: false,
        intent: { sourceLookupURL: "", identity: {}, releaseName: {}, trackers: [] },
        preview: null,
        trackerData: [],
      },
    } as unknown as InputFacet;
    render(
      <TrackerDataPage
        facet={facet}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
        trackerIconSrcByName={{}}
      />,
    );
    expect(screen.getByText("No tracker data available.")).toBeInTheDocument();
  });

  it("identifies render and image-preview actions for each populated tracker entry", () => {
    const image = vi.fn();
    const alt = vi.fn();
    const facet = {
      view: {
        trackerData: [
          {
            Tracker: "HDS",
            TrackerID: "one",
            Description: "First description",
            DescriptionHTML: "<p>First description</p>",
            ImageURLs: ["https://example.invalid/one.png"],
          },
          {
            Tracker: "BTN",
            TrackerID: "two",
            Description: "Second description",
            DescriptionHTML: "<p>Second description</p>",
            ImageURLs: ["https://example.invalid/two.png"],
          },
        ],
      },
    } as unknown as InputFacet;
    render(
      <TrackerDataPage
        facet={facet}
        setLightboxImage={image}
        setLightboxAlt={alt}
        trackerIconSrcByName={{}}
      />,
    );

    fireEvent.click(screen.getByText("Torrent ID: two"));
    fireEvent.click(screen.getByRole("button", { name: "Render HDS entry 1" }));
    expect(screen.getByRole("button", { name: "Show raw HDS entry 1" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Render BTN entry 2" })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Preview BTN image 1 in entry 2" }));
    expect(image).toHaveBeenCalledWith("https://example.invalid/two.png");
    expect(alt).toHaveBeenCalledWith("BTN image");
  });

  it("keeps an icon-only tracker name in the disclosure's accessible text", () => {
    const facet = {
      view: {
        trackerData: [
          {
            Tracker: "HDS",
            TrackerID: "synthetic-1",
            Description: "Synthetic description",
            DescriptionHTML: "",
            ImageURLs: [],
          },
        ],
      },
    } as unknown as InputFacet;
    render(
      <TrackerDataPage
        facet={facet}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
        trackerIconSrcByName={{}}
        faviconOnly
      />,
    );
    const summary = screen.getByText("Torrent ID: synthetic-1").closest("summary");
    expect(summary).not.toBeNull();
    expect(summary?.querySelector(".sr-only")).toHaveTextContent("HDS");
  });
});
