// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

type EventCallback = (payload: unknown) => void;

type RuntimeConfig = {
  apiBaseURL: string;
  bearerToken: string;
  nativeBrowseEnabled: boolean;
};

type AuthStatus = {
  authenticated?: boolean;
  needsSetup?: boolean;
  username?: string;
  bearerToken?: string;
  nativeBrowseEnabled?: boolean;
  browseRoot?: string;
  allowUnrestrictedBrowse?: boolean;
  needsBrowsePolicy?: boolean;
};

const callbackMap = new Map<string, Set<EventCallback>>();
let eventAbort: AbortController | null = null;
let apiBaseURL = "";
let bearerToken = "";
let browserMode = false;
let nativeBrowseEnabled = false;

const storedToken = () =>
  window.localStorage.getItem("upbrr.apiToken") ||
  window.sessionStorage.getItem("upbrr.apiToken") ||
  "";

const persistToken = (token: string, retain: boolean) => {
  window.sessionStorage.removeItem("upbrr.apiToken");
  window.localStorage.removeItem("upbrr.apiToken");
  if (!token) return;
  if (retain) {
    window.localStorage.setItem("upbrr.apiToken", token);
  } else {
    window.sessionStorage.setItem("upbrr.apiToken", token);
  }
};

const setRuntimeConfig = (config: Partial<RuntimeConfig>) => {
  apiBaseURL = (config.apiBaseURL || "").replace(/\/$/, "");
  bearerToken = config.bearerToken || bearerToken || storedToken();
  nativeBrowseEnabled = Boolean(config.nativeBrowseEnabled);
  installRESTBridge();
  recreateEventStream();
};

const isWebUIRuntime = () => {
  const runtime = (window as typeof window & { runtime?: unknown }).runtime;
  return (
    !runtime && (window.location.protocol === "http:" || window.location.protocol === "https:")
  );
};

const endpoint = (path: string) => `${apiBaseURL}${path}`;

const parseJSONResponse = async <T>(response: Response): Promise<T | null> => {
  const text = await response.text();
  if (!text.trim()) return null;
  return JSON.parse(text) as T;
};

