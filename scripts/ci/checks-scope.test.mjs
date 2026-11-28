// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import assert from "node:assert/strict";
import test from "node:test";
import { selectChecks } from "./checks-scope.mjs";

test("routes changed inputs to their consuming suites", () => {
  for (const [paths, suites] of [
    [["internal/core/core_test.go"], ["go"]],
    [["webui/src/App.test.tsx"], ["frontend"]],
    [
      ["documentation/package.json", "pkg/api/server.go"],
      ["docs", "go"],
    ],
    [["webui/src/pages/settings/index.tsx"], ["frontend", "go"]],
    [["webui/osv-scanner.toml"], ["monitor", "frontend", "go"]],
    [["pkg/api/testdata/prepared_release_display.json"], ["frontend", "go"]],
    [["scripts/ci/test-loopback.sh"], ["go", "frontend"]],
    [[".github/workflows/vuln.yml"], ["monitor", "go"]],
    [[".github/workflows/checks.yml"], ["docs", "frontend", "go", "monitor"]],
    [null, ["docs", "frontend", "go", "monitor"]],
  ]) {
    assert.deepEqual(selectChecks(paths), new Set(suites), String(paths));
  }
});
