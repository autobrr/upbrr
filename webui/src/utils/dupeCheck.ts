// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { DupeCheckResult } from "../types";

type DupeSkipReasonSource = Pick<Partial<DupeCheckResult>, "SkipReason">;

type DupeRuleSkipSource = DupeSkipReasonSource &
  Pick<Partial<DupeCheckResult>, "Skipped" | "SkipCode" | "SkipRules">;

/** Returns the displayable skip reason carried by a duplicate-check result. */
export const dupeSkipReason = (result: DupeSkipReasonSource) =>
  String(result.SkipReason || "").trim();

/** Reports rule-validation skips from structural outcome fields. */
export const isRuleSkippedResult = (result: DupeRuleSkipSource) => {
  if (!result.Skipped) return false;
  if (String(result.SkipCode || "").trim() === "rule_failed") return true;
  if ((result.SkipRules || []).some((rule) => String(rule).trim() !== "")) {
    return true;
  }
  return false;
};
