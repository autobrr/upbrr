// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

/**
 * Returns the comparable display form used for metadata progress paths.
 *
 * The value is trimmed, slash-normalized, and cleaned of `.`/`..` segments while
 * preserving case. Callers that need OS-specific identity rules should use
 * `isMetadataProgressPathMatch`.
 */
export const normalizeMetadataProgressPath = (value: string) =>
  cleanPath(value.trim().replaceAll("\\", "/"));

const windowsDrivePathPattern = /^[a-z]:(?:\/|$)/i;

const isWindowsPath = (value: string) =>
  windowsDrivePathPattern.test(value) || value.startsWith("//");

const cleanPath = (value: string) => {
  if (!value) {
    return "";
  }

  const isAbsolute = value.startsWith("/") || windowsDrivePathPattern.test(value);
  const parts = value.split("/");
  const cleanParts: string[] = [];

  for (const part of parts) {
    if (!part || part === ".") {
      continue;
    }
    if (part === "..") {
      const previous = cleanParts[cleanParts.length - 1];
      if (previous && previous !== ".." && !previous.endsWith(":")) {
        cleanParts.pop();
      } else if (!isAbsolute) {
        cleanParts.push(part);
      }
      continue;
    }
    cleanParts.push(part);
  }

  if (value.startsWith("//")) {
    return `//${cleanParts.join("/")}`;
  }
  if (value.startsWith("/")) {
    return `/${cleanParts.join("/")}`;
  }
  return cleanParts.join("/");
};

const metadataProgressPathIdentity = (value: string) => {
  const normalized = normalizeMetadataProgressPath(value);
  return isWindowsPath(normalized) ? normalized.toLocaleLowerCase("en-US") : normalized;
};

/**
 * Returns whether a progress event belongs to the active metadata request.
 *
 * Empty targets accept any event. Non-empty targets use the cleaned path form
 * plus Windows-aware case folding for drive and UNC paths, which lets
 * backend-canonical progress events attach to the active request without
 * accepting unrelated paths.
 */
export const isMetadataProgressPathMatch = (eventPath: string, progressTarget: string) => {
  const normalizedTarget = metadataProgressPathIdentity(progressTarget);
  if (!normalizedTarget) {
    return true;
  }
  return metadataProgressPathIdentity(eventPath) === normalizedTarget;
};
