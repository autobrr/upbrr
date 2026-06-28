// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { EventsOn as wailsEventsOn } from "../../wailsjs/runtime/runtime";

type EventCallback = (payload: unknown) => void;

declare global {
  interface Window {
    __UPBRR_BASE_URL__?: string;
  }
}

const callbackMap = new Map<string, Set<EventCallback>>();
const nativeBrowseAvailabilityListeners = new Set<() => void>();
let eventStreamController: AbortController | null = null;
let eventStreamReconnectTimer: ReturnType<typeof setTimeout> | null = null;
let browserMode = false;
let csrfToken = "";
let nativeBrowseEnabled = false;
let caseInsensitivePaths = navigator.platform.toLowerCase().startsWith("win");

// Mirrors trackerauth.MaxCookieImportContentBytes so browser imports reject
// over-limit files before decode and over-limit decoded text before posting.
const maxCookieImportContentBytes = 1024 * 1024;
const encodedTextByteLength = (value: string) => new TextEncoder().encode(value).length;
const sessionChangedMessage =
  "Web session changed in another tab. Reload this tab to continue with the active login.";

const isWebUIRuntime = () => {
  const runtime = (window as typeof window & { runtime?: unknown }).runtime;
  return (
    !runtime && (window.location.protocol === "http:" || window.location.protocol === "https:")
  );
};

const parseJSONResponse = async <T>(response: Response): Promise<T | null> => {
  const text = await response.text();
  if (!text.trim()) {
    return null;
  }
  return JSON.parse(text) as T;
};

const isAuthFailureStatus = (status: number) => status === 401 || status === 403;

const normalizeBrowserBaseURL = (value: unknown) => {
  if (typeof value !== "string") {
    return "/";
  }
  const trimmed = value.trim();
  if (!trimmed || trimmed === "/") {
    return "/";
  }
  const path = trimmed.startsWith("/") ? trimmed : `/${trimmed}`;
  return path.endsWith("/") ? path : `${path}/`;
};

const browserBaseURL = () => normalizeBrowserBaseURL(window.__UPBRR_BASE_URL__);

/**
 * Prefixes browser-mode API and event paths with the base URL injected by the
 * embedded web server. Root and Wails desktop runtimes resolve to root paths.
 */
export const withBrowserBasePath = (path: string) => {
  const baseURL = browserBaseURL();
  const normalizedPath = path.startsWith("/") ? path.slice(1) : path;
  return `${baseURL}${normalizedPath}`;
};

const setNativeBrowseEnabled = (enabled: boolean) => {
  if (nativeBrowseEnabled === enabled) {
    return;
  }
  nativeBrowseEnabled = enabled;
  nativeBrowseAvailabilityListeners.forEach((listener) => listener());
};

const setRuntimePathCaseSensitivity = (caseInsensitive: unknown) => {
  if (typeof caseInsensitive === "boolean") {
    caseInsensitivePaths = caseInsensitive;
  }
};

/**
 * Refreshes browser auth state for a retry without switching this tab to a
 * different web session.
 */
const refreshBrowserAuthState = async () => {
  if (!browserMode) {
    return false;
  }
  const response = await fetch(withBrowserBasePath("/api/auth/status"), { credentials: "include" });
  const payload = await parseJSONResponse<
    Record<string, unknown> & {
      authenticated?: boolean;
      csrfToken?: string;
      nativeBrowseEnabled?: boolean;
      caseInsensitivePaths?: boolean;
    }
  >(response);
  if (!response.ok || !payload?.authenticated) {
    return false;
  }
  const nextCSRFToken = String(payload.csrfToken || "");
  if (csrfToken && nextCSRFToken && nextCSRFToken !== csrfToken) {
    throw new Error(sessionChangedMessage);
  }
  csrfToken = nextCSRFToken;
  setRuntimePathCaseSensitivity(payload.caseInsensitivePaths);
  setNativeBrowseEnabled(Boolean(payload.nativeBrowseEnabled));
  recreateEventSource();
  return csrfToken !== "";
};

