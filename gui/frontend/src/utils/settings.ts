// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ConfigMap, ConfigValue } from "../types";

export const formatLabel = (value: string) => {
  if (value.includes("_")) {
    return value.replaceAll(/_/g, " ");
  }
  return value
    .replaceAll(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replaceAll(/([A-Z])([A-Z][a-z])/g, "$1 $2");
};

export const normalizeDefaultTrackerList = (value: ConfigValue): string[] => {
  if (Array.isArray(value)) {
    return value.map((entry) => String(entry ?? "").trim()).filter(Boolean);
  }
  if (typeof value === "string") {
    return value
      .split(",")
      .map((entry) => entry.trim())
      .filter(Boolean);
  }
  return [];
};

export const trackerHasDetails = (value: ConfigValue): boolean => {
  if (value === null || value === undefined) return false;
  if (typeof value === "string") return value.trim().length > 0;
  if (typeof value === "number") return value > 0;
  if (typeof value === "boolean") return value;
  if (Array.isArray(value)) {
    return value.some((entry) => trackerHasDetails(entry));
  }
  if (typeof value === "object") {
    return Object.values(value as ConfigMap).some((entry) => trackerHasDetails(entry));
  }
  return false;
};
