// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { TrackerUploadItem } from "../types";

type ResolveSelectedUploadTrackersInput = {
  trackerUploadItems: TrackerUploadItem[];
  releasePageTrackerSelection: Record<string, boolean>;
  uploadToggles: Record<string, boolean>;
  dupedTrackerSet: Set<string>;
  skippedDupeTrackerSet: Set<string>;
  ruleSkippedTrackerSet: Set<string>;
  failedDupeTrackerSet: Set<string>;
};

const hasOwn = (value: Record<string, boolean>, key: string) =>
  Object.prototype.hasOwnProperty.call(value, key);

/**
 * resolveSelectedUploadTrackers returns the trackers that may be sent to
 * metadata, upload, dry-run, and image-upload requests.
 *
 * A tracker must still be selected on the input page, enabled on the upload page,
 * configured in the current tracker list, and not blocked by duplicate hits,
 * skipped dupe checks, rule failures, or failed dupe checks. Blocked-tracker
 * sets use lower-case trimmed tracker names; returned names keep the upload
 * toggle keys used by the caller. Callers that pass the result to backend APIs
 * where an empty tracker list means defaults must guard filtered-empty selections
 * separately.
 */
export const resolveSelectedUploadTrackers = ({
  trackerUploadItems,
  releasePageTrackerSelection,
  uploadToggles,
  dupedTrackerSet,
  skippedDupeTrackerSet,
  ruleSkippedTrackerSet,
  failedDupeTrackerSet,
}: ResolveSelectedUploadTrackersInput) => {
  const validTrackers = new Set(trackerUploadItems.map((item) => item.name));
  return Object.entries(uploadToggles)
    .filter(([name, enabled]) => {
      if (!enabled) return false;
      if (!validTrackers.has(name)) return false;
      if (!releasePageTrackerSelection[name]) return false;
      const normalized = name.toLowerCase().trim();
      if (!normalized) return false;
      if (dupedTrackerSet.has(normalized)) return false;
      if (skippedDupeTrackerSet.has(normalized)) return false;
      if (ruleSkippedTrackerSet.has(normalized)) return false;
      if (failedDupeTrackerSet.has(normalized)) return false;
      return true;
    })
    .map(([name]) => name);
};

/**
 * hasFilteredEmptyUploadTrackerSelection returns true when the release page has
 * at least one explicit selected tracker, but upload eligibility filters remove
 * every selected tracker before the backend call.
 *
 * Missing upload toggle keys are treated as startup/uninitialized state, while
 * disabled toggles and duplicate, skipped, rule-failure, or failed dupe-check
 * blocks count as upload filters.
 */
export const hasFilteredEmptyUploadTrackerSelection = ({
  trackerUploadItems,
  releasePageTrackerSelection,
  uploadToggles,
  dupedTrackerSet,
  skippedDupeTrackerSet,
  ruleSkippedTrackerSet,
  failedDupeTrackerSet,
}: ResolveSelectedUploadTrackersInput) => {
  const selectedReleaseTrackers = trackerUploadItems.filter(
    (item) => releasePageTrackerSelection[item.name] === true,
  );
  if (selectedReleaseTrackers.length === 0) {
    return false;
  }
  return selectedReleaseTrackers.every((item) => {
    const normalized = item.name.toLowerCase().trim();
    if (!normalized) return true;
    if (!hasOwn(uploadToggles, item.name)) return false;
    if (!uploadToggles[item.name]) return true;
    if (dupedTrackerSet.has(normalized)) return true;
    if (skippedDupeTrackerSet.has(normalized)) return true;
    if (ruleSkippedTrackerSet.has(normalized)) return true;
    if (failedDupeTrackerSet.has(normalized)) return true;
    return false;
  });
};
