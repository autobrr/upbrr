// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type {
  ApplicationInfo,
  BrowseDirectoryResponse,
  DescriptionBuilderPreview,
  DVDMenuCaptureResult,
  DupeCheckSnapshot,
  ExternalIDOverrides,
  HistoryEntry,
  HistoryOverview,
  ImageHostPolicyMetadata,
  MetadataPreview,
  OwnerJobSnapshot,
  PlaylistInfo,
  PlaylistInstruction,
  PreparationPreview,
  PrepareInput,
  PrepareResult,
  ReleaseNameOverrides,
  ReleaseRef,
  ScreenshotImage,
  ScreenshotPlan,
  ScreenshotPurpose,
  ScreenshotResult,
  ScreenshotSelection,
  TrackerAuthCapability,
  TrackerAuthLoginRequest,
  TrackerAuthStatus,
  TrackerDryRunPreview,
  TrackerUploadSnapshot,
  UploadedImageLink,
  UploadImagesResult,
  UploadReviewResult,
} from "../types";
import { requestApp } from "./client";

type LogEntry = {
  ID: number;
  Time: string;
  Level: string;
  Message: string;
};

type ConfigImportResult = { message: string; warnings: string[] };

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

/** Canonical release preparation and metadata preview operations. */
export const preparationClient = {
  detectDiscType: (path: string, signal?: AbortSignal) =>
    requestApp<string>("DetectDiscType", { Path: path }, { signal }),
  fetchMetadata: (
    correlationID: string,
    path: string,
    sourceLookupURL: string,
    overrides: ExternalIDOverrides,
    nameOverrides: ReleaseNameOverrides,
    playlist: PlaylistInstruction,
    confirmBDMVRescan: boolean,
    signal?: AbortSignal,
  ) =>
    requestApp<MetadataPreview>(
      "FetchMetadata",
      {
        CorrelationID: correlationID,
        Path: path,
        SourceLookupURL: sourceLookupURL,
        Overrides: overrides,
        NameOverrides: nameOverrides,
        Playlist: playlist,
        ConfirmBDMVRescan: confirmBDMVRescan,
      },
      { signal },
    ),
  prepareRelease: (input: PrepareInput) =>
    requestApp<PrepareResult>("PrepareRelease", { Input: input }),
  resetMetadata: (
    correlationID: string,
    path: string,
    sourceLookupURL: string,
    overrides: ExternalIDOverrides,
    nameOverrides: ReleaseNameOverrides,
    playlist: PlaylistInstruction,
    confirmBDMVRescan: boolean,
    signal?: AbortSignal,
  ) =>
    requestApp<MetadataPreview>(
      "ResetMetadata",
      {
        CorrelationID: correlationID,
        Path: path,
        SourceLookupURL: sourceLookupURL,
        Overrides: overrides,
        NameOverrides: nameOverrides,
        Playlist: playlist,
        ConfirmBDMVRescan: confirmBDMVRescan,
      },
      { signal },
    ),
  selectBlurayCandidate: (
    correlationID: string,
    path: string,
    releaseID: string,
    signal?: AbortSignal,
  ) =>
    requestApp<MetadataPreview>(
      "SelectBlurayCandidate",
      { CorrelationID: correlationID, Path: path, ReleaseID: releaseID },
      { signal },
    ),
  fetchDescriptionBuilder: (release: ReleaseRef, trackers: string[], signal?: AbortSignal) =>
    requestApp<DescriptionBuilderPreview>(
      "FetchDescriptionBuilder",
      {
        Release: release,
        Trackers: trackers,
      },
      { signal },
    ),
  fetchPreparation: (
    path: string,
    overrides: ExternalIDOverrides,
    nameOverrides: ReleaseNameOverrides,
    trackers: string[],
    ignoreDupesFor: string[],
  ) =>
    requestApp<PreparationPreview>("FetchPreparation", {
      Path: path,
      Overrides: overrides,
      NameOverrides: nameOverrides,
      Trackers: trackers,
      IgnoreDupesFor: ignoreDupesFor,
    }),
  fetchTrackerDryRun: (
    release: ReleaseRef,
    trackers: string[],
    ignoreDupesFor: string[],
    questionnaireAnswers: Record<string, Record<string, string>>,
    descriptionGroups: DescriptionBuilderPreview["Groups"],
    debug: boolean,
    noSeed: boolean,
    runLogLevel: string,
    signal?: AbortSignal,
  ) =>
    requestApp<TrackerDryRunPreview>(
      "FetchTrackerDryRun",
      {
        Release: release,
        Trackers: trackers,
        IgnoreDupesFor: ignoreDupesFor,
        QuestionnaireAnswers: questionnaireAnswers,
        DescriptionGroups: descriptionGroups,
        Debug: debug,
        NoSeed: noSeed,
        RunLogLevel: runLogLevel,
      },
      { signal },
    ),
};