const requestJSON = async <T>(
  method: string,
  path: string,
  body?: unknown,
  auth = true,
): Promise<T> => {
  const response = await fetch(endpoint(path), {
    method,
    headers: {
      ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      ...(auth && bearerToken ? { Authorization: `Bearer ${bearerToken}` } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const payload = await parseJSONResponse<T & { error?: string }>(response);
  if (!response.ok) {
    throw new Error(String(payload?.error || response.statusText || "Request failed"));
  }
  if (payload === null) {
    throw new Error("Request returned an empty response");
  }
  return payload as T;
};

const getJSON = <T>(path: string, auth = true) => requestJSON<T>("GET", path, undefined, auth);
const postJSON = <T>(path: string, body?: unknown, auth = true) =>
  requestJSON<T>("POST", path, body, auth);
const putJSON = <T>(path: string, body?: unknown) => requestJSON<T>("PUT", path, body);
const deleteJSON = <T>(path: string) => requestJSON<T>("DELETE", path);

const addRESTListener = (eventName: string, callback: EventCallback) => {
  if (!callbackMap.has(eventName)) {
    callbackMap.set(eventName, new Set());
  }
  callbackMap.get(eventName)!.add(callback);
  ensureEventStream();
  return () => callbackMap.get(eventName)?.delete(callback);
};

const ensureEventStream = () => {
  if (eventAbort || !bearerToken || callbackMap.size === 0) return;
  eventAbort = new AbortController();
  void readEventStream(eventAbort.signal);
};

const readEventStream = async (signal: AbortSignal) => {
  try {
    const response = await fetch(endpoint("/api/v1/events"), {
      headers: { Authorization: `Bearer ${bearerToken}` },
      signal,
    });
    if (!response.ok || !response.body) return;

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      let boundary = buffer.indexOf("\n\n");
      while (boundary >= 0) {
        dispatchSSE(buffer.slice(0, boundary));
        buffer = buffer.slice(boundary + 2);
        boundary = buffer.indexOf("\n\n");
      }
    }
  } catch {
    // Event streams are best-effort; callers also poll job snapshots.
  } finally {
    eventAbort = null;
  }
};

const dispatchSSE = (chunk: string) => {
  let eventName = "message";
  const data: string[] = [];
  chunk.split(/\r?\n/).forEach((line) => {
    if (line.startsWith("event:")) eventName = line.slice(6).trim();
    if (line.startsWith("data:")) data.push(line.slice(5).trimStart());
  });
  if (data.length === 0) return;
  const payload = JSON.parse(data.join("\n"));
  callbackMap.get(eventName)?.forEach((callback) => callback(payload));
};

const recreateEventStream = () => {
  if (eventAbort) {
    eventAbort.abort();
    eventAbort = null;
  }
  ensureEventStream();
};

const installRESTBridge = () => {
  (globalThis as any).go = {
    ...(globalThis as any).go,
    guiapp: {
      ...((globalThis as any).go?.guiapp || {}),
      App: {
        BrowsePath: async () => (await postJSON<string>("/api/v1/files/native/file")) || "",
        BrowseFile: () => postJSON<string>("/api/v1/files/native/file"),
        BrowseFolder: () => postJSON<string>("/api/v1/files/native/folder"),
        BrowseDirectory: (path: string, mode: "file" | "folder") =>
          postJSON("/api/v1/files/browse", { path, mode }),
        ListUIStates: () => getJSON("/api/v1/ui-state"),
        GetUIState: (id: string) => getJSON(`/api/v1/ui-state/${encodeURIComponent(id)}`),
        SaveUIState: (id: string, label: string, state: unknown) =>
          postJSON("/api/v1/ui-state", { id, label, state }),
        DetectDiscType: (path: string) => postJSON<string>("/api/v1/media/disc-type", { path }),
        FetchMetadata: (
          path: string,
          sourceLookupURL: string,
          overrides: unknown,
          nameOverrides: unknown,
          trackers: string[],
        ) =>
          postJSON("/api/v1/metadata/fetch", {
            path,
            sourceLookupURL,
            overrides,
            nameOverrides,
            trackers,
          }),
        ResetMetadata: (
          path: string,
          sourceLookupURL: string,
          overrides: unknown,
          nameOverrides: unknown,
          trackers: string[],
        ) =>
          postJSON("/api/v1/metadata/reset", {
            path,
            sourceLookupURL,
            overrides,
            nameOverrides,
            trackers,
          }),
        FetchDescriptionBuilder: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          trackers: string[],
          ignoreDupesFor: string[],
        ) =>
          postJSON("/api/v1/description-builder/fetch", {
            path,
            overrides,
            nameOverrides,
            trackers,
            ignoreDupesFor,
          }),
        FetchPreparation: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          trackers: string[],
          ignoreDupesFor: string[],
        ) =>
          postJSON("/api/v1/preparation/fetch", {
            path,
            overrides,
            nameOverrides,
            trackers,
            ignoreDupesFor,
          }),
        FetchTrackerDryRun: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          trackers: string[],
          ignoreRuleFailures: boolean,
          ignoreDupesFor: string[],
          questionnaireAnswers: Record<string, Record<string, string>>,
          descriptionGroups: unknown,
          debug: boolean,
          runLogLevel: string,
        ) =>
          postJSON("/api/v1/tracker-dry-run/fetch", {
            path,
            overrides,
            nameOverrides,
            trackers,
            ignoreRuleFailures,
            ignoreDupesFor,
            questionnaireAnswers,
            descriptionGroups,
            debug,
            runLogLevel,
          }),
        CheckDupes: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          trackers: string[],
        ) => postJSON("/api/v1/dupes/check", { path, overrides, nameOverrides, trackers }),
        StartDupeCheck: async (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          trackers: string[],
        ) => {
          const result = await postJSON<{ jobID: string }>("/api/v1/jobs/dupes", {
            path,
            overrides,
            nameOverrides,
            trackers,
          });
          return result.jobID;
        },
        CancelDupeCheck: (jobID: string) =>
          deleteJSON(`/api/v1/jobs/dupes/${encodeURIComponent(jobID)}`),
        GetDupeCheckSnapshot: (jobID: string) =>
          getJSON(`/api/v1/jobs/dupes/${encodeURIComponent(jobID)}`),
        FetchScreenshotPlan: (path: string, overrides: unknown, nameOverrides: unknown) =>
          postJSON("/api/v1/screenshots/plan", { path, overrides, nameOverrides }),
        GenerateScreenshots: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          selections: unknown,
          purpose: string,
        ) =>
          postJSON("/api/v1/screenshots/generate", {
            path,
            overrides,
            nameOverrides,
            selections,
            purpose,
          }),
        PreviewScreenshotFrame: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          timestampSeconds: number,
        ) =>
          postJSON("/api/v1/screenshots/preview-frame", {
            path,
            overrides,
            nameOverrides,
            timestampSeconds,
          }),
        DeleteScreenshot: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          imagePath: string,
        ) => postJSON("/api/v1/screenshots/delete", { path, overrides, nameOverrides, imagePath }),
        SaveFinalScreenshotSelections: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          images: unknown,
        ) =>
          putJSON("/api/v1/screenshots/final-selections", {
            path,
            overrides,
            nameOverrides,
            images,
          }),
        ReadScreenshotImage: (path: string) =>
          postJSON<string>("/api/v1/screenshots/read-image", { path }),
        ListUploadCandidates: (path: string, overrides: unknown, nameOverrides: unknown) =>
          postJSON("/api/v1/screenshots/upload-candidates", { path, overrides, nameOverrides }),
        ListUploadedImages: (path: string, overrides: unknown, nameOverrides: unknown) =>
          postJSON("/api/v1/screenshots/uploaded-images", { path, overrides, nameOverrides }),
        UploadImages: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          host: string,
          images: unknown,
        ) =>
          postJSON("/api/v1/screenshots/upload", { path, overrides, nameOverrides, host, images }),
        DeleteUploadedImage: (path: string, imagePath: string, host: string) =>
          postJSON("/api/v1/screenshots/delete-uploaded", { path, imagePath, host }),
        DeleteTrackerImageURL: (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          url: string,
        ) =>
          postJSON("/api/v1/screenshots/delete-tracker-url", {
            path,
            overrides,
            nameOverrides,
            url,
          }),
        RenderDescription: (raw: string) => postJSON<string>("/api/v1/description/render", { raw }),
        SaveDescriptionOverride: (
          path: string,
          groupKey: string,
          raw: string,
          trackers: string[],
          overrides: unknown,
          nameOverrides: unknown,
        ) =>
          postJSON("/api/v1/description/override", {
            path,
            groupKey,
            raw,
            trackers,
            overrides,
            nameOverrides,
          }),
        DiscoverPlaylists: (path: string) => postJSON("/api/v1/playlists/discover", { path }),
        SavePlaylistSelection: (path: string, playlists: string[], useAll: boolean) =>
          putJSON("/api/v1/playlists/selection", { path, playlists, useAll }),
        LoadPlaylistSelection: (path: string) =>
          postJSON("/api/v1/playlists/selection/get", { path }),
        GetConfig: () => getJSON<string>("/api/v1/config/current"),
        GetDefaultConfig: () => getJSON<string>("/api/v1/config/default"),
        SaveConfig: (payload: string) => putJSON("/api/v1/config/current", { payload }),
        ExportConfig: async () => {
          const payload = await getJSON<string>("/api/v1/config/export");
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
          const resp = await postJSON<{ result: string; warnings: string[] }>(
            "/api/v1/config/import",
            { fileName: fileData.name, fileContent: fileData.content },
          );
          return { message: resp.result, warnings: resp.warnings ?? [] };
        },
        GetWebAuthStatus: () => getJSON("/api/v1/auth/web-status"),
        CreateWebAuth: async (username: string, password: string) => {
          const response = await postJSON<AuthStatus>(
            "/api/v1/auth/bootstrap",
            { username, password, retainLogin: true },
            false,
          );
          if (response.bearerToken) {
            bearerToken = response.bearerToken;
            persistToken(response.bearerToken, true);
            recreateEventStream();
          }
          return getJSON("/api/v1/auth/web-status");
        },
        CreateAPIToken: (name: string) => postJSON("/api/v1/tokens", { name }),
        RevokeAPIToken: (id: string) => deleteJSON(`/api/v1/tokens/${encodeURIComponent(id)}`),
        GetLogPath: () => getJSON<string>("/api/v1/logs/path"),
        GetRecentLogs: (limit: number) => postJSON("/api/v1/logs/recent", { limit }),
        StartLogStream: () => postJSON<string>("/api/v1/logs/streams"),
        StopLogStream: (streamID: string) =>
          deleteJSON(`/api/v1/logs/streams/${encodeURIComponent(streamID)}`),
        GetLogExclusions: () => getJSON<string[]>("/api/v1/logs/exclusions"),
        UpdateLogExclusions: (patterns: string[]) =>
          putJSON("/api/v1/logs/exclusions", { patterns }),
        ListKnownTrackers: () => getJSON<string[]>("/api/v1/trackers/known"),
        ListHistory: () => getJSON("/api/v1/history"),
        GetHistoryOverview: (sourcePath: string) =>
          postJSON("/api/v1/history/overview", { sourcePath }),
        DeleteHistoryRelease: (sourcePath: string) =>
          postJSON("/api/v1/history/delete", { sourcePath }),
        StartTrackerUpload: async (
          path: string,
          overrides: unknown,
          nameOverrides: unknown,
          trackers: string[],
          ignoreRuleFailures: boolean,
          ignoreDupesFor: string[],
          questionnaireAnswers: Record<string, Record<string, string>>,
          descriptionGroups: unknown,
          debug: boolean,
          runLogLevel: string,
        ) => {
          const result = await postJSON<{ jobID: string }>("/api/v1/jobs/uploads", {
            path,
            overrides,
            nameOverrides,
            trackers,
            ignoreRuleFailures,
            ignoreDupesFor,
            questionnaireAnswers,
            descriptionGroups,
            debug,
            runLogLevel,
          });
          return result.jobID;
        },
        CancelTrackerUpload: (jobID: string) =>
          deleteJSON(`/api/v1/jobs/uploads/${encodeURIComponent(jobID)}`),
        RetryFailedTrackerUpload: async (jobID: string) => {
          const result = await postJSON<{ jobID: string }>(
            `/api/v1/jobs/uploads/${encodeURIComponent(jobID)}/retry`,
          );
          return result.jobID;
        },
        GetTrackerUploadSnapshot: (jobID: string) =>
          getJSON(`/api/v1/jobs/uploads/${encodeURIComponent(jobID)}`),
        OpenExternalURL: async (url: string) => {
          window.open(url, "_blank", "noopener,noreferrer");
        },
      },
    },
  };
};

