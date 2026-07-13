// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { describe, expect, it } from "vitest";

import type { ScreenshotImage, ScreenshotSelection } from "../types";
import {
  buildExistingSlotImages,
  pendingSelections,
  selectionTimestamp,
  slotTolerance,
  staleSlotPaths,
} from "./screenshotSlots";

const image = (index: number, timestamp: number, path: string): ScreenshotImage =>
  ({ Index: index, TimestampSeconds: timestamp, Path: path }) as ScreenshotImage;

const selection = (index: number, timestamp: number, frame = 0): ScreenshotSelection => ({
  Index: index,
  TimestampSeconds: timestamp,
  Frame: frame,
  Source: "auto",
});

const FPS = 24;

describe("slotTolerance", () => {
  it("floors at half a second and widens for slow frame rates", () => {
    expect(slotTolerance(24)).toBe(0.5);
    expect(slotTolerance(1)).toBe(1);
    expect(slotTolerance(0)).toBe(0.5);
  });
});

describe("selectionTimestamp", () => {
  it("prefers seconds and falls back to the frame number", () => {
    expect(selectionTimestamp(selection(1, 12.5), FPS)).toBe(12.5);
    expect(selectionTimestamp(selection(1, 0, 48), FPS)).toBe(2);
    expect(selectionTimestamp(selection(1, 0, 48), 0)).toBe(0);
  });
});

describe("buildExistingSlotImages", () => {
  it("lets a promoted preview claim a slot the plan does not know about", () => {
    const slots = buildExistingSlotImages(
      [image(1, 10, "shot-1.png")],
      [image(2, 20, "shot-preview-2.png")],
    );
    expect(slots.get(1)?.Path).toBe("shot-1.png");
    expect(slots.get(2)?.Path).toBe("shot-preview-2.png");
  });
});

describe("pendingSelections", () => {
  it("skips slots whose file still matches, keeps retimed and empty ones", () => {
    const slots = buildExistingSlotImages([image(1, 10, "shot-1.png"), image(2, 20, "shot-2.png")]);
    const pending = pendingSelections(
      [selection(1, 10.2), selection(2, 95), selection(3, 30)],
      slots,
      FPS,
    );
    // 1 still matches (within tolerance), 2 was retimed, 3 was never shot.
    expect(pending.map((entry) => entry.Index)).toEqual([2, 3]);
  });

  it("returns the full baseline when nothing exists yet", () => {
    const selections = [selection(1, 10), selection(2, 20)];
    expect(pendingSelections(selections, new Map(), FPS)).toHaveLength(2);
  });

  it("skips a slot filled by a promoted preview", () => {
    const slots = buildExistingSlotImages([], [image(1, 10, "shot-preview-1.png")]);
    expect(pendingSelections([selection(1, 10), selection(2, 20)], slots, FPS)).toHaveLength(1);
  });

  it("compares a frame-only selection against the file timestamp", () => {
    const slots = buildExistingSlotImages([image(1, 2, "shot-1.png")]);
    // Frame 48 @ 24fps = 2s, which is the file it already has.
    expect(pendingSelections([selection(1, 0, 48)], slots, FPS)).toHaveLength(0);
    // Frame 480 @ 24fps = 20s: retimed, so it must be reshot.
    expect(pendingSelections([selection(1, 0, 480)], slots, FPS)).toHaveLength(1);
  });

  it("reshoots a slot whose file drifted by more than the frame tolerance", () => {
    const slots = buildExistingSlotImages([image(1, 10, "shot-1.png")]);
    expect(pendingSelections([selection(1, 10.4)], slots, FPS)).toHaveLength(0);
    expect(pendingSelections([selection(1, 10.6)], slots, FPS)).toHaveLength(1);
  });
});

describe("staleSlotPaths", () => {
  it("retires the file a re-shot slot replaced, and nothing else", () => {
    const slots = buildExistingSlotImages([image(1, 10, "shot-1.png"), image(2, 20, "shot-2.png")]);
    const stale = staleSlotPaths([image(2, 95, "shot-2-new.png")], slots);
    expect([...stale]).toEqual(["shot-2.png"]);
  });

  it("does not retire a file the capture rewrote in place", () => {
    const slots = buildExistingSlotImages([image(1, 10, "shot-1.png")]);
    expect(staleSlotPaths([image(1, 10, "shot-1.png")], slots).size).toBe(0);
  });

  it("retires the preview file a re-shot slot replaced", () => {
    const slots = buildExistingSlotImages([], [image(1, 10, "shot-preview-1.png")]);
    const stale = staleSlotPaths([image(1, 40, "shot-1-new.png")], slots);
    expect([...stale]).toEqual(["shot-preview-1.png"]);
  });

  it("leaves untouched slots alone", () => {
    const slots = buildExistingSlotImages([image(1, 10, "shot-1.png"), image(2, 20, "shot-2.png")]);
    expect(staleSlotPaths([image(3, 30, "shot-3.png")], slots).size).toBe(0);
  });
});
