// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { describe, expect, it } from "vitest";

import { isMetadataProgressPathMatch, normalizeMetadataProgressPath } from "./metadataProgress";

describe("normalizeMetadataProgressPath", () => {
  it("trims paths and normalizes separators", () => {
    expect(normalizeMetadataProgressPath(" C:\\Media\\Movie.mkv ")).toBe("C:/Media/Movie.mkv");
  });

  it("cleans dot segments", () => {
    expect(normalizeMetadataProgressPath("C:/Media/Extras/../Movie.mkv")).toBe(
      "C:/Media/Movie.mkv",
    );
  });
});

describe("isMetadataProgressPathMatch", () => {
  it("matches separator-normalized paths", () => {
    expect(isMetadataProgressPathMatch("C:\\Media\\Movie.mkv", "C:/Media/Movie.mkv")).toBe(true);
  });

  it("matches Windows paths that differ by case", () => {
    expect(isMetadataProgressPathMatch("c:/media/movie.mkv", "C:/Media/Movie.mkv")).toBe(true);
  });

  it("matches cleaned backend paths to request paths", () => {
    expect(isMetadataProgressPathMatch("C:/Media/Extras/../Movie.mkv", "C:/Media/Movie.mkv")).toBe(
      true,
    );
  });

  it("keeps POSIX paths case-sensitive", () => {
    expect(isMetadataProgressPathMatch("/media/movie.mkv", "/Media/Movie.mkv")).toBe(false);
  });

  it("rejects unrelated paths", () => {
    expect(isMetadataProgressPathMatch("C:/Media/Other.mkv", "C:/Media/Movie.mkv")).toBe(false);
  });

  it("allows events when no progress target is active", () => {
    expect(isMetadataProgressPathMatch("C:/Media/Movie.mkv", "")).toBe(true);
  });
});
