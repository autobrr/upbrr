// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

export const defaultInputHistoryLimit = 20;
export const sourcePathHistoryStorageKey = "source-path-history-v1";

export type SourcePathMode = "file" | "folder";

export type SourcePathHistoryEntry = {
  path: string;
  mode: SourcePathMode;
};

const defaultSourcePathMode: SourcePathMode = "folder";

export const resolveInputHistoryLimit = (value: unknown): number => {
  if (typeof value === "string" && value.trim()) {
    return resolveInputHistoryLimit(Number(value));
  }
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return defaultInputHistoryLimit;
  }
  const normalized = Math.trunc(value);
  if (normalized < 0) {
    return defaultInputHistoryLimit;
  }
  return normalized;
};

const normalizeSourcePathMode = (value: unknown): SourcePathMode =>
  value === "file" || value === "folder" ? value : defaultSourcePathMode;

export const inferSourcePathMode = (path: string): SourcePathMode => {
  const normalized = path.trim().replaceAll("\\", "/").toLowerCase();
  if (/(^|\/)(bdmv|video_ts)(\/|$)/.test(normalized)) {
    return "folder";
  }
  return /\.(avi|iso|m2ts|m4v|mkv|mov|mp4|mpeg|mpg|ts|webm|wmv)$/.test(normalized)
    ? "file"
    : "folder";
};

/**
 * Compares host filesystem paths after separator normalization.
 *
 * Case folding is caller-controlled so browser UI comparisons can match the
 * server runtime instead of the user's browser platform.
 */
export const sameSourcePath = (
  left: string,
  right: string,
  caseInsensitivePaths: boolean,
): boolean => {
  const normalize = (value: string) => value.trim().replaceAll("\\", "/");
  const normalizedLeft = normalize(left);
  const normalizedRight = normalize(right);
  return caseInsensitivePaths
    ? normalizedLeft.toLowerCase() === normalizedRight.toLowerCase()
    : normalizedLeft === normalizedRight;
};

/**
 * Normalizes persisted source-path history, dropping malformed, blank, duplicate,
 * and over-limit entries while preserving the first spelling and mode kept.
 *
 * Pass the server runtime's path case-sensitivity so embedded web history
 * dedupes host paths instead of browser-platform paths.
 */
export const normalizeSourcePathHistory = (
  value: unknown,
  limit: unknown,
  caseInsensitivePaths = false,
): SourcePathHistoryEntry[] => {
  const effectiveLimit = resolveInputHistoryLimit(limit);
  if (effectiveLimit <= 0 || !Array.isArray(value)) {
    return [];
  }

  const entries: SourcePathHistoryEntry[] = [];
  for (const item of value) {
    const path =
      typeof item === "string"
        ? item.trim()
        : item && typeof item === "object" && "path" in item && typeof item.path === "string"
          ? item.path.trim()
          : "";
    if (!path || entries.some((entry) => sameSourcePath(entry.path, path, caseInsensitivePaths))) {
      continue;
    }
    const mode =
      typeof item === "string" ? defaultSourcePathMode : normalizeSourcePathMode(item.mode);
    entries.push({ path, mode });
    if (entries.length >= effectiveLimit) {
      break;
    }
  }
  return entries;
};

/**
 * Prepends a source path to history and removes any older runtime-equivalent
 * entry before applying the configured history limit.
 */
export const addSourcePathHistoryEntry = (
  current: unknown,
  path: string,
  mode: unknown,
  limit: unknown,
  caseInsensitivePaths = false,
): SourcePathHistoryEntry[] => {
  const effectiveLimit = resolveInputHistoryLimit(limit);
  if (effectiveLimit <= 0) {
    return [];
  }

  const trimmed = path.trim();
  const base = Array.isArray(current) ? current : [];
  if (!trimmed) {
    return normalizeSourcePathHistory(base, effectiveLimit, caseInsensitivePaths);
  }
  return normalizeSourcePathHistory(
    [{ path: trimmed, mode: normalizeSourcePathMode(mode) }, ...base],
    effectiveLimit,
    caseInsensitivePaths,
  );
};

export const filterBrowseEntries = <T extends { name: string; path: string }>(
  entries: T[],
  search: string,
): T[] => {
  const query = search.trim().toLowerCase();
  if (!query) {
    return entries;
  }
  return entries.filter((entry) => {
    const name = entry.name.toLowerCase();
    const path = entry.path.toLowerCase();
    return name.includes(query) || path.includes(query);
  });
};
