// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
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

  it.each([
    "https://images.example.invalid/hdr-preview.png",
    "https://images.example.invalid/hdr.png",
  ])("opens an HDR description image using its retained full-size URL (%s)", (preview) => {
    const full = "https://images.example.invalid/hdr.png";
    const openImage = vi.fn();
    render(
      <RenderedDescription
        html={`<details open><summary>source_hdr</summary><img src="${preview}" alt="HDR10+ plot"></details>`}
        imagePreviews={{ [full]: preview }}
        onImageOpen={openImage}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "HDR10+ plot" }));
    expect(openImage).toHaveBeenCalledWith(full, "HDR10+ plot");
  });

  it("opens a previously saved full-size HDR image without a preview map", () => {
    const full = "https://images.example.invalid/hdr.png";
    const openImage = vi.fn();
    render(
      <RenderedDescription
        html={`<img src="${full}" alt="HDR10+ plot">`}
        onImageOpen={openImage}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "HDR10+ plot" }));
    expect(openImage).toHaveBeenCalledWith(full, "HDR10+ plot");
  });

  it.each([
    ["comparison__image--linked", "https://images.example.invalid/by-id?id=42"],
    ["", "https://images.example.invalid/thumb.png"],
  ])(
    "accepts extensionless full URLs only for marked comparison images (%s)",
    (marker, expected) => {
      const onImageOpen = vi.fn();
      render(
        <RenderedDescription
          html={`<div class="comparison"><a href="https://images.example.invalid/by-id?id=42"><img class="comparison__image ${marker}" src="https://images.example.invalid/thumb.png" alt="Source"></a></div>`}
          onImageOpen={onImageOpen}
        />,
      );
      fireEvent(
        screen.getByRole("img", { name: "Source" }),
        new MouseEvent("auxclick", { bubbles: true, cancelable: true }),
      );
      expect(onImageOpen).toHaveBeenCalledWith(expected, "Source");
    },
  );

  it.each(["/relative.png", "data:image/png;base64,AAAA", "javascript:alert(1)"])(
    "does not grant lightbox authority to an unmapped unsafe image (%s)",
    (source) => {
      const openImage = vi.fn();
      render(
        <RenderedDescription html={`<img src="${source}" alt="Image">`} onImageOpen={openImage} />,
      );
      fireEvent.click(screen.getByRole("img"));
      expect(openImage).not.toHaveBeenCalled();
      expect(screen.queryByRole("button")).toBeNull();
    },
  );
});

it("switches every row together, wraps on arrows, and opens the selected full image", () => {
  const labels = ["Main input", "Source 2", "Source 3"];
  const preview = "data:image/gif;base64,R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs";
  const full = (row: number, source: number) =>
    `https://images.example.invalid/${row}-${source}.png`;
  const rows = Array.from(
    { length: 5 },
    (_, row) =>
      `<li><ul class="comparison__row">${labels
        .map(
          (label, source) =>
            `<li class="comparison__image-container"><figure>${row === 0 ? `<figcaption>${label}</figcaption>` : ""}<a href="${full(row, source)}"><img class="comparison__image" alt="${label}, row ${row + 1}" src="${preview}"></a></figure></li>`,
        )
        .join("")}</ul></li>`,
  ).join("");
  const comparison = `<div class="comparison"><details class="comparison__details"><summary>Show</summary><ul class="comparison__screenshots">${rows}</ul></details></div>`;
  const imagePreviews = Object.fromEntries(
    Array.from({ length: 5 }, (_, row) =>
      labels.map((_, source) => [full(row, source), preview]),
    ).flat(),
  );
  const onImageOpen = vi.fn();
  const view = (open: typeof onImageOpen) => (
    <>
      <button>Outside comparison</button>
      <RenderedDescription
        html={comparison.repeat(2)}
        imagePreviews={{ ...imagePreviews }}
        onImageOpen={open}
      />
      <div role="dialog" data-image-lightbox>
        <button>Close image</button>
      </div>
    </>
  );
  const { container, rerender, unmount } = render(view(vi.fn()));
  const [first, second] = container.querySelectorAll("details");
  const summary = within(first).getByText("Show");
  first.open = true;
  summary.focus();

  const visible = (details = first) =>
    within(details)
      .getAllByRole("img")
      .map((image) => image.getAttribute("alt"));
  expect(visible()).toEqual(labelsFor("Main input"));
  fireEvent.mouseMove(first.querySelector(".comparison__screenshots")!, {
    clientX: window.innerWidth / 2,
  });
  expect(visible()).toEqual(labelsFor("Source 2"));
  rerender(view(onImageOpen));
  expect(first).toBeInTheDocument();
  expect(visible()).toEqual(labelsFor("Source 2"));
  fireEvent.keyDown(summary, { key: "ArrowRight" });
  expect(visible()).toEqual(labelsFor("Source 3"));
  fireEvent.keyDown(summary, { key: "ArrowRight" });
  expect(visible()).toEqual(labelsFor("Main input"));
  fireEvent.keyDown(summary, { key: "ArrowLeft" });
  expect(visible()).toEqual(labelsFor("Source 3"));
  const activeImage = within(first).getByRole("img", { name: "Source 3, row 1" });
  expect(
    activeImage.dispatchEvent(new MouseEvent("auxclick", { bubbles: true, cancelable: true })),
  ).toBe(false);
  expect(onImageOpen).toHaveBeenCalledWith(full(0, 2), "Source 3, row 1");
  const overlay = first.querySelector<HTMLElement>(".comparison__screenshots")!;
  overlay.focus();
  expect(overlay).toHaveFocus();
  expect(first.open).toBe(true);
  fireEvent.keyDown(overlay, { key: "Escape" });
  expect(first.open).toBe(false);
  expect(summary).toHaveFocus();
  second.open = true;
  expect(visible(second)).toEqual(labelsFor("Main input"));
  const secondSummary = second.querySelector("summary")!;
  secondSummary.focus();
  screen.getByRole("button", { name: "Close image" }).focus();
  expect(second.open).toBe(true);
  secondSummary.focus();
  screen.getByRole("button", { name: "Outside comparison" }).focus();
  expect(second.open).toBe(false);
  unmount();

  function labelsFor(source: string) {
    return Array.from({ length: 5 }, (_, row) => `${source}, row ${row + 1}`);
  }
});
