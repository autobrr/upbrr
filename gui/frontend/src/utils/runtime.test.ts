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

  it("refreshes browser auth state and retries app calls once", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ error: "csrf validation failed" }, { status: 403 }))
      .mockResolvedValueOnce(
        jsonResponse({
          authenticated: true,
          csrfToken: "fresh-csrf",
          nativeBrowseEnabled: true,
        }),
      )
      .mockResolvedValueOnce(jsonResponse("job-1"));
    vi.stubGlobal("fetch", fetchMock);

    const { initializeBrowserBridge } = await import("./runtime");
    initializeBrowserBridge("stale-csrf", false);

    const result = await (globalThis as any).go.guiapp.App.StartDupeCheck(
      "C:/media/movie.mkv",
      {},
      {},
      ["AITHER"],
    );

    expect(result).toBe("job-1");
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/api/app/StartDupeCheck",
      expect.objectContaining({
        credentials: "include",
        headers: expect.objectContaining({ "X-CSRF-Token": "stale-csrf" }),
      }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(2, "/api/auth/status", {
      credentials: "include",
    });
    expect(fetchMock).toHaveBeenNthCalledWith(
      3,
      "/api/app/StartDupeCheck",
      expect.objectContaining({
        credentials: "include",
        headers: expect.objectContaining({ "X-CSRF-Token": "fresh-csrf" }),
      }),
    );
  });

  it("notifies listeners when auth refresh changes native browse availability", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ error: "csrf validation failed" }, { status: 403 }))
      .mockResolvedValueOnce(
        jsonResponse({
          authenticated: true,
          csrfToken: "fresh-csrf",
          nativeBrowseEnabled: true,
        }),
      )
      .mockResolvedValueOnce(jsonResponse("job-1"));
    vi.stubGlobal("fetch", fetchMock);

    const {
      initializeBrowserBridge,
      isBrowserNativeBrowseAvailable,
      subscribeBrowserNativeBrowseAvailability,
    } = await import("./runtime");
    initializeBrowserBridge("stale-csrf", false);
    const listener = vi.fn();
    const unsubscribe = subscribeBrowserNativeBrowseAvailability(listener);

    await (globalThis as any).go.guiapp.App.StartDupeCheck("C:/media/movie.mkv", {}, {}, [
      "AITHER",
    ]);

    expect(listener).toHaveBeenCalledOnce();
    expect(isBrowserNativeBrowseAvailable()).toBe(true);
    unsubscribe();
  });
});
