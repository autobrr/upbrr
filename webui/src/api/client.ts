// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { OperationFailure } from "../types";

type EventCallback = (payload: unknown) => void;
type AppRequestHandler = (
  method: string,
  body?: unknown,
  options?: { signal?: AbortSignal },
) => Promise<unknown>;

declare global {
  interface Window {
    __UPBRR_BASE_URL__?: string;
  }
}

const callbacks = new Map<string, Set<EventCallback>>();
let eventStreamController: AbortController | null = null;
let eventStreamReconnectTimer: ReturnType<typeof setTimeout> | null = null;
let csrfToken = "";
let caseInsensitivePaths = navigator.platform.toLowerCase().startsWith("win");
let testAppRequestHandler: AppRequestHandler | null = null;

const sessionChangedMessage =
  "Web session changed in another tab. Reload this tab to continue with the active login.";

const parseJSONResponse = async <T>(response: Response): Promise<T | null> => {
  const text = await response.text();
  if (!text.trim()) return null;
  return JSON.parse(text) as T;
};

/** Stable domain failure returned by an accepted application operation. */
class OperationFailureError extends Error {
  readonly failure: OperationFailure;

  constructor(failure: OperationFailure) {
    super(
      failure.Recovery && failure.Recovery !== "none"
        ? `${failure.Message} Recovery: ${failure.Recovery.replaceAll("_", " ")}.`
        : failure.Message,
    );
    this.name = "OperationFailureError";
    this.failure = failure;
  }
}

const isAuthFailureStatus = (status: number) => status === 401 || status === 403;

const normalizeBaseURL = (value: unknown) => {
  if (typeof value !== "string") return "/";
  const trimmed = value.trim();
  if (!trimmed || trimmed === "/") return "/";
  const path = trimmed.startsWith("/") ? trimmed : `/${trimmed}`;
  return path.endsWith("/") ? path : `${path}/`;
};

/** Prefixes API and event paths with the base URL injected by the WebUI server. */
export const withBasePath = (path: string) => {
  const normalizedPath = path.startsWith("/") ? path.slice(1) : path;
  return `${normalizeBaseURL(window.__UPBRR_BASE_URL__)}${normalizedPath}`;
};

const setPathCaseSensitivity = (caseInsensitive: unknown) => {
  if (typeof caseInsensitive === "boolean") caseInsensitivePaths = caseInsensitive;
};

const refreshAuthState = async () => {
  const response = await fetch(withBasePath("/api/auth/status"), { credentials: "include" });
  const payload = await parseJSONResponse<
    Record<string, unknown> & {
      authenticated?: boolean;
      csrfToken?: string;
      caseInsensitivePaths?: boolean;
    }
  >(response);
  if (!response.ok || !payload?.authenticated) return false;

  const nextCSRFToken = String(payload.csrfToken || "");
  if (csrfToken && nextCSRFToken && nextCSRFToken !== csrfToken) {
    throw new Error(sessionChangedMessage);
  }
  csrfToken = nextCSRFToken;
  setPathCaseSensitivity(payload.caseInsensitivePaths);
  recreateEventStream();
  return csrfToken !== "";
};

const closeEventStream = () => {
  if (eventStreamReconnectTimer) {
    clearTimeout(eventStreamReconnectTimer);
    eventStreamReconnectTimer = null;
  }
  if (eventStreamController) {
    eventStreamController.abort();
    eventStreamController = null;
  }
};

const ensureEventStream = () => {
  if (eventStreamController || !csrfToken || callbacks.size === 0) return;
  const controller = new AbortController();
  eventStreamController = controller;
  void runEventStream(controller);
};

const recreateEventStream = () => {
  closeEventStream();
  ensureEventStream();
};

const scheduleEventStreamReconnect = () => {
  if (!csrfToken || callbacks.size === 0 || eventStreamReconnectTimer) return;
  eventStreamReconnectTimer = setTimeout(() => {
    eventStreamReconnectTimer = null;
    ensureEventStream();
  }, 1000);
};

const runEventStream = async (controller: AbortController) => {
  let reconnect = true;
  try {
    const response = await fetch(withBasePath("/api/events"), {
      method: "GET",
      credentials: "include",
      headers: { "X-CSRF-Token": csrfToken },
      signal: controller.signal,
    });
    if (!response.ok) {
      if (isAuthFailureStatus(response.status)) {
        reconnect = await refreshAuthState().catch(() => false);
      }
      return;
    }
    if (!response.body) throw new Error("Event stream response body is unavailable");
    await readEventStream(response.body, controller.signal);
  } catch (_error) {
    if (!controller.signal.aborted) scheduleEventStreamReconnect();
  } finally {
    if (eventStreamController === controller) {
      eventStreamController = null;
      if (reconnect && !controller.signal.aborted) scheduleEventStreamReconnect();
    }
  }
};

