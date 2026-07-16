// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ComponentPropsWithoutRef } from "react";
import * as SwitchPrimitive from "@radix-ui/react-switch";
import { cn } from "../../utils/cn";

type SwitchChangeEvent = {
  target: {
    checked: boolean;
  };
};

type SwitchProps = Omit<
  ComponentPropsWithoutRef<typeof SwitchPrimitive.Root>,
  "onChange" | "onCheckedChange"
> & {
  onChange?: (event: SwitchChangeEvent) => void;
  onCheckedChange?: (checked: boolean) => void;
};

export function Switch({ className, onChange, onCheckedChange, ...props }: Readonly<SwitchProps>) {
  return (
    <SwitchPrimitive.Root
      className={cn(
        "inline-flex h-4 w-8 shrink-0 cursor-pointer items-center rounded-full border border-white/20 bg-white/10 p-0 transition data-[state=checked]:border-[var(--accent-2)] data-[state=checked]:bg-[rgba(53,194,193,0.35)] disabled:cursor-not-allowed disabled:opacity-50 hover:![transform:none]",
        className,
      )}
      onCheckedChange={(checked) => {
        onCheckedChange?.(checked);
        onChange?.({ target: { checked } });
      }}
      {...props}
    >
      <SwitchPrimitive.Thumb className="pointer-events-none block h-3 w-3 rounded-full bg-white/60 [transform:translateX(2px)] transition-transform data-[state=checked]:bg-[var(--accent-2)] data-[state=checked]:[transform:translateX(18px)]" />
    </SwitchPrimitive.Root>
  );
}
