// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useMemo, useState } from "react";
import type { HistoryEntry, HistoryOverview } from "../../types";

const formatDate = (value: string) => {
  if (!value) {
    return "—";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
};

const isUploadedStatus = (value: string) => {
  const normalized = value?.trim().toLowerCase();
  return normalized === "uploaded" || normalized === "success" || normalized === "completed";
};

const formatLastUpload = (
  latestUploadStatus: string,
  statusLabel: string,
  latestUploadAt: string,
) => {
  if (!isUploadedStatus(latestUploadStatus) && !isUploadedStatus(statusLabel)) {
    return "never";
  }
  if (!latestUploadAt) {
    return "never";
  }
  return formatDate(latestUploadAt);
};

const releaseLabel = (entry: HistoryEntry) => {
  const title = entry.ReleaseTitle?.trim() || "Untitled release";
  const source = entry.ReleaseSource?.trim();
  const resolution = entry.ReleaseResolution?.trim();
  const extras = [source, resolution].filter(Boolean).join(" • ");
  return extras ? `${title} (${extras})` : title;
};

const releaseLabelFromOverview = (overview: HistoryOverview) => {
  const title = overview.ReleaseTitle?.trim() || "Untitled release";
  const source = overview.ReleaseSource?.trim();
  const resolution = overview.ReleaseResolution?.trim();
  const extras = [source, resolution].filter(Boolean).join(" • ");
  return extras ? `${title} (${extras})` : title;
};

export default function HistoryPage() {
  const [entries, setEntries] = useState<HistoryEntry[]>([]);
  const [selectedPath, setSelectedPath] = useState("");
  const [searchQuery, setSearchQuery] = useState("");
  const [overview, setOverview] = useState<HistoryOverview | null>(null);
  const [loading, setLoading] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    const listHistory = globalThis.go?.guiapp?.App?.ListHistory;
    if (!listHistory) {
      setError("History is unavailable in this build.");
      return;
    }

    const load = async () => {
      setLoading(true);
      setError("");
      try {
        const result = await listHistory();
        setEntries(result || []);
        if (result?.length) {
          setSelectedPath((current) => current || result[0].SourcePath);
        } else {
          setSelectedPath("");
          setOverview(null);
        }
      } catch (err) {
        setError(String(err));
      } finally {
        setLoading(false);
      }
    };

    void load();
  }, []);

  const filteredEntries = useMemo(() => {
    const query = searchQuery.trim().toLowerCase();
    if (!query) {
      return entries;
    }
    return entries.filter((entry) => {
      const title = entry.ReleaseTitle?.trim().toLowerCase() || "";
      return title.includes(query);
    });
  }, [entries, searchQuery]);

  useEffect(() => {
    if (!filteredEntries.length) {
      setSelectedPath("");
      setOverview(null);
      return;
    }
    const selectionStillVisible = filteredEntries.some(
      (entry) => entry.SourcePath === selectedPath,
    );
    if (!selectionStillVisible) {
      setSelectedPath(filteredEntries[0].SourcePath);
    }
  }, [filteredEntries, selectedPath]);

  useEffect(() => {
    if (!selectedPath) {
      setOverview(null);
      return;
    }
    const getHistoryOverview = globalThis.go?.guiapp?.App?.GetHistoryOverview;
    if (!getHistoryOverview) {
      setError("History overview is unavailable in this build.");
      return;
    }

    const loadDetail = async () => {
      setDetailLoading(true);
      setError("");
      try {
        const next = await getHistoryOverview(selectedPath);
        setOverview(next);
      } catch (err) {
        setError(String(err));
      } finally {
        setDetailLoading(false);
      }
    };

    void loadDetail();
  }, [selectedPath]);

  const selectedEntry = useMemo(
    () => entries.find((entry) => entry.SourcePath === selectedPath) || null,
    [entries, selectedPath],
  );

  const descriptionOverrides = useMemo(() => {
    if (!overview) {
      return [];
    }
    return Array.isArray(overview.DescriptionOverrides) ? overview.DescriptionOverrides : [];
  }, [overview]);

  const handleDeleteRelease = async () => {
    if (!selectedPath) {
      return;
    }
    const deleteHistoryRelease = globalThis.go?.guiapp?.App?.DeleteHistoryRelease;
    const listHistory = globalThis.go?.guiapp?.App?.ListHistory;
    if (!deleteHistoryRelease || !listHistory) {
      setError("Delete is unavailable in this build.");
      return;
    }
    const confirmed = window.confirm("Remove this stored release and all associated stored files?");
    if (!confirmed) {
      return;
    }

    setDeleting(true);
    setError("");
    try {
      await deleteHistoryRelease(selectedPath);
      const refreshed = (await listHistory()) || [];
      setEntries(refreshed);
      if (!refreshed.length) {
        setSelectedPath("");
        setOverview(null);
      }
    } catch (err) {
      setError(String(err));
    } finally {
      setDeleting(false);
    }
  };

  return (
    <div className="content-stack">
      <header className="hero">
        <p className="eyebrow">upbrr</p>
        <h1>History</h1>
        <p className="subtitle">
          Review previously processed releases stored in SQLite and inspect full stored details.
        </p>
      </header>

      <section className="panel history-shell">
        <aside className="history-list-panel">
          <div className="history-list-header">
            <p className="label">Stored releases</p>
            <p className="helper">Most recently updated first</p>
            <label className="history-search-field">
              <span className="label">Search by title</span>
              <input
                type="text"
                value={searchQuery}
                onChange={(event) => setSearchQuery(event.target.value)}
                placeholder="Filter titles"
              />
            </label>
          </div>

          {loading ? <p className="muted">Loading history...</p> : null}
          {!loading && entries.length === 0 ? (
            <p className="muted">No stored releases found.</p>
          ) : null}
          {!loading && entries.length > 0 && filteredEntries.length === 0 ? (
            <p className="muted">No releases match the current title filter.</p>
          ) : null}

          <div className="history-list">
            {filteredEntries.map((entry) => (
              <button
                key={entry.SourcePath}
                type="button"
                className={`history-item ${entry.SourcePath === selectedPath ? "active" : ""}`}
                onClick={() => setSelectedPath(entry.SourcePath)}
              >
                <span className="history-item-title">{releaseLabel(entry)}</span>
                <span className="history-item-meta">{entry.LatestUploadStatus || "Stored"}</span>
                <span className="history-item-meta">
                  Updated {formatDate(entry.MetadataUpdatedAt)}
                </span>
              </button>
            ))}
          </div>
        </aside>

        <div className="history-detail-panel">
          {detailLoading ? <p className="muted">Loading overview...</p> : null}

          {!detailLoading && !overview ? (
            <p className="muted">Select a stored release to view details.</p>
          ) : null}

          {overview ? (
            <div className="history-detail-stack">
              <div className="history-detail-actions">
                <button
                  type="button"
                  className="ghost history-delete-button"
                  disabled={deleting || detailLoading || !selectedPath}
                  onClick={() => {
                    void handleDeleteRelease();
                  }}
                >
                  {deleting ? "Removing..." : "Remove from database"}
                </button>
              </div>

              <div className="summary">
                <div>
                  <p className="label">Release</p>
                  <p className="value">
                    {selectedEntry
                      ? releaseLabel(selectedEntry)
                      : releaseLabelFromOverview(overview)}
                  </p>
                </div>
                <div>
                  <p className="label">Status</p>
                  <p className="value">{overview.StatusLabel || "Stored"}</p>
                </div>
                <div>
                  <p className="label">Metadata Updated</p>
                  <p className="value">{formatDate(overview.MetadataUpdatedAt)}</p>
                </div>
                <div>
                  <p className="label">Last Upload</p>
                  <p className="value">
                    {formatLastUpload(
                      overview.LatestUploadStatus,
                      overview.StatusLabel,
                      overview.LatestUploadAt,
                    )}
                  </p>
                </div>
              </div>

              <div className="history-grid">
                <article className="history-card">
                  <h3>Path</h3>
                  <p className="mono">{overview.SourcePath}</p>
                </article>

                <article className="history-card">
                  <h3>External IDs</h3>
                  <p>TMDB: {overview.ExternalIDs?.TMDBID || 0}</p>
                  <p>IMDb: {overview.ExternalIDs?.IMDBID || 0}</p>
                  <p>TVDB: {overview.ExternalIDs?.TVDBID || 0}</p>
                  <p>TVmaze: {overview.ExternalIDs?.TVmazeID || 0}</p>
                </article>

                <article className="history-card">
                  <h3>Counts</h3>
                  <p>Tracker metadata: {overview.TrackerMetadata?.length || 0}</p>
                  <p>Rule failures: {overview.TrackerRuleFailures?.length || 0}</p>
                  <p>Screenshots: {overview.Screenshots?.length || 0}</p>
                  <p>Final selections: {overview.FinalSelections?.length || 0}</p>
                  <p>Uploaded images: {overview.UploadedImages?.length || 0}</p>
                  <p>Upload history: {overview.UploadHistory?.length || 0}</p>
                </article>

                <article className="history-card history-card--wide">
                  <h3>Description Overrides</h3>
                  {descriptionOverrides.length ? (
                    <ul className="history-simple-list">
                      {descriptionOverrides.map((override, index) => {
                        const groupKey = override.GroupKey?.trim() || "default";
                        return (
                          <li key={`${groupKey}-${override.UpdatedAt}-${index}`}>
                            <strong>{groupKey}</strong>
                            <pre>{override.Description?.trim() || "(empty)"}</pre>
                          </li>
                        );
                      })}
                    </ul>
                  ) : (
                    <p className="muted">(none)</p>
                  )}
                </article>

                <article className="history-card history-card--wide">
                  <h3>Upload History</h3>
                  {overview.UploadHistory?.length ? (
                    <ul className="history-simple-list">
                      {overview.UploadHistory.map((row, index) => (
                        <li key={`${row.Tracker}-${row.CreatedAt}-${index}`}>
                          <strong>{row.Tracker || "UNKNOWN"}</strong> — {row.Status || "unknown"} —{" "}
                          {formatDate(row.CreatedAt)}
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <p className="muted">No upload records.</p>
                  )}
                </article>

                <article className="history-card history-card--wide">
                  <h3>Tracker Rule Failures</h3>
                  {overview.TrackerRuleFailures?.length ? (
                    <ul className="history-simple-list">
                      {overview.TrackerRuleFailures.map((failure, index) => (
                        <li key={`${failure.Tracker}-${failure.Rule}-${index}`}>
                          <strong>{failure.Tracker || "UNKNOWN"}</strong>: {failure.Rule}{" "}
                          {failure.Reason ? `— ${failure.Reason}` : ""}
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <p className="muted">No tracker rule failures stored.</p>
                  )}
                </article>

                <article className="history-card history-card--wide">
                  <h3>External Metadata (raw)</h3>
                  <pre>{JSON.stringify(overview.ExternalMetadata || {}, null, 2)}</pre>
                </article>

                <article className="history-card history-card--wide">
                  <h3>Release Overrides (raw)</h3>
                  <pre>{JSON.stringify(overview.ReleaseNameOverrides || {}, null, 2)}</pre>
                </article>

                <article className="history-card history-card--wide">
                  <h3>Metadata (raw)</h3>
                  <pre>{JSON.stringify(overview.Metadata || {}, null, 2)}</pre>
                </article>
              </div>
            </div>
          ) : null}

          {error ? <p className="error">{error}</p> : null}
        </div>
      </section>
    </div>
  );
}
