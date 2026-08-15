// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ScreenshotsFacet } from "../../releaseSession/types";
import type { ScreenshotPlan } from "../../types";
import ScreenshotsPage from ".";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const plan = (): ScreenshotPlan => ({
  SourcePath: "C:\\media\\Example.Release.2026.1080p-GRP.mkv",
  DiscType: "",
  DurationSeconds: 120,
  FrameRate: 24,
  SuggestedSelections: [{ Index: 0, TimestampSeconds: 12, Frame: 288, Source: "auto" }],
  ExistingScreenshots: [],
  ExistingTrackerScreenshots: [],
  FinalSelections: [],
  TrackerImageLinks: [],
  PreviewImages: [],
  MetadataTimestamp: "2026-07-16T00:00:00Z",
  RequiresManualFrames: false,
});

const facet = (): ScreenshotsFacet => {
  const screenshotPlan = plan();
  return {
    view: {
      revision: 1,
      status: "ready",
      plan: screenshotPlan,
      workflowMode: true,
      artifacts: {
        id: "media-1",
        workflowId: "workflow-1",
        revision: 6,
        release: { id: "release-1", revision: 2 },
        releaseRef: {
          SourcePath: "C:\\media\\Example.Release.2026.1080p-GRP.mkv",
          Generation: 1,
        },
        projectionSet: { id: "projections-1", revision: 4 },
        captureFingerprint: "1".repeat(64),
        requirementsFingerprint: "2".repeat(64),
        imageRequirementsPrepared: false,
        artifacts: [
          {
            id: "artifact-1",
            index: 3,
            kind: "screenshot",
            purpose: "final",
            selected: true,
            order: 0,
            width: 1920,
            height: 1080,
            sizeBytes: 1024,
            url: "/api/app/release-workflow-media?artifactId=artifact-1",
          },
        ],
        status: "completed",
        createdAt: "2026-07-21T00:00:00Z",
      },
      selections: screenshotPlan.SuggestedSelections,
      finalSelectionArtifactIDs: ["artifact-1"],
      previewImage: "data:image/png;base64,live",
      staleReason: "",
      error: "",
    },
    load: vi.fn(async () => true),
    changeSelection: vi.fn(),
    generate: vi.fn(async () => true),
    previewFrame: vi.fn(async () => true),
    remove: vi.fn(async () => true),
    removeMany: vi.fn(async () => true),
    selectFinal: vi.fn(async () => true),
    reorderFinal: vi.fn(async () => true),
    saveFinal: vi.fn(async () => true),
    selectArtifact: vi.fn(async () => true),
    deleteArtifacts: vi.fn(async () => true),
    readImage: vi.fn(async (path) => `data:image/png;base64,${encodeURIComponent(path)}`),
  };
};

