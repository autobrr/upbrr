// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { InputHTMLAttributes } from "react";
import { cn } from "../../utils/cn";

type SwitchProps = Omit<InputHTMLAttributes<HTMLInputElement>, "type">;

export function Switch({ className, ...props }: Readonly<SwitchProps>) {
  return (
    <label className={cn("relative inline-flex h-4 w-8 shrink-0 cursor-pointer", className)}>
      <input className="peer sr-only" type="checkbox" {...props} />
      <span className="h-4 w-8 rounded-full border border-white/20 bg-white/10 transition peer-checked:border-[var(--accent-2)] peer-checked:bg-[rgba(53,194,193,0.35)]" />
      <span className="pointer-events-none absolute left-0.5 top-0.5 h-3 w-3 rounded-full bg-white/60 transition peer-checked:translate-x-4 peer-checked:bg-[var(--accent-2)]" />
    </label>
  );
}
