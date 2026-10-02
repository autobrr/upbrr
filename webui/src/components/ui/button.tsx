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
        "inline-flex min-h-9 shrink-0 items-center justify-center rounded-md border px-3 py-1 text-sm font-semibold transition disabled:cursor-not-allowed disabled:opacity-50",
        variant === "primary"
          ? "border-primary bg-primary text-primary-foreground hover:brightness-110"
          : "border-border bg-secondary text-secondary-foreground hover:bg-accent hover:text-accent-foreground",
        className,
      )}
      {...props}
    />
  );
}
