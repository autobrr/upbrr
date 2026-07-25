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
  _options: { signal?: AbortSignal } = {},
) => {
  const operation = operations[method];
  if (!operation) throw new Error(`unexpected app request: ${method}`);

  const payload = (body || {}) as Record<string, any>;
  const argsByMethod: Record<string, unknown[]> = {
    BrowseDirectory: [payload.path, payload.mode],
    RenderDescription: [payload.Raw],
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
    CreateAPIToken: [payload.name, payload.ownerId, payload.scopes],
    RevokeAPIToken: [payload.id],
    GetHistoryOverview: [payload.SourcePath],
    DeleteHistoryRelease: [payload.SourcePath],
    GetTrackerIcon: [payload.Domain, payload.URL],
  };
  return operation(...(argsByMethod[method] || []));
};