const readEventStream = async (body: ReadableStream<Uint8Array>, signal: AbortSignal) => {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  const abortRead = () => void reader.cancel();
  signal.addEventListener("abort", abortRead, { once: true });
  try {
    for (;;) {
      if (signal.aborted) break;
      const { value, done } = await reader.read();
      if (done || signal.aborted) break;
      buffer += decoder.decode(value, { stream: true });
      const parts = buffer.split(/\r?\n\r?\n/);
      buffer = parts.pop() || "";
      parts.forEach(dispatchEventBlock);
    }
  } finally {
    signal.removeEventListener("abort", abortRead);
    reader.releaseLock();
  }
};

const dispatchEventBlock = (block: string) => {
  let eventName = "message";
  const data: string[] = [];
  for (const line of block.split(/\r?\n/)) {
    if (line.startsWith("event:")) eventName = line.slice("event:".length).trim();
    else if (line.startsWith("data:")) data.push(line.slice("data:".length).trimStart());
  }
  if (!callbacks.has(eventName) || data.length === 0) return;
  const payload = JSON.parse(data.join("\n"));
  callbacks.get(eventName)?.forEach((callback) => callback(payload));
};

const postJSON = async <T>(path: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
  const requestInit = (): RequestInit => ({
    method: "POST",
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
      ...(csrfToken ? { "X-CSRF-Token": csrfToken } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  });
  let response = await fetch(withBasePath(path), requestInit());
  let payload = await parseJSONResponse<T & { error?: string; failure?: OperationFailure }>(
    response,
  );
  if (!response.ok && isAuthFailureStatus(response.status) && (await refreshAuthState())) {
    response = await fetch(withBasePath(path), requestInit());
    payload = await parseJSONResponse<T & { error?: string }>(response);
  }
  if (!response.ok) {
    if (payload?.failure) throw new OperationFailureError(payload.failure);
    throw new Error(String(payload?.error || response.statusText || "Request failed"));
  }
  if (payload === null) throw new Error("Request returned an empty response");
  return payload as T;
};

/** Initializes cookie-bound WebUI requests and event delivery for one authenticated session. */
export const initializeWebClient = (token: string, hostCaseInsensitivePaths?: boolean) => {
  csrfToken = token;
  setPathCaseSensitivity(hostCaseInsensitivePaths);
  recreateEventStream();
};

/** Updates the active session CSRF token and host path comparison semantics. */
export const updateWebCSRFToken = (token: string, hostCaseInsensitivePaths?: boolean) => {
  csrfToken = token;
  setPathCaseSensitivity(hostCaseInsensitivePaths);
  recreateEventStream();
};

/** Reports whether the WebUI host compares filesystem paths case-insensitively. */
export const isHostPathCaseInsensitive = () => caseInsensitivePaths;

/** Subscribes to a cookie-bound WebUI server-sent event. */
export const subscribeWebEvent = (eventName: string, callback: EventCallback) => {
  const listeners = callbacks.get(eventName) ?? new Set<EventCallback>();
  listeners.add(callback);
  callbacks.set(eventName, listeners);
  ensureEventStream();
  return () => {
    listeners.delete(callback);
    if (listeners.size === 0) callbacks.delete(eventName);
    if (callbacks.size === 0) closeEventStream();
  };
};

/** Sends one typed application operation to the WebUI server. */
export const requestApp = <T>(
  method: string,
  body?: unknown,
  options: { signal?: AbortSignal } = {},
): Promise<T> => {
  if (testAppRequestHandler) {
    return testAppRequestHandler(method, body, options).then((result) => result as T);
  }
  return postJSON<T>(`/api/app/${method}`, body, options.signal);
};

/** Installs a request transport for unit tests without creating runtime globals. */
export const setAppRequestHandlerForTests = (handler: AppRequestHandler | null) => {
  testAppRequestHandler = handler;
};

/** Auth operations sharing the WebUI base path, cookies, and CSRF lifecycle. */
export const authClient = {
  status: async () => {
    const response = await fetch(withBasePath("/api/auth/status"), { credentials: "include" });
    const payload = await parseJSONResponse<Record<string, unknown> & { error?: string }>(response);
    if (!response.ok) {
      throw new Error(String(payload?.error || response.statusText || "Request failed"));
    }
    return payload || {};
  },
  bootstrap: (username: string, password: string, retainLogin: boolean) =>
    postJSON("/api/auth/bootstrap", { username, password, retainLogin }),
  login: (username: string, password: string, retainLogin: boolean) =>
    postJSON("/api/auth/login", { username, password, retainLogin }),
  saveBrowsePolicy: (browseRoot: string, allowUnrestrictedBrowse: boolean) =>
    postJSON("/api/auth/browse-policy", { browseRoot, allowUnrestrictedBrowse }),
  logout: () => postJSON("/api/auth/logout"),
};
