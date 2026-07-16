// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { describe, expect, it, vi } from "vitest";
import type { OwnerJobSnapshot, ReleaseRef } from "../types";
import { JobRegistryCoordinator } from "./coordinator";
import type { JobRegistryClock, JobRegistryTransport } from "./types";

const release: ReleaseRef = { SourcePath: "C:\\media\\Example", Generation: 3 };

const dupeJob = (
  correlationID = "correlation-1",
): Extract<OwnerJobSnapshot, { kind: "duplicate_check" }> => {
  const dupe = {
    jobID: "dupe-1",
    correlationID,
    release,
    runtimeGeneration: 2,
    status: "running",
    trackers: [],
    completedCount: 0,
    totalCount: 1,
    summary: {
      SourcePath: release.SourcePath,
      Results: [],
      Notes: [],
      Eligibility: { Release: release, Trackers: [], EligibleTrackers: [] },
    },
    startedAt: "2026-07-15T00:00:00Z",
    finishedAt: "",
  };
  return {
    kind: "duplicate_check",
    jobID: dupe.jobID,
    correlationID,
    release,
    status: dupe.status,
    startedAt: dupe.startedAt,
    finishedAt: "",
    dupe,
  };
};

const uploadJob = (
  jobID = "upload-1",
  correlationID = "upload-correlation",
  retryOf?: string,
): Extract<OwnerJobSnapshot, { kind: "tracker_upload" }> => ({
  kind: "tracker_upload",
  jobID,
  correlationID,
  retryOf,
  release,
  status: "failed",
  startedAt: "2026-07-15T00:00:01Z",
  finishedAt: "2026-07-15T00:00:02Z",
  upload: {
    jobID,
    correlationID,
    retryOf,
    release,
    runtimeGeneration: 2,
    status: "failed",
    currentTask: "upload",
    currentTaskStatus: "failed",
    currentMessage: "Upload failed.",
    currentCompletedPieces: 0,
    currentTotalPieces: 1,
    currentPercent: 0,
    currentHashRateMiB: 0,
    trackers: [],
    failedTrackers: ["GRP"],
    uploadedCount: 0,
    startedAt: "2026-07-15T00:00:01Z",
    finishedAt: "2026-07-15T00:00:02Z",
  },
});

class FakeClock implements JobRegistryClock {
  callback: (() => void) | null = null;
  cleared = false;

  setInterval(callback: () => void) {
    this.callback = callback;
    return "poll";
  }

  clearInterval() {
    this.cleared = true;
    this.callback = null;
  }

  tick() {
    this.callback?.();
  }
}

const transportHarness = () => {
  let jobs: OwnerJobSnapshot[] = [];
  let listener: ((snapshot: OwnerJobSnapshot) => void) | null = null;
  const transport = {
    list: vi.fn(async () => jobs),
    subscribe: vi.fn((next) => {
      listener = next;
      return () => {
        listener = null;
      };
    }),
    startDupe: vi.fn(async (_command, correlationID) => {
      jobs = [dupeJob(correlationID)];
      return "dupe-1";
    }),
    startUpload: vi.fn(async (_token: string, _correlationID: string) => "upload-1"),
    retryUpload: vi.fn(async (_jobID: string, _correlationID: string) => "upload-2"),
    cancelDupe: vi.fn(async () => undefined),
    cancelUpload: vi.fn(async () => undefined),
  } satisfies JobRegistryTransport;
  return {
    transport,
    setJobs: (next: OwnerJobSnapshot[]) => {
      jobs = next;
    },
    emit: (snapshot: OwnerJobSnapshot) => listener?.(snapshot),
    hasListener: () => listener !== null,
  };
};

