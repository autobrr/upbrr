// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

export const appearanceStorageKey = "upbrr:appearance:v1";
export const legacyModeStorageKey = "theme";
export const accentChoices = ["blueish", "pink", "green", "purple", "grayish", "orange"] as const;

export const themeCatalog = [
  { id: "minimal", name: "Minimal" },
  { id: "autobrr", name: "autobrr" },
  { id: "kanagawa-dragon", name: "Kanagawa Dragon", accents: accentChoices },
  { id: "kanagawa-wave", name: "Kanagawa Wave", accents: accentChoices },
  { id: "the-kyle", name: "The Kyle" },
  { id: "napster", name: "Napster", lightOnly: true },
  { id: "nightwalker", name: "Nightwalker" },
  { id: "swizzin", name: "Swizzin" },
] as const;

export type ThemeId = (typeof themeCatalog)[number]["id"];
export type ThemeMode = "light" | "dark" | "auto";
export type AccentId = (typeof accentChoices)[number];
export type AccentThemeId = "kanagawa-dragon" | "kanagawa-wave";
export type AppearancePreference = Readonly<{
  version: 1;
  theme: ThemeId;
  mode: ThemeMode;
  accents: Partial<Record<AccentThemeId, AccentId>>;
}>;

type AppearanceStorage = Pick<Storage, "getItem" | "setItem">;

export const defaultAppearance = (): AppearancePreference => ({
  version: 1,
  theme: "minimal",
  mode: "auto",
  accents: {},
});

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null && !Array.isArray(value);

const isThemeId = (value: unknown): value is ThemeId =>
  typeof value === "string" && themeCatalog.some((theme) => theme.id === value);

const isMode = (value: unknown): value is ThemeMode =>
  value === "light" || value === "dark" || value === "auto";

export const supportsAccents = (theme: ThemeId): theme is AccentThemeId =>
  theme === "kanagawa-dragon" || theme === "kanagawa-wave";

const isAccent = (value: unknown): value is AccentId =>
  typeof value === "string" && accentChoices.some((choice) => choice === value);

/** Rejects malformed or unknown persisted values at the storage boundary. */
export function parseAppearance(raw: string | null): AppearancePreference | null {
  if (raw === null) return null;
  try {
    const value: unknown = JSON.parse(raw);
    if (!isRecord(value) || value.version !== 1 || !isThemeId(value.theme) || !isMode(value.mode)) {
      return null;
    }
    if (value.accents !== undefined && !isRecord(value.accents)) return null;
    const accents: AppearancePreference["accents"] = {};
    if (isRecord(value.accents)) {
      for (const [theme, accent] of Object.entries(value.accents)) {
        if (!isThemeId(theme) || !supportsAccents(theme) || !isAccent(accent)) return null;
        accents[theme] = accent;
      }
    }
    return { version: 1, theme: value.theme, mode: value.mode, accents };
  } catch {
    return null;
  }
}

/** Keeps requested mode in the legacy key for older frontend builds. */
export function writeAppearance(
  storage: AppearanceStorage | null,
  preference: AppearancePreference,
) {
  let saved = false;
  try {
    storage?.setItem(appearanceStorageKey, JSON.stringify(preference));
    saved = storage !== null;
  } catch {
    // Browser storage may be unavailable; the current in-memory choice still applies.
  }
  try {
    storage?.setItem(legacyModeStorageKey, preference.mode);
  } catch {
    // Keep the selected appearance active even when downgrade storage is unavailable.
  }
  return saved;
}

/** Reads the new key once; a valid legacy mode is migrated only when it is absent. */
export function loadAppearance(storage: AppearanceStorage | null): AppearancePreference {
  try {
    const raw = storage?.getItem(appearanceStorageKey);
    if (raw != null) return parseAppearance(raw) ?? defaultAppearance();
    const legacyMode = storage?.getItem(legacyModeStorageKey);
    if (isMode(legacyMode)) {
      const migrated = { ...defaultAppearance(), mode: legacyMode };
      writeAppearance(storage, migrated);
      return migrated;
    }
  } catch {
    // Ignore unavailable storage and use the bundled default.
  }
  return defaultAppearance();
}

export function getAppearanceStorage(): AppearanceStorage | null {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

export function requestedMode(
  preference: AppearancePreference,
  prefersDark: boolean,
): "light" | "dark" {
  return preference.mode === "auto" ? (prefersDark ? "dark" : "light") : preference.mode;
}

export function effectiveMode(
  preference: AppearancePreference,
  prefersDark: boolean,
): "light" | "dark" {
  if (preference.theme === "napster") return "light";
  return requestedMode(preference, prefersDark);
}

export function applyAppearance(
  root: HTMLElement,
  preference: AppearancePreference,
  prefersDark: boolean,
) {
  const mode = effectiveMode(preference, prefersDark);
  root.dataset.theme = preference.theme;
  if (supportsAccents(preference.theme)) {
    root.dataset.accent = preference.accents[preference.theme] ?? "blueish";
  } else {
    delete root.dataset.accent;
  }
  root.classList.toggle("dark", mode === "dark");
  root.classList.toggle("light", mode === "light");
  root.style.colorScheme = mode;
}

/** Built into a blocking script in index.html from this same source module. */
export function bootstrapAppearance() {
  const preference = loadAppearance(getAppearanceStorage());
  let prefersDark = false;
  try {
    prefersDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
  } catch {
    // A browser without matchMedia starts in light mode.
  }
  applyAppearance(document.documentElement, preference, prefersDark);
}
