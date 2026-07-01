// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const jsonResponse = (payload: unknown, init?: ResponseInit) =>
  new Response(JSON.stringify(payload), {
    headers: { "Content-Type": "application/json" },
    ...init,
  });

const eventStreamResponse = (payload: unknown, onCancel?: () => void) => {
  const encoder = new TextEncoder();
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(
        encoder.encode(`event: metadata:progress\ndata: ${JSON.stringify(payload)}\n\n`),
      );
    },
    cancel() {
      onCancel?.();
    },
  });
  return new Response(stream, {
    headers: { "Content-Type": "text/event-stream" },
  });
};

describe("browser runtime bridge", () => {
  beforeEach(() => {
    vi.resetModules();
  });

  afterEach(() => {
    delete window.__UPBRR_BASE_URL__;
    vi.restoreAllMocks();
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
      { Anime: true },
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
          MetadataOverrides: { Anime: true },
        }),
      }),
    );
  });

  it("prefixes browser API calls with the injected base URL", async () => {
    window.__UPBRR_BASE_URL__ = "/upbrr/";
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(jsonResponse({ ok: true })));
    vi.stubGlobal("fetch", fetchMock);

    const { browserAuth, initializeBrowserBridge, withBrowserBasePath } = await import("./runtime");
    initializeBrowserBridge("csrf-token", true);

    expect(withBrowserBasePath("/api/auth/status")).toBe("/upbrr/api/auth/status");

    await browserAuth.status();
    await (globalThis as any).go.guiapp.App.GetConfig();

    expect(fetchMock).toHaveBeenNthCalledWith(1, "/upbrr/api/auth/status", {
      credentials: "include",
    });
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/upbrr/api/app/GetConfig",
      expect.objectContaining({
        method: "POST",
        credentials: "include",
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
          csrfToken: "stale-csrf",
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
        headers: expect.objectContaining({ "X-CSRF-Token": "stale-csrf" }),
      }),
    );
  });

  it("does not adopt a different browser session during auth refresh", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ error: "csrf validation failed" }, { status: 403 }))
      .mockResolvedValueOnce(
        jsonResponse({
          authenticated: true,
          csrfToken: "other-session-csrf",
          nativeBrowseEnabled: true,
        }),
      );
    vi.stubGlobal("fetch", fetchMock);

    const { initializeBrowserBridge } = await import("./runtime");
    initializeBrowserBridge("session-a-csrf", false);

    await expect(
      (globalThis as any).go.guiapp.App.StartDupeCheck("C:/media/movie.mkv", {}, {}, {}, [
        "AITHER",
      ]),
    ).rejects.toThrow("Web session changed in another tab");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("notifies listeners when auth refresh changes native browse availability", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ error: "csrf validation failed" }, { status: 403 }))
      .mockResolvedValueOnce(
        jsonResponse({
          authenticated: true,
          csrfToken: "stale-csrf",
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

    await (globalThis as any).go.guiapp.App.StartDupeCheck("C:/media/movie.mkv", {}, {}, {}, [
      "AITHER",
    ]);

    expect(listener).toHaveBeenCalledOnce();
    expect(isBrowserNativeBrowseAvailable()).toBe(true);
    unsubscribe();
  });

  it("opens browser events with the initialized session token header", async () => {
    const cancelStream = vi.fn();
    const fetchMock = vi
      .fn()
      .mockResolvedValue(eventStreamResponse({ jobID: "job-1" }, cancelStream));
    vi.stubGlobal("fetch", fetchMock);

    const { EventsOn, initializeBrowserBridge } = await import("./runtime");
    initializeBrowserBridge("csrf-token", true);
    const listener = vi.fn();
    const off = EventsOn("metadata:progress", listener);

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/events",
      expect.objectContaining({
        method: "GET",
        credentials: "include",
        headers: { "X-CSRF-Token": "csrf-token" },
      }),
    );
    await vi.waitFor(() => expect(listener).toHaveBeenCalledWith({ jobID: "job-1" }));
    off();
    await vi.waitFor(() => expect(cancelStream).toHaveBeenCalledOnce());
  });

  it("stores runtime path case sensitivity from bridge initialization", async () => {
    const { initializeBrowserBridge, isRuntimePathCaseInsensitive } = await import("./runtime");

    initializeBrowserBridge("csrf-token", true, true);
    expect(isRuntimePathCaseInsensitive()).toBe(true);

    initializeBrowserBridge("csrf-token", true, false);
    expect(isRuntimePathCaseInsensitive()).toBe(false);
  });

  it("rejects oversized decoded tracker cookie content before posting", async () => {
    const originalCreateElement = document.createElement.bind(document);
    const input = document.createElement("input");
    Object.defineProperty(input, "files", {
      configurable: true,
      value: [new File(["x"], "cookies.txt")],
    });
    vi.spyOn(input, "click").mockImplementation(() => {
      input.dispatchEvent(new Event("change"));
    });
    vi.spyOn(document, "createElement").mockImplementation((tagName: string) => {
      if (tagName === "input") {
        return input;
      }
      return originalCreateElement(tagName);
    });
    const readAsText = vi.fn();
    vi.stubGlobal(
      "FileReader",
      vi.fn().mockImplementation(function (this: any) {
        this.readAsText = readAsText.mockImplementation(() => {
          Object.defineProperty(this, "result", {
            configurable: true,
            value: "x".repeat(1024 * 1024 + 1),
          });
          this.onload?.(new ProgressEvent("load"));
        });
      }),
    );
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const { initializeBrowserBridge } = await import("./runtime");
    initializeBrowserBridge("csrf-token", true);

    await expect((globalThis as any).go.guiapp.App.ImportTrackerAuthCookies("PTP")).rejects.toThrow(
      "cookie file content exceeds 1048576 byte limit",
    );
    expect(readAsText).toHaveBeenCalled();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("rejects oversized raw tracker cookie files before decoding", async () => {
    const originalCreateElement = document.createElement.bind(document);
    const input = document.createElement("input");
    const file = new File(["x"], "cookies.txt");
    Object.defineProperty(file, "size", { configurable: true, value: 1024 * 1024 + 1 });
    Object.defineProperty(input, "files", {
      configurable: true,
      value: [file],
    });
    vi.spyOn(input, "click").mockImplementation(() => {
      input.dispatchEvent(new Event("change"));
    });
    vi.spyOn(document, "createElement").mockImplementation((tagName: string) => {
      if (tagName === "input") {
        return input;
      }
      return originalCreateElement(tagName);
    });
    const readAsText = vi.fn();
    vi.stubGlobal(
      "FileReader",
      vi.fn().mockImplementation(function (this: any) {
        this.readAsText = readAsText.mockImplementation(() => {
          Object.defineProperty(this, "result", { configurable: true, value: "session=abc" });
          this.onload?.(new ProgressEvent("load"));
        });
      }),
    );
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ trackerID: "PTP" }));
    vi.stubGlobal("fetch", fetchMock);

    const { initializeBrowserBridge } = await import("./runtime");
    initializeBrowserBridge("csrf-token", true);

    await expect((globalThis as any).go.guiapp.App.ImportTrackerAuthCookies("PTP")).rejects.toThrow(
      "cookie file content exceeds 1048576 byte limit",
    );
    expect(readAsText).not.toHaveBeenCalled();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("posts valid tracker cookie files from the browser bridge", async () => {
    const originalCreateElement = document.createElement.bind(document);
    const input = document.createElement("input");
    Object.defineProperty(input, "files", {
      configurable: true,
      value: [new File(["session=abc"], "cookies.txt")],
    });
    vi.spyOn(input, "click").mockImplementation(() => {
      input.dispatchEvent(new Event("change"));
    });
    vi.spyOn(document, "createElement").mockImplementation((tagName: string) => {
      if (tagName === "input") {
        return input;
      }
      return originalCreateElement(tagName);
    });
    vi.stubGlobal(
      "FileReader",
      vi.fn().mockImplementation(function (this: any) {
        this.readAsText = vi.fn(() => {
          Object.defineProperty(this, "result", { configurable: true, value: "session=abc" });
          this.onload?.(new ProgressEvent("load"));
        });
      }),
    );
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ trackerID: "PTP" }));
    vi.stubGlobal("fetch", fetchMock);

    const { initializeBrowserBridge } = await import("./runtime");
    initializeBrowserBridge("csrf-token", true);

    await expect(
      (globalThis as any).go.guiapp.App.ImportTrackerAuthCookies("PTP"),
    ).resolves.toEqual({
      trackerID: "PTP",
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/app/ImportTrackerAuthCookieContent",
      expect.objectContaining({
        body: JSON.stringify({
          Tracker: "PTP",
          FileName: "cookies.txt",
          Content: "session=abc",
        }),
      }),
    );
  });
});
