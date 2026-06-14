// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { describe, expect, it } from "vitest";

import {
  addSourcePathHistoryEntry,
  defaultInputHistoryLimit,
  filterBrowseEntries,
  inferSourcePathMode,
  normalizeSourcePathHistory,
  resolveInputHistoryLimit,
  sameSourcePath,
} from "./inputHistory";

describe("resolveInputHistoryLimit", () => {
  it("uses the default for missing, invalid, and negative values", () => {
    expect(resolveInputHistoryLimit(undefined)).toBe(defaultInputHistoryLimit);
    expect(resolveInputHistoryLimit(Number.NaN)).toBe(defaultInputHistoryLimit);
    expect(resolveInputHistoryLimit(-1)).toBe(defaultInputHistoryLimit);
  });

  it("accepts zero and truncates positive values", () => {
    expect(resolveInputHistoryLimit(0)).toBe(0);
    expect(resolveInputHistoryLimit(7.9)).toBe(7);
    expect(resolveInputHistoryLimit("5")).toBe(5);
  });
});

describe("normalizeSourcePathHistory", () => {
  it("trims, deduplicates, and caps entries", () => {
    expect(
      normalizeSourcePathHistory(
        [" C:/media/movie.mkv ", "", "C:/media/movie.mkv", "D:/shows/Season 01"],
        2,
      ),
    ).toEqual([
      { path: "C:/media/movie.mkv", mode: "folder" },
      { path: "D:/shows/Season 01", mode: "folder" },
    ]);
  });

  it("returns empty history when disabled or malformed", () => {
    expect(normalizeSourcePathHistory([{ path: "C:/media/movie.mkv", mode: "file" }], 0)).toEqual(
      [],
    );
    expect(normalizeSourcePathHistory({ path: "C:/media/movie.mkv" }, 10)).toEqual([]);
  });

  it("keeps persisted modes and supports legacy string entries", () => {
    expect(
      normalizeSourcePathHistory(
        [
          { path: "C:/media/movie.mkv", mode: "file" },
          { path: "D:/shows/Season 01", mode: "folder" },
          "E:/legacy/movie.mkv",
        ],
        5,
      ),
    ).toEqual([
      { path: "C:/media/movie.mkv", mode: "file" },
      { path: "D:/shows/Season 01", mode: "folder" },
      { path: "E:/legacy/movie.mkv", mode: "folder" },
    ]);
  });

  it("deduplicates equivalent paths with runtime-aware comparison", () => {
    expect(
      normalizeSourcePathHistory(
        ["C:\\Media\\Movie.mkv", "c:/media/movie.mkv", "D:/shows/Season 01"],
        5,
        true,
      ),
    ).toEqual([
      { path: "C:\\Media\\Movie.mkv", mode: "folder" },
      { path: "D:/shows/Season 01", mode: "folder" },
    ]);

    expect(
      normalizeSourcePathHistory(["C:/Media/Movie.mkv", "c:/media/movie.mkv"], 5, false),
    ).toEqual([
      { path: "C:/Media/Movie.mkv", mode: "folder" },
      { path: "c:/media/movie.mkv", mode: "folder" },
    ]);
  });
});

describe("addSourcePathHistoryEntry", () => {
  it("moves an existing entry to the top and enforces the limit", () => {
    expect(
      addSourcePathHistoryEntry(
        [
          { path: "D:/shows/Season 01", mode: "folder" },
          { path: "C:/media/movie.mkv", mode: "folder" },
          { path: "E:/archive/release", mode: "folder" },
        ],
        " C:/media/movie.mkv ",
        "file",
        2,
      ),
    ).toEqual([
      { path: "C:/media/movie.mkv", mode: "file" },
      { path: "D:/shows/Season 01", mode: "folder" },
    ]);
  });

  it("ignores blank entries", () => {
    expect(
      addSourcePathHistoryEntry([{ path: "C:/media/movie.mkv", mode: "file" }], " ", "file", 5),
    ).toEqual([{ path: "C:/media/movie.mkv", mode: "file" }]);
  });

  it("moves runtime-equivalent entries to the top", () => {
    expect(
      addSourcePathHistoryEntry(
        [
          { path: "D:/shows/Season 01", mode: "folder" },
          { path: "C:\\Media\\Movie.mkv", mode: "folder" },
        ],
        "c:/media/movie.mkv",
        "file",
        5,
        true,
      ),
    ).toEqual([
      { path: "c:/media/movie.mkv", mode: "file" },
      { path: "D:/shows/Season 01", mode: "folder" },
    ]);
  });
});

describe("inferSourcePathMode", () => {
  it("infers common video file paths as files", () => {
    expect(inferSourcePathMode("C:/media/movie.mkv")).toBe("file");
    expect(inferSourcePathMode("C:/media/disc.iso")).toBe("file");
  });

  it("keeps disc and extensionless paths as folders", () => {
    expect(inferSourcePathMode("C:/media/Movie/BDMV")).toBe("folder");
    expect(inferSourcePathMode("D:/shows/Season 01")).toBe("folder");
  });
});

describe("sameSourcePath", () => {
  it("normalizes separators and folds case only for case-insensitive runtimes", () => {
    expect(sameSourcePath("C:\\Media\\Movie.mkv", "c:/media/movie.mkv", true)).toBe(true);
    expect(sameSourcePath("/media/Movie.mkv", "/media/movie.mkv", false)).toBe(false);
    expect(sameSourcePath("/media/Movie.mkv", "/media/Movie.mkv", false)).toBe(true);
  });
});

describe("filterBrowseEntries", () => {
  const entries = [
    { name: "Movie.2024.mkv", path: "C:/media/Movie.2024.mkv" },
    { name: "Season 01", path: "D:/shows/Example/Season 01" },
    { name: "Extras", path: "D:/shows/Example/Extras" },
  ];

  it("filters current entries by name or path case-insensitively", () => {
    expect(filterBrowseEntries(entries, "movie")).toEqual([entries[0]]);
    expect(filterBrowseEntries(entries, "example/season")).toEqual([entries[1]]);
  });

  it("returns every entry for empty search", () => {
    expect(filterBrowseEntries(entries, " ")).toEqual(entries);
  });
});
