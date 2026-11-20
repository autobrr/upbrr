// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { type MouseEvent as ReactMouseEvent, useEffect, useMemo, useRef } from "react";
import { handleExternalLinkClick } from "../utils/externalLinks";
import "./rendered-description.css";

type Props = {
  html: string;
  imagePreviews?: Readonly<Record<string, string>>;
  onImageOpen?: (url: string, alt: string) => void;
};

const configureRenderedLinks = (root: HTMLElement) => {
  root.querySelectorAll<HTMLAnchorElement>("a[href]").forEach((anchor) => {
    anchor.target = "_blank";
    anchor.rel = "noreferrer";
  });
};

const handleRenderedDescriptionLinkClick = (event: ReactMouseEvent<HTMLElement>) => {
  const target = event.target;
  if (!(target instanceof Element) || !target.closest("a[href]")) {
    return;
  }

  handleExternalLinkClick(event);
  if (!event.defaultPrevented) {
    event.preventDefault();
    event.stopPropagation();
  }
};

/** Displays server-sanitized markup and opens valid HTTP(S) image URLs through the optional lightbox callback. */
export default function RenderedDescription({ html, imagePreviews, onImageOpen }: Props) {
  const renderedMarkup = useMemo(() => ({ __html: html }), [html]);
  const rootRef = useRef<HTMLDivElement | null>(null);

  const handleLinkClick = (event: ReactMouseEvent<HTMLElement>) => {
    const target = event.target;
    if (onImageOpen && target instanceof Element) {
      const image =
        target instanceof HTMLImageElement
          ? target
          : target.closest("a[href], button[data-description-lightbox]")?.querySelector("img");
      const full = image?.dataset.fullImage;
      if (full) {
        event.preventDefault();
        event.stopPropagation();
        onImageOpen(full, image.alt || "Description image");
        return;
      }
    }
    handleRenderedDescriptionLinkClick(event);
  };

  useEffect(() => {
    const root = rootRef.current;
    if (!root) {
      return;
    }

    if (onImageOpen) {
      const fullByPreview = new Map(
        Object.entries(imagePreviews || {}).map(([full, preview]) => [preview, full]),
      );
      root.querySelectorAll<HTMLImageElement>("img[src]").forEach((image) => {
        const generatedButton = image.parentElement?.closest("button[data-description-lightbox]");
        if (generatedButton) generatedButton.replaceWith(image);
        delete image.dataset.fullImage;
        const source = image.getAttribute("src") || "";
        const anchor = image.closest("a[href]")?.getAttribute("href");
        const linkedComparisonImage =
          anchor &&
          image.matches(".comparison .comparison__image") &&
          (image.classList.contains("comparison__image--linked") ||
            /^https?:\/\/[^/?#]+\/[^?#]*\.(?:avif|gif|jpe?g|png|webp)(?:[?#]|$)/i.test(anchor));
        const full =
          anchor && (imagePreviews?.[anchor] === source || linkedComparisonImage)
            ? anchor
            : fullByPreview.get(source) || source;
        if (!full) return;
        let parsed: URL;
        try {
          parsed = new URL(full);
        } catch {
          return;
        }
        if ((parsed.protocol !== "http:" && parsed.protocol !== "https:") || !parsed.hostname)
          return;
        image.dataset.fullImage = full;
        if (!image.closest("a[href]")) {
          const button = document.createElement("button");
          button.type = "button";
          button.className = "description-image-button";
          button.dataset.descriptionLightbox = "true";
          button.setAttribute("aria-label", image.alt || "View full-size description image");
          image.replaceWith(button);
          button.append(image);
        }
      });
    }
    configureRenderedLinks(root);
  }, [html, imagePreviews, onImageOpen]);

  useEffect(() => {
    const root = rootRef.current;
    if (!root) return;

    const comparisons = Array.from(root.querySelectorAll<HTMLElement>(".comparison"));
    const cleanups: Array<() => void> = [];

    comparisons.forEach((comparison) => {
      const details = comparison.querySelector<HTMLDetailsElement>(".comparison__details");
      const summary = details?.querySelector<HTMLElement>(":scope > summary");
      const screenshots = details?.querySelector<HTMLElement>(":scope > .comparison__screenshots");
      const rows = Array.from(comparison.querySelectorAll<HTMLElement>(".comparison__row"));
      if (!details || !summary || !screenshots || rows.length === 0) {
        return;
      }

      const maxColumns = rows.reduce((max, row) => Math.max(max, row.children.length), 0);
      if (maxColumns <= 1) {
        return;
      }

      let current = 0;

      const applyColumn = (next: number) => {
        const clamped = Math.min(Math.max(next, 1), maxColumns);
        if (clamped === current) {
          return;
        }
        current = clamped;
        rows.forEach((row) => {
          const columns = Array.from(row.children);
          columns.forEach((cell, index) => {
            const isActive = index + 1 === current;
            cell.classList.toggle("comparison__image-container--hidden", !isActive);
            cell.setAttribute("aria-hidden", String(!isActive));
            if (!isActive && cell.contains(document.activeElement)) summary.focus();
          });
        });
      };

      const handleKeyDown = (event: KeyboardEvent) => {
        if (!details.open) {
          return;
        }
        if (event.key === "ArrowLeft") {
          event.preventDefault();
          applyColumn(current - 1 < 1 ? maxColumns : current - 1);
          return;
        }
        if (event.key === "ArrowRight") {
          event.preventDefault();
          applyColumn(current + 1 > maxColumns ? 1 : current + 1);
          return;
        }
        if (event.key === "Escape") {
          event.preventDefault();
          details.open = false;
          summary.focus();
          return;
        }
        const digit = Number.parseInt(event.key, 10);
        if (!Number.isNaN(digit) && digit >= 1 && digit <= maxColumns) {
          event.preventDefault();
          applyColumn(digit);
        }
      };

      const handleMouseMove = (event: MouseEvent) => {
        if (!details.open) return;
        const width = globalThis.innerWidth || 1;
        const ratio = event.clientX / width;
        applyColumn(Math.min(maxColumns, Math.max(1, Math.ceil(ratio * maxColumns))));
      };

      const syncDisclosure = () => {
        summary.textContent = details.open ? "Close" : "Show";
      };

      const handleFocusOut = (event: FocusEvent) => {
        const next = event.relatedTarget;
        // The portaled image lightbox restores focus to its opener inside this disclosure.
        if (
          details.open &&
          next instanceof Element &&
          !details.contains(next) &&
          !next.closest("[data-image-lightbox]")
        ) {
          details.open = false;
        }
      };

      // Native details grouping keeps a single comparison overlay open, even across previews.
      details.name = "rendered-description-comparison";

      details.addEventListener("toggle", syncDisclosure);
      details.addEventListener("keydown", handleKeyDown);
      details.addEventListener("focusout", handleFocusOut);
      screenshots.addEventListener("mousemove", handleMouseMove);
      syncDisclosure();
      applyColumn(1);

      cleanups.push(() => {
        details.removeEventListener("toggle", syncDisclosure);
        details.removeEventListener("keydown", handleKeyDown);
        details.removeEventListener("focusout", handleFocusOut);
        screenshots.removeEventListener("mousemove", handleMouseMove);
      });
    });

    return () => {
      cleanups.forEach((cleanup) => cleanup());
    };
  }, [html]);

  return (
    <div
      ref={rootRef}
      className="tracker-description rendered whitespace-pre-wrap text-muted-foreground leading-relaxed"
      onAuxClick={handleLinkClick}
      onClick={handleLinkClick}
      dangerouslySetInnerHTML={renderedMarkup}
    />
  );
}
