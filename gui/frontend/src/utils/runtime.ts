// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { EventsOn as wailsEventsOn } from "../../wailsjs/runtime/runtime";

type EventCallback = (payload: unknown) => void;

const callbackMap = new Map<string, Set<EventCallback>>();
let eventSource: EventSource | null = null;
let browserMode = false;
let csrfToken = "";
let nativeBrowseEnabled = false;

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

const addBrowserListener = (eventName: string, callback: EventCallback) => {
  const isNew = !callbackMap.has(eventName);
  if (!callbackMap.has(eventName)) {
    callbackMap.set(eventName, new Set());
  }
  const set = callbackMap.get(eventName)!;
  set.add(callback);
  ensureEventSource();
  if (isNew && eventSource) {
    eventSource.addEventListener(eventName, (event) => {
      const payload = JSON.parse((event as MessageEvent).data);
      callbackMap.get(eventName)?.forEach((listener) => listener(payload));
    });
  }
  return () => {
    set.delete(callback);
  };
};

const ensureEventSource = () => {
  if (!browserMode || eventSource) {
    return;
  }
  eventSource = new EventSource("/api/events", { withCredentials: true });
  eventSource.onmessage = () => undefined;
  const attach = (eventName: string) => {
    eventSource?.addEventListener(eventName, (event) => {
      const payload = JSON.parse((event as MessageEvent).data);
      callbackMap.get(eventName)?.forEach((callback) => callback(payload));
    });
  };
  callbackMap.forEach((_value, key) => attach(key));
};

const recreateEventSource = () => {
  if (eventSource) {
    eventSource.close();
    eventSource = null;
  }
  ensureEventSource();
};

const postJSON = async <T>(path: string, body?: unknown): Promise<T> => {
  const response = await fetch(path, {
    method: "POST",
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
      ...(csrfToken ? { "X-CSRF-Token": csrfToken } : {}),
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

const getJSON = async <T>(path: string): Promise<T> => {
  const response = await fetch(path, {
    method: "GET",
    credentials: "include",
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

export const initializeBrowserBridge = (token: string, browseEnabled = false) => {
  browserMode = isWebUIRuntime();
  nativeBrowseEnabled = browseEnabled;
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
        BrowseFolder: () => call<string>("BrowseFolder"),
        BrowseDirectory: (path: string, mode: "file" | "folder") =>
          call("BrowseDirectory", { path, mode }),
        ListUIStates: () => getJSON("/api/app/UIState"),
        GetUIState: (id: string) => getJSON(`/api/app/UIState?id=${encodeURIComponent(id)}`),
        SaveUIState: (id: string, label: string, state: unknown) =>
          call("UIState", { id, label, state }),
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
          ignoreRuleFailures: boolean,
          ignoreDupesFor: string[],
          questionnaireAnswers: Record<string, Record<string, string>>,
          descriptionGroups: unknown,
          debug: boolean,
          runLogLevel: string,
        ) =>
          call("FetchTrackerDryRun", {
            Path: path,
            Overrides: overrides,
            NameOverrides: nameOverrides,
            Trackers: trackers,
            IgnoreRuleFailures: ignoreRuleFailures,
            IgnoreDupesFor: ignoreDupesFor,
            QuestionnaireAnswers: questionnaireAnswers,
            DescriptionGroups: descriptionGroups,
            Debug: debug,
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
          ignoreRuleFailures: boolean,
          ignoreDupesFor: string[],
          questionnaireAnswers: Record<string, Record<string, string>>,
          descriptionGroups: unknown,
          debug: boolean,
          runLogLevel: string,
        ) =>
          call("StartTrackerUpload", {
            Path: path,
            Overrides: overrides,
            NameOverrides: nameOverrides,
            Trackers: trackers,
            IgnoreRuleFailures: ignoreRuleFailures,
            IgnoreDupesFor: ignoreDupesFor,
            QuestionnaireAnswers: questionnaireAnswers,
            DescriptionGroups: descriptionGroups,
            Debug: debug,
            RunLogLevel: runLogLevel,
          }),
        CancelTrackerUpload: (jobID: string) => call("CancelTrackerUpload", { JobID: jobID }),
        RetryFailedTrackerUpload: (jobID: string) =>
          call("RetryFailedTrackerUpload", { JobID: jobID }),
        GetTrackerUploadSnapshot: (jobID: string) =>
          call("GetTrackerUploadSnapshot", { JobID: jobID }),
      },
    },
  };
  recreateEventSource();
};

export const isBrowserMode = () => {
  browserMode = isWebUIRuntime();
  return browserMode;
};

export const isBrowserNativeBrowseAvailable = () => {
  if (!isBrowserMode()) {
    return true;
  }
  return nativeBrowseEnabled;
};

export const updateBrowserCSRFToken = (token: string) => {
  csrfToken = token;
  recreateEventSource();
};

export const browserAuth = {
  status: async () => {
    const response = await fetch("/api/auth/status", { credentials: "include" });
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

export const EventsOn = (eventName: string, callback: EventCallback) => {
  if (!browserMode) {
    return wailsEventsOn(eventName, callback as any);
  }
  return addBrowserListener(eventName, callback);
};
