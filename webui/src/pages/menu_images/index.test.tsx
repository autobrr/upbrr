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
});
