// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { spawn } from "node:child_process";
import { expect, test } from "@playwright/test";
import {
  createE2EWorkspace,
  e2eBinary,
  readE2EAuthCounters,
  releaseWorkflowParityFixture,
  repoRoot,
} from "./helpers/e2eHarness";

test("CLI full upload approves trackers before downstream work", async () => {
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
  try {
    const result = await runCLI(
      [
        "--config",
        workspace.configPath,
        "--trackers",
        releaseWorkflowParityFixture.trackerID,
        "--no-seed",
        "--unattended_confirm",
        workspace.sourcePath,
      ],
      workspace.env,
      "y\ny\n",
    );
    expect(result.code, result.output).toBe(0);
    expect(result.output).toContain("Duplicate check:");
    expect(result.output).toMatch(/Use .* as .*\? \[y\/N\]:/);
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

test("CLI skips an auth-blocked tracker while uploading a ready sibling", async () => {
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
  workspace.env.UPBRR_E2E_AUTH_SCENARIOS = "HDS=validation_only_missing_cookies";
  try {
    const result = await runCLI(
      [
        "--config",
        workspace.configPath,
        "--trackers",
        `${releaseWorkflowParityFixture.trackerID},HDS`,
        "--no-seed",
        "--unattended_confirm",
        workspace.sourcePath,
      ],
      workspace.env,
      "y\ny\n",
    );
    expect(result.code, result.output).toBe(0);
    expect(result.output).not.toContain("Skipping HDS before dupe check");
    expect(result.output).not.toContain("tracker authentication remains incomplete");
    expect(result.output).not.toMatch(
      /(?:login|two-factor|2fa|cookie file)[^\r\n]*(?:\?\s*$|:\s*$)/im,
    );
    expect(workspace.fake.counters.trackerUploads).toBe(1);
    expect(workspace.fake.counters.clientSearches).toBe(
      releaseWorkflowParityFixture.expectedClientSearches,
    );
    expect(workspace.fake.counters.clientInjections).toBe(0);
    expect(await readE2EAuthCounters(workspace)).toEqual({
      capabilityCalls: 1,
      validationCalls: 1,
      loginAttempts: 0,
      validations: { BTN: 1, HDS: 1 },
    });
  } finally {
    await workspace.cleanup();
  }
});

test("CLI strict unattended stops before downstream work", async () => {
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
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
    expect(result.code, result.output).not.toBe(0);
    expect(result.output).toContain(
      "strict unattended upload requires global action approve_trackers",
    );
    expect(workspace.fake.counters.trackerUploads).toBe(0);
    expect(workspace.fake.counters.clientInjections).toBe(0);
  } finally {
    await workspace.cleanup();
  }
});

test("CLI debug injects into the client by default", async () => {
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
  try {
    const debug = await runCLI(
      [
        "--config",
        workspace.configPath,
        "--trackers",
        releaseWorkflowParityFixture.trackerID,
        "--debug",
        "--unattended_confirm",
        workspace.sourcePath,
      ],
      workspace.env,
      "y\ny\n",
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
  const workspace = await createE2EWorkspace({ mediaKind: "tv" });
  try {
    const noSeed = await runCLI(
      [
        "--config",
        workspace.configPath,
        "--trackers",
        releaseWorkflowParityFixture.trackerID,
        "--debug",
        "-ns",
        "--unattended_confirm",
        workspace.sourcePath,
      ],
      workspace.env,
      "y\ny\n",
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
  input = "",
): Promise<{ code: number | null; output: string }> {
  return new Promise((resolve) => {
    const child = spawn(e2eBinary, args, {
      cwd: repoRoot,
      env,
      stdio: ["pipe", "pipe", "pipe"],
      windowsHide: true,
    });
    const chunks: string[] = [];
    child.stdout?.on("data", (chunk) => chunks.push(String(chunk)));
    child.stderr?.on("data", (chunk) => chunks.push(String(chunk)));
    child.stdin?.end(input);
    child.on("close", (code) => resolve({ code, output: chunks.join("") }));
  });
}
