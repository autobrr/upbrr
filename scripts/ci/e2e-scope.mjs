// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { changedPaths } from "./changed-paths.mjs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

const runtimeRoots = [
  "cmd/upbrr/",
  "internal/",
  "pkg/",
  "config/",
  "webui/src/",
  "webui/public/",
  "webui/scripts/",
  "webui/e2e/",
];
const buildFiles = new Set([
  "go.mod",
  "go.sum",
  "go.work",
  "go.work.sum",
  "package.json",
  "pnpm-lock.yaml",
  "pnpm-workspace.yaml",
  ".npmrc",
  ".gitattributes",
  "Makefile",
  "webui/package.json",
  "webui/pnpm-lock.yaml",
  "webui/pnpm-workspace.yaml",
  "webui/.npmrc",
  "webui/index.html",
  "webui/knip.ts",
  "scripts/build.sh",
  "scripts/build.ps1",
  "scripts/ci/test-loopback.sh",
  "scripts/ci/test-loopback.test.sh",
  "scripts/sync-webui-assets.sh",
  "scripts/sync-webui-assets.ps1",
  ".github/workflows/e2e.yml",
  ".github/actions/prepare-e2e/action.yml",
  "scripts/ci/changed-paths.mjs",
]);

export function isFrontendUnitTest(path) {
  return (
    /^webui\/src\/.*\.(test|spec)\.tsx?$/.test(path) ||
    path === "webui/src/test/setup.ts" ||
    path === "webui/src/test/appRequestMock.ts"
  );
}

/** Selects the inputs to the embedded application and the normal Playwright projects. */
export function requiresE2E(paths) {
  return paths.some((path) => {
    if (path.endsWith("_test.go") || isFrontendUnitTest(path)) return false;
    if (/(^|\/)(AGENTS|CLAUDE|README)\.md$/.test(path)) return false;
    if (
      /^internal\/(architecturepolicy|literalpolicy|logpolicy|pathing\/policy)\//.test(
        path,
      )
    ) {
      return false;
    }
    if (
      /^webui\/(e2e\/visual-quality[^/]*\.spec\.ts|visual-quality[^/]*\.config\.ts)$/.test(
        path,
      )
    ) {
      return false;
    }
    return (
      runtimeRoots.some((root) => path.startsWith(root)) ||
      buildFiles.has(path) ||
      /^webui\/(\.env[^/]*|tsconfig[^/]*\.json|(vite|vitest|playwright)\.config\.[^/]+)$/.test(
        path,
      ) ||
      /^scripts\/ci\/e2e-scope\./.test(path)
    );
  });
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const [base, head] = process.argv.slice(2);
  const paths = changedPaths(base, head);
  console.log(paths === null || requiresE2E(paths));
}