const addBrowserListener = (eventName: string, callback: EventCallback) => {
  if (!callbackMap.has(eventName)) {
    callbackMap.set(eventName, new Set());
  }
  const set = callbackMap.get(eventName)!;
  set.add(callback);
  ensureEventSource();
  return () => {
    set.delete(callback);
    if (set.size === 0) {
      callbackMap.delete(eventName);
    }
    if (callbackMap.size === 0) {
      closeEventSource();
    }
  };
};

const ensureEventSource = () => {
  if (!browserMode || eventStreamController || !csrfToken || callbackMap.size === 0) {
    return;
  }
  const controller = new AbortController();
  eventStreamController = controller;
  void runBrowserEventStream(controller);
};

const recreateEventSource = () => {
  closeEventSource();
  ensureEventSource();
};

const closeEventSource = () => {
  if (eventStreamReconnectTimer) {
    clearTimeout(eventStreamReconnectTimer);
    eventStreamReconnectTimer = null;
  }
  if (eventStreamController) {
    eventStreamController.abort();
    eventStreamController = null;
  }
};

const scheduleEventStreamReconnect = () => {
  if (!browserMode || !csrfToken || callbackMap.size === 0 || eventStreamReconnectTimer) {
    return;
  }
  eventStreamReconnectTimer = setTimeout(() => {
    eventStreamReconnectTimer = null;
    ensureEventSource();
  }, 1000);
};

/**
 * Opens the browser-mode SSE stream with the same cookie-bound CSRF header as
 * app calls.
 *
 * Native EventSource cannot send CSRF headers, so fetch streaming is used to
 * avoid storing CSRF tokens in URLs while preserving reconnect behavior.
 */
const runBrowserEventStream = async (controller: AbortController) => {
  let reconnect = true;
  try {
    const response = await fetch(withBrowserBasePath("/api/events"), {
      method: "GET",
      credentials: "include",
      headers: { "X-CSRF-Token": csrfToken },
      signal: controller.signal,
    });
    if (!response.ok) {
      if (isAuthFailureStatus(response.status)) {
        reconnect = await refreshBrowserAuthState().catch(() => false);
      }
      return;
    }
    if (!response.body) {
      throw new Error("Event stream response body is unavailable");
    }
    await readBrowserEventStream(response.body, controller.signal);
  } catch (_err) {
    if (!controller.signal.aborted) {
      // Network interruptions should behave like EventSource reconnects.
      scheduleEventStreamReconnect();
    }
    return;
  } finally {
    if (eventStreamController === controller) {
      eventStreamController = null;
      if (reconnect && !controller.signal.aborted) {
        scheduleEventStreamReconnect();
      }
    }
  }
};

/**
 * Reads browser-mode SSE frames until the stream ends or the supplied signal is
 * aborted. Aborts cancel the active reader so unsubscribe cannot leave a pending
 * read behind.
 */
const readBrowserEventStream = async (body: ReadableStream<Uint8Array>, signal: AbortSignal) => {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  const abortRead = () => {
    void reader.cancel();
  };
  signal.addEventListener("abort", abortRead, { once: true });
  try {
    for (;;) {
      if (signal.aborted) {
        break;
      }
      const { value, done } = await reader.read();
      if (done || signal.aborted) {
        break;
      }
      buffer += decoder.decode(value, { stream: true });
      const parts = buffer.split(/\r?\n\r?\n/);
      buffer = parts.pop() || "";
      for (const part of parts) {
        dispatchBrowserEventBlock(part);
      }
    }
  } finally {
    signal.removeEventListener("abort", abortRead);
    reader.releaseLock();
  }
};

const dispatchBrowserEventBlock = (block: string) => {
  let eventName = "message";
  const data: string[] = [];
  for (const line of block.split(/\r?\n/)) {
    if (line.startsWith("event:")) {
      eventName = line.slice("event:".length).trim();
    } else if (line.startsWith("data:")) {
      data.push(line.slice("data:".length).trimStart());
    }
  }
  if (!callbackMap.has(eventName) || data.length === 0) {
    return;
  }
  const payload = JSON.parse(data.join("\n"));
  callbackMap.get(eventName)?.forEach((callback) => callback(payload));
};

