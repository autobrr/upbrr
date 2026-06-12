// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { spawn } from "node:child_process";
import { expect, test } from "@playwright/test";
import { repoRoot } from "./helpers/e2eHarness";

test("Wails/backend parity Go tests pass", async () => {
  const result = await run("go", [
    "test",
    "-race",
    "-v",
    "-timeout",
    "20m",
    "./internal/guiapp",
    "./internal/webserver",
    "./internal/guishared",
    "./pkg/api",
  ]);
  expect(result.code, result.output).toBe(0);
});

function run(command: string, args: string[]): Promise<{ code: number | null; output: string }> {
  return new Promise((resolve) => {
    const child = spawn(command, args, {
      cwd: repoRoot,
      stdio: ["ignore", "pipe", "pipe"],
      windowsHide: true,
    });
    const chunks: string[] = [];
    child.stdout?.on("data", (chunk) => chunks.push(String(chunk)));
    child.stderr?.on("data", (chunk) => chunks.push(String(chunk)));
    child.on("close", (code) => resolve({ code, output: chunks.join("") }));
  });
}
