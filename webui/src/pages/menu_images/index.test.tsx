// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { MenuImagesFacet } from "../../releaseSession/types";
import MenuImagesPage from "./index";

afterEach(cleanup);

const facet = (overrides: Partial<MenuImagesFacet> = {}): MenuImagesFacet => ({
  view: {
    revision: 1,
    status: "ready",
    images: [],
    staleReason: "",
    error: "",
  },
  load: vi.fn(async () => true),
  importFiles: vi.fn(async () => true),
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
          staleReason: "",
          error: "",
        },
        cancelCapture,
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(cancelCapture).toHaveBeenCalledOnce();
  });

  it("counts only DVD menu artifacts and renders the independent menu collection", () => {
    const images = Array.from({ length: 2 }, (_, index) => ({
      image: {
        artifactID: `menu-${index + 1}`,
        index,
        timestampSeconds: 0,
        purpose: "menu" as const,
        width: 720,
        height: 480,
        sizeBytes: 1024,
      },
      contentURL: `/media/menu-${index + 1}`,
    }));
    renderPage(
      facet({
        view: {
          revision: 2,
          status: "ready",
          images,
          artifacts: {
            id: "media-1",
            workflowId: "workflow-1",
            revision: 2,
            release: { id: "release-1", revision: 1 },
            releaseRef: { SourcePath: "C:\\media\\Example.Release.2026.DVD-GRP", Generation: 1 },
            projectionSet: { id: "projections-1", revision: 1 },
            captureFingerprint: "1".repeat(64),
            requirementsFingerprint: "2".repeat(64),
            imageRequirementsPrepared: true,
            artifacts: [
              ...Array.from({ length: 4 }, (_, index) => ({
                id: `screen-${index + 1}`,
                kind: "screenshot" as const,
                purpose: "final" as const,
                selected: true,
              })),
              ...Array.from({ length: 2 }, (_, index) => ({
                id: `menu-${index + 1}`,
                kind: "dvd_menu" as const,
                purpose: "menu" as const,
                selected: true,
              })),
              {
                id: "hosted-menu",
                kind: "hosted_image" as const,
                purpose: "menu" as const,
                selected: true,
                source: "menu-1",
              },
            ],
            status: "completed",
            createdAt: "2026-07-26T00:00:00Z",
          },
          staleReason: "",
          error: "",
        },
      }),
    );

    expect(screen.getByText("2 captured menu image(s)")).toBeInTheDocument();
    expect(screen.getByText("2 saved")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /^Preview DVD menu/ })).toHaveLength(2);
  });
});