const postJSON = async <T>(path: string, body?: unknown): Promise<T> => {
  const requestInit = (): RequestInit => ({
    method: "POST",
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
      ...(csrfToken ? { "X-CSRF-Token": csrfToken } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  let response = await fetch(withBrowserBasePath(path), requestInit());
  let payload = await parseJSONResponse<T & { error?: string }>(response);
  if (!response.ok && isAuthFailureStatus(response.status) && (await refreshBrowserAuthState())) {
    response = await fetch(withBrowserBasePath(path), requestInit());
    payload = await parseJSONResponse<T & { error?: string }>(response);
  }
  if (!response.ok) {
    throw new Error(String(payload?.error || response.statusText || "Request failed"));
  }
  if (payload === null) {
    throw new Error("Request returned an empty response");
  }
  return payload as T;
};

/**
 * Installs the browser-mode app bridge and pins app calls/events to token.
 *
 * runtimeCaseInsensitivePaths should come from the web server so browser path
 * comparisons match the host filesystem, not the client platform.
 */
export const initializeBrowserBridge = (
  token: string,
  browseEnabled = false,
  runtimeCaseInsensitivePaths?: boolean,
) => {
  browserMode = isWebUIRuntime();
  setNativeBrowseEnabled(browseEnabled);
  setRuntimePathCaseSensitivity(runtimeCaseInsensitivePaths);
  if (!browserMode) {
    return;
  }
  csrfToken = token;

  const call = <T>(method: string, body?: unknown) => postJSON<T>(`/api/app/${method}`, body);

  (globalThis as any).go = {
    guiapp: {
      App: {
        BrowsePath: () => call<string>("BrowseFile"),
        BrowseFile: () => call<string>("BrowseFile"),
        BrowseImageFiles: () => call<string[]>("BrowseImageFiles"),
        BrowseFolder: () => call<string>("BrowseFolder"),
        BrowseDirectory: (path: string, mode: "file" | "folder") =>
          call("BrowseDirectory", { path, mode }),
        DetectDiscType: (path: string) => call<string>("DetectDiscType", { Path: path }),
        FetchMetadata: (
          path: string,
          sourceLookupURL: string,
          overrides: unknown,
          nameOverrides: unknown,
          trackers: string[],
        ) =>
          call("FetchMetadata", {
            Path: path,
            SourceLookupURL: sourceLookupURL,
            Overrides: overrides,
            NameOverrides: nameOverrides,
            Trackers: trackers,
          }),
        ResetMetadata: (
          path: string,
          sourceLookupURL: string,
          overrides: unknown,
          nameOverrides: unknown,
          trackers: string[],
        ) =>
          call("ResetMetadata", {
            Path: path,
            SourceLookupURL: sourceLookupURL,
            Overrides: overrides,
            NameOverrides: nameOverrides,
            Trackers: trackers,
          }),
        SelectBlurayCandidate: (path: string, releaseID: string) =>
          call("SelectBlurayCandidate", {
            Path: path,
            ReleaseID: releaseID,
          }),
        FetchDescriptionBuilder: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          trackers: string[],
          ignoreDupesFor: string[],
        ) =>
          call("FetchDescriptionBuilder", {
            Path: path,
            Overrides: overrides,
            NameOverrides: nameOverrides,
            Trackers: trackers,
            IgnoreDupesFor: ignoreDupesFor,
          }),
        FetchPreparation: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          trackers: string[],
          ignoreDupesFor: string[],
        ) =>
          call("FetchPreparation", {
            Path: path,
            Overrides: overrides,
            NameOverrides: nameOverrides,
            Trackers: trackers,
            IgnoreDupesFor: ignoreDupesFor,
          }),
        FetchTrackerDryRun: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          trackers: string[],
          ignoreDupesFor: string[],
          questionnaireAnswers: Record<string, Record<string, string>>,
          descriptionGroups: unknown,
          debug: boolean,
          noSeed: boolean,
          runLogLevel: string,
        ) =>
          call("FetchTrackerDryRun", {
            Path: path,
            Overrides: overrides,
            NameOverrides: nameOverrides,
            Trackers: trackers,
            IgnoreDupesFor: ignoreDupesFor,
            QuestionnaireAnswers: questionnaireAnswers,
            DescriptionGroups: descriptionGroups,
            Debug: debug,
            NoSeed: noSeed,
            RunLogLevel: runLogLevel,
          }),
        CheckDupes: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          trackers: string[],
        ) =>
          call("CheckDupes", {
            Path: path,
            Overrides: overrides,
            NameOverrides: nameOverrides,
            Trackers: trackers,
          }),
        StartDupeCheck: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          trackers: string[],
        ) =>
          call("StartDupeCheck", {
            Path: path,
            Overrides: overrides,
            NameOverrides: nameOverrides,
            Trackers: trackers,
          }),
        CancelDupeCheck: (jobID: string) => call("CancelDupeCheck", { JobID: jobID }),
        GetDupeCheckSnapshot: (jobID: string) => call("GetDupeCheckSnapshot", { JobID: jobID }),
        FetchScreenshotPlan: (path: string, overrides: unknown, nameOverrides: unknown) =>
          call("FetchScreenshotPlan", {
            Path: path,
            Overrides: overrides,
            NameOverrides: nameOverrides,
          }),
        GenerateScreenshots: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          selections: unknown,
          purpose: string,
        ) =>
          call("GenerateScreenshots", {
            Path: path,
            Overrides: overrides,
            NameOverrides: nameOverrides,
            Selections: selections,
            Purpose: purpose,
          }),
        PreviewScreenshotFrame: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          timestampSeconds: number,
        ) =>
          call("PreviewScreenshotFrame", {
            Path: path,
            Overrides: overrides,
            NameOverrides: nameOverrides,
            TimestampSeconds: timestampSeconds,
          }),
        DeleteScreenshot: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          imagePath: string,
        ) =>
          call("DeleteScreenshot", {
            Path: path,
            Overrides: overrides,
            NameOverrides: nameOverrides,
            ImagePath: imagePath,
          }),
        SaveFinalScreenshotSelections: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          images: unknown,
        ) =>
          call("SaveFinalScreenshotSelections", {
            Path: path,
            Overrides: overrides,
            NameOverrides: nameOverrides,
            Images: images,
          }),
        ImportMenuImages: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          paths: string[],
        ) =>
          call("ImportMenuImages", {
            Path: path,
            Overrides: overrides,
            NameOverrides: nameOverrides,
            Paths: paths,
          }),
        ReadScreenshotImage: (path: string) => call("ReadScreenshotImage", { Path: path }),
        ListUploadCandidates: (path: string, overrides: unknown, nameOverrides: unknown) =>
          call("ListUploadCandidates", {
            Path: path,
            Overrides: overrides,
            NameOverrides: nameOverrides,
          }),
        ListUploadedImages: (path: string, overrides: unknown, nameOverrides: unknown) =>
          call("ListUploadedImages", {
            Path: path,
            Overrides: overrides,
            NameOverrides: nameOverrides,
          }),
        UploadImages: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          trackers: string[],
          host: string,
          images: unknown,
        ) =>
          call("UploadImages", {
            Path: path,
            Overrides: overrides,
            NameOverrides: nameOverrides,
            Trackers: trackers,
            Host: host,
            Images: images,
          }),
        DeleteUploadedImage: (path: string, imagePath: string, host: string) =>
          call("DeleteUploadedImage", { Path: path, ImagePath: imagePath, Host: host }),
        DeleteTrackerImageURL: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          url: string,
        ) =>
          call("DeleteTrackerImageURL", {
            Path: path,
            Overrides: overrides,
            NameOverrides: nameOverrides,
            URL: url,
          }),
        RenderDescription: (raw: string) => call("RenderDescription", { Raw: raw }),
        SaveDescriptionOverride: (
          path: string,
          groupKey: string,
          raw: string,
          trackers: string[],
          overrides: unknown,
          nameOverrides: unknown,
        ) =>
          call("SaveDescriptionOverride", {
            Path: path,
            GroupKey: groupKey,
            Raw: raw,
            Trackers: trackers,
            Overrides: overrides,
            NameOverrides: nameOverrides,
          }),
        DiscoverPlaylists: (path: string) => call("DiscoverPlaylists", { Path: path }),
        SavePlaylistSelection: (path: string, playlists: string[], useAll: boolean) =>
          call("SavePlaylistSelection", { Path: path, Playlists: playlists, UseAll: useAll }),
        LoadPlaylistSelection: (path: string) => call("LoadPlaylistSelection", { Path: path }),
        GetConfig: () => call("GetConfig"),
        GetApplicationInfo: () => call("GetApplicationInfo"),
        GetDefaultConfig: () => call("GetDefaultConfig"),
        SaveConfig: (payload: string) => call("SaveConfig", { Payload: payload }),
        ExportConfig: async () => {
          const payload = await call<string>("ExportConfig");
          const blob = new Blob([payload], { type: "application/json" });
          const url = URL.createObjectURL(blob);
          const anchor = document.createElement("a");
          anchor.href = url;
          anchor.download = "upbrr-config.json";
          anchor.click();
          URL.revokeObjectURL(url);
          return anchor.download;
        },
        ImportConfig: async () => {
          const fileData = await new Promise<{ name: string; content: string }>(
            (resolve, reject) => {
              const input = document.createElement("input");
              input.type = "file";
              input.accept = ".py,.yaml,.yml,.json";
              input.onchange = () => {
                const file = input.files?.[0];
                if (!file) {
                  resolve({ name: "", content: "" });
                  return;
                }
                const reader = new FileReader();
                reader.onload = () =>
                  resolve({ name: file.name, content: reader.result as string });
                reader.onerror = () => reject(reader.error);
                reader.readAsText(file);
              };
              input.addEventListener("cancel", () => resolve({ name: "", content: "" }));
              input.click();
            },
          );
          if (!fileData.name) return { message: "", warnings: [] };
          const resp = await call<{ result: string; warnings: string[] }>("ImportConfig", {
            FileName: fileData.name,
            FileContent: fileData.content,
          });
          return { message: resp.result, warnings: resp.warnings ?? [] };
        },
        GetLogPath: () => call("GetLogPath"),
        GetRecentLogs: (limit: number) => call("GetRecentLogs", { Limit: limit }),
        StartLogStream: () => call("StartLogStream"),
        StopLogStream: (streamID: string) => call("StopLogStream", { StreamID: streamID }),
        GetLogExclusions: () => call("GetLogExclusions"),
        UpdateLogExclusions: (patterns: string[]) =>
          call("UpdateLogExclusions", { Patterns: patterns }),
        ListKnownTrackers: () => call("ListKnownTrackers"),
        ListTrackerAuthCapabilities: () => call("ListTrackerAuthCapabilities"),
        GetTrackerAuthStatus: (tracker: string) =>
          call("GetTrackerAuthStatus", { Tracker: tracker }),
        ImportTrackerAuthCookies: async (tracker: string) => {
          const fileData = await new Promise<{ name: string; content: string }>(
            (resolve, reject) => {
              const input = document.createElement("input");
              input.type = "file";
              input.accept = ".txt,.json";
              input.onchange = () => {
                const file = input.files?.[0];
                if (!file) {
                  resolve({ name: "", content: "" });
                  return;
                }
                if (file.size > maxCookieImportContentBytes) {
                  reject(
                    new Error(
                      `tracker auth: cookie file content exceeds ${maxCookieImportContentBytes} byte limit`,
                    ),
                  );
                  return;
                }
                const reader = new FileReader();
                reader.onload = () => {
                  const content = reader.result as string;
                  if (encodedTextByteLength(content) > maxCookieImportContentBytes) {
                    reject(
                      new Error(
                        `tracker auth: cookie file content exceeds ${maxCookieImportContentBytes} byte limit`,
                      ),
                    );
                    return;
                  }
                  resolve({ name: file.name, content });
                };
                reader.onerror = () => reject(reader.error);
                reader.readAsText(file);
              };
              input.addEventListener("cancel", () => resolve({ name: "", content: "" }));
              input.click();
            },
          );
          if (!fileData.name) {
            return call("GetTrackerAuthStatus", { Tracker: tracker });
          }
          return call("ImportTrackerAuthCookieContent", {
            Tracker: tracker,
            FileName: fileData.name,
            Content: fileData.content,
          });
        },
        ImportTrackerAuthCookieContent: (tracker: string, fileName: string, content: string) =>
          call("ImportTrackerAuthCookieContent", {
            Tracker: tracker,
            FileName: fileName,
            Content: content,
          }),
        TestTrackerAuth: (tracker: string) => call("TestTrackerAuth", { Tracker: tracker }),
        LoginTrackerAuth: (tracker: string, login: unknown) =>
          call("LoginTrackerAuth", { Tracker: tracker, Login: login }),
        SubmitTrackerAuth2FA: (challengeID: string, code: string) =>
          call("SubmitTrackerAuth2FA", { ChallengeID: challengeID, Code: code }),
        DeleteTrackerAuth: (tracker: string) => call("DeleteTrackerAuth", { Tracker: tracker }),
        GetImageHostPolicyMetadata: () => call("GetImageHostPolicyMetadata"),
        ListHistory: () => call("ListHistory"),
        GetHistoryOverview: (sourcePath: string) =>
          call("GetHistoryOverview", { SourcePath: sourcePath }),
        DeleteHistoryRelease: (sourcePath: string) =>
          call("DeleteHistoryRelease", { SourcePath: sourcePath }),
        StartTrackerUpload: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          trackers: string[],
          ignoreDupesFor: string[],
          questionnaireAnswers: Record<string, Record<string, string>>,
          descriptionGroups: unknown,
          debug: boolean,
          noSeed: boolean,
          runLogLevel: string,
        ) =>
          call("StartTrackerUpload", {
            Path: path,
            Overrides: overrides,
            NameOverrides: nameOverrides,
            Trackers: trackers,
            IgnoreDupesFor: ignoreDupesFor,
            QuestionnaireAnswers: questionnaireAnswers,
            DescriptionGroups: descriptionGroups,
            Debug: debug,
            NoSeed: noSeed,
            RunLogLevel: runLogLevel,
          }),
        CancelTrackerUpload: (jobID: string) => call("CancelTrackerUpload", { JobID: jobID }),
        RetryFailedTrackerUpload: (jobID: string) =>
          call("RetryFailedTrackerUpload", { JobID: jobID }),
        GetTrackerUploadSnapshot: (jobID: string) =>
          call("GetTrackerUploadSnapshot", { JobID: jobID }),
        GetTrackerIcon: (domain: string, url: string) =>
          call<string>("GetTrackerIcon", { Domain: domain, URL: url }),
      },
    },
  };
  recreateEventSource();
};

