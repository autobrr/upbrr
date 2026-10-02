// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { StrictMode } from "react";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { appearanceStorageKey } from "./appearance";
import { AppearanceSettings } from "./AppearanceSettings";
import { AppearanceProvider, useAppearance } from "./provider";
import { createMemoryStorage } from "./testStorage";

function mockSystemMode(initial: boolean) {
  let onChange: ((event: MediaQueryListEvent) => void) | null = null;
  const media = {
    matches: initial,
    addEventListener: vi.fn((_type: string, listener: (event: MediaQueryListEvent) => void) => {
      onChange = listener;
    }),
    removeEventListener: vi.fn((_type: string, listener: (event: MediaQueryListEvent) => void) => {
      if (onChange === listener) onChange = null;
    }),
  };
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => media),
  );
  return {
    media,
    change: (matches: boolean) => {
      if (onChange) onChange({ matches } as MediaQueryListEvent);
    },
  };
}

function Controls() {
  const { preference, selectMode } = useAppearance();
  return (
    <div>
      <span data-testid="requested-mode">{preference.mode}</span>
      <button type="button" onClick={() => selectMode("light")}>
        Explicit light
      </button>
      <button type="button" onClick={() => selectMode("auto")}>
        Automatic
      </button>
    </div>
  );
}

beforeEach(() => vi.stubGlobal("localStorage", createMemoryStorage()));

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  document.documentElement.removeAttribute("data-theme");
  document.documentElement.removeAttribute("data-accent");
  document.documentElement.classList.remove("light", "dark");
});

describe("AppearanceProvider", () => {
  it("follows OS changes only in automatic mode and cleans StrictMode listeners", () => {
    const system = mockSystemMode(false);
    const add = vi.spyOn(window, "addEventListener");
    const remove = vi.spyOn(window, "removeEventListener");
    const view = render(
      <StrictMode>
        <AppearanceProvider>
          <Controls />
        </AppearanceProvider>
      </StrictMode>,
    );
    expect(document.documentElement.classList.contains("light")).toBe(true);

    act(() => system.change(true));
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Explicit light" }));
    act(() => system.change(false));
    act(() => system.change(true));
    expect(document.documentElement.classList.contains("light")).toBe(true);
    expect(screen.getByTestId("requested-mode")).toHaveTextContent("light");

    fireEvent.click(screen.getByRole("button", { name: "Automatic" }));
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    view.unmount();
    expect(system.media.addEventListener).toHaveBeenCalledTimes(
      system.media.removeEventListener.mock.calls.length,
    );
    const storageAdds = add.mock.calls.filter(([type]) => type === "storage").length;
    const storageRemoves = remove.mock.calls.filter(([type]) => type === "storage").length;
    expect(storageAdds).toBe(storageRemoves);
  });

  it("synchronizes valid cross-tab preferences and ignores unrelated or malformed events", () => {
    mockSystemMode(false);
    render(
      <AppearanceProvider>
        <Controls />
      </AppearanceProvider>,
    );
    const changed = JSON.stringify({ version: 1, theme: "nightwalker", mode: "dark", accents: {} });
    act(() =>
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: appearanceStorageKey,
          newValue: changed,
          storageArea: window.localStorage,
        }),
      ),
    );
    expect(document.documentElement.dataset.theme).toBe("nightwalker");
    expect(document.documentElement.classList.contains("dark")).toBe(true);

    act(() =>
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: "source-path-history-v1",
          newValue: "[]",
          storageArea: window.localStorage,
        }),
      ),
    );
    act(() =>
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: appearanceStorageKey,
          newValue: "invalid",
          storageArea: window.localStorage,
        }),
      ),
    );
    expect(document.documentElement.dataset.theme).toBe("nightwalker");
  });

  it("shows all eight themes, with accents only for Kanagawa and light-only guidance for Napster", () => {
    mockSystemMode(true);
    render(
      <AppearanceProvider>
        <AppearanceSettings />
      </AppearanceProvider>,
    );
    expect(
      screen.getAllByRole("radio", {
        name: /Minimal|autobrr|Kanagawa|The Kyle|Napster|Nightwalker|Swizzin/,
      }),
    ).toHaveLength(8);
    fireEvent.click(screen.getByRole("radio", { name: "Kanagawa Wave" }));
    expect(screen.getByRole("group", { name: "Accent" })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "pink" }));
    expect(document.documentElement.dataset.accent).toBe("pink");
    fireEvent.click(screen.getByRole("radio", { name: "Napster" }));
    expect(screen.queryByRole("group", { name: "Accent" })).toBeNull();
    expect(screen.getByText(/light-only theme/)).toBeVisible();
    expect(document.documentElement.classList.contains("light")).toBe(true);
    expect(
      screen
        .getByRole("radio", { name: "Kanagawa Wave" })
        .closest("label")
        ?.querySelector(".theme-preview"),
    ).toHaveClass("dark");
    fireEvent.click(screen.getByRole("radio", { name: "Kanagawa Wave" }));
    expect(document.documentElement.dataset.accent).toBe("pink");
  });
});
