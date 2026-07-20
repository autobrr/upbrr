// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { TrackerAuthStatus } from "../types";

const jsonResponse = (payload: unknown, init?: ResponseInit) =>
  new Response(JSON.stringify(payload), {
    headers: { "Content-Type": "application/json" },
    ...init,
  });

const eventStreamResponse = (payload: unknown, onCancel?: () => void) => {
  const encoder = new TextEncoder();
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(encoder.encode(`event: test:event\ndata: ${JSON.stringify(payload)}\n\n`));
    },
    cancel() {
      onCancel?.();
    },
  });
  return new Response(stream, {
    headers: { "Content-Type": "text/event-stream" },
  });
};

describe("web client", () => {
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

    const { initializeWebClient } = await import("./client");
    const { screenshotClient } = await import("./app");
    initializeWebClient("csrf-token", true);

    const release = { SourcePath: "C:/media/Example.mkv", Generation: 3 };
    const result = await screenshotClient.fetchPlan(release);

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
          Release: release,
        }),
      }),
    );
  });

  it("uses the abortable exact DVD menu capture payload", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ Images: [], Warnings: [] }))
      .mockResolvedValueOnce(jsonResponse([]))
      .mockResolvedValueOnce(jsonResponse({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);

    const { initializeWebClient } = await import("./client");
    const { menuImageClient } = await import("./app");
    initializeWebClient("csrf-token", true);

    const controller = new AbortController();
    const release = { SourcePath: "C:/media/Example", Generation: 3 };
    await menuImageClient.capture(release, controller.signal);
    await menuImageClient.list(release);
    await menuImageClient.remove(release, "C:/managed/menu.png");

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/api/app/CaptureDVDMenus",
      expect.objectContaining({
        signal: controller.signal,
        body: JSON.stringify({
          Release: release,
        }),
      }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/api/app/ListDVDMenuScreenshots",
      expect.objectContaining({
        body: JSON.stringify({
          Release: release,
        }),
      }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      3,
      "/api/app/DeleteDVDMenuScreenshot",
      expect.objectContaining({
        body: JSON.stringify({
          Release: release,
          ImagePath: "C:/managed/menu.png",
        }),
      }),
    );
  });

  it("preserves tracker auth status fields from browser app routes", async () => {
    const status: TrackerAuthStatus = {
      trackerID: "MTV",
      displayName: "MTV",
      state: "needs_2fa",
      cookieCount: 2,
      lastCheckedAt: "2026-07-08T01:02:03Z",
      lastError: "tracker auth validation failed",
      encryptedStorage: true,
      needs2FA: true,
      challengeID: "challenge-123",
      message: "enter 2FA code",
    };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(status));
    vi.stubGlobal("fetch", fetchMock);

    const { initializeWebClient } = await import("./client");
    const { trackerAuthClient } = await import("./app");
    initializeWebClient("csrf-token", true);

    const result = await trackerAuthClient.getStatus("MTV");

    expect(result).toEqual(status);
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/app/GetTrackerAuthStatus",
      expect.objectContaining({
        body: JSON.stringify({ Tracker: "MTV" }),
      }),
    );
  });

  it("preserves tracker auth app route error messages", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          jsonResponse({ error: "tracker auth: validation failed" }, { status: 400 }),
        ),
    );

    const { initializeWebClient } = await import("./client");
    const { trackerAuthClient } = await import("./app");
    initializeWebClient("csrf-token", true);

    await expect(trackerAuthClient.test("MTV")).rejects.toThrow("tracker auth: validation failed");
  });

  it("renders structured operation failures with stable recovery guidance", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse(
          {
            error: "Prepared release changed.",
            failure: {
              Code: "stale_generation",
              Operation: "media",
              Message: "Prepared release changed.",
              Recovery: "refresh_release",
            },
          },
          { status: 409 },
        ),
      ),
    );

    const { initializeWebClient, requestApp } = await import("./client");
    initializeWebClient("csrf-token", true);

    await expect(requestApp("FetchScreenshotPlan", {})).rejects.toThrow(
      "Prepared release changed. Recovery: refresh release.",
    );
  });

  it("prefixes browser API calls with the injected base URL", async () => {
    window.__UPBRR_BASE_URL__ = "/upbrr/";
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(jsonResponse({ ok: true })));
    vi.stubGlobal("fetch", fetchMock);

    const { authClient, initializeWebClient, withBasePath } = await import("./client");
    const { configClient } = await import("./app");
    initializeWebClient("csrf-token", true);

    expect(withBasePath("/api/auth/status")).toBe("/upbrr/api/auth/status");

    await authClient.status();
    await configClient.get();

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

    const { authClient } = await import("./client");

    await expect(authClient.status()).rejects.toThrow("login required");
  });

  it("refreshes browser auth state and retries app calls once", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ error: "csrf validation failed" }, { status: 403 }))
      .mockResolvedValueOnce(
        jsonResponse({
          authenticated: true,
          csrfToken: "stale-csrf",
        }),
      )
      .mockResolvedValueOnce(jsonResponse("job-1"));
    vi.stubGlobal("fetch", fetchMock);

    const { initializeWebClient } = await import("./client");
    const { dupeClient } = await import("./app");
    initializeWebClient("stale-csrf", false);

    const result = await dupeClient.start(
      { SourcePath: "C:/media/movie.mkv", Generation: 1 },
      ["AITHER"],
      "dupe-correlation",
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
        }),
      );
    vi.stubGlobal("fetch", fetchMock);

    const { initializeWebClient } = await import("./client");
    const { dupeClient } = await import("./app");
    initializeWebClient("session-a-csrf", false);

    await expect(
      dupeClient.start(
        { SourcePath: "C:/media/movie.mkv", Generation: 1 },
        ["AITHER"],
        "dupe-correlation",
      ),
    ).rejects.toThrow("Web session changed in another tab");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("opens browser events with the initialized session token header", async () => {
    const cancelStream = vi.fn();
    const fetchMock = vi
      .fn()
      .mockResolvedValue(eventStreamResponse({ jobID: "job-1" }, cancelStream));
    vi.stubGlobal("fetch", fetchMock);

    const { subscribeWebEvent, initializeWebClient } = await import("./client");
    initializeWebClient("csrf-token", true);
    const listener = vi.fn();
    const off = subscribeWebEvent("test:event", listener);

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

  it("stores host path case sensitivity from client initialization", async () => {
    const { initializeWebClient, isHostPathCaseInsensitive } = await import("./client");

    initializeWebClient("csrf-token", true);
    expect(isHostPathCaseInsensitive()).toBe(true);

    initializeWebClient("csrf-token", false);
    expect(isHostPathCaseInsensitive()).toBe(false);
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

    const { initializeWebClient } = await import("./client");
    const { trackerAuthClient } = await import("./app");
    initializeWebClient("csrf-token", true);

    await expect(trackerAuthClient.importCookies("PTP")).rejects.toThrow(
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

    const { initializeWebClient } = await import("./client");
    const { trackerAuthClient } = await import("./app");
    initializeWebClient("csrf-token", true);

    await expect(trackerAuthClient.importCookies("PTP")).rejects.toThrow(
      "cookie file content exceeds 1048576 byte limit",
    );
    expect(readAsText).not.toHaveBeenCalled();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("posts valid tracker cookie files from the browser client", async () => {
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

    const { initializeWebClient } = await import("./client");
    const { trackerAuthClient } = await import("./app");
    initializeWebClient("csrf-token", true);

    await expect(trackerAuthClient.importCookies("PTP")).resolves.toEqual({
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
