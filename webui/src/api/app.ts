// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type {
  ApplicationInfo,
  BrowseDirectoryResponse,
  HistoryEntry,
  HistoryOverview,
  ImageHostPolicyMetadata,
  TrackerAuthCapability,
  TrackerAuthLoginRequest,
  TrackerAuthStatus,
  TrackerCatalog,
} from "../types";
import type {
  AttachReleaseWorkflowMediaRequest,
  CancelReleaseWorkflowRequest,
  ContinueReleaseWorkflowRequest,
  DeleteReleaseWorkflowMediaRequest,
  FramePreview,
  InvalidateReleaseWorkflowTrackersRequest,
  MediaPlan,
  Operation,
  PreviewReleaseWorkflowFrameRequest,
  ReleaseWorkflowCurrent,
  RemoveReleaseWorkflowHostedImagesRequest,
  ReorderReleaseWorkflowMediaRequest,
  ResetReleaseWorkflowDescriptionOverrideRequest,
  RetryReleaseWorkflowClientInjectionRequest,
  RetryReleaseWorkflowImageHostRequest,
  RetryReleaseWorkflowUploadRequest,
  SaveReleaseWorkflowDescriptionOverrideRequest,
  SetReleaseWorkflowMediaSelectionRequest,
  UploadReleaseWorkflowImagesRequest,
  WorkflowResourceRef,
} from "./generated/release-workflow";
import { requestApp, requestAppForm, withBasePath } from "./client";

type LogEntry = {
  ID: number;
  Time: string;
  Level: string;
  Message: string;
};

type ConfigImportResult = { message: string; warnings: string[] };

export type APITokenScope = "workflow:read" | "workflow:write" | "workflow:execute";

export type APITokenRecord = Readonly<{
  id: string;
  name: string;
  ownerId: string;
  scopes: APITokenScope[];
  createdAt: string;
  revokedAt?: string;
}>;

export type CreatedAPIToken = Readonly<{
  record: APITokenRecord;
  token: string;
}>;

const maxCookieImportContentBytes = 1024 * 1024;
const encodedTextByteLength = (value: string) => new TextEncoder().encode(value).length;

const selectTextFile = (accept: string) =>
  new Promise<{ name: string; content: string }>((resolve, reject) => {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = accept;
    input.onchange = () => {
      const file = input.files?.[0];
      if (!file) {
        resolve({ name: "", content: "" });
        return;
      }
      const reader = new FileReader();
      reader.onload = () => resolve({ name: file.name, content: reader.result as string });
      reader.onerror = () => reject(reader.error);
      reader.readAsText(file);
    };
    input.addEventListener("cancel", () => resolve({ name: "", content: "" }));
    input.click();
  });

/** Host filesystem browsing exposed through the authenticated WebUI route. */
export const hostBrowser = {
  list: (path: string, mode: "file" | "folder") =>
    requestApp<BrowseDirectoryResponse>("BrowseDirectory", { path, mode }),
};

/** Application build and runtime capability information. */
export const applicationClient = {
  getInfo: () => requestApp<ApplicationInfo>("GetApplicationInfo"),
};

/** Persistent public API bearer-token management for authenticated WebUI operators. */
export const apiTokenClient = {
  list: () => requestApp<APITokenRecord[]>("ListAPITokens"),
  create: (name: string, ownerId: string, scopes: APITokenScope[]) =>
    requestApp<CreatedAPIToken>("CreateAPIToken", { name, ownerId, scopes }),
  revoke: (id: string) => requestApp<void>("RevokeAPIToken", { id }),
};

