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
        "inline-flex h-5 w-5 shrink-0 cursor-pointer items-center justify-center rounded border border-input bg-card p-0 text-card-foreground transition data-[state=checked]:border-primary data-[state=checked]:bg-primary data-[state=checked]:text-primary-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-50",
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
        "tracker-pill inline-flex min-h-10 min-w-0 cursor-pointer select-none items-center justify-start gap-2 rounded-md border border-border bg-card px-3 py-2 text-left text-sm font-medium leading-tight text-card-foreground transition hover:bg-accent hover:text-accent-foreground data-[state=checked]:border-primary data-[state=checked]:bg-accent data-[state=checked]:text-accent-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring",
        className,
      )}
      checked={checked}
      onCheckedChange={(nextChecked) => onCheckedChange?.(nextChecked === true)}
      {...props}
    >
      <span className="flex h-4 w-4 shrink-0 items-center justify-center rounded border border-current">
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
      </span>
      <span className="min-w-0 [overflow-wrap:anywhere]">{children}</span>
    </CheckboxPrimitive.Root>
  );
}