export const initializeDesktopBridge = async () => {
  browserMode = false;
  const desktopRuntime = (globalThis as any).go?.guiapp?.DesktopRuntime;
  const config = (await desktopRuntime?.GetRuntimeConfig?.()) as RuntimeConfig | undefined;
  setRuntimeConfig(config || {});
};

export const initializeBrowserBridge = (token: string, browseEnabled = false) => {
  browserMode = isWebUIRuntime();
  setRuntimeConfig({
    apiBaseURL: "",
    bearerToken: token || storedToken(),
    nativeBrowseEnabled: browseEnabled,
  });
};

export const isBrowserMode = () => {
  browserMode = isWebUIRuntime();
  return browserMode;
};

export const isBrowserNativeBrowseAvailable = () => {
  if (!isBrowserMode()) return true;
  return nativeBrowseEnabled;
};

export const updateBrowserCSRFToken = (token: string) => {
  bearerToken = token || bearerToken || storedToken();
  recreateEventStream();
};

export const browserAuth = {
  status: async () => {
    const token = storedToken();
    if (token) bearerToken = token;
    const payload = await getJSON<AuthStatus>("/api/v1/auth/status", Boolean(token));
    initializeBrowserBridge(token, !!payload.nativeBrowseEnabled);
    return { ...payload, authenticated: Boolean(token && payload.authenticated) };
  },
  bootstrap: async (username: string, password: string, retainLogin: boolean) => {
    const payload = await postJSON<AuthStatus>(
      "/api/v1/auth/bootstrap",
      { username, password, retainLogin },
      false,
    );
    if (payload.bearerToken) {
      bearerToken = payload.bearerToken;
      persistToken(payload.bearerToken, retainLogin);
    }
    return payload;
  },
  login: async (username: string, password: string, retainLogin: boolean) => {
    const payload = await postJSON<AuthStatus>(
      "/api/v1/auth/token",
      { username, password, retainLogin },
      false,
    );
    if (payload.bearerToken) {
      bearerToken = payload.bearerToken;
      persistToken(payload.bearerToken, retainLogin);
    }
    return payload;
  },
  saveBrowsePolicy: (browseRoot: string, allowUnrestrictedBrowse: boolean) =>
    putJSON("/api/v1/auth/browse-policy", { browseRoot, allowUnrestrictedBrowse }),
  logout: async () => {
    try {
      if (bearerToken) await postJSON("/api/v1/auth/logout");
    } finally {
      bearerToken = "";
      persistToken("", false);
      recreateEventStream();
    }
  },
};

export const EventsOn = (eventName: string, callback: EventCallback) =>
  addRESTListener(eventName, callback);
