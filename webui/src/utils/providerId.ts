// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

/** Formats a canonical numeric IMDb title ID for external text contracts. */
export const formatIMDbID = (id: number | null | undefined): string => {
  if (!Number.isSafeInteger(id) || (id ?? 0) <= 0) return "";
  return `tt${String(id).padStart(7, "0")}`;
};
