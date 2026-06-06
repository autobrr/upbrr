// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useRef } from "react";
import { handleExternalLinkClick } from "../utils/externalLinks";

type Props = {
  html: string;
};

export default function RenderedDescription({ html }: Props) {
  const rootRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const root = rootRef.current;
    if (!root) {
      return;
    }

    const comparisons = Array.from(root.querySelectorAll<HTMLElement>(".comparison"));
    const cleanups: Array<() => void> = [];

    comparisons.forEach((comparison) => {
      const details = comparison.querySelector<HTMLDetailsElement>(".comparison__details");
      const summary = comparison.querySelector<HTMLElement>(".comparison__details > summary");
      const rows = Array.from(comparison.querySelectorAll<HTMLElement>(".comparison__row"));
      if (!details || rows.length === 0) {
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
          const columns = Array.from(row.children) as HTMLElement[];
          columns.forEach((cell, index) => {
            const isActive = index + 1 === current;
            cell.classList.toggle("comparison__image-container--hidden", !isActive);
            const image = cell.querySelector<HTMLElement>(".comparison__image");
            if (image) {
              image.classList.toggle("comparison__image--hidden", !isActive);
            }
          });
        });
      };

      const handleKeyDown = (event: KeyboardEvent) => {
        if (!details.open && !comparison.classList.contains("comparison--open")) {
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
          comparison.classList.remove("comparison--open");
          return;
        }
        const digit = Number.parseInt(event.key, 10);
        if (!Number.isNaN(digit) && digit >= 1 && digit <= maxColumns) {
          event.preventDefault();
          applyColumn(digit);
        }
      };

      const handleMouseMove = (event: MouseEvent) => {
        if (
          (!details.open && !comparison.classList.contains("comparison--open")) ||
          maxColumns <= 1
        ) {
          return;
        }
        const width = globalThis.innerWidth || 1;
        const ratio = event.clientX / width;
        applyColumn(Math.min(maxColumns, Math.max(1, Math.ceil(ratio * maxColumns))));
      };

      const handleToggle = () => {
        if (details.open || comparison.classList.contains("comparison--open")) {
          applyColumn(current);
          globalThis.addEventListener("keydown", handleKeyDown);
          globalThis.addEventListener("mousemove", handleMouseMove);
        } else {
          globalThis.removeEventListener("keydown", handleKeyDown);
          globalThis.removeEventListener("mousemove", handleMouseMove);
        }
      };

      const handleSummaryClick = (event: MouseEvent) => {
        event.preventDefault();
        details.open = !details.open;
        comparison.classList.toggle("comparison--open", details.open);
        handleToggle();
      };

      details.addEventListener("toggle", handleToggle);
      if (summary) {
        summary.addEventListener("click", handleSummaryClick);
      }
      applyColumn(1);
      if (details.open) {
        handleToggle();
      }

      cleanups.push(() => {
        details.removeEventListener("toggle", handleToggle);
        if (summary) {
          summary.removeEventListener("click", handleSummaryClick);
        }
        globalThis.removeEventListener("keydown", handleKeyDown);
        globalThis.removeEventListener("mousemove", handleMouseMove);
      });
    });

    return () => {
      cleanups.forEach((cleanup) => cleanup());
    };
  }, [html]);

  return (
    <div
      ref={rootRef}
      className="tracker-description rendered"
      onClick={handleExternalLinkClick}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
}
