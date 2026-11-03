// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useId, useRef, useState } from "react";
import type { ReactNode } from "react";
import * as Tooltip from "@radix-ui/react-tooltip";

/** Floating optional help. Pass id to describe a control via aria-describedby; keep warnings inline. */
export function FieldHelp({
  label,
  id,
  children,
}: Readonly<{ label: string; id?: string; children: ReactNode }>) {
  const generatedId = useId();
  const descriptionId = id ?? generatedId;
  const [open, setOpen] = useState(false);
  const pointerType = useRef<string | null>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  return (
    <Tooltip.Provider delayDuration={300}>
      <Tooltip.Root open={open} onOpenChange={setOpen}>
        <span className="inline-flex shrink-0 self-start align-middle">
          <Tooltip.Trigger
            ref={trigger}
            type="button"
            aria-label={`Help for ${label}`}
            aria-expanded={open}
            className="inline-flex size-6 shrink-0 items-center justify-center rounded p-0 text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
            onPointerDown={(event) => {
              pointerType.current = event.pointerType;
              if (event.pointerType === "touch") event.preventDefault();
            }}
            onKeyDown={() => {
              pointerType.current = null;
            }}
            onClick={(event) => {
              if (pointerType.current !== "touch") return;
              event.preventDefault();
              setOpen((previous) => !previous);
            }}
          >
            <svg
              aria-hidden="true"
              width="15"
              height="15"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
            >
              <circle cx="12" cy="12" r="9" />
              <path d="M12 11v6M12 7h.01" />
            </svg>
          </Tooltip.Trigger>
          <span id={descriptionId} hidden>
            {children}
          </span>
        </span>
        <Tooltip.Portal>
          <Tooltip.Content
            data-testid="field-help-content"
            sideOffset={6}
            collisionPadding={8}
            onPointerDownOutside={(event) => {
              if (trigger.current?.contains(event.detail.originalEvent.target as Node)) {
                event.preventDefault();
              }
            }}
            className="z-[1300] max-h-[var(--radix-tooltip-content-available-height)] max-w-[min(20rem,calc(100vw-1rem))] overflow-y-auto rounded-md border border-border bg-popover px-3 py-2 text-sm leading-5 font-normal text-popover-foreground shadow-md [overflow-wrap:anywhere]"
          >
            {children}
          </Tooltip.Content>
        </Tooltip.Portal>
      </Tooltip.Root>
    </Tooltip.Provider>
  );
}
