// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ButtonHTMLAttributes } from "react";
import { cn } from "../../utils/cn";

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "secondary";
};

export function Button({ className, variant = "secondary", ...props }: Readonly<ButtonProps>) {
  return (
    <button
      className={cn(
        "inline-flex h-8 shrink-0 items-center justify-center rounded-md border px-3 text-sm font-semibold transition disabled:cursor-not-allowed disabled:opacity-50",
        variant === "primary"
          ? "border-transparent bg-[var(--accent)] text-slate-950 shadow-[0_8px_24px_rgba(245,185,66,0.22)] hover:brightness-110"
          : "border-white/10 bg-white/5 text-[var(--text)] hover:bg-white/10",
        className,
      )}
      {...props}
    />
  );
}
