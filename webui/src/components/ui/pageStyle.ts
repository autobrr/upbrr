// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

/** Visual roles repeated across the route pages. */
export const pageStyle = {
  panel:
    "relative z-[1] rounded-[var(--radius)] border border-border bg-card p-[14px] text-card-foreground shadow-[var(--shadow)]",
  eyebrow: "mb-1.5 text-xs tracking-[0.3em] text-primary-text uppercase",
  subtitle: "m-0 text-base text-muted-foreground",
  label: "text-[0.85rem] text-muted-foreground",
  value: "text-[1.1rem] font-semibold",
  error: "mt-[9px] text-destructive-text",
} as const;