/** Returns whether app calls should use the browser HTTP bridge instead of Wails. */
export const isBrowserMode = () => {
  browserMode = isWebUIRuntime();
  return browserMode;
};

/**
 * Reports whether browser-mode callers can use host-native browse dialogs.
 * Wails desktop mode always provides native browse support.
 */
export const isBrowserNativeBrowseAvailable = () => {
  if (!isBrowserMode()) {
    return true;
  }
  return nativeBrowseEnabled;
};

/**
 * Reports whether the current runtime compares host filesystem paths
 * case-insensitively.
 */
export const isRuntimePathCaseInsensitive = () => caseInsensitivePaths;

/**
 * Subscribes to browser-mode native browse availability changes.
 */
export const subscribeBrowserNativeBrowseAvailability = (listener: () => void) => {
  nativeBrowseAvailabilityListeners.add(listener);
  return () => {
    nativeBrowseAvailabilityListeners.delete(listener);
  };
};

/**
 * Updates the CSRF token used by browser-mode app calls and event streams.
 *
 * Passing runtimeCaseInsensitivePaths also refreshes the host path-comparison
 * contract carried by auth/status responses.
 */
export const updateBrowserCSRFToken = (token: string, runtimeCaseInsensitivePaths?: boolean) => {
  csrfToken = token;
  setRuntimePathCaseSensitivity(runtimeCaseInsensitivePaths);
  recreateEventSource();
};

/**
 * Browser-mode auth calls that share the injected base path and cookie-bound
 * CSRF handling used by the app bridge.
 */
export const browserAuth = {
  status: async () => {
    const response = await fetch(withBrowserBasePath("/api/auth/status"), {
      credentials: "include",
    });
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

/**
 * Subscribes to Wails events in desktop mode or the cookie-bound web event
 * stream in browser mode.
 */
export const EventsOn = (eventName: string, callback: EventCallback) => {
  if (!browserMode) {
    return wailsEventsOn(eventName, callback as any);
  }
  return addBrowserListener(eventName, callback);
};
