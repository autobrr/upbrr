// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { OwnerJobSnapshot, ReleaseRef } from "../types";
import type {
  DupeStartCommand,
  JobRegistryClock,
  JobRegistryState,
  JobRegistryTransport,
  PendingJobStart,
} from "./types";

const defaultClock: JobRegistryClock = {
  setInterval: (callback, delayMs) => globalThis.setInterval(callback, delayMs),
  clearInterval: (handle) => globalThis.clearInterval(handle as ReturnType<typeof setInterval>),
};

const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T;

const compareJobs = (left: OwnerJobSnapshot, right: OwnerJobSnapshot) => {
  const byTime = left.startedAt.localeCompare(right.startedAt);
  return byTime !== 0 ? byTime : left.jobID.localeCompare(right.jobID);
};

const sameRelease = (left: ReleaseRef, right: ReleaseRef) =>
  left.SourcePath === right.SourcePath && left.Generation === right.Generation;

const safeError = (error: unknown) =>
  error instanceof Error && error.message ? error.message : String(error);

export class JobRegistryCoordinator {
  private readonly listeners = new Set<() => void>();
  private readonly jobs = new Map<string, OwnerJobSnapshot>();
  private readonly pending = new Map<string, PendingJobStart>();
  private unsubscribe: (() => void) | null = null;
  private pollHandle: unknown;
  private disposed = false;
  private bootstrapped = false;
  private transientError = "";
  private refreshRevision = 0;
  private currentState: JobRegistryState = {
    jobs: [],
    pending: [],
    bootstrapped: false,
    transientError: "",
  };

  constructor(
    readonly ownerKey: string,
    private readonly transport: JobRegistryTransport,
    private readonly clock: JobRegistryClock = defaultClock,
    private readonly pollIntervalMs = 1500,
    private readonly newCorrelationID: () => string = () =>
      globalThis.crypto?.randomUUID?.() ??
      `job-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`,
  ) {}

  start() {
    if (this.disposed || this.unsubscribe) return;
    this.unsubscribe = this.transport.subscribe((snapshot) => this.applySnapshot(snapshot));
    this.pollHandle = this.clock.setInterval(() => void this.refresh(), this.pollIntervalMs);
    void this.refresh();
  }

  subscribe(listener: () => void) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  snapshot(): JobRegistryState {
    return this.currentState;
  }

  async refresh() {
    const revision = ++this.refreshRevision;
    try {
      const listed = await this.transport.list();
      if (this.disposed || revision !== this.refreshRevision) return;
      this.jobs.clear();
      listed.forEach((job) => this.jobs.set(job.jobID, clone(job)));
      for (const [correlationID] of this.pending) {
        if (listed.some((job) => job.correlationID === correlationID)) {
          this.pending.delete(correlationID);
        }
      }
      this.bootstrapped = true;
      this.transientError = "";
      this.emit();
    } catch (error) {
      if (this.disposed || revision !== this.refreshRevision) return;
      this.bootstrapped = true;
      this.transientError = safeError(error);
      this.emit();
    }
  }

  startDupe(command: DupeStartCommand) {
    return this.startJob("duplicate_check", command.release, (correlationID) =>
      this.transport.startDupe(command, correlationID),
    );
  }

  startUpload(release: ReleaseRef, token: string) {
    return this.startJob("tracker_upload", release, (correlationID) =>
      this.transport.startUpload(token, correlationID),
    );
  }

  retryUpload(job: OwnerJobSnapshot) {
    return this.startJob(
      "tracker_upload",
      job.release,
      (correlationID) => this.transport.retryUpload(job.jobID, correlationID),
      job.jobID,
    );
  }

  cancel(job: OwnerJobSnapshot) {
    return job.kind === "duplicate_check"
      ? this.transport.cancelDupe(job.jobID)
      : this.transport.cancelUpload(job.jobID);
  }

  dispose() {
    if (this.disposed) return;
    this.disposed = true;
    this.refreshRevision++;
    this.unsubscribe?.();
    this.unsubscribe = null;
    if (this.pollHandle !== undefined) this.clock.clearInterval(this.pollHandle);
    this.listeners.clear();
    this.jobs.clear();
    this.pending.clear();
  }

  private async startJob(
    kind: OwnerJobSnapshot["kind"],
    release: ReleaseRef,
    start: (correlationID: string) => Promise<string>,
    retryOf?: string,
  ) {
    const correlationID = this.newCorrelationID();
    this.pending.set(correlationID, { kind, correlationID, release: clone(release), retryOf });
    this.emit();
    try {
      const jobID = await start(correlationID);
      if (this.disposed) return jobID;
      this.pending.set(correlationID, {
        kind,
        correlationID,
        release: clone(release),
        retryOf,
        acceptedJobID: jobID,
      });
      this.emit();
      void this.refresh();
      return jobID;
    } catch (error) {
      await this.refresh();
      const recovered = Array.from(this.jobs.values()).find(
        (job) => job.correlationID === correlationID && sameRelease(job.release, release),
      );
      this.pending.delete(correlationID);
      this.emit();
      if (recovered) return recovered.jobID;
      throw error;
    }
  }

  private applySnapshot(snapshot: OwnerJobSnapshot) {
    if (this.disposed) return;
    this.jobs.set(snapshot.jobID, clone(snapshot));
    this.pending.delete(snapshot.correlationID);
    this.transientError = "";
    this.emit();
  }

  private emit() {
    if (this.disposed) return;
    this.currentState = {
      jobs: clone(Array.from(this.jobs.values()).sort(compareJobs)),
      pending: clone(Array.from(this.pending.values())),
      bootstrapped: this.bootstrapped,
      transientError: this.transientError,
    };
    this.listeners.forEach((listener) => listener());
  }
}
