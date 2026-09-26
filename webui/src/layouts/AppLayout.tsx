// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ReactNode } from "react";
import type { ApplicationInfo } from "../types";
import { formatApplicationVersion, getApplicationVersionDisplay } from "../utils/applicationInfo";
import { cn } from "../utils/cn";
import { handleExternalLinkClick } from "../utils/externalLinks";

export type NavigationItem = Readonly<{
  label: string;
  active: boolean;
  onSelect: () => void;
  disabled?: boolean;
  reason?: string;
  nested?: boolean;
}>;

type Props = Readonly<{
  applicationInfo: ApplicationInfo | null;
  releaseNavigation: readonly NavigationItem[];
  utilityNavigation: readonly NavigationItem[];
  children: ReactNode;
}>;

const groupClass =
  "grid gap-1 rounded-lg border border-sidebar-border bg-sidebar p-1.5 max-[960px]:flex max-[960px]:flex-nowrap max-[960px]:overflow-x-auto max-[960px]:p-1";

function NavigationGroup({ items }: Readonly<{ items: readonly NavigationItem[] }>) {
  return items.map((item) => (
    <button
      key={item.label}
      className={cn(
        "w-full rounded-md border border-transparent bg-transparent px-2 py-1.5 text-left text-[0.84rem] font-semibold leading-tight text-sidebar-foreground transition hover:bg-sidebar-accent hover:text-sidebar-accent-foreground disabled:cursor-not-allowed disabled:opacity-45 max-[960px]:w-auto max-[960px]:shrink-0 max-[960px]:whitespace-nowrap",
        item.nested && "pl-4 text-[0.8rem] font-medium max-[960px]:pl-2",
        item.active && "border-sidebar-ring bg-sidebar-accent text-sidebar-accent-foreground",
      )}
      type="button"
      aria-current={item.active ? "page" : undefined}
      disabled={item.disabled}
      title={item.reason}
      onClick={item.onSelect}
    >
      {item.label}
    </button>
  ));
}

/** Shared navigation and page frame for every authenticated route. */
export function AppLayout({
  applicationInfo,
  releaseNavigation,
  utilityNavigation,
  children,
}: Props) {
  const version = applicationInfo ? getApplicationVersionDisplay(applicationInfo) : null;
  const versionLabel = applicationInfo ? formatApplicationVersion(applicationInfo) : "";

  return (
    <div className="app-shell">
      <a
        className="sr-only focus:not-sr-only focus:fixed focus:left-3 focus:top-3 focus:z-[2000] focus:rounded-md focus:bg-primary focus:px-3 focus:py-2 focus:text-primary-foreground"
        href="#main-content"
      >
        Skip to content
      </a>
      <div className="relative z-[1] block min-h-screen ml-[204px] max-[960px]:ml-0">
        <aside className="fixed left-0 top-0 z-[1000] flex h-screen w-[204px] flex-col gap-2.5 border-r border-sidebar-border bg-sidebar p-2.5 text-sidebar-foreground max-[960px]:static max-[960px]:h-auto max-[960px]:w-full max-[960px]:border-r-0 max-[960px]:border-b">
          <nav aria-label="Release workflow" className={groupClass}>
            <NavigationGroup items={releaseNavigation} />
          </nav>
          <nav aria-label="Workspace" className={`${groupClass} mt-auto max-[960px]:mt-0`}>
            <NavigationGroup items={utilityNavigation} />
          </nav>
          <div className="mt-1 grid gap-1 rounded-lg border border-sidebar-border bg-sidebar-accent/40 px-2 py-1.5 text-[0.72rem] leading-tight text-sidebar-foreground max-[960px]:hidden">
            {version ? (
              <div className="grid min-w-0 font-semibold" title={versionLabel}>
                <span>{version.version}</span>
                {version.buildDate ? <span>({version.buildDate})</span> : null}
              </div>
            ) : null}
            <div className="flex min-w-0 items-center justify-between gap-1">
              <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">
                © 2026 autobrr
              </span>
              <div className="flex items-center gap-1">
                <a
                  className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md border border-sidebar-border transition hover:border-sidebar-ring hover:text-sidebar-accent-foreground"
                  href="https://discord.autobrr.com"
                  target="_blank"
                  rel="noreferrer"
                  onAuxClick={handleExternalLinkClick}
                  onClick={handleExternalLinkClick}
                  aria-label="Open the autobrr Discord"
                  title="autobrr Discord"
                >
                  <svg
                    aria-hidden="true"
                    viewBox="0 0 24 24"
                    className="h-4 w-4"
                    fill="currentColor"
                  >
                    <path d="M19.5 5.34A17.3 17.3 0 0 0 15.44 4l-.5 1.02a15.8 15.8 0 0 0-5.86 0L8.55 4A17.5 17.5 0 0 0 4.5 5.35C1.93 9.2 1.23 12.96 1.58 16.67a17.7 17.7 0 0 0 4.98 2.51l1.2-1.64a11.2 11.2 0 0 1-1.88-.9l.46-.36c3.63 1.68 7.57 1.68 11.16 0l.47.36c-.6.36-1.23.66-1.89.9l1.2 1.64a17.6 17.6 0 0 0 4.98-2.51c.42-4.3-.72-8.03-2.76-11.33ZM8.68 14.4c-1.09 0-1.98-1-1.98-2.22 0-1.23.87-2.23 1.98-2.23 1.12 0 2 1 1.98 2.23 0 1.22-.87 2.22-1.98 2.22Zm6.64 0c-1.1 0-1.98-1-1.98-2.22 0-1.23.87-2.23 1.98-2.23 1.12 0 2 1 1.98 2.23 0 1.22-.86 2.22-1.98 2.22Z" />
                  </svg>
                </a>
                <a
                  className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md border border-sidebar-border transition hover:border-sidebar-ring hover:text-sidebar-accent-foreground"
                  href="https://github.com/autobrr/upbrr"
                  target="_blank"
                  rel="noreferrer"
                  onAuxClick={handleExternalLinkClick}
                  onClick={handleExternalLinkClick}
                  aria-label="Open autobrr/upbrr on GitHub"
                  title="autobrr/upbrr"
                >
                  <svg
                    aria-hidden="true"
                    viewBox="0 0 16 16"
                    className="h-4 w-4"
                    fill="currentColor"
                  >
                    <path d="M8 0C3.58 0 0 3.67 0 8.2c0 3.62 2.29 6.69 5.47 7.78.4.08.55-.18.55-.4l-.01-1.4c-2.22.5-2.69-1.1-2.69-1.1-.36-.95-.89-1.2-.89-1.2-.73-.51.05-.5.05-.5.81.06 1.24.85 1.24.85.72 1.27 1.89.9 2.35.69.07-.53.28-.9.51-1.1-1.78-.21-3.64-.91-3.64-4.04 0-.89.31-1.62.82-2.19-.08-.21-.36-1.04.08-2.16 0 0 .68-.22 2.2.84A7.37 7.37 0 0 1 8 3.99c.68 0 1.36.09 2 .28 1.52-1.06 2.19-.84 2.19-.84.44 1.12.16 1.95.08 2.16.52.57.82 1.3.82 2.19 0 3.14-1.87 3.83-3.65 4.04.29.25.54.76.54 1.54l-.01 2.22c0 .22.14.48.55.4A8.13 8.13 0 0 0 16 8.2C16 3.67 12.42 0 8 0Z" />
                  </svg>
                </a>
              </div>
            </div>
          </div>
        </aside>
        <main id="main-content" className="content" tabIndex={-1}>
          {children}
        </main>
      </div>
    </div>
  );
}
