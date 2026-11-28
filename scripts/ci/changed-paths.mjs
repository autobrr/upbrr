// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { execFileSync } from "node:child_process";

export function changedPaths(base, head) {
  if (!base || !head) return null;
  try {
    // Keep old and new rename paths, deletions, and every changed file without API pagination limits.
    const paths = execFileSync(
      "git",
      ["diff", "--name-only", "--no-renames", "-z", `${base}...${head}`, "--"],
      {
        encoding: "utf8",
      },
    );
    return paths.split("\0").filter(Boolean);
  } catch {
    console.warn("Could not read the complete PR diff. Selecting all checks.");
    return null;
  }
}
