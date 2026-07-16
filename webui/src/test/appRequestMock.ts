// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

type Operation = (...args: any[]) => unknown;
export type AppOperationMocks = Record<string, Operation | undefined>;

let currentOperations: AppOperationMocks | undefined;

export const installAppOperationMocks = (operations: AppOperationMocks) => {
  currentOperations = operations;
  return operations;
};

export const getAppOperationMocks = () => currentOperations;

export const clearAppOperationMocks = () => {
  currentOperations = undefined;
};

/** Adapts typed request bodies to positional operation mocks used by component tests. */
export const invokeAppRequestMock = async (
  operations: AppOperationMocks,
  method: string,
  body?: unknown,
  options: { signal?: AbortSignal } = {},
) => {
  const operation = operations[method];
  if (!operation) throw new Error(`unexpected app request: ${method}`);

  const payload = (body || {}) as Record<string, any>;
  const argsByMethod: Record<string, unknown[]> = {
    BrowseDirectory: [payload.path, payload.mode],
    DetectDiscType: [payload.Path],
    FetchMetadata: [
      payload.Path,
      payload.SourceLookupURL,
      payload.Overrides,
      payload.NameOverrides,
      payload.Trackers,
      payload.Playlist,
      payload.ConfirmBDMVRescan,
    ],
    PrepareRelease: [payload.Input],
    ResetMetadata: [
      payload.Path,
      payload.SourceLookupURL,
      payload.Overrides,
      payload.NameOverrides,
      payload.Trackers,
      payload.Playlist,
      payload.ConfirmBDMVRescan,
    ],
    SelectBlurayCandidate: [payload.Path, payload.ReleaseID],
    FetchDescriptionBuilder: [payload.Release, payload.Trackers, payload.IgnoreDupesFor],
    FetchPreparation: [
      payload.Path,
      payload.Overrides,
      payload.NameOverrides,
      payload.Trackers,
      payload.IgnoreDupesFor,
    ],
    FetchTrackerDryRun: [
      payload.Release,
      payload.Trackers,
      payload.IgnoreDupesFor,
      payload.QuestionnaireAnswers,
      payload.DescriptionGroups,
      payload.Debug,
      payload.NoSeed,
      payload.RunLogLevel,
    ],
    StartDupeCheck: [payload.Release, payload.Trackers, payload.CorrelationID],
    CancelDupeCheck: [payload.JobID],
    GetDupeCheckSnapshot: [payload.JobID],
    FetchScreenshotPlan: [payload.Release],
    GenerateScreenshots: [payload.Release, payload.Selections, payload.Purpose],
    PreviewScreenshotFrame: [payload.Release, payload.TimestampSeconds],
    DeleteScreenshot: [payload.Release, payload.ImagePath],
    SaveFinalScreenshotSelections: [payload.Release, payload.Images],
    ImportMenuImages: [payload.Release, payload.Paths],
    CaptureDVDMenus: [payload.Release, options.signal],
    ListDVDMenuScreenshots: [payload.Release],
    DeleteDVDMenuScreenshot: [payload.Release, payload.ImagePath],
    ReadScreenshotImage: [payload.Path],
    ListUploadCandidates: [payload.Release],
    ListUploadedImages: [payload.Release],
    UploadImages: [payload.Release, payload.Trackers, payload.Host, payload.Images],
    DeleteUploadedImage: [payload.Release, payload.ImagePath, payload.Host],
    DeleteTrackerImageURL: [payload.Release, payload.URL],
    RenderDescription: [payload.Raw],
    SaveDescriptionOverride: [payload.Release, payload.GroupKey, payload.Raw, payload.Trackers],
    DiscoverPlaylists: [payload.Path],
    SaveConfig: [payload.Payload],
    GetRecentLogs: [payload.Limit],
    StopLogStream: [payload.StreamID],
    UpdateLogExclusions: [payload.Patterns],
    GetTrackerAuthStatus: [payload.Tracker],
    ImportTrackerAuthCookieContent: [payload.Tracker, payload.FileName, payload.Content],
    TestTrackerAuth: [payload.Tracker],
    LoginTrackerAuth: [payload.Tracker, payload.Login],
    SubmitTrackerAuth2FA: [payload.ChallengeID, payload.Code],
    DeleteTrackerAuth: [payload.Tracker],
    GetHistoryOverview: [payload.SourcePath],
    DeleteHistoryRelease: [payload.SourcePath],
    ReviewTrackerUpload: [
      payload.Release,
      payload.Trackers,
      payload.IgnoreDupesFor,
      payload.QuestionnaireAnswers,
      payload.DescriptionGroups,
      payload.Debug,
      payload.NoSeed,
      payload.RunLogLevel,
    ],
    StartReviewedTrackerUpload: [payload.Token, payload.CorrelationID],
    CancelTrackerUpload: [payload.JobID],
    RetryFailedTrackerUpload: [payload.JobID, payload.CorrelationID],
    ListJobs: [],
    GetTrackerUploadSnapshot: [payload.JobID],
    GetTrackerIcon: [payload.Domain, payload.URL],
  };
  return operation(...(argsByMethod[method] || []));
};
