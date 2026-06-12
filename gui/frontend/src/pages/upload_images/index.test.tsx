// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import UploadImagesPage from "./index";

describe("UploadImagesPage", () => {
  afterEach(() => {
    cleanup();
  });

  it("shows remote tracker image URLs without downloaded local artifacts", () => {
    const trackerImageURL = "https://images.example/screen.png";

    render(
      <UploadImagesPage
        path="C:/media/Movie.mkv"
        uploadHost="imgbb"
        setUploadHost={vi.fn()}
        configuredImageHosts={["imgbb"]}
        resolveImageHostLabel={(value) => value}
        uploadImagesLoading={false}
        uploadProgress={{ current: 0, total: 0 }}
        setAllUploadSelections={vi.fn()}
        handleUploadImages={vi.fn()}
        uploadImagesError=""
        uploadImageFailures={[]}
        uploadCandidates={[]}
        uploadSelections={{}}
        toggleUploadSelection={vi.fn()}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
        uploadedRecordByPath={new Map()}
        uploadedImages={[]}
        uploadedImageRecords={[]}
        trackerImageLinks={[]}
        trackerImageURLs={[trackerImageURL]}
        handleDeleteUploadedImage={vi.fn()}
        handleDeleteTrackerImage={vi.fn()}
      />,
    );

    expect(screen.getByText(trackerImageURL)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Raw URL" })).toHaveAttribute("href", trackerImageURL);
  });
});
