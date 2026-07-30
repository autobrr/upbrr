// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { UploadedImagesFacet } from "../../releaseSession/types";
import UploadImagesPage from "./index";

afterEach(cleanup);

describe("UploadImagesPage", () => {
  it("loads stale candidates so the session can default-select every image", async () => {
    const load = vi.fn(async () => true);
    const facet: UploadedImagesFacet = {
      view: {
        revision: 1,
        status: "idle",
        candidates: [],
        uploaded: [],
        selectedArtifactIDs: [],
        failures: [],
        progress: { correlationID: "", attempts: [] },
        staleReason: "Image assets changed.",
        error: "",
      },
      load,
      select: vi.fn(),
      selectAll: vi.fn(),
      upload: vi.fn(async () => true),
      remove: vi.fn(async () => true),
    };

    render(
      <UploadImagesPage
        facet={facet}
        resolveImageHostLabel={(value) => value}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
      />,
    );

    await waitFor(() => expect(load).toHaveBeenCalledOnce());
  });

  it("forwards backend-derived host preparation intent", () => {
    const upload = vi.fn(async () => true);
    const facet: UploadedImagesFacet = {
      view: {
        revision: 1,
        status: "ready",
        candidates: [
          {
            image: {
              artifactID: "artifact-1",
              index: 0,
              timestampSeconds: 1,
              purpose: "final",
              width: 1920,
              height: 1080,
              sizeBytes: 1,
            },
            contentURL: "/api/app/release-workflow-media?artifactId=artifact-1",
          },
        ],
        uploaded: [],
        selectedArtifactIDs: ["artifact-1"],
        failures: [],
        progress: { correlationID: "", attempts: [] },
        staleReason: "",
        error: "",
      },
      load: vi.fn(async () => true),
      select: vi.fn(),
      selectAll: vi.fn(),
      upload,
      remove: vi.fn(async () => true),
    };
    render(
      <UploadImagesPage
        facet={facet}
        resolveImageHostLabel={(value) => value}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Prepare required hosts (1)" }));
    expect(upload).toHaveBeenCalledOnce();
  });

  it("shows live progress for each required image host", () => {
    const facet: UploadedImagesFacet = {
      view: {
        revision: 2,
        status: "running",
        candidates: [],
        uploaded: [],
        selectedArtifactIDs: [],
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
      select: vi.fn(),
      selectAll: vi.fn(),
      upload: vi.fn(async () => true),
      remove: vi.fn(async () => true),
    };

    render(
      <UploadImagesPage
        facet={facet}
        resolveImageHostLabel={(value) => value}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
      />,
    );

    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
    expect(
      screen.getByText("3 of 6 image-host uploads processed across 2 hosts."),
    ).toBeInTheDocument();
    expect(screen.getByText("imgbox")).toBeInTheDocument();
    expect(screen.getByText(/2 trackers/)).toBeInTheDocument();
    expect(screen.getByText(/RF · fallback/)).toBeInTheDocument();
  });
});
