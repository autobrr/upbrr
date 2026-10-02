// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { createContext, useContext, useEffect, useState } from "react";
import type { ReactNode } from "react";
import {
  accentChoices,
  appearanceStorageKey,
  applyAppearance,
  effectiveMode,
  getAppearanceStorage,
  loadAppearance,
  parseAppearance,
  requestedMode,
  supportsAccents,
  writeAppearance,
  type AccentId,
  type AppearancePreference,
  type ThemeId,
  type ThemeMode,
} from "./appearance";

type AppearanceActions = Readonly<{
  preference: AppearancePreference;
  effectiveMode: "light" | "dark";
  previewMode: "light" | "dark";
  selectTheme: (theme: ThemeId) => void;
  selectMode: (mode: ThemeMode) => void;
  selectAccent: (accent: AccentId) => void;
}>;

const AppearanceContext = createContext<AppearanceActions | null>(null);

function systemPrefersDark() {
  try {
    return window.matchMedia("(prefers-color-scheme: dark)").matches;
  } catch {
    return false;
  }
}

/** Owns user appearance and browser preference synchronization above auth and routes. */
export function AppearanceProvider({ children }: Readonly<{ children: ReactNode }>) {
  const [preference, setPreference] = useState(() => loadAppearance(getAppearanceStorage()));
  const [prefersDark, setPrefersDark] = useState(systemPrefersDark);

  useEffect(() => {
    applyAppearance(document.documentElement, preference, prefersDark);
  }, [preference, prefersDark]);

  useEffect(() => {
    let media: MediaQueryList;
    try {
      media = window.matchMedia("(prefers-color-scheme: dark)");
    } catch {
      return;
    }
    const onChange = (event: MediaQueryListEvent) => setPrefersDark(event.matches);
    media.addEventListener("change", onChange);
    return () => media.removeEventListener("change", onChange);
  }, []);

  useEffect(() => {
    const onStorage = (event: StorageEvent) => {
      if (event.key !== appearanceStorageKey || event.storageArea !== getAppearanceStorage())
        return;
      const next = parseAppearance(event.newValue);
      if (next) setPreference(next);
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  const selectTheme = (theme: ThemeId) => {
    const next = { ...preference, theme };
    setPreference(next);
    writeAppearance(getAppearanceStorage(), next);
  };

  const selectMode = (mode: ThemeMode) => {
    const next = { ...preference, mode };
    setPreference(next);
    writeAppearance(getAppearanceStorage(), next);
  };

  const selectAccent = (accent: AccentId) => {
    if (!supportsAccents(preference.theme) || !accentChoices.includes(accent)) return;
    const next = {
      ...preference,
      accents: { ...preference.accents, [preference.theme]: accent },
    };
    setPreference(next);
    writeAppearance(getAppearanceStorage(), next);
  };

  return (
    <AppearanceContext.Provider
      value={{
        preference,
        effectiveMode: effectiveMode(preference, prefersDark),
        previewMode: requestedMode(preference, prefersDark),
        selectTheme,
        selectMode,
        selectAccent,
      }}
    >
      {children}
    </AppearanceContext.Provider>
  );
}

export function useAppearance() {
  const context = useContext(AppearanceContext);
  if (!context) throw new Error("AppearanceProvider is missing");
  return context;
}
