// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ScreenshotImage, ScreenshotSelection } from "../types";

// Mirrors screenshotTimestampMatchesSelection in internal/services/screenshots.
export const slotTolerance = (frameRate: number): number =>
  frameRate > 0 ? Math.max(0.5, 1 / frameRate) : 0.5;

export const selectionTimestamp = (entry: ScreenshotSelection, frameRate: number): number => {
  if (Number.isFinite(entry.TimestampSeconds) && entry.TimestampSeconds > 0) {
    return entry.TimestampSeconds;
  }
  if (Number.isFinite(entry.Frame) && entry.Frame > 0 && frameRate > 0) {
    return entry.Frame / frameRate;
  }
  return 0;
};

/**
 * Image currently filling each capture slot. Plan.ExistingScreenshots covers files
 * on disk; promoted previews are written as -preview- files that never reach it, so
 * they are passed in separately - previewImages keeps their true slot Index.
 */
export const buildExistingSlotImages = (
  existing: readonly ScreenshotImage[] | undefined,
  promotedPreviews: readonly ScreenshotImage[] = [],
): Map<number, ScreenshotImage> => {
  const slots = new Map<number, ScreenshotImage>();
  for (const image of [...(existing || []), ...promotedPreviews]) {
    if (Number.isFinite(image.Index)) {
      slots.set(image.Index, image);
    }
  }
  return slots;
};

/**
 * Baseline slots still needing a capture. A slot is skipped only while its file
 * still matches the selection; retiming a slot must re-shoot it, not count as done.
 */
export const pendingSelections = (
  selections: readonly ScreenshotSelection[],
  slots: ReadonlyMap<number, ScreenshotImage>,
  frameRate: number,
): ScreenshotSelection[] => {
  const tolerance = slotTolerance(frameRate);
  return selections.filter((entry) => {
    const existing = slots.get(entry.Index);
    if (!existing) return true;
    const drift = Math.abs((existing.TimestampSeconds || 0) - selectionTimestamp(entry, frameRate));
    return drift > tolerance;
  });
};

/** Files superseded by a capture: a re-shot slot lands on a new filename. */
export const staleSlotPaths = (
  captured: readonly ScreenshotImage[],
  slots: ReadonlyMap<number, ScreenshotImage>,
): Set<string> => {
  const capturedPaths = new Set(captured.map((image) => image.Path));
  const stale = new Set<string>();
  for (const image of captured) {
    const replaced = slots.get(image.Index)?.Path;
    if (replaced && !capturedPaths.has(replaced)) {
      stale.add(replaced);
    }
  }
  return stale;
};
