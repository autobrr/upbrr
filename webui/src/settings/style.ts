// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

/** Layout shared by schema fields and the hand-built settings sections. */
export const settingsStyle = {
  form: "settings-form flex flex-col gap-5",
  grid: "grid min-w-0 grid-cols-[repeat(auto-fit,minmax(min(100%,18rem),1fr))] gap-x-6 gap-y-5",
  field:
    "settings-field grid min-w-0 content-start gap-1.5 text-sm text-card-foreground [&>*]:min-w-0 [&>span:first-child]:font-medium [&>label:first-child]:font-medium [&>input]:min-h-9 [&>select]:min-h-9",
  switchRow:
    "settings-switch-row settings-field--switch flex min-h-14 cursor-pointer items-center justify-between gap-4 py-2 text-sm text-foreground [&>span]:min-w-0 [&>span]:font-medium",
  toggle:
    "flex min-h-10 cursor-pointer items-center justify-between gap-2 rounded-[10px] border border-foreground/10 bg-card/55 px-[9px] py-[7px] text-[0.88rem] text-foreground",
  subgroup:
    "flex min-w-0 flex-col gap-4 rounded-lg border border-foreground/10 bg-background/50 p-4 sm:p-5",
  card: "settings-card flex flex-col gap-2 rounded-lg border border-foreground/10 bg-foreground/[0.035] p-2.5",
  title: "font-semibold text-foreground",
  collapsible: "flex flex-col gap-2",
  collapsibleCard: "settings-card flex flex-col gap-2",
  summary:
    "flex cursor-pointer list-none items-center justify-between gap-2 rounded-lg border border-foreground/10 bg-foreground/[0.045] p-2.5 font-semibold text-foreground transition-colors [&::-webkit-details-marker]:hidden",
  body: "flex flex-col gap-2 rounded-b-lg border border-t-0 border-foreground/10 bg-foreground/[0.035] p-2.5",
  header: "flex items-center justify-between gap-2",
  map: "grid gap-2",
  mapControls:
    "settings-map__controls flex flex-wrap items-center justify-start gap-2 [&>select]:min-w-[180px]",
  detailGrid: "grid grid-cols-[repeat(auto-fit,minmax(160px,1fr))] gap-[9px]",
  detailCard: "grid min-w-0 gap-1.5 rounded-lg border border-foreground/10 bg-card/50 p-2.5",
  detailLabel: "m-0 text-[0.78rem] tracking-[0.04em] text-muted-foreground uppercase",
  detailValue: "m-0 text-[0.96rem] font-semibold text-foreground [overflow-wrap:anywhere]",
  authBadge:
    "settings-auth-badge inline-flex self-start rounded-full border border-foreground/10 bg-foreground/5 px-[9px] py-[5px] text-[0.8rem] font-semibold text-foreground",
  authReady: "is-ready border-status-success/35 bg-status-success/20",
  authWarning: "is-warning border-status-warning/35 bg-status-warning/20",
  authIdle: "is-idle border-muted-foreground/35 bg-muted-foreground/20",
} as const;

/** Confirmation dialogs shared by configuration import and token revocation. */
export const confirmationDialogStyle = {
  overlay: "fixed inset-0 z-[1200] bg-black/70 backdrop-blur-[6px]",
  content:
    "fixed top-1/2 left-1/2 z-[1201] grid w-[min(480px,calc(100vw-40px))] -translate-x-1/2 -translate-y-1/2 grid-cols-[auto_1fr] gap-3 rounded-[14px] border border-foreground/10 bg-card p-[18px] shadow-lg",
  actions: "col-span-full flex flex-wrap justify-end gap-2 border-t border-foreground/10 pt-3",
} as const;
