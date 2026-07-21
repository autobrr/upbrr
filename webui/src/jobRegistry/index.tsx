// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ReactNode } from "react";
import { createContext, useContext, useEffect, useMemo, useSyncExternalStore } from "react";
import type { OwnerJobSnapshot, ReleaseRef } from "../types";
import { JobRegistryCoordinator } from "./coordinator";
import { productionJobRegistryTransport } from "./production";
import type { DupeStartCommand, JobRegistryState, JobRegistryTransport } from "./types";

const RegistryContext = createContext<JobRegistryCoordinator | null>(null);

export function JobRegistryProvider({
  ownerKey,
  transport,
  children,
}: Readonly<{
  ownerKey: string;
  transport?: JobRegistryTransport;
  children: ReactNode;
}>) {
  const coordinator = useMemo(
    () => new JobRegistryCoordinator(ownerKey, transport ?? productionJobRegistryTransport()),
    [ownerKey, transport],
  );
  useEffect(() => {
    coordinator.start();
    return () => coordinator.dispose();
  }, [coordinator]);
  return <RegistryContext.Provider value={coordinator}>{children}</RegistryContext.Provider>;
}

const useCoordinatorState = (): [JobRegistryCoordinator, JobRegistryState] => {
  const coordinator = useContext(RegistryContext);
  if (!coordinator) throw new Error("JobRegistryProvider is required");
  const state = useSyncExternalStore(
    (listener) => coordinator.subscribe(listener),
    () => coordinator.snapshot(),
    () => coordinator.snapshot(),
  );
  return [coordinator, state];
};

const isSameRelease = (left: ReleaseRef, right: ReleaseRef) =>
  left.SourcePath === right.SourcePath && left.Generation === right.Generation;

/** Current exact-generation duplicate/upload Jobs and narrow lifecycle commands. */
export const useReleaseJobs = (release: ReleaseRef) => {
  const [coordinator, state] = useCoordinatorState();
  const releaseJobs = state.jobs.filter((job) => isSameRelease(job.release, release));
  const dupeJobs = releaseJobs.filter(
    (job): job is Extract<OwnerJobSnapshot, { kind: "duplicate_check" }> =>
      job.kind === "duplicate_check",
  );
  const uploadJobs = releaseJobs.filter(
    (job): job is Extract<OwnerJobSnapshot, { kind: "tracker_upload" }> =>
      job.kind === "tracker_upload",
  );
  const dupe = dupeJobs[dupeJobs.length - 1];
  const upload = uploadJobs[uploadJobs.length - 1];
  return {
    dupe,
    upload,
    pending: state.pending.filter((job) => isSameRelease(job.release, release)),
    bootstrapped: state.bootstrapped,
    transientError: state.transientError,
    startDupe: (command: Omit<DupeStartCommand, "release">) =>
      coordinator.startDupe({ ...command, release }),
    startUpload: (token: string) => coordinator.startUpload(release, token),
    retryUpload: (job: OwnerJobSnapshot) => coordinator.retryUpload(job),
    cancel: (job: OwnerJobSnapshot) => coordinator.cancel(job),
  } as const;
};

/** Immutable retained Job history for shell notifications. */
export const useJobNotifications = () => {
  const [, state] = useCoordinatorState();
  return {
    jobs: state.jobs,
    pending: state.pending,
    bootstrapped: state.bootstrapped,
    transientError: state.transientError,
  } as const;
};

export type { JobRegistryTransport } from "./types";
