// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useCallback, useEffect, useState } from "react";
import { Button } from "../../components/ui/button";
import { Checkbox } from "../../components/ui/checkbox";
import type { PlaylistInfo } from "../../types";

interface PlaylistSelectionPageProps {
  path: string;
  onBack: () => void;
  onConfirm: () => Promise<void>;
  preparing: boolean;
  progressLines: string[];
  progressError: string;
}

const PlaylistSelectionPage = ({
  path,
  onBack,
  onConfirm,
  preparing,
  progressLines,
  progressError,
}: PlaylistSelectionPageProps) => {
  const [loading, setLoading] = useState(true);
  const [playlists, setPlaylists] = useState<PlaylistInfo[]>([]);
  const [selectedIndices, setSelectedIndices] = useState<Set<number>>(new Set());
  const [useAll, setUseAll] = useState(false);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);

  const discoverPlaylists = useCallback(async () => {
    try {
      setLoading(true);
      setError("");
      const discover = globalThis.go?.guiapp?.App?.DiscoverPlaylists;
      if (!discover) {
        throw new Error("DiscoverPlaylists API not available");
      }
      const discovered = await discover(path);
      if (discovered) {
        // Sort by score descending (highest first) and take top 10.
        const sorted = discovered.sort((a, b) => (b.score || 0) - (a.score || 0)).slice(0, 10);
        setPlaylists(sorted);
        if (sorted.length === 1) {
          setSelectedIndices(new Set([0]));
        }
      } else {
        setPlaylists([]);
      }
    } catch (err) {
      setError(`Failed to discover playlists: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setLoading(false);
    }
  }, [path]);

  useEffect(() => {
    discoverPlaylists();
  }, [discoverPlaylists]);

  const handleTogglePlaylist = (index: number) => {
    setUseAll(false);
    const newSelected = new Set(selectedIndices);
    if (newSelected.has(index)) {
      newSelected.delete(index);
    } else {
      newSelected.add(index);
    }
    setSelectedIndices(newSelected);
  };

  const handleSelectAll = () => {
    if (useAll) {
      setUseAll(false);
      setSelectedIndices(new Set());
    } else {
      setUseAll(true);
      const all = new Set<number>();
      for (let i = 0; i < playlists.length; i++) {
        all.add(i);
      }
      setSelectedIndices(all);
    }
  };

  const handleAutoSelect = () => {
    setUseAll(false);
    setSelectedIndices(new Set([0]));
  };

  const handleConfirm = async () => {
    try {
      setSaving(true);
      setError("");

      const selected = Array.from(selectedIndices)
        .sort((a, b) => a - b)
        .map((idx) => playlists[idx].file);

      if (selected.length === 0) {
        setError("Please select at least one playlist");
        return;
      }

      const saveFn = globalThis.go?.guiapp?.App?.SavePlaylistSelection;
      if (!saveFn) {
        throw new Error("SavePlaylistSelection API not available");
      }

      await saveFn(path, selected, useAll);
      await onConfirm();
    } catch (err) {
      setError(`Failed to save selection: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setSaving(false);
    }
  };

  const displayCount = playlists.length;

  if (loading) {
    return (
      <div className="mx-auto max-w-2xl p-3">
        <div className="panel">
          <h2>Discovering Playlists</h2>
          <p className="muted mt-1 text-sm">Scanning {path} for MPLS files...</p>
        </div>
      </div>
    );
  }

  if (playlists.length === 0) {
    return (
      <div className="mx-auto max-w-2xl p-3">
        <div className="panel">
          <h2>No Playlists Found</h2>
          <p className="muted mt-1 text-sm">No MPLS playlists were found in {path}</p>
          <div className="mt-3 flex justify-end">
            <Button onClick={onBack} type="button">
              Back
            </Button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-2xl p-3">
      <div className="panel">
        <h2>Select BDMV Playlists</h2>
        <p className="muted mt-1 text-sm [overflow-wrap:anywhere]">
          Choose which playlists to use for {path}
        </p>

        {error ? (
          <div className="mt-3 rounded border-l-4 border-red-500 bg-red-500/10 px-3 py-2 text-sm text-red-100">
            {error}
          </div>
        ) : null}
        {progressError ? (
          <div className="mt-3 rounded border-l-4 border-red-500 bg-red-500/10 px-3 py-2 text-sm text-red-100">
            {progressError}
          </div>
        ) : null}

        <div className="my-3 overflow-hidden rounded-md border border-[var(--border)]">
          {playlists.slice(0, displayCount).map((playlist, index) => {
            const totalSize = playlist.items?.reduce((sum, item) => sum + item.size, 0) || 0;
            const fileCount = playlist.items?.length || 0;
            const checkboxId = `playlist-${index}`;
            return (
              <div
                key={playlist.file}
                className="grid gap-1 border-b border-[var(--border)] px-3 py-2 last:border-b-0 hover:bg-[var(--hover)]"
              >
                <div className="flex select-none items-center gap-2">
                  <Checkbox
                    id={checkboxId}
                    checked={selectedIndices.has(index)}
                    onCheckedChange={() => handleTogglePlaylist(index)}
                    disabled={saving}
                  />
                  <label
                    className="cursor-pointer font-semibold text-[var(--text)]"
                    htmlFor={checkboxId}
                  >
                    {playlist.file}
                  </label>
                </div>
                <span className="ml-6 text-xs text-[var(--muted)]">
                  {formatDuration(playlist.duration)} • {fileCount} files • {formatBytes(totalSize)}{" "}
                  • Score: {playlist.score.toFixed(2)}
                </span>
              </div>
            );
          })}
        </div>

        {playlists.length > 1 ? (
          <div className="my-3 flex flex-wrap gap-2">
            <Button onClick={handleSelectAll} type="button" disabled={saving || displayCount === 0}>
              {useAll ? "Deselect All" : `Select All Top ${displayCount}`}
            </Button>
            <Button onClick={handleAutoSelect} type="button" disabled={saving}>
              Auto-Select Best
            </Button>
          </div>
        ) : null}

        <div className="mt-3 flex justify-end gap-2">
          <Button onClick={onBack} type="button" disabled={saving}>
            Back
          </Button>
          <Button
            onClick={handleConfirm}
            variant="primary"
            type="button"
            disabled={saving || selectedIndices.size === 0}
          >
            {saving ? (preparing ? "Preparing..." : "Saving...") : "Confirm Selection"}
          </Button>
        </div>

        {preparing ? (
          <div
            className="mt-3 rounded-md border border-[var(--border)] bg-[var(--panel-light)] p-2"
            role="status"
            aria-live="polite"
          >
            <h3 className="mb-1 text-sm font-semibold">BDInfo progress</h3>
            <pre className="m-0 max-h-40 overflow-auto whitespace-pre-wrap text-xs text-[var(--muted)] [overflow-wrap:anywhere]">
              {progressLines.length > 0 ? progressLines.join("\n") : "Starting BDInfo..."}
            </pre>
          </div>
        ) : null}
      </div>
    </div>
  );
};

export default PlaylistSelectionPage;

const formatDuration = (seconds: number): string => {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = Math.floor(seconds % 60);

  if (h > 0) {
    return `${h}h ${m}m ${s}s`;
  }
  if (m > 0) {
    return `${m}m ${s}s`;
  }
  return `${s}s`;
};

const formatBytes = (bytes: number): string => {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return Math.round((bytes / Math.pow(k, i)) * 100) / 100 + " " + sizes[i];
};
