// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createMemoryStorage } from "./testStorage";
import {
  appearanceStorageKey,
  applyAppearance,
  bootstrapAppearance,
  defaultAppearance,
  effectiveMode,
  legacyModeStorageKey,
  loadAppearance,
  parseAppearance,
  writeAppearance,
  type AppearancePreference,
} from "./appearance";

function memoryStorage(initial: Record<string, string> = {}) {
  const values = new Map(Object.entries(initial));
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
  };
}

beforeEach(() => vi.stubGlobal("localStorage", createMemoryStorage()));

afterEach(() => {
  vi.unstubAllGlobals();
  document.documentElement.removeAttribute("data-theme");
  document.documentElement.removeAttribute("data-accent");
  document.documentElement.classList.remove("light", "dark");
});

describe("appearance preference", () => {
  it("migrates a valid legacy mode only when the versioned key is absent", () => {
    const storage = memoryStorage({ [legacyModeStorageKey]: "dark" });
    const preference = loadAppearance(storage);
    expect(preference).toEqual({ version: 1, theme: "minimal", mode: "dark", accents: {} });
    expect(parseAppearance(storage.getItem(appearanceStorageKey))).toEqual(preference);
    expect(storage.getItem(legacyModeStorageKey)).toBe("dark");

    storage.setItem(appearanceStorageKey, "invalid json");
    expect(loadAppearance(storage)).toEqual(defaultAppearance());
  });

  it.each([
    "not json",
    JSON.stringify({ version: 2, theme: "minimal", mode: "auto" }),
    JSON.stringify({ version: 1, theme: "unknown", mode: "auto" }),
    JSON.stringify({ version: 1, theme: "minimal", mode: "sepia" }),
    JSON.stringify({
      version: 1,
      theme: "kanagawa-wave",
      mode: "dark",
      accents: { "kanagawa-wave": "red" },
    }),
    JSON.stringify({ version: 1, theme: "minimal", mode: "auto", accents: { minimal: "pink" } }),
  ])("rejects malformed or unsupported persisted values", (raw) => {
    expect(parseAppearance(raw)).toBeNull();
    expect(loadAppearance(memoryStorage({ [appearanceStorageKey]: raw }))).toEqual(
      defaultAppearance(),
    );
  });

  it("keeps the current choice usable when storage cannot be read or written", () => {
    const unavailable = {
      getItem: () => {
        throw new Error("blocked");
      },
      setItem: () => {
        throw new Error("blocked");
      },
    };
    expect(loadAppearance(unavailable)).toEqual(defaultAppearance());
    const chosen: AppearancePreference = {
      version: 1,
      theme: "nightwalker",
      mode: "dark",
      accents: {},
    };
    expect(writeAppearance(unavailable, chosen)).toBe(false);
    const root = document.createElement("html");
    applyAppearance(root, chosen, false);
    expect(root.dataset.theme).toBe("nightwalker");
    expect(root.classList.contains("dark")).toBe(true);
  });

  it("keeps requested mode while Napster renders light and clears old accent state", () => {
    const root = document.createElement("html");
    const kanagawa: AppearancePreference = {
      version: 1,
      theme: "kanagawa-dragon",
      mode: "dark",
      accents: { "kanagawa-dragon": "pink" },
    };
    applyAppearance(root, kanagawa, false);
    expect(root.dataset.accent).toBe("pink");
    expect(root.classList.contains("dark")).toBe(true);

    const napster = { ...kanagawa, theme: "napster" as const };
    expect(effectiveMode(napster, true)).toBe("light");
    expect(napster.mode).toBe("dark");
    applyAppearance(root, napster, true);
    expect(root.dataset.accent).toBeUndefined();
    expect(root.classList.contains("light")).toBe(true);
    expect(root.classList.contains("dark")).toBe(false);
    applyAppearance(root, kanagawa, false);
    expect(root.dataset.accent).toBe("pink");
  });

  it("applies stored appearance before React mounts", () => {
    window.localStorage.setItem(
      appearanceStorageKey,
      JSON.stringify({ version: 1, theme: "swizzin", mode: "auto", accents: {} }),
    );
    vi.stubGlobal("matchMedia", () => ({ matches: true }));
    bootstrapAppearance();
    expect(document.documentElement.dataset.theme).toBe("swizzin");
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(document.documentElement.style.colorScheme).toBe("dark");
  });
});
