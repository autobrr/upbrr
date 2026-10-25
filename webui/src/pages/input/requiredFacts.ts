// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { PreparedRelease } from "../../types";

/** Missing upload facts belong to the saved generation, never unsaved correction drafts. */
export function missingRequiredFacts(release: PreparedRelease | null): string[] {
  if (!release) return [];
  const values: [string, string | undefined][] = [
    ["category", release.Identity?.Category],
    ["type", release.Media?.Type],
    ["source", release.Media?.Source],
    ["resolution", release.Naming?.Resolution],
  ];
  return values
    .filter(
      ([field, value]) =>
        !value?.trim() || (field === "category" && value.trim().toLowerCase() === "unknown"),
    )
    .map(([field]) => `release_name.${field}`);
}
