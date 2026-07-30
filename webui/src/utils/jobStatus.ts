// SPDX-License-Identifier: GPL-2.0-or-later

/** Normalizes backend job status values while preserving non-nullish falsy values. */
export const normalizeJobStatus = (status: unknown) =>
  String(status ?? "")
    .toLowerCase()
    .trim();
