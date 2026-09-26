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
        "inline-flex h-6 w-11 shrink-0 cursor-pointer items-center rounded-full border border-input bg-muted p-0 transition data-[state=checked]:border-primary data-[state=checked]:bg-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-50 hover:![transform:none]",
        className,
      )}
      onCheckedChange={(checked) => {
        onCheckedChange?.(checked);
        onChange?.({ target: { checked } });
      }}
      {...props}
    >
      <SwitchPrimitive.Thumb className="pointer-events-none block h-5 w-5 rounded-full bg-foreground [transform:translateX(2px)] transition-transform data-[state=checked]:bg-primary-foreground data-[state=checked]:[transform:translateX(20px)]" />
    </SwitchPrimitive.Root>
  );
}
