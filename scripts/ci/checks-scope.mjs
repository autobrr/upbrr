// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { changedPaths } from "./changed-paths.mjs";
import { isFrontendUnitTest } from "./e2e-scope.mjs";

// Unknown and shared inputs select all suites. Go integration tests also read
// frontend source and repository configuration outside the Go packages.
function suitesFor(path) {
  if (path.startsWith("documentation/")) return ["docs"];
  if (path.startsWith("docs/") && path.endsWith(".md")) return [];
  if (isFrontendUnitTest(path)) return ["frontend"];
  if (path === "webui/osv-scanner.toml" || path === "webui/pnpm-lock.yaml") {
    return ["frontend", "go", "monitor"];
  }
  if (path.startsWith("pkg/api/testdata/")) return ["frontend", "go"];
  if (path.startsWith("webui/")) return ["frontend", "go"];
  if (/^(cmd|internal|pkg|config)\//.test(path) || path.endsWith(".go")) {
    return ["go"];
  }
  if (path === "scripts/ci/test-loopback.sh") return ["frontend", "go"];
  if (path.startsWith("scripts/ci/go-test-")) return ["go"];
  if (
    path.startsWith("scripts/vulnerability/") ||
    path === ".github/workflows/vuln.yml"
  ) {
    return ["monitor", "go"];
  }
  return allSuites;
}

const allSuites = ["go", "frontend", "docs", "monitor"];

export function selectChecks(paths) {
  return new Set(paths === null ? allSuites : paths.flatMap(suitesFor));
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const selected = selectChecks(changedPaths(...process.argv.slice(2)));
  for (const suite of allSuites) console.log(`${suite}=${selected.has(suite)}`);
}
