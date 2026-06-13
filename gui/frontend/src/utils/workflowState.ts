// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { UIState } from "../types";

/** Normalizes workflow source paths for comparison without resolving them on the host filesystem. */
export const normalizeWorkflowSourcePath = (value?: string | null) => {
  const normalized = String(value || "")
    .trim()
    .replaceAll("\\", "/")
    .toLowerCase();
  if (normalized === "/" || /^[a-z]:\/$/iu.test(normalized)) {
    return normalized;
  }
  return normalized.replace(/\/+$/u, "");
};

const isAbsoluteWorkflowPath = (value: string) =>
  /^[a-z]:\//iu.test(value) || value.startsWith("/") || value.startsWith("//");

const pathSegments = (value: string) => value.split("/").filter(Boolean);

const pathEndsWithSegments = (absolutePath: string, relativePath: string) => {
  const absoluteParts = pathSegments(absolutePath);
  const relativeParts = pathSegments(relativePath);
  if (relativeParts.length === 0 || relativeParts.length > absoluteParts.length) {
    return false;
  }
  return relativeParts.every(
    (part, index) => absoluteParts[absoluteParts.length - relativeParts.length + index] === part,
  );
};

const stateStringAt = (state: UIState | undefined | null, ...path: string[]) => {
  let current: unknown = state || {};
  for (const key of path) {
    if (!current || typeof current !== "object" || Array.isArray(current)) {
      return "";
    }
    current = (current as Record<string, unknown>)[key];
  }
  return typeof current === "string" ? current : "";
};

/** Returns the normalized source identity used to match persisted workflow state records. */
export const workflowStateSourceKey = (state?: UIState | null) =>
  normalizeWorkflowSourcePath(workflowStateSourcePath(state));

/**
 * Returns the best raw source path in a workflow state.
 *
 * Pathless legacy records fall back to source-bearing workflow snapshots so restore can keep the
 * workflow scoped to the original source instead of resetting to an empty path.
 */
export const workflowStateSourcePath = (state?: UIState | null) => {
  const currentPath = stateStringAt(state, "path");
  if (currentPath.trim()) {
    return currentPath;
  }
  const raw =
    stateStringAt(state, "preview", "SourcePath") ||
    stateStringAt(state, "dupeSummary", "SourcePath") ||
    stateStringAt(state, "dupeCheckSnapshot", "sourcePath") ||
    stateStringAt(state, "prepPreview", "SourcePath") ||
    stateStringAt(state, "trackerDryRunPreview", "SourcePath") ||
    stateStringAt(state, "trackerUploadSnapshot", "sourcePath");
  return raw;
};

/**
 * Compares source paths for live workflow freshness checks.
 *
 * This accepts absolute-to-relative suffix matches for backend responses that normalize a user's
 * relative input to an absolute host path. It should not be used as a durable storage key.
 */
export const sourcePathMatches = (currentPath: string, candidatePath?: string | null) => {
  const current = normalizeWorkflowSourcePath(currentPath);
  const candidate = normalizeWorkflowSourcePath(candidatePath);
  if (!current || !candidate) {
    return false;
  }
  if (current === candidate) {
    return true;
  }
  const currentAbsolute = isAbsoluteWorkflowPath(current);
  const candidateAbsolute = isAbsoluteWorkflowPath(candidate);
  if (currentAbsolute && candidateAbsolute) {
    return false;
  }
  if (!currentAbsolute && !candidateAbsolute) {
    return false;
  }
  return currentAbsolute
    ? pathEndsWithSegments(current, candidate)
    : pathEndsWithSegments(candidate, current);
};

/** Compares source paths for exact durable identity after normalization. */
export const sourcePathEquals = (currentPath: string, candidatePath?: string | null) => {
  const current = normalizeWorkflowSourcePath(currentPath);
  const candidate = normalizeWorkflowSourcePath(candidatePath);
  return Boolean(current && candidate && current === candidate);
};

/** Reports whether a workflow state contains preview data for a different source path. */
export const hasConflictingPreviewSource = (state: UIState) => {
  const currentPath = state.path;
  const previewPath = state.preview?.SourcePath;
  return Boolean(currentPath && previewPath && !sourcePathMatches(currentPath, previewPath));
};
