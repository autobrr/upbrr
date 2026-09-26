// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { accentChoices, supportsAccents, themeCatalog, type ThemeMode } from "./appearance";
import { useAppearance } from "./provider";

const modeLabels: Readonly<Record<ThemeMode, string>> = {
  auto: "Use system setting",
  light: "Light",
  dark: "Dark",
};

/** Selects only bundled free palettes, requested mode, and supported accents. */
export function AppearanceSettings() {
  const { preference, previewMode, selectTheme, selectMode, selectAccent } = useAppearance();
  const activeAccent = supportsAccents(preference.theme)
    ? (preference.accents[preference.theme] ?? "blueish")
    : null;
  return (
    <section className="space-y-5" aria-labelledby="appearance-title">
      <div>
        <h2 id="appearance-title" className="text-xl font-semibold text-foreground">
          Appearance
        </h2>
        <p className="text-sm text-muted-foreground">
          Choose a bundled theme. Changes apply immediately and are saved on this device.
        </p>
      </div>

      <fieldset className="grid grid-cols-[repeat(auto-fit,minmax(165px,1fr))] gap-3">
        <legend className="sr-only">Theme</legend>
        {themeCatalog.map((theme) => (
          <label key={theme.id} className="block cursor-pointer">
            <input
              className="peer sr-only"
              type="radio"
              name="appearance-theme"
              value={theme.id}
              checked={preference.theme === theme.id}
              onChange={() => selectTheme(theme.id)}
            />
            <span className="block rounded-lg border border-border bg-card p-2 text-left text-card-foreground transition-colors hover:border-primary peer-checked:border-primary peer-focus-visible:ring-2 peer-focus-visible:ring-ring">
              <span
                className={`theme-preview ${theme.id !== "napster" && previewMode === "dark" ? "dark" : ""}`}
                data-theme={theme.id}
                data-accent={
                  supportsAccents(theme.id)
                    ? (preference.accents[theme.id] ?? "blueish")
                    : undefined
                }
                aria-hidden="true"
              >
                <span className="theme-preview__sidebar" />
                <span className="theme-preview__content">
                  <span className="theme-preview__heading" />
                  <span className="theme-preview__line" />
                  <span className="theme-preview__button" />
                </span>
              </span>
              <span className="mt-2 flex items-center justify-between gap-2 text-sm font-medium">
                {theme.name}
                {preference.theme === theme.id ? <span aria-hidden="true">✓</span> : null}
              </span>
            </span>
          </label>
        ))}
      </fieldset>

      <fieldset className="space-y-2">
        <legend className="text-sm font-semibold text-foreground">Mode</legend>
        <div className="flex flex-wrap gap-3">
          {(["auto", "light", "dark"] as const).map((mode) => (
            <label
              key={mode}
              className="inline-flex min-h-9 cursor-pointer items-center gap-2 text-sm text-foreground"
            >
              <input
                type="radio"
                name="appearance-mode"
                value={mode}
                checked={preference.mode === mode}
                onChange={() => selectMode(mode)}
                className="accent-primary"
              />
              {modeLabels[mode]}
            </label>
          ))}
        </div>
      </fieldset>

      {supportsAccents(preference.theme) ? (
        <fieldset className="space-y-2">
          <legend className="text-sm font-semibold text-foreground">Accent</legend>
          <div className="flex flex-wrap gap-2">
            {accentChoices.map((accent) => (
              <button
                key={accent}
                type="button"
                className="inline-flex min-h-9 items-center gap-2 rounded-md border border-border bg-card px-3 py-1.5 text-sm capitalize text-card-foreground hover:border-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring aria-pressed:border-primary"
                aria-pressed={activeAccent === accent}
                onClick={() => selectAccent(accent)}
              >
                {accent}
                {activeAccent === accent ? <span aria-hidden="true">✓</span> : null}
              </button>
            ))}
          </div>
        </fieldset>
      ) : null}

      {preference.theme === "napster" ? (
        <p className="text-sm text-muted-foreground">
          Napster is a light-only theme. Your selected mode is kept for the next theme.
        </p>
      ) : null}
    </section>
  );
}
