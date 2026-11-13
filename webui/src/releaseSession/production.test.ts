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

  it("binds comparison approval to exact media and preview to its separate source", async () => {
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
    const instructions = {
      action: "approve",
      expectedFingerprint: "review-1",
      plan: {
        imported: true,
        sources: [],
        rows: [],
        mainSourceId: "2",
        reuseMainScreenshots: false,
        preferences: { count: 1, darkPercent: 0, motionPercent: 0 },
        fingerprint: "review-1",
      },
    };
    await ports.workflow.editComparisons(
      current,
      instructions,
      "approve-1",
      new AbortController().signal,
    );
    await ports.workflow.previewFrame(
      current,
      "",
      12,
      "preview-1",
      new AbortController().signal,
      "source-2",
      -1,
    );
    expect(requests).toEqual([
      {
        method: "EditReleaseWorkflowComparisons",
        body: {
          workflowId: "workflow-1",
          expectedRevision: 7,
          media: { id: "media-1", revision: 6 },
          instructions,
          idempotencyKey: "approve-1",
        },
      },
      {
        method: "PreviewReleaseWorkflowFrame",
        body: {
          workflowId: "workflow-1",
          expectedRevision: 7,
          comparisonSourceId: "source-2",
          comparisonStepDirection: -1,
          timestampSeconds: 12,
          idempotencyKey: "preview-1",
        },
      },
    ]);
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
