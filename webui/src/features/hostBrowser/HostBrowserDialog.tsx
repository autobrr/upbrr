// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { pageStyle } from "../../components/ui/pageStyle";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { hostBrowser as hostBrowserClient } from "../../api/app";
import type { BrowseDirectoryRequest, BrowseDirectoryResponse } from "../../types";
import { filterBrowseEntries, type SourcePathMode } from "../../utils/inputHistory";

type Props = Readonly<{
  mode: SourcePathMode | null;
  initialPath: string;
  onClose: () => void;
  onSelect: (path: string, isDir: boolean, mode: SourcePathMode) => void;
}>;

/** Browses the host filesystem and returns only a selected path to the input owner. */
export function HostBrowserDialog({ mode, initialPath, onClose, onSelect }: Props) {
  const [directory, setDirectory] = useState<BrowseDirectoryResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [search, setSearch] = useState("");
  const [scope, setScope] = useState<"roots" | "directory">("roots");
  const requestRevision = useRef(0);
  const requestController = useRef<AbortController | null>(null);
  const searchTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const cancelPending = useCallback(() => {
    requestRevision.current += 1;
    requestController.current?.abort();
    if (searchTimer.current !== null) clearTimeout(searchTimer.current);
    searchTimer.current = null;
  }, []);

  const loadDirectory = useCallback(
    async (request: BrowseDirectoryRequest) => {
      cancelPending();
      const revision = requestRevision.current;
      const controller = new AbortController();
      requestController.current = controller;
      setScope(request.scope ?? "directory");
      setLoading(true);
      setError("");
      try {
        const result = await hostBrowserClient.list(request, controller.signal);
        if (requestRevision.current === revision) setDirectory(result);
      } catch (cause) {
        if (requestRevision.current === revision) {
          setError(cause instanceof Error ? cause.message : String(cause));
        }
      } finally {
        if (requestRevision.current === revision) setLoading(false);
      }
    },
    [cancelPending],
  );

  useEffect(() => {
    if (mode) {
      setDirectory(null);
      setSearch("");
      const path = initialPath.trim();
      void loadDirectory({ path, mode, scope: path ? "directory" : "roots" });
    }
    return cancelPending;
    // Opening a browser captures the input draft; later edits do not restart the listing.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode, loadDirectory, cancelPending]);

  const recursiveSearch = scope === "roots" && Boolean(search.trim());
  const entries = useMemo(
    () =>
      scope === "roots"
        ? directory?.entries || []
        : filterBrowseEntries(directory?.entries || [], search),
    [directory?.entries, search, scope],
  );
  const navigate = (path: string, nextScope: "roots" | "directory") => {
    if (!mode) return;
    setSearch("");
    setDirectory(null);
    void loadDirectory({ path, mode, scope: nextScope });
  };
  const changeSearch = (value: string) => {
    setSearch(value);
    if (!mode || scope !== "roots") return;
    cancelPending();
    setDirectory(null);
    setLoading(true);
    setError("");
    const request: BrowseDirectoryRequest = { path: "", mode, scope: "roots", query: value };
    if (value.trim()) searchTimer.current = setTimeout(() => void loadDirectory(request), 250);
    else void loadDirectory(request);
  };
  const close = () => {
    cancelPending();
    onClose();
  };
  const select = (path: string, isDir: boolean) => {
    if (!mode || (mode === "folder" && !isDir) || (mode === "file" && isDir)) return;
    onSelect(path, isDir, mode);
    close();
  };

  return (
    <Dialog.Root open={Boolean(mode)} onOpenChange={(open) => !open && close()}>
      <Dialog.Portal>
        <Dialog.Overlay className="host-browser-overlay fixed inset-0 z-[9000] bg-black/70" />
        <Dialog.Content className="fixed top-1/2 left-1/2 z-[9001] flex max-h-[min(760px,calc(100vh-48px))] w-[min(900px,calc(100vw-48px))] -translate-x-1/2 -translate-y-1/2 flex-col gap-3 rounded-[14px] border border-foreground/10 bg-card p-4 shadow-[var(--shadow)]">
          <div className="flex items-center justify-between gap-2.5">
            <div>
              <Dialog.Title asChild>
                <h2 className={pageStyle.label}>Host browser</h2>
              </Dialog.Title>
              <Dialog.Description asChild>
                <p className="host-browser-path font-mono text-[0.95rem] mt-1 mb-0 text-muted-foreground [overflow-wrap:anywhere]">
                  {scope === "roots"
                    ? "All browse roots"
                    : directory?.currentPath || "Loading directory..."}
                </p>
              </Dialog.Description>
            </div>
            <Dialog.Close asChild>
              <button className="ghost" type="button">
                Close
              </button>
            </Dialog.Close>
          </div>
          <div className="flex flex-wrap items-center gap-2.5">
            <button
              className="ghost"
              type="button"
              disabled={scope === "roots" || !directory?.parentPath || loading}
              onClick={() => {
                if (directory?.parentPath) navigate(directory.parentPath, "directory");
              }}
            >
              Up
            </button>
            <button className="ghost" type="button" onClick={() => navigate("", "roots")}>
              Roots
            </button>
            {mode === "folder" && scope === "directory" && directory?.currentPath ? (
              <button
                className="primary"
                type="button"
                disabled={loading}
                onClick={() => select(directory.currentPath, true)}
              >
                Select folder
              </button>
            ) : null}
            <label
              className="grid min-w-[min(280px,100%)] flex-1 gap-1 text-[0.82rem] text-muted-foreground"
              htmlFor="host-browser-search"
            >
              <span>{scope === "roots" ? "Search all browse roots" : "Filter this directory"}</span>
              <input
                id="host-browser-search"
                className="h-8 w-full rounded-lg border border-foreground/10 bg-card/45 px-2.5 text-foreground focus-visible:border-chart-2 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                value={search}
                onChange={(event) => changeSearch(event.target.value)}
                placeholder={
                  scope === "roots" ? "Find files and folders recursively" : "Filter loaded entries"
                }
                disabled={scope === "directory" && (loading || !directory)}
              />
            </label>
            {search ? (
              <button className="ghost" type="button" onClick={() => changeSearch("")}>
                Clear search
              </button>
            ) : null}
          </div>
          {error ? (
            <p className={pageStyle.error} role="alert">
              {error}
            </p>
          ) : null}
          {loading ? (
            <p className="text-muted-foreground" role="status">
              {recursiveSearch ? "Searching all browse roots..." : "Loading host paths..."}
            </p>
          ) : null}
          {!loading && directory?.incomplete ? (
            <p className="text-muted-foreground" role="status">
              Search results are incomplete. Narrow your search or open a root to browse directly.
            </p>
          ) : null}
          {!loading && directory ? (
            <div className="grid min-h-60 gap-1.5 overflow-auto pr-1">
              {entries.length === 0 ? (
                <p className="text-muted-foreground m-0 px-2.5 py-[18px]">No matching paths.</p>
              ) : (
                entries.map((entry) => (
                  <div
                    className="flex w-full items-center justify-between gap-2.5 rounded-[10px] border border-foreground/10 bg-foreground/[0.04] px-2.5 py-2"
                    key={entry.path}
                  >
                    <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">
                      <span>
                        {entry.isDir ? "[DIR] " : ""}
                        {entry.name}
                      </span>
                      {recursiveSearch ? (
                        <span className="block font-mono text-xs text-muted-foreground">
                          {entry.path}
                        </span>
                      ) : null}
                    </span>
                    <span className="text-[0.85rem] whitespace-nowrap text-muted-foreground">
                      {entry.isDir
                        ? "Folder"
                        : `${Math.round(entry.size / 1024).toLocaleString()} KiB`}
                    </span>
                    <span className="flex flex-wrap justify-end gap-1.5">
                      {entry.isDir ? (
                        <button
                          className="ghost"
                          type="button"
                          aria-label={`Open ${recursiveSearch ? entry.path : entry.name}`}
                          onClick={() => navigate(entry.path, "directory")}
                        >
                          Open
                        </button>
                      ) : null}
                      {((mode === "folder" && entry.isDir) ||
                        (mode === "file" && !entry.isDir)) && (
                        <button
                          className="primary"
                          type="button"
                          aria-label={`Select ${recursiveSearch ? entry.path : entry.name}`}
                          onClick={() => select(entry.path, entry.isDir)}
                        >
                          Select
                        </button>
                      )}
                    </span>
                  </div>
                ))
              )}
            </div>
          ) : null}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
