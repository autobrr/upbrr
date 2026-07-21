// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { OwnerJobSnapshot, ReleaseRef } from "../types";

export type DupeStartCommand = Readonly<{
  release: ReleaseRef;
  trackers: readonly string[];
}>;

export type PendingJobStart = Readonly<{
  kind: OwnerJobSnapshot["kind"];
  correlationID: string;
  release: ReleaseRef;
  retryOf?: string;
  acceptedJobID?: string;
}>;

export type JobRegistryState = Readonly<{
  jobs: readonly OwnerJobSnapshot[];
  pending: readonly PendingJobStart[];
  bootstrapped: boolean;
  transientError: string;
}>;

export type JobRegistryTransport = Readonly<{
  list: () => Promise<OwnerJobSnapshot[]>;
  subscribe: (listener: (snapshot: OwnerJobSnapshot) => void) => () => void;
  startDupe: (command: DupeStartCommand, correlationID: string) => Promise<string>;
  startUpload: (token: string, correlationID: string) => Promise<string>;
  retryUpload: (jobID: string, correlationID: string) => Promise<string>;
  cancelDupe: (jobID: string) => Promise<void>;
  cancelUpload: (jobID: string) => Promise<void>;
}>;

export type JobRegistryClock = Readonly<{
  setInterval: (callback: () => void, delayMs: number) => unknown;
  clearInterval: (handle: unknown) => void;
}>;
