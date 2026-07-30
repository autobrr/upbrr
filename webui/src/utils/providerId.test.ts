// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { describe, expect, it } from "vitest";

import { formatIMDbID } from "./providerId";

describe("formatIMDbID", () => {
  it.each([
    { name: "undefined", id: undefined, want: "" },
    { name: "zero", id: 0, want: "" },
    { name: "negative", id: -1, want: "" },
    { name: "short", id: 456, want: "tt0000456" },
    { name: "seven digits", id: 1_234_567, want: "tt1234567" },
    { name: "long", id: 12_345_678, want: "tt12345678" },
  ])("formats $name", ({ id, want }) => {
    expect(formatIMDbID(id)).toBe(want);
  });
});
