// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ScreenshotsFacet } from "../../releaseSession/types";
import type { ScreenshotImage, ScreenshotPlan } from "../../types";
import ScreenshotsPage from ".";

const image = (
  path: string,
  index: number,
  purpose: ScreenshotImage["Purpose"],
): ScreenshotImage => ({
  Index: index,
  TimestampSeconds: index + 1,
  Path: path,
  Purpose: purpose,
  Width: 1920,
  Height: 1080,
  SizeBytes: 1024,
});

const plan = (): ScreenshotPlan => ({
  SourcePath: "C:\\media\\Example.Release.2026.1080p-GRP.mkv",
  DiscType: "",
  DurationSeconds: 120,
  FrameRate: 24,
  SuggestedSelections: [{ Index: 0, TimestampSeconds: 12, Frame: 288, Source: "auto" }],
  ExistingScreenshots: [image("C:\\tmp\\existing.png", 0, "final")],
  ExistingTrackerScreenshots: [image("C:\\tmp\\tracker.png", 1, "final")],
  FinalSelections: [image("C:\\tmp\\final.png", 2, "final")],
  TrackerImageLinks: [
    {
      Tracker: "EXAMPLE",
      URL: "https://images.example/screenshot.png",
      Path: "C:\\tmp\\tracker.png",
    },
  ],
  PreviewImages: [image("C:\\tmp\\preview.png", 3, "preview")],
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
      result: null,
      selections: screenshotPlan.SuggestedSelections,
      finalSelectionPaths: screenshotPlan.FinalSelections.map((entry) => entry.Path),
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
    removeTrackerURL: vi.fn(async () => true),
    removeTrackerURLs: vi.fn(async () => true),
    selectFinal: vi.fn(async () => true),
    reorderFinal: vi.fn(async () => true),
    saveFinal: vi.fn(async () => true),
    readImage: vi.fn(async (path) => `data:image/png;base64,${encodeURIComponent(path)}`),
  };
};

describe("ScreenshotsPage", () => {
  it("restores the main gallery layout while routing actions through the session facet", async () => {
    const screenshots = facet();
    const updateScreenshotConfigValue = vi.fn();
    render(
      <ScreenshotsPage
        facet={screenshots}
        screenshotConfig={{ Screens: 4, ToneMap: false }}
        updateScreenshotConfigValue={updateScreenshotConfigValue}
        loadSettings={vi.fn()}
        settingsLoading={false}
        settingsDirty
        settingsSaved=""
        settingsError=""
        applyScreenshotSettings={vi.fn()}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
      />,
    );

    for (const heading of [
      "Live Preview",
      "Tracker Images",
      "Existing Captures",
      "Tracker Temp Images",
      "Frame Selection",
      "Preview Captures",
      "Final Captures",
    ]) {
      expect(screen.getByRole("heading", { name: heading })).toBeVisible();
    }

    await waitFor(() => expect(screenshots.readImage).toHaveBeenCalledTimes(4));
    expect(screen.getByAltText("Existing 1")).toHaveAttribute(
      "src",
      expect.stringContaining("data:image/png"),
    );

    fireEvent.click(screen.getByRole("button", { name: "Load suggestions" }));
    expect(screenshots.load).toHaveBeenCalledOnce();

    fireEvent.click(screen.getByRole("button", { name: "Generate screenshots" }));
    expect(screenshots.generate).toHaveBeenCalledWith("final");

    const frameSection = screen
      .getByRole("heading", { name: "Frame Selection" })
      .closest("section");
    if (!frameSection) throw new Error("frame selection section missing");
    fireEvent.click(within(frameSection).getByRole("button", { name: "Preview" }));
    expect(screenshots.generate).toHaveBeenCalledWith("preview", [
      { Index: 0, TimestampSeconds: 12, Frame: 288, Source: "auto" },
    ]);

    fireEvent.click(screen.getAllByRole("button", { name: "Add to final" })[0]);
    expect(screenshots.selectFinal).toHaveBeenCalledWith("C:\\tmp\\existing.png", true);

    fireEvent.change(screen.getByLabelText("Screenshot count"), { target: { value: "6" } });
    expect(updateScreenshotConfigValue).toHaveBeenCalledWith("Screens", 6);
  });
});
