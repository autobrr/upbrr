// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { dupeClient, jobsClient, uploadClient } from "../api/app";
import { subscribeWebEvent } from "../api/client";
import type { OwnerJobSnapshot } from "../types";
import type { JobRegistryTransport } from "./types";

export const productionJobRegistryTransport = (): JobRegistryTransport => ({
  list: jobsClient.list,
  subscribe: (listener) =>
    subscribeWebEvent("jobs:update", (payload) => listener(payload as OwnerJobSnapshot)),
  startDupe: (command, correlationID) =>
    dupeClient.start(command.release, [...command.trackers], correlationID),
  startUpload: uploadClient.startReviewed,
  retryUpload: uploadClient.retryFailed,
  cancelDupe: dupeClient.cancel,
  cancelUpload: uploadClient.cancel,
});
