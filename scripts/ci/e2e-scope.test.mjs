// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import {
  mkdtempSync,
  mkdirSync,
  renameSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { requiresE2E } from "./e2e-scope.mjs";

test("selects runtime, E2E, assets, manifests, and build inputs", () => {
  for (const path of [
    "cmd/upbrr/serve.go",
    "internal/core/core.go",
    "internal/core/core_test.go",
    "pkg/api/workflow.go",
    "config/example.yaml",
    "webui/src/App.tsx",
    "webui/src/App.test.tsx",
    "webui/public/help.md",
    "webui/scripts/build-appearance-bootstrap.mjs",
    "webui/e2e/helpers/e2eHarness.ts",
    "webui/e2e/api-full-upload.spec.ts",
    "go.mod",
    "go.sum",
    "go.work",
    "go.work.sum",
    ".gitattributes",
    "package.json",
    "pnpm-lock.yaml",
    "pnpm-workspace.yaml",
    ".npmrc",
    "Makefile",
    "webui/package.json",
    "webui/pnpm-lock.yaml",
    "webui/pnpm-workspace.yaml",
    "webui/.npmrc",
    "webui/.env.production",
    "webui/index.html",
    "webui/tsconfig.json",
    "webui/vite.config.ts",
    "webui/vitest.config.ts",
    "webui/playwright.config.ts",
    "webui/knip.ts",
    "scripts/build.sh",
    "scripts/build.ps1",
    "scripts/sync-webui-assets.sh",
    "scripts/sync-webui-assets.ps1",
    "scripts/ci/e2e-scope.mjs",
    "scripts/ci/e2e-scope.test.mjs",
    ".github/workflows/e2e.yml",
  ]) {
    assert.equal(requiresE2E([path]), true, path);
  }
});

test("skips docs, separate visual suites, and unrelated tooling", () => {
  assert.equal(
    requiresE2E([
      "AGENTS.md",
      "CONTRIBUTING.md",
      "ADDING_TRACKERS.md",
      "internal/AGENTS.md",
      "pkg/api/README.md",
      "webui/AGENTS.md",
      "webui/e2e/CLAUDE.md",
      "docs/design.md",
      "documentation/package.json",
      "webui/visual-quality.config.ts",
      "webui/visual-quality-touch.config.ts",
      "webui/e2e/visual-quality-capture.spec.ts",
      "webui/e2e/visual-quality-touch.spec.ts",
      "Dockerfile",
      ".github/workflows/docker.yml",
      "cmd/pathpolicy/main.go",
      "internal/architecturepolicy/check.go",
      "internal/literalpolicy/check.go",
      "internal/logpolicy/check.go",
      "internal/pathing/policy/check.go",
      "scripts/ci/docker_test.go",
    ]),
    false,
  );
  assert.equal(requiresE2E([]), false);
});

test("does not truncate large changesets or split filenames on newlines", () => {
  const docs = Array.from({ length: 4_000 }, (_, index) => `docs/${index}.md`);
  assert.equal(requiresE2E(docs), false);
  assert.equal(requiresE2E([...docs, "webui/src/name\nwith-newline.ts"]), true);
});

test("uses the merge base, includes deleted/renamed paths, and runs on unreadable or manual input", (t) => {
  const cwd = mkdtempSync(join(tmpdir(), "upbrr-e2e-scope-"));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  const env = {
    ...process.env,
    GIT_AUTHOR_NAME: "E2E Scope Test",
    GIT_AUTHOR_EMAIL: "e2e@example.invalid",
    GIT_COMMITTER_NAME: "E2E Scope Test",
    GIT_COMMITTER_EMAIL: "e2e@example.invalid",
  };
  const git = (...args) =>
    execFileSync("git", args, {
      cwd,
      env,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
  const select = (...refs) =>
    execFileSync(
      process.execPath,
      [fileURLToPath(new URL("./e2e-scope.mjs", import.meta.url)), ...refs],
      {
        cwd,
        encoding: "utf8",
        stdio: ["ignore", "pipe", "pipe"],
      },
    ).trim();
  const commit = (message) => {
    git("add", ".");
    git(
      "-c",
      `core.hooksPath=${hooks}`,
      "-c",
      "commit.gpgsign=false",
      "commit",
      "-m",
      message,
    );
    return git("rev-parse", "HEAD");
  };
  git("init");
  mkdirSync(join(cwd, "internal"));
  mkdirSync(join(cwd, "docs"));
  const hooks = join(cwd, "empty-hooks");
  mkdirSync(hooks);
  writeFileSync(
    join(cwd, "internal", "runtime.go"),
    "synthetic runtime fixture\n",
  );
  const base = commit("fixture base");
  writeFileSync(join(cwd, "docs", "guide.md"), "synthetic documentation\n");
  const docsHead = commit("docs change");
  assert.equal(select(base, docsHead), "false");
  renameSync(
    join(cwd, "internal", "runtime.go"),
    join(cwd, "docs", "runtime.md"),
  );
  assert.equal(select(docsHead, commit("rename runtime to docs")), "true");
  git("checkout", "--detach", base);
  rmSync(join(cwd, "internal", "runtime.go"));
  const newerBase = commit("delete runtime on base branch");
  assert.equal(select(base, newerBase), "true");
  assert.equal(select(newerBase, docsHead), "false");
  assert.equal(select("missing-base", docsHead), "true");
  assert.equal(select(), "true");
  assert.equal(select("", ""), "true");
});
