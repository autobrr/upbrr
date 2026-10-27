// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useId, useState } from "react";
import type { ReactNode } from "react";

/** Inline help stays in document flow so it cannot cover inputs or validation messages. */
export function FieldHelp({
  label,
  id,
  children,
}: Readonly<{ label: string; id?: string; children: ReactNode }>) {
  const generatedId = useId();
  const helpId = id ?? generatedId;
  const [open, setOpen] = useState(false);
  const [pinned, setPinned] = useState(false);
  useEffect(() => {
    if (!open) return;
    const dismiss = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      setOpen(false);
      setPinned(false);
    };
    document.addEventListener("keydown", dismiss);
    return () => document.removeEventListener("keydown", dismiss);
  }, [open]);
  return (
    <span
      className="inline-flex min-w-0 flex-wrap items-center gap-1"
      onPointerEnter={(event) => {
        if (event.pointerType !== "touch") setOpen(true);
      }}
      onPointerLeave={(event) => {
        if (!pinned && !event.currentTarget.contains(document.activeElement)) setOpen(false);
      }}
    >
      <button
        type="button"
        aria-label={`Help for ${label}`}
        aria-controls={helpId}
        aria-expanded={open}
        className="inline-flex size-7 shrink-0 items-center justify-center rounded-full border border-border text-sm font-semibold text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
        onFocus={() => setOpen(true)}
        onBlur={() => {
          setOpen(false);
          setPinned(false);
        }}
        onClick={() => {
          setOpen(!pinned);
          setPinned(!pinned);
        }}
      >
        <span aria-hidden="true">?</span>
      </button>
      <span
        id={helpId}
        hidden={!open}
        className="w-full rounded-md border border-border bg-muted p-2 text-sm font-normal text-muted-foreground [overflow-wrap:anywhere]"
      >
        {children}
      </span>
    </span>
  );
}
