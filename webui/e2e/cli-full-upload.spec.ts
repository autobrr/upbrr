// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { spawn } from "node:child_process";
import { expect, test } from "@playwright/test";
import {
  createE2EWorkspace,
  e2eBinary,
  releaseWorkflowParityFixture,
  repoRoot,
} from "./helpers/e2eHarness";

test("CLI full upload uses local fake services and records an upload", async () => {
  const workspace = await createE2EWorkspace();
  try {
    const result = await runCLI(
      [
        "--config",
        workspace.configPath,
        "--trackers",
        releaseWorkflowParityFixture.trackerID,
        "--no-seed",
        "--unattended",
        workspace.sourcePath,
      ],
      workspace.env,
    );
    expect(result.code, result.output).toBe(0);
    expect(result.output).toMatch(/uploaded|complete|Upload/i);
    expect(workspace.fake.counters.trackerUploads).toBe(
      releaseWorkflowParityFixture.expectedTrackerUploads,
    );
    expect(workspace.fake.counters.clientSearches).toBe(
      releaseWorkflowParityFixture.expectedClientSearches,
    );
    expect(workspace.fake.counters.clientInjections).toBe(0);
  } finally {
    await workspace.cleanup();
  }
});

test("CLI debug injects into the client by default", async () => {
  const workspace = await createE2EWorkspace();
  try {
    const debug = await runCLI(
      [
        "--config",
        workspace.configPath,
        "--trackers",
        releaseWorkflowParityFixture.trackerID,
        "--debug",
        "--unattended",
        workspace.sourcePath,
      ],
      workspace.env,
    );
    expect(debug.code, debug.output).toBe(0);
    expect(debug.output).toContain("client injection was attempted for each ready tracker");
    expect(workspace.fake.counters.trackerUploads).toBe(0);
    expect(workspace.fake.counters.clientInjections).toBe(1);
  } finally {
    await workspace.cleanup();
  }
});

test("CLI debug -ns skips client injection", async () => {
  const workspace = await createE2EWorkspace();
  try {
    const noSeed = await runCLI(
      [
        "--config",
        workspace.configPath,
        "--trackers",
        releaseWorkflowParityFixture.trackerID,
        "--debug",
        "-ns",
        "--unattended",
        workspace.sourcePath,
      ],
      workspace.env,
    );
    expect(noSeed.code, noSeed.output).toBe(0);
    expect(noSeed.output).toContain("tracker uploads and client injection are disabled");
    expect(workspace.fake.counters.trackerUploads).toBe(0);
    expect(workspace.fake.counters.clientInjections).toBe(0);
  } finally {
    await workspace.cleanup();
  }
});

function runCLI(
  args: string[],
  env: NodeJS.ProcessEnv,
): Promise<{ code: number | null; output: string }> {
  return new Promise((resolve) => {
    const child = spawn(e2eBinary, args, {
      cwd: repoRoot,
      env,
      stdio: ["ignore", "pipe", "pipe"],
      windowsHide: true,
    });
    const chunks: string[] = [];
    child.stdout?.on("data", (chunk) => chunks.push(String(chunk)));
    child.stderr?.on("data", (chunk) => chunks.push(String(chunk)));
    child.on("close", (code) => resolve({ code, output: chunks.join("") }));
  });
}
