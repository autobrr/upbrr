// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useCallback, useEffect, useState } from "react";
import type { PlaylistInfo } from "../../types";
import "./styles.css";

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
        // Sort by score descending (highest first) and take top 10
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

  const displayCount = playlists.length; // Already limited to top 10 by score

  if (loading) {
    return (
      <div className="playlist-selection-container">
        <div className="playlist-selection-content">
          <h2>Discovering Playlists</h2>
          <p>Scanning {path} for MPLS files...</p>
        </div>
      </div>
    );
  }

  if (playlists.length === 0) {
    return (
      <div className="playlist-selection-container">
        <div className="playlist-selection-content">
          <h2>No Playlists Found</h2>
          <p>No MPLS playlists were found in {path}</p>
          <div className="playlist-selection-actions">
            <button onClick={onBack} className="btn-secondary">
              Back
            </button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="playlist-selection-container">
      <div className="playlist-selection-content">
        <h2>Select BDMV Playlists</h2>
        <p>Choose which playlists to use for {path}</p>

        {error && <div className="playlist-selection-error">{error}</div>}
        {progressError && <div className="playlist-selection-error">{progressError}</div>}
        <div className="playlist-selection-list">
          {playlists.slice(0, displayCount).map((playlist, index) => {
            const totalSize = playlist.items?.reduce((sum, item) => sum + item.size, 0) || 0;
            const fileCount = playlist.items?.length || 0;
            return (
              <div key={playlist.file} className="playlist-selection-item">
                <label className="playlist-selection-checkbox-label">
                  <input
                    type="checkbox"
                    checked={selectedIndices.has(index)}
                    onChange={() => handleTogglePlaylist(index)}
                    disabled={saving}
                  />
                  <span className="playlist-selection-name">{playlist.file}</span>
                </label>
                <span className="playlist-selection-details">
                  {formatDuration(playlist.duration)} • {fileCount} files • {formatBytes(totalSize)}{" "}
                  • Score: {playlist.score.toFixed(2)}
                </span>
              </div>
            );
          })}
        </div>

        {playlists.length > 1 && (
          <div className="playlist-selection-options">
            <button
              onClick={handleSelectAll}
              className="btn-secondary"
              disabled={saving || displayCount === 0}
            >
              {useAll ? "Deselect All" : `Select All Top ${displayCount}`}
            </button>
            <button onClick={handleAutoSelect} className="btn-secondary" disabled={saving}>
              Auto-Select Best
            </button>
          </div>
        )}

        <div className="playlist-selection-actions">
          <button onClick={onBack} className="btn-secondary" disabled={saving}>
            Back
          </button>
          <button
            onClick={handleConfirm}
            className="btn-primary"
            disabled={saving || selectedIndices.size === 0}
          >
            {saving ? (preparing ? "Preparing..." : "Saving...") : "Confirm Selection"}
          </button>
        </div>

        {preparing ? (
          <div className="playlist-selection-progress" role="status" aria-live="polite">
            <h3>BDInfo progress</h3>
            <pre>{progressLines.length > 0 ? progressLines.join("\n") : "Starting BDInfo..."}</pre>
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
