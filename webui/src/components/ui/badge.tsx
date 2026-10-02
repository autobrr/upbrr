// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { HTMLAttributes } from "react";
import { cn } from "../../utils/cn";

type BadgeProps = HTMLAttributes<HTMLSpanElement> & {
  tone?: "neutral" | "info" | "danger";
};

export function Badge({ className, tone = "neutral", ...props }: Readonly<BadgeProps>) {
  return (
    <span
      className={cn(
        "inline-flex h-5 items-center rounded px-1.5 text-[11px] font-semibold leading-none",
        tone === "neutral" && "border border-border bg-secondary text-secondary-foreground",
        tone === "info" && "border border-primary bg-primary/10 text-foreground",
        tone === "danger" && "border border-destructive bg-destructive/10 text-destructive-text",
        className,
      )}
      {...props}
    />
  );
}
