// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

/**
 * Extracts the hostname from a tracker URL or returns the raw value if parsing fails.
 * Always returns lowercase.
 */
function extractTrackerHost(urlStr: string | undefined | null): string {
  if (!urlStr) {
    return "";
  }

  const trimmed = urlStr.trim();
  if (!trimmed) {
    return "";
  }

  // Prepend "//" if no scheme is present to enable proper URL hostname parsing
  const urlString = trimmed.includes("://") ? trimmed : `//${trimmed}`;
  try {
    const url = new URL(urlString, "http://placeholder");
    return url.hostname.toLowerCase();
  } catch {
    // Fall back to manual extraction
  }

  // Manual extraction: strip path and port
  let host = trimmed.toLowerCase();
  const pathIndex = host.indexOf("/");
  if (pathIndex !== -1) {
    host = host.substring(0, pathIndex);
  }
  const portIndex = host.lastIndexOf(":");
  if (portIndex !== -1) {
    host = host.substring(0, portIndex);
  }
  return host;
}

/**
 * Resolves the primary domain or name for a tracker.
 * Prioritizes the tracker's custom base URL, falling back to the clean tracker name.
 * The backend dynamically resolves the name to the appropriate default URL and domain.
 */
export function getTrackerDomain(trackerName: string, customUrl?: string): string {
  if (customUrl) {
    const host = extractTrackerHost(customUrl);
    if (host) {
      return host;
    }
  }

  return trackerName.trim();
}
