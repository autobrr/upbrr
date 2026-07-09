// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { DupeCheckResult } from "../types";

type DupeSkipReasonSource = Pick<Partial<DupeCheckResult>, "SkipReason" | "Notes">;

type DupeRuleSkipSource = DupeSkipReasonSource &
  Pick<Partial<DupeCheckResult>, "Skipped" | "SkipRules">;

const isRuleFailureReason = (reason: string) =>
  reason.toLowerCase().trim().startsWith("rule check failed");

/** Returns the displayable skip reason carried by a duplicate-check result. */
export const dupeSkipReason = (result: DupeSkipReasonSource) =>
  String(result.SkipReason || result.Notes?.join(" ") || "").trim();

/** Reports rule-validation skips from structured SkipRules, with legacy reason text as fallback. */
export const isRuleSkippedResult = (result: DupeRuleSkipSource) => {
  if (!result.Skipped) return false;
  if ((result.SkipRules || []).some((rule) => String(rule).trim() !== "")) {
    return true;
  }
  return isRuleFailureReason(dupeSkipReason(result));
};