/** BDMV playlist discovery for one selected preparation source. */
export const playlistClient = {
  discover: (path: string, signal?: AbortSignal) =>
    requestApp<PlaylistInfo[]>("DiscoverPlaylists", { Path: path }, { signal }),
};

/** Session-owned duplicate-check jobs. */
export const dupeClient = {
  start: (release: ReleaseRef, trackers: string[], correlationID: string) =>
    requestApp<string>("StartDupeCheck", {
      Release: release,
      Trackers: trackers,
      CorrelationID: correlationID,
    }),
  cancel: (jobID: string) => requestApp<void>("CancelDupeCheck", { JobID: jobID }),
  getSnapshot: (jobID: string) =>
    requestApp<DupeCheckSnapshot>("GetDupeCheckSnapshot", { JobID: jobID }),
};

/** Screenshot planning, rendering, selection, and removal operations. */
export const screenshotClient = {
  fetchPlan: (release: ReleaseRef, signal?: AbortSignal) =>
    requestApp<ScreenshotPlan>("FetchScreenshotPlan", { Release: release }, { signal }),
  generate: (
    release: ReleaseRef,
    selections: ScreenshotSelection[],
    purpose: ScreenshotPurpose,
    signal?: AbortSignal,
  ) =>
    requestApp<ScreenshotResult>(
      "GenerateScreenshots",
      {
        Release: release,
        Selections: selections,
        Purpose: purpose,
      },
      { signal },
    ),
  previewFrame: (release: ReleaseRef, timestampSeconds: number, signal?: AbortSignal) =>
    requestApp<string>(
      "PreviewScreenshotFrame",
      {
        Release: release,
        TimestampSeconds: timestampSeconds,
      },
      { signal },
    ),
  remove: (release: ReleaseRef, imagePath: string, signal?: AbortSignal) =>
    requestApp<void>(
      "DeleteScreenshot",
      {
        Release: release,
        ImagePath: imagePath,
      },
      { signal },
    ),
  saveFinalSelections: (release: ReleaseRef, images: ScreenshotImage[], signal?: AbortSignal) =>
    requestApp<void>(
      "SaveFinalScreenshotSelections",
      {
        Release: release,
        Images: images,
      },
      { signal },
    ),
  readImage: (path: string, signal?: AbortSignal) =>
    requestApp<string>("ReadScreenshotImage", { Path: path }, { signal }),
  deleteTrackerImageURL: (release: ReleaseRef, url: string, signal?: AbortSignal) =>
    requestApp<void>(
      "DeleteTrackerImageURL",
      {
        Release: release,
        URL: url,
      },
      { signal },
    ),
};

