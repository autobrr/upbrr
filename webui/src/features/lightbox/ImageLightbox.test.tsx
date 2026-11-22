// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ImageLightbox } from "./ImageLightbox";

describe("ImageLightbox", () => {
  afterEach(cleanup);
  it("keeps dismissal outside the keyboard-accessible image scroll area", () => {
    const onClose = vi.fn();
    render(<ImageLightbox image="/full-size.png" alt="Screenshot 1" onClose={onClose} />);

    const dialog = screen.getByRole("dialog", { name: "Screenshot 1" });
    const scrollArea = within(dialog).getByRole("region", { name: "Image" });
    expect(scrollArea).toHaveAttribute("tabindex", "0");
    expect(within(scrollArea).getByRole("img")).toHaveAttribute("src", "/full-size.png");
    const close = within(dialog).getByRole("button", { name: "Close image preview" });
    expect(scrollArea).not.toContainElement(close);
    fireEvent.click(close);
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("preserves Escape dismissal while the scroll area is focused", () => {
    const onClose = vi.fn();
    render(<ImageLightbox image="/full-size.png" alt="" onClose={onClose} />);
    const scrollArea = screen.getByRole("region", { name: "Image" });
    scrollArea.focus();
    fireEvent.keyDown(scrollArea, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();
    expect(screen.getByRole("dialog", { name: "Image preview" })).toBeVisible();
  });

  it("removes the dialog when closed and can reopen another image", () => {
    const onClose = vi.fn();
    const { rerender } = render(
      <ImageLightbox image="/first.png" alt="First image" onClose={onClose} />,
    );
    rerender(<ImageLightbox image="" alt="" onClose={onClose} />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    rerender(<ImageLightbox image="/second.png" alt="Second image" onClose={onClose} />);
    expect(screen.getByRole("img", { name: "Second image" })).toHaveAttribute("src", "/second.png");
  });

  it("reports an image failure and retries when reopened", () => {
    const onClose = vi.fn();
    const { rerender } = render(
      <ImageLightbox image="/poster.png" alt="Poster" onClose={onClose} />,
    );
    fireEvent.error(screen.getByRole("img"));
    expect(screen.getByRole("status")).toHaveTextContent("Image could not be loaded.");
    expect(screen.queryByRole("link", { name: "Open original link" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Close image preview" }));
    expect(onClose).toHaveBeenCalledOnce();
    rerender(<ImageLightbox image="" alt="" onClose={onClose} />);
    rerender(<ImageLightbox image="/poster.png" alt="Poster" onClose={onClose} />);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(screen.getByRole("img")).toHaveAttribute("src", "/poster.png");
  });

  it("offers the original extensionless HTTP link only if the image fails", () => {
    const image = "https://images.example.invalid/by-id?id=42";
    render(<ImageLightbox image={image} alt="Comparison" onClose={vi.fn()} />);
    expect(screen.queryByRole("link", { name: "Open original link" })).not.toBeInTheDocument();
    fireEvent.error(screen.getByRole("img"));
    const link = screen.getByRole("link", { name: "Open original link" });
    expect(link).toHaveAttribute("href", image);
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noreferrer");
  });

  it("closes on browser history navigation and removes its listener when closed", () => {
    const onClose = vi.fn();
    const { rerender } = render(
      <ImageLightbox image="/poster.png" alt="Poster" onClose={onClose} />,
    );
    fireEvent.popState(window);
    expect(onClose).toHaveBeenCalledOnce();
    rerender(<ImageLightbox image="" alt="" onClose={onClose} />);
    fireEvent.popState(window);
    expect(onClose).toHaveBeenCalledOnce();
  });
});
