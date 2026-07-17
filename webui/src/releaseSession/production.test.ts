// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { afterEach, describe, expect, it } from "vitest";
import { setAppRequestHandlerForTests } from "../api/client";
import { productionReleaseSessionPorts } from "./production";
import type { PreparationIntent } from "./types";

afterEach(() => setAppRequestHandlerForTests(null));

const intent = (): PreparationIntent => ({
  sourceLookupURL: "https://example.invalid/source",
  identity: {},
  releaseName: {},
  playlist: { Set: true, Selected: ["00001.mpls"], UseAll: false },
});

describe("productionReleaseSessionPorts", () => {
  it("keeps the original source and carries direct playlist intent without persistence", async () => {
    const requests: Array<{ method: string; body: unknown }> = [];
    setAppRequestHandlerForTests(async (method, body) => {
      requests.push({ method, body });
      return {};
    });
    const ports = productionReleaseSessionPorts();
    const signal = new AbortController().signal;
    const sourcePath = "C:\\media\\Example Disc";

    await ports.preparation.discoverPlaylists(sourcePath, signal);
    await ports.preparation.execute({
      operation: "prepare",
      correlationID: "attempt-1",
      sourcePath,
      intent: intent(),
      controls: { confirmBDMVRescan: false },
      signal,
      onProgress: () => undefined,
    });

    expect(requests.map((request) => request.method)).toEqual([
      "DiscoverPlaylists",
      "FetchMetadata",
    ]);
    expect(requests[0].body).toEqual({ Path: sourcePath });
    expect(requests[1].body).toMatchObject({
      Path: sourcePath,
      CorrelationID: "attempt-1",
      Playlist: { Set: true, Selected: ["00001.mpls"], UseAll: false },
      ConfirmBDMVRescan: false,
    });
    expect(requests[1].body).not.toHaveProperty("Trackers");
  });

  it("sends rescan confirmation only on an explicit retry", async () => {
    const requests: Array<{ method: string; body: unknown }> = [];
    setAppRequestHandlerForTests(async (method, body) => {
      requests.push({ method, body });
      return {};
    });
    const ports = productionReleaseSessionPorts();
    const sourcePath = "C:\\media\\Example Disc\\BDMV";

    await ports.preparation.execute({
      operation: "reset",
      correlationID: "attempt-2",
      sourcePath,
      intent: intent(),
      controls: { confirmBDMVRescan: true },
      signal: new AbortController().signal,
      onProgress: () => undefined,
    });

    expect(requests).toHaveLength(1);
    expect(requests[0]).toMatchObject({
      method: "ResetMetadata",
      body: { CorrelationID: "attempt-2", Path: sourcePath, ConfirmBDMVRescan: true },
    });
  });

  it("sends image-upload correlation with the host request", async () => {
    const requests: Array<{ method: string; body: unknown }> = [];
    setAppRequestHandlerForTests(async (method, body) => {
      requests.push({ method, body });
      return { Links: [], Failures: [] };
    });
    const ports = productionReleaseSessionPorts();

    await ports.uploadedImages.upload({
      correlationID: "image-upload-1",
      release: { SourcePath: "C:\\media\\Example", Generation: 1 },
      trackers: ["AITHER"],
      host: "imgbox",
      images: [],
      signal: new AbortController().signal,
      onProgress: () => undefined,
    });

    expect(requests).toEqual([
      expect.objectContaining({
        method: "UploadImages",
        body: expect.objectContaining({
          CorrelationID: "image-upload-1",
          Host: "imgbox",
        }),
      }),
    ]);
  });

  it("transports duplicate ignores and live rule authorizations independently", async () => {
    const requests: Array<{ method: string; body: unknown }> = [];
    setAppRequestHandlerForTests(async (method, body) => {
      requests.push({ method, body });
      return {};
    });
    const ports = productionReleaseSessionPorts();
    const signal = new AbortController().signal;
    const common = {
      release: { SourcePath: "C:\\media\\Example", Generation: 1 },
      trackers: ["AITHER"],
      ignoreDupesFor: ["AITHER"],
      questionnaireAnswers: {},
      descriptionGroups: [],
      options: { noSeed: false, runLogLevel: "info" },
    } as const;

    await ports.upload.dryRun({ ...common, dupeJobID: "dupe-job" }, signal);
    await ports.upload.review(
      {
        ...common,
        ruleAuthorizations: [{ Tracker: "AITHER", Rules: ["container"] }],
      },
      signal,
    );

    expect(requests[0]).toMatchObject({
      method: "FetchTrackerDryRun",
      body: { IgnoreDupesFor: ["AITHER"], NoSeed: false },
    });
    expect(requests[0].body).not.toHaveProperty("RuleAuthorizations");
    expect(requests[1]).toMatchObject({
      method: "ReviewTrackerUpload",
      body: {
        IgnoreDupesFor: ["AITHER"],
        RuleAuthorizations: [{ Tracker: "AITHER", Rules: ["container"] }],
      },
    });
  });
});
