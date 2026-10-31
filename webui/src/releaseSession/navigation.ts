// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { PreparedRelease, WorkflowContinuation } from "../api/generated/release-workflow";
import { missingRequiredFacts } from "./requiredFacts";
import type { ReleaseRoute, RouteAccess } from "./types";

export type TrackerWorkflowRequirements = Readonly<{
  needsImages: boolean;
  needsDescriptions: boolean;
}>;

/** Projects backend goals and saved required facts into release navigation availability. */
export const routeAccess = (
  continuation: WorkflowContinuation | null | undefined,
  hasTrackerData: boolean,
  requirements: TrackerWorkflowRequirements,
  hasAudioData = false,
  hasHDRData = false,
  release: PreparedRelease | null = null,
): Readonly<Record<ReleaseRoute, RouteAccess>> => {
  const goal = (name: string): RouteAccess => {
    const availability = continuation?.availableGoals.find((candidate) => candidate.goal === name);
    return {
      available: availability?.available === true,
      reasonCode: availability?.reasonCode,
      reason:
        availability?.available === true
          ? ""
          : availability?.reason || "Workflow state is not ready yet.",
    };
  };
  const trackerAssessment = goal("trackers_assessed");
  const media = goal("media_ready");
  const descriptions = goal("descriptions_ready");
  return {
    input: { available: true, reason: "" },
    trackerData: {
      available: trackerAssessment.available && hasTrackerData,
      reasonCode: hasTrackerData ? trackerAssessment.reasonCode : undefined,
      reason: hasTrackerData ? trackerAssessment.reason : "No tracker data is available.",
    },
    hdrAnalysis: {
      available: hasHDRData,
      reason: hasHDRData ? "" : "Prepare an MKV or supported Blu-ray source first.",
    },
    audioAnalysis: {
      available: hasAudioData,
      reason: hasAudioData ? "" : "Prepare a source with authoritative audio-track facts first.",
    },
    duplicates:
      trackerAssessment.available && missingRequiredFacts(release).length > 0
        ? {
            available: false,
            reason:
              "Complete the required facts on Input and refresh metadata before opening Dupe Check.",
          }
        : trackerAssessment,
    screenshots: {
      available: media.available && requirements.needsImages,
      reasonCode: requirements.needsImages ? media.reasonCode : undefined,
      reason: requirements.needsImages
        ? media.reason
        : "Selected trackers do not use shared screenshots.",
    },
    menuImages: media,
    uploadedImages: {
      available: media.available && requirements.needsImages,
      reasonCode: requirements.needsImages ? media.reasonCode : undefined,
      reason: requirements.needsImages
        ? media.reason
        : "Selected trackers do not use shared screenshots.",
    },
    descriptions: {
      available: descriptions.available && requirements.needsDescriptions,
      reasonCode: requirements.needsDescriptions ? descriptions.reasonCode : undefined,
      reason: requirements.needsDescriptions
        ? descriptions.reason
        : "Selected trackers do not use shared descriptions.",
    },
    upload: goal("upload_reviewed"),
  };
};
