// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { TrackerLaneOutcome, UploadDryRunResult } from "../api/generated/release-workflow";

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
