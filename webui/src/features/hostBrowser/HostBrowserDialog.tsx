// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { hostBrowser as hostBrowserClient } from "../../api/app";
import type { BrowseDirectoryResponse } from "../../types";
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
  const requestRevision = useRef(0);

  const loadDirectory = useCallback(async (path: string, requestedMode: SourcePathMode) => {
    const revision = ++requestRevision.current;
    setLoading(true);
    setError("");
    try {
      const result = await hostBrowserClient.list(path, requestedMode);
      if (requestRevision.current === revision) setDirectory(result);
    } catch (cause) {
      if (requestRevision.current === revision) {
        setError(cause instanceof Error ? cause.message : String(cause));
      }
    } finally {
      if (requestRevision.current === revision) setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (mode) {
      setDirectory(null);
      setSearch("");
      void loadDirectory(initialPath, mode);
    }
    return () => {
      requestRevision.current += 1;
    };
    // Opening a browser captures the input draft; later edits do not restart the listing.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode, loadDirectory]);

  const entries = useMemo(
    () => filterBrowseEntries(directory?.entries || [], search),
    [directory?.entries, search],
  );
  const select = (path: string, isDir: boolean) => {
    if (!mode || (mode === "folder" && !isDir) || (mode === "file" && isDir)) return;
    onSelect(path, isDir, mode);
    onClose();
  };

  return (
    <Dialog.Root open={Boolean(mode)} onOpenChange={(open) => !open && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="host-browser-overlay" />
        <Dialog.Content className="host-browser-dialog">
          <div className="host-browser-header">
            <div>
              <Dialog.Title asChild>
                <h2 className="label">Host browser</h2>
              </Dialog.Title>
              <Dialog.Description asChild>
                <p className="mono host-browser-path">{directory?.currentPath || "Computer"}</p>
              </Dialog.Description>
            </div>
            <Dialog.Close asChild>
              <button className="ghost" type="button">
                Close
              </button>
            </Dialog.Close>
          </div>
          <div className="host-browser-toolbar">
            <button
              className="ghost"
              type="button"
              disabled={!directory?.parentPath || loading}
              onClick={() => {
                if (mode && directory?.parentPath) void loadDirectory(directory.parentPath, mode);
              }}
            >
              Up
            </button>
            <button
              className="ghost"
              type="button"
              disabled={loading}
              onClick={() => mode && void loadDirectory("", mode)}
            >
              Roots
            </button>
            {mode === "folder" && directory?.currentPath ? (
              <button
                className="primary"
                type="button"
                disabled={loading}
                onClick={() => select(directory.currentPath, true)}
              >
                Select folder
              </button>
            ) : null}
            <label className="host-browser-search" htmlFor="host-browser-search">
              <span>Search</span>
              <input
                id="host-browser-search"
                className="host-browser-search__input"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder="Filter current path"
                disabled={loading || !directory}
              />
            </label>
          </div>
          {error ? (
            <p className="error" role="alert">
              {error}
            </p>
          ) : null}
          {loading ? <p className="muted">Loading host paths...</p> : null}
          {!loading && directory ? (
            <div className="host-browser-list">
              {entries.length === 0 ? (
                <p className="muted host-browser-empty">No matching paths.</p>
              ) : (
                entries.map((entry) => (
                  <div className="host-browser-entry" key={entry.path}>
                    <span className="host-browser-entry__name">
                      {entry.isDir ? "[DIR] " : ""}
                      {entry.name}
                    </span>
                    <span className="host-browser-entry__meta">
                      {entry.isDir
                        ? "Folder"
                        : `${Math.round(entry.size / 1024).toLocaleString()} KiB`}
                    </span>
                    <span className="host-browser-entry__actions">
                      {entry.isDir ? (
                        <button
                          className="ghost"
                          type="button"
                          aria-label={`Open ${entry.name}`}
                          onClick={() => mode && void loadDirectory(entry.path, mode)}
                        >
                          Open
                        </button>
                      ) : null}
                      {((mode === "folder" && entry.isDir) ||
                        (mode === "file" && !entry.isDir)) && (
                        <button
                          className="primary"
                          type="button"
                          aria-label={`Select ${entry.name}`}
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
