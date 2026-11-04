// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type {
  TrackerLaneOutcome,
  UploadDryRunResult,
  UploadTrackerResult,
} from "../api/generated/release-workflow";

function hasFailure(result: UploadTrackerResult, code: string): boolean {
  return (
    result.clientFailureCode === code ||
    !!result.failures?.some((item) => item.failure.Code === code)
  );
}

function submissionStatus(result: UploadTrackerResult): string {
  if (result.submissionStatus) return result.submissionStatus;
  return result.status === "failed" &&
    result.failures?.some((item) => item.failure.Operation === "client_injection")
    ? "completed"
    : result.status;
}

/** Matches the backend's retained-result retry guard, including unresolved local receipts. */
export function canRetrySubmission(result: UploadTrackerResult): boolean {
  return (
    submissionStatus(result) === "failed" &&
    !hasFailure(result, "unknown_submission_outcome") &&
    !hasFailure(result, "receipt_retention")
  );
}

/** A client-only retry needs a completed submission and an ordinary client failure. */
export function canRetryClientInjection(result: UploadTrackerResult): boolean {
  const clientFailed = result.clientInjectionStatus
    ? result.clientInjectionStatus === "failed"
    : !result.clientInjected &&
      !result.crossSeeded &&
      !!result.failures?.some((item) => item.failure.Operation === "client_injection");
  return (
    submissionStatus(result) === "completed" &&
    clientFailed &&
    result.clientFailureCode === "client_injection_failed" &&
    !hasFailure(result, "receipt_retention")
  );
}

export function canExecuteUpload(
  outcomes: readonly TrackerLaneOutcome[],
  selectedTrackerIds: readonly string[],
  excludedTrackerIds: ReadonlySet<string>,
  dryRun: UploadDryRunResult | null,
): boolean {
  const selected = new Set(selectedTrackerIds);
  const applicable = outcomes.filter(
    (outcome) =>
      (selected.size === 0 || selected.has(outcome.trackerId)) &&
      !excludedTrackerIds.has(outcome.trackerId),
  );
  return (
    applicable.some((outcome) => outcome.uploadEligibility === "eligible") ||
    (applicable.length > 0 &&
      applicable.every((outcome) => outcome.uploadEligibility === "skipped") &&
      dryRun?.status === "skipped" &&
      dryRun.reports.every((report) => report.status === "skipped"))
  );
}
