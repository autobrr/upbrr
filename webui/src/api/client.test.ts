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
    const { releaseWorkflowClient } = await import("./app");
    initializeWebClient("csrf-token", true);

    const result = await releaseWorkflowClient.mediaPlan("workflow-1");

    expect(result).toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/app/GetReleaseWorkflowMediaPlan",
      expect.objectContaining({
        method: "POST",
        credentials: "include",
        headers: {
          "Content-Type": "application/json",
          "X-CSRF-Token": "csrf-token",
        },
        body: JSON.stringify({ workflowId: "workflow-1" }),
      }),
    );
  });

  it("reads the active-input snapshot with GET and session headers", async () => {
    const snapshot = { state: "empty", revision: 4 };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(snapshot));
    vi.stubGlobal("fetch", fetchMock);

    const { initializeWebClient } = await import("./client");
    const { activeInputClient } = await import("./app");
    initializeWebClient("csrf-token", true);

    await expect(activeInputClient.get()).resolves.toEqual(snapshot);
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/app/GetActiveInput",
      expect.objectContaining({
        method: "GET",
        credentials: "include",
        headers: { "X-CSRF-Token": "csrf-token" },
      }),
    );
  });

  it("uses exact opaque workflow media payloads", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ workflow: { id: "workflow-1" } }));
    vi.stubGlobal("fetch", fetchMock);

    const { initializeWebClient } = await import("./client");
    const { releaseWorkflowClient } = await import("./app");
    initializeWebClient("csrf-token", true);

    const controller = new AbortController();
    await releaseWorkflowClient.deleteMedia(
      {
        workflowId: "workflow-1",
        expectedRevision: 7,
        media: { id: "media-1", revision: 6 },
        artifactIds: ["menu-1"],
        idempotencyKey: "delete-menu-1",
      },
      controller.signal,
    );

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/api/app/DeleteReleaseWorkflowMedia",
      expect.objectContaining({
        signal: controller.signal,
        body: JSON.stringify({
          workflowId: "workflow-1",
          expectedRevision: 7,
          media: { id: "media-1", revision: 6 },
          artifactIds: ["menu-1"],
          idempotencyKey: "delete-menu-1",
        }),
      }),
    );
  });

  it("preserves tracker auth status fields from browser app routes", async () => {
    const status: TrackerAuthStatus = {
      trackerID: "BTN",
      displayName: "BTN",
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

    const result = await trackerAuthClient.getStatus("BTN");

    expect(result).toEqual(status);
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/app/GetTrackerAuthStatus",
      expect.objectContaining({
        body: JSON.stringify({ Tracker: "BTN" }),
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

    await expect(trackerAuthClient.test("BTN")).rejects.toThrow("tracker auth: validation failed");
  });

  it("surfaces a pending config activation conflict without accepting the candidate", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse(
          {
            error: "another validated configuration is already pending activation",
            activation: {
              status: "pending",
              activationId: "activation-existing",
              activeGeneration: 3,
              pendingGeneration: 4,
              impacts: ["trackers"],
              updatedAt: "2026-09-19T00:00:00Z",
            },
          },
          { status: 409 },
        ),
      ),
    );

    const { configClient } = await import("./app");
    await expect(configClient.save("{}")).rejects.toThrow(
      "another validated configuration is already pending activation",
    );
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

    await expect(requestApp("GetReleaseWorkflow", {})).rejects.toThrow(
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
      .mockResolvedValueOnce(jsonResponse({ workflow: { id: "workflow-1" } }));
    vi.stubGlobal("fetch", fetchMock);

    const { initializeWebClient } = await import("./client");
    const { releaseWorkflowClient } = await import("./app");
    initializeWebClient("stale-csrf", false);

    const request = {
      goal: "prepared",
      idempotencyKey: "create-1",
      intent: {
        factInstructions: {
          Identity: {},
          ReleaseName: {},
          Metadata: {},
          SourceLookup: "",
          BlurayReleaseID: "",
          Playlist: { Set: false, Selected: [], UseAll: false },
          TrackerIDs: {},
        },
      },
    };
    const result = await releaseWorkflowClient.continue(request);

    expect(result.workflow.id).toBe("workflow-1");
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/api/app/ContinueReleaseWorkflow",
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
      "/api/app/ContinueReleaseWorkflow",
      expect.objectContaining({
        credentials: "include",
        headers: expect.objectContaining({ "X-CSRF-Token": "stale-csrf" }),
      }),
    );
  });

  it("notifies session loss when an app request cannot refresh authentication", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ error: "login required" }, { status: 401 }))
      .mockResolvedValueOnce(jsonResponse({ authenticated: false }));
    vi.stubGlobal("fetch", fetchMock);

    const { initializeWebClient, requestAppGet, subscribeWebSessionLoss } =
      await import("./client");
    const onSessionLoss = vi.fn();
    const unsubscribe = subscribeWebSessionLoss(onSessionLoss);
    initializeWebClient("csrf-token", false);
    await expect(requestAppGet("GetConfig")).rejects.toThrow("login required");
    expect(onSessionLoss).toHaveBeenCalledExactlyOnceWith("lost");
    unsubscribe();
  });

  it("notifies session loss when the event stream loses authentication", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ error: "login required" }, { status: 401 }))
      .mockResolvedValueOnce(jsonResponse({ authenticated: false }));
    vi.stubGlobal("fetch", fetchMock);

    const { initializeWebClient, subscribeWebEvent, subscribeWebSessionLoss } =
      await import("./client");
    const onSessionLoss = vi.fn();
    const unsubscribeLoss = subscribeWebSessionLoss(onSessionLoss);
    initializeWebClient("csrf-token", false);
    const unsubscribeEvent = subscribeWebEvent("test:event", vi.fn());

    await vi.waitFor(() => expect(onSessionLoss).toHaveBeenCalledOnce());
    expect(onSessionLoss).toHaveBeenCalledWith("lost");
    expect(fetchMock).toHaveBeenCalledTimes(2);
    unsubscribeEvent();
    unsubscribeLoss();
  });

  it("preserves the session when the event stream cannot verify authentication", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ error: "login required" }, { status: 401 }))
      .mockResolvedValueOnce(jsonResponse({ error: "temporary failure" }, { status: 500 }));
    vi.stubGlobal("fetch", fetchMock);

    const { initializeWebClient, subscribeWebEvent, subscribeWebSessionLoss } =
      await import("./client");
    const onSessionLoss = vi.fn();
    const unsubscribeLoss = subscribeWebSessionLoss(onSessionLoss);
    initializeWebClient("csrf-token", false);
    const unsubscribeEvent = subscribeWebEvent("test:event", vi.fn());

    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    expect(onSessionLoss).not.toHaveBeenCalled();
    unsubscribeEvent();
    unsubscribeLoss();
  });

  it("reports a changed session from the event stream without reconnecting", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ error: "session changed" }, { status: 403 }))
      .mockResolvedValueOnce(jsonResponse({ authenticated: true, csrfToken: "new-csrf" }));
    vi.stubGlobal("fetch", fetchMock);

    const { initializeWebClient, subscribeWebEvent, subscribeWebSessionLoss } =
      await import("./client");
    const onSessionLoss = vi.fn();
    const unsubscribeLoss = subscribeWebSessionLoss(onSessionLoss);
    initializeWebClient("old-csrf", false);
    const unsubscribeEvent = subscribeWebEvent("test:event", vi.fn());

    await vi.waitFor(() => expect(onSessionLoss).toHaveBeenCalledExactlyOnceWith("changed"));
    expect(fetchMock).toHaveBeenCalledTimes(2);
    unsubscribeEvent();
    unsubscribeLoss();
  });

  it("preserves the session when an app request cannot verify authentication", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ error: "login required" }, { status: 401 }))
      .mockResolvedValueOnce(jsonResponse({ error: "temporary failure" }, { status: 500 }));
    vi.stubGlobal("fetch", fetchMock);

    const { initializeWebClient, requestAppGet, subscribeWebSessionLoss } =
      await import("./client");
    const onSessionLoss = vi.fn();
    const unsubscribeLoss = subscribeWebSessionLoss(onSessionLoss);
    initializeWebClient("csrf-token", false);
    await expect(requestAppGet("GetConfig")).rejects.toThrow(
      "Authentication status refresh failed (500).",
    );
    expect(onSessionLoss).not.toHaveBeenCalled();
    unsubscribeLoss();
  });

  it.each(["GET", "JSON", "form"] as const)(
    "preserves %s transport and structured failures across an auth retry",
    async (kind) => {
      const fetchMock = vi
        .fn()
        .mockResolvedValueOnce(jsonResponse({ error: "csrf validation failed" }, { status: 403 }))
        .mockResolvedValueOnce(jsonResponse({ authenticated: true, csrfToken: "csrf-token" }))
        .mockResolvedValueOnce(
          jsonResponse(
            {
              failure: {
                Code: "stale_generation",
                Operation: "media",
                Message: "Prepared release changed.",
                Recovery: "refresh_release",
              },
            },
            { status: 409 },
          ),
        );
      vi.stubGlobal("fetch", fetchMock);
      const { initializeWebClient, requestApp, requestAppGet, requestAppForm } =
        await import("./client");
      initializeWebClient("csrf-token", false);
      const signal = new AbortController().signal;
      const options = { signal, correlationID: "request-1" };
      const form = new FormData();
      form.append("source", "example");
      const request =
        kind === "GET"
          ? requestAppGet("Example", options)
          : kind === "JSON"
            ? requestApp("Example", { source: "example" }, options)
            : requestAppForm("Example", form, options);

      await expect(request).rejects.toThrow("Prepared release changed. Recovery: refresh release.");
      const expected = {
        method: kind === "GET" ? "GET" : "POST",
        credentials: "include",
        headers:
          kind === "JSON"
            ? {
                "Content-Type": "application/json",
                "X-CSRF-Token": "csrf-token",
                "X-Upbrr-Correlation-Id": "request-1",
              }
            : { "X-CSRF-Token": "csrf-token" },
        signal,
        ...(kind === "GET" ? {} : { body: kind === "JSON" ? '{"source":"example"}' : form }),
      };
      expect(fetchMock).toHaveBeenCalledTimes(3);
      expect(fetchMock).toHaveBeenNthCalledWith(1, "/api/app/Example", expected);
      expect(fetchMock).toHaveBeenNthCalledWith(3, "/api/app/Example", expected);
    },
  );

  it("rejects an empty successful application response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 204 })));
    const { requestAppGet } = await import("./client");
    await expect(requestAppGet("Example")).rejects.toThrow("Request returned an empty response");
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

    const { initializeWebClient, subscribeWebSessionLoss } = await import("./client");
    const { releaseWorkflowClient } = await import("./app");
    const onSessionLoss = vi.fn();
    const unsubscribe = subscribeWebSessionLoss(onSessionLoss);
    initializeWebClient("session-a-csrf", false);

    await expect(
      releaseWorkflowClient.continue({
        goal: "prepared",
        idempotencyKey: "create-1",
        intent: {
          factInstructions: {
            Identity: {},
            ReleaseName: {},
            Metadata: {},
            SourceLookup: "",
            BlurayReleaseID: "",
            Playlist: { Set: false, Selected: [], UseAll: false },
            TrackerIDs: {},
          },
        },
      }),
    ).rejects.toThrow("Web session changed in another tab");
    expect(onSessionLoss).toHaveBeenCalledExactlyOnceWith("changed");
    expect(fetchMock).toHaveBeenCalledTimes(2);
    unsubscribe();
  });

  it("does not adopt a changed session across overlapping auth refreshes", async () => {
    const resolveStatus: Array<(response: Response) => void> = [];
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      if (String(input).endsWith("/api/auth/status")) {
        return new Promise<Response>((resolve) => resolveStatus.push(resolve));
      }
      return Promise.resolve(jsonResponse({ error: "old session" }, { status: 403 }));
    });
    vi.stubGlobal("fetch", fetchMock);

    const { initializeWebClient, requestAppGet, subscribeWebSessionLoss } =
      await import("./client");
    const onSessionLoss = vi.fn();
    const unsubscribe = subscribeWebSessionLoss(onSessionLoss);
    initializeWebClient("old-csrf", false);

    const first = requestAppGet("GetConfig");
    const second = requestAppGet("GetApplicationInfo");
    await vi.waitFor(() => expect(resolveStatus).toHaveLength(2));

    resolveStatus[0](jsonResponse({ authenticated: true, csrfToken: "new-csrf" }));
    await expect(first).rejects.toThrow("Web session changed in another tab");
    expect(onSessionLoss).toHaveBeenCalledExactlyOnceWith("changed");

    resolveStatus[1](jsonResponse({ authenticated: true, csrfToken: "new-csrf" }));
    await expect(second).rejects.toThrow("Web session changed in another tab");
    expect(fetchMock).toHaveBeenCalledTimes(4);
    expect(onSessionLoss).toHaveBeenCalledTimes(1);
    unsubscribe();
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

  it("notifies connection subscribers after the event stream connects", async () => {
    const cancelStream = vi.fn();
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(eventStreamResponse({ revision: 2 }, cancelStream)),
    );

    const { initializeWebClient, subscribeWebEventConnection } = await import("./client");
    initializeWebClient("csrf-token", true);
    const connected = vi.fn();
    const off = subscribeWebEventConnection(connected);

    await vi.waitFor(() => expect(connected).toHaveBeenCalledOnce());
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