/** Authoritative owner-scoped release workflow commands and snapshots. */
export const releaseWorkflowClient = {
  continue: (request: ContinueReleaseWorkflowRequest, signal?: AbortSignal) =>
    requestApp<ReleaseWorkflowCurrent>("ContinueReleaseWorkflow", request, { signal }),
  current: (workflowID: string, signal?: AbortSignal) =>
    requestApp<ReleaseWorkflowCurrent>(
      "GetReleaseWorkflow",
      { workflowId: workflowID },
      { signal },
    ),
  setMediaSelection: (command: SetReleaseWorkflowMediaSelectionRequest, signal?: AbortSignal) =>
    requestApp<ReleaseWorkflowCurrent>("SetReleaseWorkflowMediaSelection", command, { signal }),
  deleteMedia: (command: DeleteReleaseWorkflowMediaRequest, signal?: AbortSignal) =>
    requestApp<ReleaseWorkflowCurrent>("DeleteReleaseWorkflowMedia", command, { signal }),
  reorderMedia: (command: ReorderReleaseWorkflowMediaRequest, signal?: AbortSignal) =>
    requestApp<ReleaseWorkflowCurrent>("ReorderReleaseWorkflowMedia", command, { signal }),
  mediaPlan: (workflowID: string, signal?: AbortSignal) =>
    requestApp<MediaPlan>("GetReleaseWorkflowMediaPlan", { workflowId: workflowID }, { signal }),
  previewFrame: (command: PreviewReleaseWorkflowFrameRequest, signal?: AbortSignal) =>
    requestApp<FramePreview>("PreviewReleaseWorkflowFrame", command, { signal }),
  stageMedia: (workflowID: string, expectedRevision: number, file: File, signal?: AbortSignal) => {
    const body = new FormData();
    body.set("workflowId", workflowID);
    body.set("expectedRevision", String(expectedRevision));
    body.set("file", file, file.name);
    return requestAppForm<WorkflowResourceRef>("StageReleaseWorkflowMedia", body, { signal });
  },
  attachMedia: (command: AttachReleaseWorkflowMediaRequest, signal?: AbortSignal) =>
    requestApp<ReleaseWorkflowCurrent>("AttachReleaseWorkflowMedia", command, { signal }),
  uploadImages: (command: UploadReleaseWorkflowImagesRequest, signal?: AbortSignal) =>
    requestApp<ReleaseWorkflowCurrent>("UploadReleaseWorkflowImages", command, { signal }),
  retryImageHost: (command: RetryReleaseWorkflowImageHostRequest, signal?: AbortSignal) =>
    requestApp<ReleaseWorkflowCurrent>("RetryReleaseWorkflowImageHost", command, { signal }),
  removeHostedImages: (command: RemoveReleaseWorkflowHostedImagesRequest, signal?: AbortSignal) =>
    requestApp<ReleaseWorkflowCurrent>("RemoveReleaseWorkflowHostedImages", command, { signal }),
  mediaURL: (workflowID: string, mediaID: string, mediaRevision: number, artifactID: string) => {
    const query = new URLSearchParams({
      workflowId: workflowID,
      mediaId: mediaID,
      mediaRevision: String(mediaRevision),
      artifactId: artifactID,
    });
    return withBasePath(`/api/app/release-workflow-media?${query.toString()}`);
  },
  saveDescriptionOverride: (
    command: SaveReleaseWorkflowDescriptionOverrideRequest,
    signal?: AbortSignal,
  ) =>
    requestApp<ReleaseWorkflowCurrent>("SaveReleaseWorkflowDescriptionOverride", command, {
      signal,
    }),
  resetDescriptionOverride: (
    command: ResetReleaseWorkflowDescriptionOverrideRequest,
    signal?: AbortSignal,
  ) =>
    requestApp<ReleaseWorkflowCurrent>("ResetReleaseWorkflowDescriptionOverride", command, {
      signal,
    }),
  retryFailedUploads: (command: RetryReleaseWorkflowUploadRequest, signal?: AbortSignal) =>
    requestApp<ReleaseWorkflowCurrent>("RetryReleaseWorkflowUpload", command, { signal }),
  retryClientInjections: (
    command: RetryReleaseWorkflowClientInjectionRequest,
    signal?: AbortSignal,
  ) =>
    requestApp<ReleaseWorkflowCurrent>("RetryReleaseWorkflowClientInjection", command, { signal }),
  cancel: (command: CancelReleaseWorkflowRequest, signal?: AbortSignal) =>
    requestApp<ReleaseWorkflowCurrent>("CancelReleaseWorkflow", command, { signal }),
  invalidateTrackers: (command: InvalidateReleaseWorkflowTrackersRequest, signal?: AbortSignal) =>
    requestApp<ReleaseWorkflowCurrent>("InvalidateReleaseWorkflowTrackers", command, { signal }),
  operation: (workflowID: string, operationID: string, signal?: AbortSignal) =>
    requestApp<Operation>(
      "GetReleaseWorkflowOperation",
      { workflowId: workflowID, operationId: operationID },
      { signal },
    ),
  cancelOperation: (workflowID: string, operationID: string, signal?: AbortSignal) =>
    requestApp<Operation>(
      "CancelReleaseWorkflowOperation",
      { workflowId: workflowID, operationId: operationID },
      { signal },
    ),
};

