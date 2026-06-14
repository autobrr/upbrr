// SPDX-License-Identifier: GPL-2.0-or-later

/** Normalizes backend job status values before UI state comparisons. */
export const normalizeJobStatus = (status: unknown) =>
  String(status || "")
    .toLowerCase()
    .trim();
