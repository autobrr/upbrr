// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { afterEach, describe, expect, it, vi } from "vitest";
import { initializeWebClient, setAppRequestHandlerForTests } from "../api/client";
import type { ReleaseWorkflowCurrent } from "../api/generated/release-workflow";
import { productionReleaseSessionPorts } from "./production";

afterEach(() => {
  setAppRequestHandlerForTests(null);
  delete window.__UPBRR_BASE_URL__;
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("productionReleaseSessionPorts", () => {
  it("forwards one typed continuation request without stage-specific translation", async () => {
    const requests: Array<{ method: string; body: unknown }> = [];
    let requestOptions: Readonly<{ signal?: AbortSignal; correlationID?: string }> | undefined;
    setAppRequestHandlerForTests(async (method, body, options) => {
      requests.push({ method, body });
      requestOptions = options;
      return {
        workflow: { id: "workflow-1", revision: 1 },
      };
    });
    const ports = productionReleaseSessionPorts();
    const sourcePath = "C:\\media\\Example Disc\\BDMV";
    const signal = new AbortController().signal;
    const request = {
      goal: "prepared",
      intent: {
        factInstructions: {
          Identity: {},
          ReleaseName: {},
          Metadata: {},
          SourceLookup: "https://example.invalid/source",
          BlurayReleaseID: "",
          Playlist: { Set: true, Selected: ["00001.mpls"], UseAll: false },
          TrackerIDs: {},
        },
        preparation: {
          SourcePath: sourcePath,
          Intent: "upload",
          ExternalFreshness: "refresh",
          Instructions: {
            Identity: {},
            ReleaseName: {},
            Metadata: {},
            SourceLookup: "https://example.invalid/source",
            BlurayReleaseID: "",
            Playlist: { Set: true, Selected: ["00001.mpls"], UseAll: false },
            TrackerIDs: {},
          },
          Policy: { KeepFolder: false, KeepImages: false, OnlyID: false },
          Search: { Skip: false },
          Controls: { Interaction: "interactive", ConfirmBDMVRescan: true },
          Force: false,
          RequirePrepared: false,
        },
      },
      idempotencyKey: "continue-1",
    };
    await ports.workflow.continue(request, signal);

    expect(requests).toEqual([
      {
        method: "ContinueReleaseWorkflow",
        body: request,
      },
    ]);
    expect(requestOptions).toEqual({ signal });
  });

  it("reloads the authoritative revision before canceling a workflow", async () => {
    const requests: Array<{ method: string; body: unknown }> = [];
    setAppRequestHandlerForTests(async (method, body) => {
      requests.push({ method, body });
      return { workflow: { id: "workflow-1", revision: 7, status: "canceled" } };
    });
    const ports = productionReleaseSessionPorts();

    await ports.workflow.cancel(
      "workflow-1",
      "user canceled",
      "cancel-1",
      new AbortController().signal,
    );

    expect(requests).toEqual([
      { method: "GetReleaseWorkflow", body: { workflowId: "workflow-1" } },
      {
        method: "CancelReleaseWorkflow",
        body: {
          workflowId: "workflow-1",
          expectedRevision: 7,
          reason: "user canceled",
          idempotencyKey: "cancel-1",
        },
      },
    ]);
  });

  it("reconciles dry-run and client-injection options through Continue", async () => {
    const requests: Array<{ method: string; body: unknown }> = [];
    const current = {
      workflow: { id: "workflow-1", revision: 7 },
    } as ReleaseWorkflowCurrent;
    setAppRequestHandlerForTests(async (method, body) => {
      requests.push({ method, body });
      return current;
    });
    const ports = productionReleaseSessionPorts();

    await ports.workflow.continue(
      {
        authority: { workflowId: "workflow-1", expectedRevision: 7 },
        goal: "dry_run",
        intent: { noSeed: true },
        idempotencyKey: "dry-run-1",
      },
      new AbortController().signal,
    );

    expect(requests).toEqual([
      {
        method: "ContinueReleaseWorkflow",
        body: {
          authority: { workflowId: "workflow-1", expectedRevision: 7 },
          goal: "dry_run",
          intent: { noSeed: true },
          idempotencyKey: "dry-run-1",
        },
      },
    ]);
  });

  it("binds image-host upload to exact workflow media", async () => {
    const requests: Array<{ method: string; body: unknown }> = [];
    setAppRequestHandlerForTests(async (method, body) => {
      requests.push({ method, body });
      return { workflow: { id: "workflow-1", revision: 8 } };
    });
    const ports = productionReleaseSessionPorts();

    const current = {
      workflow: { id: "workflow-1", revision: 7 },
      media: { id: "media-1", revision: 6 },
    } as ReleaseWorkflowCurrent;
    await ports.workflow.uploadImages(
      current,
      ["artifact-1"],
      "image-upload-1",
      new AbortController().signal,
    );

    expect(requests).toEqual([
      expect.objectContaining({
        method: "UploadReleaseWorkflowImages",
        body: {
          workflowId: "workflow-1",
          expectedRevision: 7,
          media: { id: "media-1", revision: 6 },
          artifactIds: ["artifact-1"],
          idempotencyKey: "image-upload-1",
        },
      }),
    ]);
  });

  it("builds opaque workflow media URLs under the configured base path", () => {
    window.__UPBRR_BASE_URL__ = "/upbrr/";
    const ports = productionReleaseSessionPorts();
    const current = {
      workflow: { id: "workflow 1" },
      media: { id: "media/1", revision: 4 },
    } as ReleaseWorkflowCurrent;

    expect(ports.workflow.mediaURL(current, "artifact?1")).toBe(
      "/upbrr/api/app/release-workflow-media?workflowId=workflow+1&mediaId=media%2F1&mediaRevision=4&artifactId=artifact%3F1",
    );
  });

  it("uses both input-change hints and event-stream reconnects to request resync", async () => {
    const verification = vi.fn();
    const encoder = new TextEncoder();
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(
          encoder.encode(
            'event: input:verification\ndata: {"correlationID":"prepare-1","phase":"source_inspection","status":"running","message":"Verifying source content.","completedBytes":50,"totalBytes":100}\n\n',
          ),
        );
        controller.enqueue(encoder.encode('event: input:changed\ndata: {"revision":2}\n\n'));
      },
    });
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          new Response(stream, { headers: { "Content-Type": "text/event-stream" } }),
        ),
    );
    initializeWebClient("csrf-token", true);
    const resync = vi.fn();
    const unsubscribe = productionReleaseSessionPorts().activeInput.subscribe(resync, verification);

    await vi.waitFor(() => expect(resync).toHaveBeenCalledTimes(2));
    expect(verification).toHaveBeenCalledWith({
      correlationID: "prepare-1",
      completedBytes: 50,
      totalBytes: 100,
      message: "Verifying source content.",
      status: "running",
    });
    unsubscribe();
  });
});
