// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { SelectHTMLAttributes } from "react";
import { cn } from "../../utils/cn";

/** The shared native single-choice control keeps browser keyboard and form semantics. */
export function Select({ className, ...props }: Readonly<SelectHTMLAttributes<HTMLSelectElement>>) {
  return (
    <select
      className={cn(
        "h-9 min-w-0 w-full rounded-md border border-input bg-card px-3 text-sm text-card-foreground shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50",
        className,
      )}
      {...props}
    />
  );
}
