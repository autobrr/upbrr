// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { describe, expect, it } from "vitest";

import type { UIState } from "../types";
import {
  hasConflictingPreviewSource,
  sourcePathEquals,
  sourcePathMatches,
  workflowStateSourceKey,
  workflowStateSourcePath,
} from "./workflowState";

describe("sourcePathMatches", () => {
  it("normalizes case and path separators", () => {
    expect(sourcePathMatches("C:\\Media\\Release", "c:/media/release")).toBe(true);
    expect(sourcePathMatches("C:\\Media\\Release", "D:/media/release")).toBe(false);
    expect(sourcePathMatches("", "c:/media/release")).toBe(false);
  });

  it("accepts trailing separators", () => {
    expect(sourcePathMatches("C:\\Media\\Release\\", "c:/media/release")).toBe(true);
    expect(sourcePathMatches("C:\\Media\\Release", "c:/media/release/")).toBe(true);
  });

  it("matches backend absolute paths for relative input", () => {
    expect(sourcePathMatches("Media\\Release", "C:/Users/Audionut/Media/Release")).toBe(true);
    expect(sourcePathMatches("Other\\Release", "C:/Users/Audionut/Media/Release")).toBe(false);
  });
});

describe("sourcePathEquals", () => {
  it("does not accept absolute-relative suffix matches", () => {
    expect(sourcePathEquals("Media\\Release", "C:/Users/Audionut/Media/Release")).toBe(false);
    expect(sourcePathEquals("C:\\Media\\Release", "c:/media/release")).toBe(true);
  });

  it("rejects same-suffix paths under different roots", () => {
    expect(sourcePathEquals("C:\\Library\\Release", "D:/Library/Release")).toBe(false);
  });
});

describe("workflowStateSourceKey", () => {
  it("prefers active input path over stale preview path", () => {
    const state = {
      path: "D:\\New\\Release",
      preview: {
        SourcePath: "C:\\Old\\Release",
      },
    } as UIState;

    expect(workflowStateSourceKey(state)).toBe("d:/new/release");
  });

  it("falls back to source-bearing workflow snapshots", () => {
    const state = {
      dupeCheckSnapshot: {
        sourcePath: "C:\\Media\\Checked",
      },
    } as UIState;

    expect(workflowStateSourceKey(state)).toBe("c:/media/checked");
  });

  it("keeps raw fallback source path for pathless restores", () => {
    const state = {
      preview: {
        SourcePath: "C:\\Media\\Checked",
      },
    } as UIState;

    expect(workflowStateSourcePath(state)).toBe("C:\\Media\\Checked");
    expect(workflowStateSourceKey(state)).toBe("c:/media/checked");
  });
});

describe("hasConflictingPreviewSource", () => {
  it("detects stale preview data for a different active path", () => {
    expect(
      hasConflictingPreviewSource({
        path: "D:\\New\\Release",
        preview: {
          SourcePath: "C:\\Old\\Release",
        },
      } as UIState),
    ).toBe(true);

    expect(
      hasConflictingPreviewSource({
        path: "C:\\Media\\Release",
        preview: {
          SourcePath: "c:/media/release",
        },
      } as UIState),
    ).toBe(false);

    expect(
      hasConflictingPreviewSource({
        path: "Media\\Release",
        preview: {
          SourcePath: "C:/Users/Audionut/Media/Release",
        },
      } as UIState),
    ).toBe(false);
  });
});