describe("JobRegistryCoordinator", () => {
  it("bootstraps, consumes one SSE stream, and polls through the owner listing", async () => {
    const harness = transportHarness();
    const clock = new FakeClock();
    harness.setJobs([dupeJob()]);
    const registry = new JobRegistryCoordinator(
      "owner-a",
      harness.transport,
      clock,
      10,
      () => "next-correlation",
    );
    registry.start();
    await vi.waitFor(() => expect(registry.snapshot().bootstrapped).toBe(true));
    expect(registry.snapshot().jobs).toHaveLength(1);
    expect(harness.transport.subscribe).toHaveBeenCalledOnce();

    harness.emit({
      ...dupeJob(),
      status: "completed",
      dupe: { ...dupeJob().dupe, status: "completed" },
    });
    expect(registry.snapshot().jobs[0].status).toBe("completed");
    clock.tick();
    await vi.waitFor(() => expect(harness.transport.list).toHaveBeenCalledTimes(2));

    registry.dispose();
    expect(clock.cleared).toBe(true);
    expect(harness.hasListener()).toBe(false);
  });

  it("registers a pending correlation before awaiting start", async () => {
    const harness = transportHarness();
    const registry = new JobRegistryCoordinator(
      "owner-a",
      harness.transport,
      new FakeClock(),
      10,
      () => "pending-correlation",
    );
    harness.transport.startDupe = vi.fn(async (_command, correlationID) => {
      expect(registry.snapshot().pending[0]?.correlationID).toBe(correlationID);
      harness.setJobs([dupeJob(correlationID)]);
      return "dupe-1";
    });
    registry.start();
    await registry.startDupe({ release, trackers: ["AITHER"] });

    await vi.waitFor(() => expect(registry.snapshot().pending).toEqual([]));
    expect(registry.snapshot().jobs[0].correlationID).toBe("pending-correlation");
    registry.dispose();
  });

  it("recovers an accepted start after its response is lost", async () => {
    const harness = transportHarness();
    const registry = new JobRegistryCoordinator(
      "owner-a",
      harness.transport,
      new FakeClock(),
      10,
      () => "lost-correlation",
    );
    harness.transport.startDupe = vi.fn(async (_command, correlationID) => {
      harness.setJobs([dupeJob(correlationID)]);
      throw new Error("connection lost");
    });
    registry.start();

    await expect(registry.startDupe({ release, trackers: ["AITHER"] })).resolves.toBe("dupe-1");
    expect(registry.snapshot().pending).toEqual([]);
    registry.dispose();
  });

  it("keeps transient listing errors separate from canonical Job state", async () => {
    const harness = transportHarness();
    const registry = new JobRegistryCoordinator("owner-a", harness.transport, new FakeClock());
    harness.setJobs([dupeJob()]);
    await registry.refresh();
    harness.transport.list = vi.fn(async () => {
      throw new Error("poll unavailable");
    });
    await registry.refresh();

    expect(registry.snapshot().jobs).toHaveLength(1);
    expect(registry.snapshot().transientError).toBe("poll unavailable");
  });

  it("routes explicit cancellation by Job kind", async () => {
    const harness = transportHarness();
    const registry = new JobRegistryCoordinator("owner-a", harness.transport, new FakeClock());

    await registry.cancel(dupeJob());
    await registry.cancel(uploadJob());

    expect(harness.transport.cancelDupe).toHaveBeenCalledWith("dupe-1");
    expect(harness.transport.cancelUpload).toHaveBeenCalledWith("upload-1");
  });

  it("registers linked upload retries before awaiting the replacement", async () => {
    const harness = transportHarness();
    const registry = new JobRegistryCoordinator(
      "owner-a",
      harness.transport,
      new FakeClock(),
      10,
      () => "retry-correlation",
    );
    harness.transport.retryUpload = vi.fn(async (jobID, correlationID) => {
      expect(registry.snapshot().pending).toEqual([
        expect.objectContaining({
          correlationID,
          retryOf: jobID,
          release,
        }),
      ]);
      return "upload-2";
    });

    await expect(registry.retryUpload(uploadJob())).resolves.toBe("upload-2");
    expect(harness.transport.retryUpload).toHaveBeenCalledWith("upload-1", "retry-correlation");
    expect(registry.snapshot().pending).toEqual([
      expect.objectContaining({
        acceptedJobID: "upload-2",
        retryOf: "upload-1",
      }),
    ]);
    registry.dispose();
  });

  it("rebuilds immutable retained history from owner listing", async () => {
    const harness = transportHarness();
    const retainedUpload = uploadJob();
    const listed: OwnerJobSnapshot[] = [retainedUpload, dupeJob()];
    harness.setJobs(listed);
    const registry = new JobRegistryCoordinator("owner-a", harness.transport, new FakeClock());

    await registry.refresh();
    expect(registry.snapshot().jobs.map((job) => job.jobID)).toEqual(["dupe-1", "upload-1"]);
    retainedUpload.upload.failedTrackers[0] = "mutated";
    expect(
      (registry.snapshot().jobs[1] as Extract<OwnerJobSnapshot, { kind: "tracker_upload" }>).upload
        .failedTrackers,
    ).toEqual(["GRP"]);
  });
});
