// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

class FakeEventSource {
  onmessage: (() => void) | null = null;

  constructor(
    readonly url: string,
    readonly options?: EventSourceInit,
  ) {}

  addEventListener = vi.fn();
  close = vi.fn();
}

const jsonResponse = (payload: unknown, init?: ResponseInit) =>
  new Response(JSON.stringify(payload), {
    headers: { "Content-Type": "application/json" },
    ...init,
  });

describe("browser runtime bridge", () => {
  beforeEach(() => {
    vi.resetModules();
    vi.stubGlobal("EventSource", FakeEventSource);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("posts app calls with JSON and CSRF headers", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);

    const { initializeBrowserBridge } = await import("./runtime");
    initializeBrowserBridge("csrf-token", true);

    const result = await (globalThis as any).go.guiapp.App.FetchScreenshotPlan(
      "C:/media/movie.mkv",
      { TMDBID: 1 },
      { Category: "MOVIE" },
    );

    expect(result).toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/app/FetchScreenshotPlan",
      expect.objectContaining({
        method: "POST",
        credentials: "include",
        headers: {
          "Content-Type": "application/json",
          "X-CSRF-Token": "csrf-token",
        },
        body: JSON.stringify({
          Path: "C:/media/movie.mkv",
          Overrides: { TMDBID: 1 },
          NameOverrides: { Category: "MOVIE" },
        }),
      }),
    );
  });

  it("throws response errors from browser auth calls", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse({ error: "login required" }, { status: 401 })),
    );

    const { browserAuth } = await import("./runtime");

    await expect(browserAuth.status()).rejects.toThrow("login required");
  });
});
