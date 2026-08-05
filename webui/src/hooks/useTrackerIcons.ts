// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useRef, useState } from "react";
import { trackerCatalogClient } from "../api/app";
import type { TrackerUploadItem } from "../types";
import { getTrackerDomain } from "../utils/favicon";

export type TrackerIconCache = Record<string, string>;

const trackerKey = (tracker: string) => tracker.toLowerCase().trim();

export function useTrackerIcons(
  trackerUploadItems: TrackerUploadItem[],
  useFavicons: boolean,
): TrackerIconCache {
  const [iconSrcByTracker, setIconSrcByTracker] = useState<TrackerIconCache>({});
  const fetchKeyByTracker = useRef<Record<string, string>>({});

  useEffect(() => {
    if (!useFavicons) {
      setIconSrcByTracker({});
      fetchKeyByTracker.current = {};
      return;
    }

    const configuredKeys = new Set(
      trackerUploadItems.map((item) => trackerKey(item.name)).filter(Boolean),
    );

    setIconSrcByTracker((prev) => {
      const next: TrackerIconCache = {};
      Object.entries(prev).forEach(([key, value]) => {
        if (configuredKeys.has(key) && value) {
          next[key] = value;
        }
      });
      if (
        Object.keys(prev).length === Object.keys(next).length &&
        Object.entries(next).every(([key, value]) => prev[key] === value)
      ) {
        return prev;
      }
      return next;
    });

    Object.keys(fetchKeyByTracker.current).forEach((key) => {
      if (!configuredKeys.has(key)) {
        delete fetchKeyByTracker.current[key];
      }
    });

    const getTrackerIcon = trackerCatalogClient.getIcon;

    let active = true;
    trackerUploadItems.forEach((item) => {
      const key = trackerKey(item.name);
      if (!key) return;

      const customURL =
        typeof item.config?.FaviconURL === "string" ? item.config.FaviconURL.trim() : "";
      const domain = getTrackerDomain(item.name, customURL);
      if (!domain) return;

      const fetchKey = `${domain}\n${customURL}`;
      if (fetchKeyByTracker.current[key] === fetchKey) return;
      fetchKeyByTracker.current[key] = fetchKey;

      getTrackerIcon(domain, customURL)
        .then((dataUrl) => {
          if (!active) return;
          setIconSrcByTracker((prev) => {
            if (!dataUrl) {
              if (!prev[key]) return prev;
              const next = { ...prev };
              delete next[key];
              return next;
            }
            if (prev[key] === dataUrl) return prev;
            return { ...prev, [key]: dataUrl };
          });
        })
        .catch(() => {
          if (!active) return;
          setIconSrcByTracker((prev) => {
            if (!prev[key]) return prev;
            const next = { ...prev };
            delete next[key];
            return next;
          });
        });
    });

    return () => {
      active = false;
    };
  }, [trackerUploadItems, useFavicons]);

  return iconSrcByTracker;
}

export function trackerIconFor(cache: TrackerIconCache, tracker: string): string {
  return cache[trackerKey(tracker)] || "";
}
