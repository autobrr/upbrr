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
        tone === "neutral" && "border border-slate-500/35 bg-slate-700/45 text-slate-200",
        tone === "info" && "border border-cyan-400/35 bg-cyan-500/15 text-cyan-100",
        tone === "danger" && "border border-red-400/35 bg-red-500/15 text-red-100",
        className,
      )}
      {...props}
    />
  );
}
