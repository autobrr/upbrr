// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { TrackerUploadItem } from "../types";
import { useTrackerIcons } from "./useTrackerIcons";

afterEach(() => {
  vi.unstubAllGlobals();
  cleanup();
});

describe("useTrackerIcons", () => {
  it("does not fetch when favicons are disabled", () => {
    const getTrackerIcon = vi.fn().mockResolvedValue("data:image/png;base64,a");
    vi.stubGlobal("go", { guiapp: { App: { GetTrackerIcon: getTrackerIcon } } });
    const trackers: TrackerUploadItem[] = [{ name: "AITHER", config: {} }];

    const { result } = renderHook(() => useTrackerIcons(trackers, false));

    expect(result.current).toEqual({});
    expect(getTrackerIcon).not.toHaveBeenCalled();
  });

  it("fetches configured tracker icons through the app-level cache", async () => {
    const getTrackerIcon = vi.fn().mockResolvedValue("data:image/png;base64,a");
    vi.stubGlobal("go", { guiapp: { App: { GetTrackerIcon: getTrackerIcon } } });
    const trackers: TrackerUploadItem[] = [
      { name: "AITHER", config: { FaviconURL: "https://icons.example/aither.png" } },
    ];

    const { result } = renderHook(() => useTrackerIcons(trackers, true));

    await waitFor(() =>
      expect(getTrackerIcon).toHaveBeenCalledWith(
        "icons.example",
        "https://icons.example/aither.png",
      ),
    );
    await waitFor(() => expect(result.current.aither).toBe("data:image/png;base64,a"));
  });

  it("fetches newly added configured trackers without refetching unchanged trackers", async () => {
    const getTrackerIcon = vi
      .fn()
      .mockResolvedValueOnce("data:image/png;base64,a")
      .mockResolvedValueOnce("data:image/png;base64,b");
    vi.stubGlobal("go", { guiapp: { App: { GetTrackerIcon: getTrackerIcon } } });
    const firstTrackers: TrackerUploadItem[] = [{ name: "AITHER", config: {} }];
    const nextTrackers: TrackerUploadItem[] = [
      { name: "AITHER", config: {} },
      { name: "BLU", config: {} },
    ];

    const { result, rerender } = renderHook(({ trackers }) => useTrackerIcons(trackers, true), {
      initialProps: { trackers: firstTrackers },
    });

    await waitFor(() => expect(result.current.aither).toBe("data:image/png;base64,a"));
    rerender({ trackers: nextTrackers });

    await waitFor(() => expect(result.current.blu).toBe("data:image/png;base64,b"));
    expect(getTrackerIcon).toHaveBeenCalledTimes(2);
    expect(getTrackerIcon).toHaveBeenNthCalledWith(1, "AITHER", "");
    expect(getTrackerIcon).toHaveBeenNthCalledWith(2, "BLU", "");
  });
});
