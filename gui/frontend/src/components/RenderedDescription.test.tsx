// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import RenderedDescription from "./RenderedDescription";

describe("RenderedDescription external links", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    delete (globalThis as typeof globalThis & { go?: unknown }).go;
  });

  it("marks rendered links to open in a new tab", () => {
    render(<RenderedDescription html='<a href="https://example.com/image">View image</a>' />);

    const link = screen.getByRole("link", { name: "View image" });

    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noreferrer");
  });

  it("opens web UI links in a new tab", () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    render(<RenderedDescription html='<a href="https://example.com/torrent">Torrent</a>' />);

    fireEvent.click(screen.getByRole("link", { name: "Torrent" }));

    expect(open).toHaveBeenCalledWith(
      "https://example.com/torrent",
      "_blank",
      "noopener,noreferrer",
    );
  });

  it("opens GUI links through the default-browser bridge", async () => {
    const openExternalURL = vi.fn().mockResolvedValue(undefined);
    (globalThis as any).go = {
      guiapp: { App: { OpenExternalURL: openExternalURL } },
    };
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    render(<RenderedDescription html='<a href="https://example.com/release">Release</a>' />);

    fireEvent.click(screen.getByRole("link", { name: "Release" }));

    await waitFor(() => {
      expect(openExternalURL).toHaveBeenCalledWith("https://example.com/release");
    });
    expect(open).not.toHaveBeenCalled();
  });

  it("routes aux-clicks through the same external-link handling", async () => {
    const openExternalURL = vi.fn().mockResolvedValue(undefined);
    (globalThis as any).go = {
      guiapp: { App: { OpenExternalURL: openExternalURL } },
    };
    render(<RenderedDescription html='<a href="https://example.com/raw">Raw URL</a>' />);

    fireEvent(
      screen.getByRole("link", { name: "Raw URL" }),
      new MouseEvent("auxclick", { bubbles: true, button: 1 }),
    );

    await waitFor(() => {
      expect(openExternalURL).toHaveBeenCalledWith("https://example.com/raw");
    });
  });

  it("blocks relative links instead of opening them against the app origin", () => {
    const openExternalURL = vi.fn().mockResolvedValue(undefined);
    (globalThis as any).go = {
      guiapp: { App: { OpenExternalURL: openExternalURL } },
    };
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    render(<RenderedDescription html='<a href="/relative/release">Relative release</a>' />);

    const event = new MouseEvent("click", { bubbles: true, cancelable: true });
    const dispatched = screen.getByRole("link", { name: "Relative release" }).dispatchEvent(event);

    expect(dispatched).toBe(false);
    expect(event.defaultPrevented).toBe(true);
    expect(openExternalURL).not.toHaveBeenCalled();
    expect(open).not.toHaveBeenCalled();
  });
});
