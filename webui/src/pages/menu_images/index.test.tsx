// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { MenuImagesFacet } from "../../releaseSession/types";
import type { DVDMenuCaptureResult } from "../../types";
import MenuImagesPage from "./index";

afterEach(cleanup);

const captureResult = (overrides: Partial<DVDMenuCaptureResult> = {}): DVDMenuCaptureResult => ({
  SourcePath: "C:\\media\\Example",
  Images: [
    {
      Path: "C:\\managed\\menu.png",
      TimestampSeconds: 0,
      Index: 0,
      Purpose: "menu",
      Width: 1920,
      Height: 1080,
      SizeBytes: 1,
      Discovery: "reachable",
    },
  ],
  SelectedLanguage: "en",
  Region: 0,
  DiscoveredMenus: 1,
  VisitedStates: 2,
  VisitedButtons: 1,
  MaxItems: 6,
  Complete: true,
  Partial: false,
  Truncated: false,
  Warnings: [],
  Engine: {
    EngineVersion: "phase0a-1",
    SchemaVersion: 1,
    SupportedFeatures: [],
    FFmpegVersion: "8.0",
    FFmpegDVDVideo: true,
    MissingFFmpegOptions: [],
  },
  ...overrides,
});

const facet = (overrides: Partial<MenuImagesFacet> = {}): MenuImagesFacet => ({
  view: {
    revision: 1,
    status: "ready",
    images: [],
    capture: null,
    staleReason: "",
    error: "",
  },
  load: vi.fn(async () => true),
  importPaths: vi.fn(async () => true),
  capture: vi.fn(async () => true),
  cancelCapture: vi.fn(),
  remove: vi.fn(async () => true),
  ...overrides,
});

const renderPage = (menuFacet: MenuImagesFacet) =>
  render(
    <MenuImagesPage
      facet={menuFacet}
      currentDiscType="DVD"
      maxMenuItems={6}
      onContinue={vi.fn()}
      setLightboxImage={vi.fn()}
      setLightboxAlt={vi.fn()}
    />,
  );

describe("MenuImagesPage", () => {
  it("forwards capture intent through the facet", async () => {
    const capture = vi.fn(async () => true);
    renderPage(facet({ capture }));

    fireEvent.click(screen.getByRole("button", { name: "Capture DVD menus" }));

    await waitFor(() => expect(capture).toHaveBeenCalledOnce());
  });

  it("forwards explicit capture cancellation", () => {
    const cancelCapture = vi.fn();
    renderPage(
      facet({
        view: {
          revision: 2,
          status: "running",
          images: [],
          capture: null,
          staleReason: "",
          error: "",
        },
        cancelCapture,
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(cancelCapture).toHaveBeenCalledOnce();
  });

  it("renders stable capture warnings and truncation from the immutable view", () => {
    renderPage(
      facet({
        view: {
          revision: 3,
          status: "ready",
          images: [],
          capture: captureResult({
            Truncated: true,
            Warnings: [{ Code: "render_partial", Message: "One menu could not be rendered." }],
          }),
          staleReason: "",
          error: "",
        },
      }),
    );

    expect(screen.getByText("Maximum reached")).toBeInTheDocument();
    expect(screen.getByText("Configured maximum: 6.")).toBeInTheDocument();
    expect(screen.getByText("One menu could not be rendered.")).toBeInTheDocument();
  });
});
