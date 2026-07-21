// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ComponentPropsWithoutRef, ReactNode } from "react";
import * as CheckboxPrimitive from "@radix-ui/react-checkbox";
import { cn } from "../../utils/cn";

type CheckboxChangeEvent = {
  target: {
    checked: boolean;
  };
};

type CheckboxProps = Omit<
  ComponentPropsWithoutRef<typeof CheckboxPrimitive.Root>,
  "checked" | "onChange" | "onCheckedChange"
> & {
  checked?: boolean;
  onChange?: (event: CheckboxChangeEvent) => void;
  onCheckedChange?: (checked: boolean) => void;
};

export function Checkbox({
  className,
  checked = false,
  onChange,
  onCheckedChange,
  ...props
}: Readonly<CheckboxProps>) {
  return (
    <CheckboxPrimitive.Root
      className={cn(
        "inline-flex h-4 w-4 shrink-0 cursor-pointer items-center justify-center rounded border border-[var(--input-border)] bg-[var(--input-bg)] text-white transition data-[state=checked]:border-blue-600 data-[state=checked]:bg-blue-600 disabled:cursor-not-allowed disabled:opacity-50",
        className,
      )}
      checked={checked}
      onCheckedChange={(nextChecked) => {
        const resolvedChecked = nextChecked === true;
        onCheckedChange?.(resolvedChecked);
        onChange?.({ target: { checked: resolvedChecked } });
      }}
      {...props}
    >
      <CheckboxPrimitive.Indicator asChild>
        <svg aria-hidden="true" width="12" height="12" viewBox="0 0 12 12" fill="none">
          <path
            d="M9.75 3.25 5 8 2.25 5.25"
            stroke="currentColor"
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth="1.8"
          />
        </svg>
      </CheckboxPrimitive.Indicator>
    </CheckboxPrimitive.Root>
  );
}

type PillCheckboxProps = Omit<
  ComponentPropsWithoutRef<typeof CheckboxPrimitive.Root>,
  "checked" | "onCheckedChange"
> & {
  checked?: boolean;
  children: ReactNode;
  onCheckedChange?: (checked: boolean) => void;
};

export function PillCheckbox({
  className,
  checked = false,
  children,
  onCheckedChange,
  ...props
}: Readonly<PillCheckboxProps>) {
  return (
    <CheckboxPrimitive.Root
      className={cn(
        "tracker-pill inline-flex min-h-6 cursor-pointer select-none items-center justify-center rounded-full border border-[var(--border)] bg-[var(--panel-light)] px-2.5 py-1 text-[0.78rem] font-semibold leading-none text-[var(--muted)] transition data-[state=checked]:border-[var(--accent-2)] data-[state=checked]:bg-blue-500/15 data-[state=checked]:text-[var(--text)]",
        className,
      )}
      checked={checked}
      onCheckedChange={(nextChecked) => onCheckedChange?.(nextChecked === true)}
      {...props}
    >
      {children}
    </CheckboxPrimitive.Root>
  );
}