describe("ScreenshotsPage", () => {
  it("loads frame suggestions when an authoritative workflow first opens the page", async () => {
    const base = facet();
    const screenshots: ScreenshotsFacet = {
      ...base,
      view: {
        ...base.view,
        status: "idle",
        workflowMode: true,
        plan: null,
        selections: [],
      },
    };

    render(
      <ScreenshotsPage facet={screenshots} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />,
    );

    await waitFor(() => expect(screenshots.load).toHaveBeenCalledOnce());
  });

  it("does not retry a failed automatic frame suggestion load", () => {
    const base = facet();
    const screenshots: ScreenshotsFacet = {
      ...base,
      view: {
        ...base.view,
        status: "error",
        workflowMode: true,
        plan: null,
        selections: [],
        error: "Unable to load frame suggestions.",
      },
    };

    render(
      <ScreenshotsPage facet={screenshots} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />,
    );

    expect(screenshots.load).not.toHaveBeenCalled();
  });

  it("restores the main gallery layout while routing actions through the session facet", async () => {
    const screenshots = facet();
    render(
      <ScreenshotsPage facet={screenshots} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />,
    );

    for (const heading of ["Live Preview", "Generated Screenshots"]) {
      expect(screen.getByRole("heading", { name: heading })).toBeVisible();
    }
    const frameSummary = screen.getByText("Frame Selection · 1 frame");
    const frameDetails = frameSummary.closest("details");
    expect(frameDetails).not.toHaveAttribute("open");

    fireEvent.click(screen.getByRole("button", { name: "Load suggestions" }));
    expect(screenshots.load).toHaveBeenCalledOnce();

    fireEvent.click(screen.getByRole("button", { name: "Generate screenshots" }));
    expect(screenshots.generate).toHaveBeenCalledWith("final");

    if (!frameDetails) throw new Error("frame selection details missing");
    fireEvent.click(frameSummary);
    fireEvent.click(within(frameDetails).getByRole("button", { name: "Preview" }));
    expect(screenshots.generate).toHaveBeenCalledWith("preview", [
      { Index: 0, TimestampSeconds: 12, Frame: 288, Source: "auto" },
    ]);
    expect(screen.queryByText("Screenshot settings")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Reload settings" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Apply settings" })).not.toBeInTheDocument();
  });

  it("renders and mutates workflow-owned screenshots by opaque artifact ID", () => {
    const base = facet();
    const screenshots: ScreenshotsFacet = {
      ...base,
      view: {
        ...base.view,
        workflowMode: true,
        artifacts: {
          id: "media-1",
          workflowId: "workflow-1",
          revision: 6,
          release: { id: "release-1", revision: 2 },
          releaseRef: {
            SourcePath: "C:\\media\\Example.Release.2026.1080p-GRP.mkv",
            Generation: 1,
          },
          projectionSet: { id: "projections-1", revision: 4 },
          captureFingerprint: "1".repeat(64),
          requirementsFingerprint: "2".repeat(64),
          imageRequirementsPrepared: false,
          artifacts: [
            {
              id: "artifact-1",
              kind: "screenshot",
              purpose: "final",
              selected: true,
              order: 0,
              width: 1920,
              height: 1080,
              sizeBytes: 1024,
              url: "/api/app/release-workflow-media?artifactId=artifact-1",
            },
          ],
          status: "completed",
          createdAt: "2026-07-21T00:00:00Z",
        },
      },
    };
    const confirm = vi.fn(() => true);
    vi.stubGlobal("confirm", confirm);
    const view = render(
      <ScreenshotsPage facet={screenshots} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />,
    );
    const page = within(view.container);

    const gallery = page.getByRole("heading", { name: "Generated Screenshots" }).closest("section");
    if (!gallery) throw new Error("generated screenshot gallery missing");
    expect(within(gallery).getByAltText("Screenshot 1")).toHaveAttribute(
      "src",
      expect.stringContaining("artifactId=artifact-1"),
    );
    fireEvent.click(within(gallery).getByRole("button", { name: "Unselect" }));
    expect(screenshots.selectArtifact).toHaveBeenCalledWith("artifact-1", false);
    fireEvent.click(within(gallery).getByRole("button", { name: "Delete" }));
    expect(screenshots.deleteArtifacts).toHaveBeenCalledWith(["artifact-1"]);
    fireEvent.click(within(gallery).getByRole("button", { name: "Delete all" }));
    expect(confirm).toHaveBeenCalledOnce();
    expect(screenshots.deleteArtifacts).toHaveBeenLastCalledWith(["artifact-1"]);

    const captureActions = page
      .getByRole("button", { name: "Generate screenshots" })
      .closest("section");
    const frameDetails = page.getByText("Frame Selection · 1 frame").closest("details");
    expect(captureActions?.compareDocumentPosition(frameDetails as Node)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
    expect(frameDetails).not.toHaveAttribute("open");
    vi.unstubAllGlobals();
  });

  it("captures the live preview as a new retained screenshot", () => {
    const screenshots = facet();
    render(
      <ScreenshotsPage facet={screenshots} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />,
    );

    const preview = screen.getByRole("heading", { name: "Live Preview" }).closest("section");
    if (!preview) throw new Error("live preview missing");
    fireEvent.change(within(preview).getByLabelText("Seconds"), { target: { value: "37.5" } });
    fireEvent.click(within(preview).getByRole("button", { name: "Capture preview" }));

    expect(screenshots.generate).toHaveBeenCalledWith("final", [
      { Index: 4, TimestampSeconds: 37.5, Frame: 900, Source: "manual" },
    ]);
  });

  it("excludes DVD menus and hosted menu variants from normal screenshot counts", () => {
    const base = facet();
    const artifacts = [
      ...Array.from({ length: 4 }, (_, index) => ({
        id: `screen-${index + 1}`,
        kind: "screenshot" as const,
        purpose: "final" as const,
        selected: true,
        order: index,
        url: `/media/screen-${index + 1}`,
      })),
      ...Array.from({ length: 2 }, (_, index) => ({
        id: `menu-${index + 1}`,
        kind: "dvd_menu" as const,
        purpose: "menu" as const,
        selected: true,
        order: index + 4,
        url: `/media/menu-${index + 1}`,
      })),
      {
        id: "hosted-menu",
        kind: "hosted_image" as const,
        purpose: "menu" as const,
        selected: true,
        order: 6,
        source: "menu-1",
        url: "https://img.example.invalid/menu-1.png",
      },
    ];
    const screenshots: ScreenshotsFacet = {
      ...base,
      view: {
        ...base.view,
        artifacts: {
          ...base.view.artifacts!,
          artifacts,
        },
        finalSelectionArtifactIDs: ["screen-1", "screen-2", "screen-3", "screen-4"],
      },
    };

    render(
      <ScreenshotsPage facet={screenshots} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />,
    );

    expect(screen.getByText("4 captured screenshot(s)")).toBeInTheDocument();
    expect(screen.getAllByAltText(/^Screenshot \d$/)).toHaveLength(4);
  });
});