/** DVD menu capture and persisted menu-image operations. */
export const menuImageClient = {
  importPaths: (release: ReleaseRef, paths: string[], signal?: AbortSignal) =>
    requestApp<void>(
      "ImportMenuImages",
      {
        Release: release,
        Paths: paths,
      },
      { signal },
    ),
  capture: (release: ReleaseRef, signal?: AbortSignal) =>
    requestApp<DVDMenuCaptureResult>("CaptureDVDMenus", { Release: release }, { signal }),
  list: (release: ReleaseRef, signal?: AbortSignal) =>
    requestApp<ScreenshotImage[]>("ListDVDMenuScreenshots", { Release: release }, { signal }),
  remove: (release: ReleaseRef, imagePath: string, signal?: AbortSignal) =>
    requestApp<void>(
      "DeleteDVDMenuScreenshot",
      {
        Release: release,
        ImagePath: imagePath,
      },
      { signal },
    ),
};

/** Image-host upload state and operations. */
export const uploadedImageClient = {
  listCandidates: (release: ReleaseRef, signal?: AbortSignal) =>
    requestApp<ScreenshotImage[]>("ListUploadCandidates", { Release: release }, { signal }),
  listUploaded: (release: ReleaseRef, signal?: AbortSignal) =>
    requestApp<UploadedImageLink[]>("ListUploadedImages", { Release: release }, { signal }),
  upload: (
    correlationID: string,
    release: ReleaseRef,
    trackers: string[],
    host: string,
    images: ScreenshotImage[],
    signal?: AbortSignal,
  ) =>
    requestApp<UploadImagesResult>(
      "UploadImages",
      {
        CorrelationID: correlationID,
        Release: release,
        Trackers: trackers,
        Host: host,
        Images: images,
      },
      { signal },
    ),
  remove: (release: ReleaseRef, imagePath: string, host: string, signal?: AbortSignal) =>
    requestApp<void>(
      "DeleteUploadedImage",
      { Release: release, ImagePath: imagePath, Host: host },
      { signal },
    ),
};

/** Description rendering and override persistence. */
export const descriptionClient = {
  render: (raw: string, signal?: AbortSignal) =>
    requestApp<string>("RenderDescription", { Raw: raw }, { signal }),
  saveOverride: (
    release: ReleaseRef,
    groupKey: string,
    raw: string,
    trackers: string[],
    signal?: AbortSignal,
  ) =>
    requestApp<DescriptionBuilderPreview["Groups"][number]>(
      "SaveDescriptionOverride",
      {
        Release: release,
        GroupKey: groupKey,
        Raw: raw,
        Trackers: trackers,
      },
      { signal },
    ),
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
  listKnown: () => requestApp<string[]>("ListKnownTrackers"),
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

/** Reviewed tracker-upload handoff and session-owned job lifecycle. */
export const uploadClient = {
  review: (
    release: ReleaseRef,
    trackers: string[],
    ignoreDupesFor: string[],
    questionnaireAnswers: Record<string, Record<string, string>>,
    descriptionGroups: DescriptionBuilderPreview["Groups"],
    debug: boolean,
    noSeed: boolean,
    runLogLevel: string,
    signal?: AbortSignal,
  ) =>
    requestApp<UploadReviewResult>(
      "ReviewTrackerUpload",
      {
        Release: release,
        Trackers: trackers,
        IgnoreDupesFor: ignoreDupesFor,
        QuestionnaireAnswers: questionnaireAnswers,
        DescriptionGroups: descriptionGroups,
        Debug: debug,
        NoSeed: noSeed,
        RunLogLevel: runLogLevel,
      },
      { signal },
    ),
  startReviewed: (token: string, correlationID: string) =>
    requestApp<string>("StartReviewedTrackerUpload", {
      Token: token,
      CorrelationID: correlationID,
    }),
  cancel: (jobID: string) => requestApp<void>("CancelTrackerUpload", { JobID: jobID }),
  retryFailed: (jobID: string, correlationID: string) =>
    requestApp<string>("RetryFailedTrackerUpload", { JobID: jobID, CorrelationID: correlationID }),
  getSnapshot: (jobID: string) =>
    requestApp<TrackerUploadSnapshot>("GetTrackerUploadSnapshot", { JobID: jobID }),
};

/** Authenticated-owner retained Job listing. */
export const jobsClient = {
  list: () => requestApp<OwnerJobSnapshot[]>("ListJobs"),
};
