// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ChildProcess } from "node:child_process";
import { stripVTControlCharacters } from "node:util";

/** Requires this child's completed post-bind log before HTTP probes; preserves the startup polling deadline. */
export async function waitForHTTP(
  url: string,
  child: Pick<ChildProcess, "exitCode" | "signalCode">,
  output: string[],
  streams: string[][],
  timeoutMs = 20_000,
) {
  const deadline = Date.now() + timeoutMs;
  const marker = `web: serving web UI on ${new URL(url).host}`;
  const requireLiveChild = () => {
    if (child.exitCode !== null || child.signalCode !== null) {
      throw new Error(
        `server exited with ${child.exitCode ?? child.signalCode}:\n${output.join("")}`,
      );
    }
  };
  while (Date.now() < deadline) {
    requireLiveChild();
    // The port reservation is released before startup; another process's HTTP 200 is not readiness.
    const bound = streams.some((chunks) =>
      stripVTControlCharacters(chunks.join(""))
        .split("\n")
        .slice(0, -1)
        .some((line) => line.trimEnd().endsWith(marker) || line.includes(`${marker} `)),
    );
    if (bound) {
      let response: Response | undefined;
      try {
        response = await fetch(url);
      } catch {
        // Retry until this child's listener is accepting requests or the deadline expires.
      }
      requireLiveChild();
      if (response?.ok) return;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`server did not become ready:\n${output.join("")}`);
}
