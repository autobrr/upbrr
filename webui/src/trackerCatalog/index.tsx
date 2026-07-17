// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ReactNode } from "react";
import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { trackerCatalogClient } from "../api/app";
import type { TrackerCatalog } from "../types";

type TrackerCatalogState = Readonly<{
  catalog: TrackerCatalog | null;
  loading: boolean;
  loaded: boolean;
  error: string;
  removeUnsupported(name: string): void;
}>;

const TrackerCatalogContext = createContext<TrackerCatalogState | null>(null);

const useTrackerCatalogOwner = (enabled: boolean): TrackerCatalogState => {
  const [catalog, setCatalog] = useState<TrackerCatalog | null>(null);
  const [loading, setLoading] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!enabled || loaded) return;
    let active = true;
    setLoading(true);
    void trackerCatalogClient
      .list()
      .then((result) => {
        if (!active) return;
        if (!result || !Array.isArray(result.entries) || !Array.isArray(result.unsupported)) {
          throw new Error("Tracker catalog response is invalid.");
        }
        setCatalog(result);
        setError("");
      })
      .catch((reason: unknown) => {
        if (active) setError(String(reason));
      })
      .finally(() => {
        if (!active) return;
        setLoading(false);
        setLoaded(true);
      });
    return () => {
      active = false;
    };
  }, [enabled, loaded]);

  const removeUnsupported = useCallback((name: string) => {
    setCatalog((current) =>
      current
        ? {
            ...current,
            unsupported: current.unsupported.filter((entry) => entry !== name),
          }
        : current,
    );
  }, []);

  return useMemo(
    () => ({ catalog, loading, loaded, error, removeUnsupported }),
    [catalog, error, loaded, loading, removeUnsupported],
  );
};

/** Owns the single production tracker-catalog request shared across app workflows. */
export function TrackerCatalogProvider({ children }: Readonly<{ children: ReactNode }>) {
  const state = useTrackerCatalogOwner(true);
  return <TrackerCatalogContext.Provider value={state}>{children}</TrackerCatalogContext.Provider>;
}

/** Returns the shared catalog, with an isolated owner for standalone hook tests. */
export function useTrackerCatalog(): TrackerCatalogState {
  const shared = useContext(TrackerCatalogContext);
  const standalone = useTrackerCatalogOwner(shared === null);
  return shared ?? standalone;
}

/** Returns the app-owned snapshot without starting a request outside the app provider. */
export function useTrackerCatalogSnapshot(): TrackerCatalogState | null {
  return useContext(TrackerCatalogContext);
}
