// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { DupeCheckResult } from "../types";

type DupeSkipReasonSource = Pick<Partial<DupeCheckResult>, "SkipReason">;

/** Returns the displayable skip reason carried by a duplicate-check result. */
export const dupeSkipReason = (result: DupeSkipReasonSource) =>
  String(result.SkipReason || "").trim();
