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
        "inline-flex h-8 shrink-0 items-center justify-center rounded-md border px-3 text-sm font-medium shadow-sm transition-colors disabled:cursor-not-allowed disabled:opacity-50",
        variant === "primary"
          ? "border-transparent bg-blue-600 text-white hover:bg-blue-700"
          : "border-[var(--input-border)] bg-[var(--panel)] text-[var(--text)] hover:bg-[var(--hover)]",
        className,
      )}
      {...props}
    />
  );
}
