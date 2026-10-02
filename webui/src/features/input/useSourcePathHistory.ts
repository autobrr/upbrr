// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useCallback, useEffect, useState } from "react";
import { isHostPathCaseInsensitive } from "../../api/client";
import {
  addSourcePathHistoryEntry,
  defaultInputHistoryLimit,
  inferSourcePathMode,
  normalizeSourcePathHistory,
  sourcePathHistoryStorageKey,
  type SourcePathHistoryEntry,
  type SourcePathMode,
} from "../../utils/inputHistory";

function browserStorage(): Storage | null {
  try {
    return document.defaultView?.localStorage || null;
  } catch {
    return null;
  }
}

/** Owns bounded, case-aware source history for the authenticated input flow. */
export function useSourcePathHistory(limit: number, releaseSourcePath: string) {
  const [entries, setEntries] = useState<SourcePathHistoryEntry[]>(() => {
    try {
      return normalizeSourcePathHistory(
        JSON.parse(browserStorage()?.getItem(sourcePathHistoryStorageKey) || "[]"),
        defaultInputHistoryLimit,
        isHostPathCaseInsensitive(),
      );
    } catch {
      return [];
    }
  });

  const remember = useCallback(
    (path: string, mode: SourcePathMode) => {
      setEntries((current) =>
        addSourcePathHistoryEntry(current, path, mode, limit, isHostPathCaseInsensitive()),
      );
    },
    [limit],
  );

  useEffect(() => {
    if (releaseSourcePath) remember(releaseSourcePath, inferSourcePathMode(releaseSourcePath));
  }, [releaseSourcePath, remember]);

  useEffect(() => {
    setEntries((current) =>
      normalizeSourcePathHistory(current, limit, isHostPathCaseInsensitive()),
    );
  }, [limit]);

  useEffect(() => {
    try {
      if (entries.length)
        browserStorage()?.setItem(sourcePathHistoryStorageKey, JSON.stringify(entries));
      else browserStorage()?.removeItem(sourcePathHistoryStorageKey);
    } catch {
      // Browser policy may disable local storage; the in-memory history remains usable.
    }
  }, [entries]);

  return { entries, remember };
}
