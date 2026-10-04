// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { execFileSync } from "node:child_process";
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
  "scripts/sync-webui-assets.sh",
  "scripts/sync-webui-assets.ps1",
  ".github/workflows/e2e.yml",
]);

/** Selects runtime, normal E2E, and shared build inputs; source-tree unit tests stay conservative. */
export function requiresE2E(paths) {
  return paths.some((path) => {
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
  let run = true;
  if (base && head) {
    try {
      // Keep old and new rename paths, deletions, and every changed file without API pagination limits.
      const paths = execFileSync(
        "git",
        [
          "diff",
          "--name-only",
          "--no-renames",
          "-z",
          `${base}...${head}`,
          "--",
        ],
        {
          encoding: "utf8",
        },
      );
      run = requiresE2E(paths.split("\0").filter(Boolean));
    } catch {
      console.warn(
        "Could not read the complete PR diff; running all E2E projects.",
      );
    }
  }
  console.log(run);
}
