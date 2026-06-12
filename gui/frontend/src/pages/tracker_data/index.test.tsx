// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { MetadataPreview } from "../../types";
import TrackerDataPage from "./index";

describe("TrackerDataPage", () => {
  afterEach(() => {
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
});
