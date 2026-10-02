// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import RenderedDescription from "./RenderedDescription";

describe("RenderedDescription external links", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
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

  it("routes aux-clicks through the same external-link handling", () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    render(<RenderedDescription html='<a href="https://example.com/raw">Raw URL</a>' />);

    fireEvent(
      screen.getByRole("link", { name: "Raw URL" }),
      new MouseEvent("auxclick", { bubbles: true, button: 1 }),
    );

    expect(open).toHaveBeenCalledWith("https://example.com/raw", "_blank", "noopener,noreferrer");
  });

  it("blocks relative links instead of opening them against the app origin", () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    render(<RenderedDescription html='<a href="/relative/release">Relative release</a>' />);

    const event = new MouseEvent("click", { bubbles: true, cancelable: true });
    const dispatched = screen.getByRole("link", { name: "Relative release" }).dispatchEvent(event);

    expect(dispatched).toBe(false);
    expect(event.defaultPrevented).toBe(true);
    expect(open).not.toHaveBeenCalled();
  });

  it("opens a bare preview image at full size in the lightbox", () => {
    const openImage = vi.fn();
    const full = "https://images.example.invalid/full.png";
    const preview = "data:image/gif;base64,R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs";
    render(
      <RenderedDescription
        html={`<img src="${preview}" alt="Screenshot">`}
        imagePreviews={{ [full]: preview }}
        onImageOpen={openImage}
      />,
    );
    const image = screen.getByRole("img", { name: "Screenshot" });
    expect(image.closest("button")).toHaveAttribute("type", "button");
    const allowed = image.dispatchEvent(
      new MouseEvent("click", { bubbles: true, cancelable: true }),
    );
    expect(allowed).toBe(false);
    expect(openImage).toHaveBeenCalledWith(full, "Screenshot");
  });

  it("opens linked preview images at full size in the lightbox", () => {
    const openImage = vi.fn();
    const openExternal = vi.spyOn(window, "open").mockImplementation(() => null);
    const full = "https://images.example.invalid/full.png";
    const preview = "data:image/gif;base64,R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs";
    render(
      <RenderedDescription
        html={`<a href="about:blank"><img src="${preview}" alt="Screenshot"></a>`}
        imagePreviews={{ [full]: preview }}
        onImageOpen={openImage}
      />,
    );
    const allowed = screen
      .getByRole("img", { name: "Screenshot" })
      .dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    expect(allowed).toBe(false);
    expect(openImage).toHaveBeenCalledWith(full, "Screenshot");
    expect(openExternal).not.toHaveBeenCalled();
  });
});
