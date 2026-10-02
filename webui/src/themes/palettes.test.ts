// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { afterEach, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import path from "node:path";
import { themeCatalog } from "./appearance";

const palettes = readFileSync(path.resolve(process.cwd(), "src/themes/palettes.css"), "utf8");

afterEach(() => {
  delete document.documentElement.dataset.theme;
  document.documentElement.classList.remove("light", "dark");
});

it("selects each scoped palette regardless of the order of the other palettes", () => {
  expect(palettes.slice(0, 40)).toContain("Copyright");
  const markers = [
    ...palettes.matchAll(
      /\/\* (?:minimal|autobrr|kanagawa-dragon|kanagawa-wave|the-kyle|napster|nightwalker|swizzin): [^*]+\*\//g,
    ),
  ];
  expect(markers).toHaveLength(themeCatalog.length);
  const variationStart = palettes.indexOf(":is(:root, .theme-preview)");
  expect(variationStart).toBeGreaterThan(0);
  const sections = markers.map((marker, index) =>
    palettes.slice(marker.index, markers[index + 1]?.index ?? variationStart),
  );
  const prelude = palettes.slice(0, markers[0].index);
  const variations = palettes.slice(variationStart);
  const readings: string[][] = [];

  for (const orderedSections of [sections, [...sections].reverse()]) {
    const style = document.createElement("style");
    style.textContent = prelude + orderedSections.join("") + variations;
    document.head.append(style);
    const selected = themeCatalog.map((theme) => {
      document.documentElement.dataset.theme = theme.id;
      document.documentElement.classList.remove("dark");
      document.documentElement.classList.add("light");
      return getComputedStyle(document.documentElement).getPropertyValue("--background").trim();
    });
    readings.push(selected);
    style.remove();
  }

  expect(readings[0].every(Boolean)).toBe(true);
  expect(readings[1]).toEqual(readings[0]);
});
