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

export const isSkipAutoTorrentEnabled = (configData?: ConfigMap | null) => {
  const metadata = configData?.Metadata;
  if (!metadata || typeof metadata !== "object" || Array.isArray(metadata)) {
    return false;
  }
  return Boolean((metadata as ConfigMap).SkipAutoTorrent);
};
