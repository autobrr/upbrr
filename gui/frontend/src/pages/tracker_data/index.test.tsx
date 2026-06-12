// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { MetadataPreview } from "../../types";
import TrackerDataPage from "./index";

describe("TrackerDataPage", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    cleanup();
  });

  it("does not decode sanitized attribute entities before rendering", () => {
    const preview = {
      TrackerData: [
        {
          Tracker: "BLU",
          TrackerID: "1",
          TorrentURL: "",
          InfoHash: "",
          TMDBID: 0,
          IMDBID: 0,
          TVDBID: 0,
          MALID: 0,
          Category: "",
          Description: "raw",
          DescriptionHTML: `<img src="http://invalid.invalid/&#34; onerror=&#34;alert(1)" />`,
          ImageURLs: [],
          Filename: "",
          Matched: false,
          UpdatedAt: "",
        },
      ],
    } as unknown as MetadataPreview;

    const { container } = render(
      <TrackerDataPage
        preview={preview}
        trackerUploadItems={[]}
        renderedDescriptions={{ "BLU-0": true }}
        setRenderedDescriptions={vi.fn()}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
      />,
    );

    const image = container.querySelector(".tracker-description.rendered img");
    expect(image).toBeInstanceOf(HTMLImageElement);
    expect(image?.getAttribute("onerror")).toBeNull();
  });

  it("hides tracker names when favicon-only mode is enabled", () => {
    const preview = {
      TrackerData: [
        {
          Tracker: "BLU",
          TrackerID: "1",
          TorrentURL: "",
          InfoHash: "",
          TMDBID: 0,
          IMDBID: 0,
          TVDBID: 0,
          MALID: 0,
          Category: "",
          Description: "",
          DescriptionHTML: "",
          ImageURLs: [],
          Filename: "",
          Matched: false,
          UpdatedAt: "",
        },
      ],
    } as unknown as MetadataPreview;

    render(
      <TrackerDataPage
        preview={preview}
        trackerUploadItems={[{ name: "BLU", config: {} }]}
        renderedDescriptions={{}}
        setRenderedDescriptions={vi.fn()}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
        useFavicons={true}
        faviconOnly={true}
      />,
    );

    expect(screen.queryByText("BLU")).toBeNull();
  });

  it("uses configured tracker favicon URLs", async () => {
    const getTrackerIcon = vi.fn().mockResolvedValue("");
    vi.stubGlobal("go", { guiapp: { App: { GetTrackerIcon: getTrackerIcon } } });
    const preview = {
      TrackerData: [
        {
          Tracker: "BLU",
          TrackerID: "1",
          TorrentURL: "https://blutopia.cc/torrents/1",
          InfoHash: "",
          TMDBID: 0,
          IMDBID: 0,
          TVDBID: 0,
          MALID: 0,
          Category: "",
          Description: "",
          DescriptionHTML: "",
          ImageURLs: [],
          Filename: "",
          Matched: false,
          UpdatedAt: "",
        },
      ],
    } as unknown as MetadataPreview;

    render(
      <TrackerDataPage
        preview={preview}
        trackerUploadItems={[
          { name: "BLU", config: { FaviconURL: "https://icons.example/blu.png" } },
        ]}
        renderedDescriptions={{}}
        setRenderedDescriptions={vi.fn()}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
        useFavicons={true}
      />,
    );

    await screen.findByText("Torrent ID: 1");
    expect(getTrackerIcon).toHaveBeenCalledWith("icons.example", "https://icons.example/blu.png");
  });

  it("does not fetch favicons for trackers missing from config", () => {
    const getTrackerIcon = vi.fn().mockResolvedValue("");
    vi.stubGlobal("go", { guiapp: { App: { GetTrackerIcon: getTrackerIcon } } });
    const preview = {
      TrackerData: [
        {
          Tracker: "UNCONFIGURED",
          TrackerID: "1",
          TorrentURL: "",
          InfoHash: "",
          TMDBID: 0,
          IMDBID: 0,
          TVDBID: 0,
          MALID: 0,
          Category: "",
          Description: "",
          DescriptionHTML: "",
          ImageURLs: [],
          Filename: "",
          Matched: false,
          UpdatedAt: "",
        },
      ],
    } as unknown as MetadataPreview;

    render(
      <TrackerDataPage
        preview={preview}
        trackerUploadItems={[]}
        renderedDescriptions={{}}
        setRenderedDescriptions={vi.fn()}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
        useFavicons={true}
        faviconOnly={true}
      />,
    );

    expect(screen.getAllByText("UNCONFIGURED").length).toBeGreaterThan(0);
    expect(getTrackerIcon).not.toHaveBeenCalled();
  });
});
