// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { UploadedImagesFacet } from "../../releaseSession/types";
import UploadImagesPage from "./index";

afterEach(cleanup);

describe("UploadImagesPage", () => {
  it("forwards explicit upload intent", () => {
    const upload = vi.fn(async () => true);
    const facet: UploadedImagesFacet = {
      view: {
        revision: 1,
        status: "ready",
        candidates: [
          {
            image: {
              Index: 0,
              TimestampSeconds: 1,
              Path: "C:\\managed\\shot.png",
              Purpose: "final",
              Width: 1920,
              Height: 1080,
              SizeBytes: 1,
            },
            dataURI: "data:image/png;base64,AA==",
          },
        ],
        uploaded: [],
        selectedPaths: ["C:\\managed\\shot.png"],
        host: "example",
        failures: [],
        progress: { correlationID: "", attempts: [] },
        staleReason: "",
        error: "",
      },
      load: vi.fn(async () => true),
      chooseHost: vi.fn(),
      select: vi.fn(),
      selectAll: vi.fn(),
      upload,
      remove: vi.fn(async () => true),
    };
    render(
      <UploadImagesPage
        facet={facet}
        configuredImageHosts={["example"]}
        resolveImageHostLabel={(value) => value}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Upload selected (1)" }));
    expect(upload).toHaveBeenCalledOnce();
  });

  it("shows live progress for each required image host", () => {
    const facet: UploadedImagesFacet = {
      view: {
        revision: 2,
        status: "running",
        candidates: [],
        uploaded: [],
        selectedPaths: [],
        host: "example",
        failures: [],
        progress: {
          correlationID: "image-upload-1",
          attempts: [
            {
              correlationID: "image-upload-1",
              attemptID: "imgbox|global",
              host: "imgbox",
              usageScope: "global",
              trackers: ["AITHER", "ANT"],
              fallback: false,
              completed: 1,
              total: 3,
              succeeded: 1,
              failed: 0,
              reused: 0,
              status: "running",
              message: "Uploading images.",
              timestamp: "2026-07-16T00:00:00Z",
            },
            {
              correlationID: "image-upload-1",
              attemptID: "pixhost|tracker:RF",
              host: "pixhost",
              usageScope: "tracker:RF",
              trackers: ["RF"],
              fallback: true,
              completed: 2,
              total: 3,
              succeeded: 2,
              failed: 0,
              reused: 0,
              status: "running",
              message: "Uploading images.",
              timestamp: "2026-07-16T00:00:01Z",
            },
          ],
        },
        staleReason: "",
        error: "",
      },
      load: vi.fn(async () => true),
      chooseHost: vi.fn(),
      select: vi.fn(),
      selectAll: vi.fn(),
      upload: vi.fn(async () => true),
      remove: vi.fn(async () => true),
    };

    render(
      <UploadImagesPage
        facet={facet}
        configuredImageHosts={["example"]}
        resolveImageHostLabel={(value) => value}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
      />,
    );

    expect(screen.getByRole("progressbar", { name: "Image host upload progress" })).toHaveAttribute(
      "aria-valuenow",
      "3",
    );
    expect(
      screen.getByText("3 of 6 image-host uploads processed across 2 hosts."),
    ).toBeInTheDocument();
    expect(screen.getByText("imgbox")).toBeInTheDocument();
    expect(screen.getByText(/2 trackers/)).toBeInTheDocument();
    expect(screen.getByText(/RF · fallback/)).toBeInTheDocument();
  });
});
