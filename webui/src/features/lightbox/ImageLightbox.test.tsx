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
});