/** Stateless generic BBCode rendering. */
export const descriptionClient = {
  render: (raw: string, signal?: AbortSignal) =>
    requestApp<string>("RenderDescription", { Raw: raw }, { signal }),
};

/** Config persistence plus browser-native import and download behavior. */
export const configClient = {
  get: () => requestApp<string>("GetConfig"),
  getDefault: () => requestApp<string>("GetDefaultConfig"),
  save: (payload: string) => requestApp<void>("SaveConfig", { Payload: payload }),
  exportDownload: async () => {
    const payload = await requestApp<string>("ExportConfig");
    const blob = new Blob([payload], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = "upbrr-config.json";
    anchor.click();
    URL.revokeObjectURL(url);
    return anchor.download;
  },
  importFile: async (): Promise<ConfigImportResult> => {
    const fileData = await selectTextFile(".py,.yaml,.yml,.json");
    if (!fileData.name) return { message: "", warnings: [] };
    const response = await requestApp<{ result: string; warnings: string[] }>("ImportConfig", {
      FileName: fileData.name,
      FileContent: fileData.content,
    });
    return { message: response.result, warnings: response.warnings ?? [] };
  },
};

/** Sanitized recent-log and live-log stream operations. */
export const loggingClient = {
  getPath: () => requestApp<string>("GetLogPath"),
  getRecent: (limit: number) => requestApp<LogEntry[]>("GetRecentLogs", { Limit: limit }),
  startStream: () => requestApp<string>("StartLogStream"),
  stopStream: (streamID: string) => requestApp<void>("StopLogStream", { StreamID: streamID }),
  getExclusions: () => requestApp<string[]>("GetLogExclusions"),
  updateExclusions: (patterns: string[]) =>
    requestApp<void>("UpdateLogExclusions", { Patterns: patterns }),
};

/** Tracker catalog and image-host policy metadata. */
export const trackerCatalogClient = {
  list: () => requestApp<TrackerCatalog>("ListTrackerCatalog"),
  getImageHostPolicyMetadata: () =>
    requestApp<ImageHostPolicyMetadata>("GetImageHostPolicyMetadata"),
  getIcon: (domain: string, url: string) =>
    requestApp<string>("GetTrackerIcon", { Domain: domain, URL: url }),
};

/** Tracker authentication status, browser file import, login, 2FA, and removal. */
export const trackerAuthClient = {
  listCapabilities: () => requestApp<TrackerAuthCapability[]>("ListTrackerAuthCapabilities"),
  getStatus: (tracker: string) =>
    requestApp<TrackerAuthStatus>("GetTrackerAuthStatus", { Tracker: tracker }),
  importCookies: async (tracker: string) => {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = ".txt,.json";
    const fileData = await new Promise<{ name: string; content: string }>((resolve, reject) => {
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
    });
    if (!fileData.name) return trackerAuthClient.getStatus(tracker);
    return trackerAuthClient.importCookieContent(tracker, fileData.name, fileData.content);
  },
  importCookieContent: (tracker: string, fileName: string, content: string) =>
    requestApp<TrackerAuthStatus>("ImportTrackerAuthCookieContent", {
      Tracker: tracker,
      FileName: fileName,
      Content: content,
    }),
  test: (tracker: string) => requestApp<TrackerAuthStatus>("TestTrackerAuth", { Tracker: tracker }),
  login: (tracker: string, login: TrackerAuthLoginRequest) =>
    requestApp<TrackerAuthStatus>("LoginTrackerAuth", { Tracker: tracker, Login: login }),
  submit2FA: (challengeID: string, code: string) =>
    requestApp<TrackerAuthStatus>("SubmitTrackerAuth2FA", { ChallengeID: challengeID, Code: code }),
  remove: (tracker: string) =>
    requestApp<TrackerAuthStatus>("DeleteTrackerAuth", { Tracker: tracker }),
};

/** Persisted release history operations. */
export const historyClient = {
  list: () => requestApp<HistoryEntry[]>("ListHistory"),
  getOverview: (sourcePath: string) =>
    requestApp<HistoryOverview>("GetHistoryOverview", { SourcePath: sourcePath }),
  removeRelease: (sourcePath: string) =>
    requestApp<void>("DeleteHistoryRelease", { SourcePath: sourcePath }),
};
