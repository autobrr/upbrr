// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useMemo, useState } from "react";
import { historyClient } from "../../api/app";
import type { HistoryEntry, HistoryOverview } from "../../types";
import { cn } from "../../utils/cn";

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

type Props = {
  onReleaseDeleted?: (sourcePath: string) => void;
};

export default function HistoryPage({ onReleaseDeleted }: Props) {
  const [entries, setEntries] = useState<HistoryEntry[]>([]);
  const [selectedPath, setSelectedPath] = useState("");
  const [searchQuery, setSearchQuery] = useState("");
  const [overview, setOverview] = useState<HistoryOverview | null>(null);
  const [loading, setLoading] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    const listHistory = historyClient.list;
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
    const getHistoryOverview = historyClient.getOverview;
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
    const deleteHistoryRelease = historyClient.removeRelease;
    const listHistory = historyClient.list;
    const confirmed = window.confirm("Remove this stored release and all associated stored files?");
    if (!confirmed) {
      return;
    }

    setDeleting(true);
    setError("");
    try {
      const deletedPath = selectedPath;
      await deleteHistoryRelease(deletedPath);
      onReleaseDeleted?.(deletedPath);
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

      <section className="panel grid min-h-[560px] gap-3 lg:grid-cols-[minmax(260px,320px)_minmax(0,1fr)]">
        <aside className="rounded-lg border border-white/10 bg-white/5 p-3">
          <div className="mb-2">
            <p className="label">Stored releases</p>
            <p className="helper">Most recently updated first</p>
            <label className="mt-2 grid gap-1.5">
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

          <div className="grid max-h-[520px] gap-1.5 overflow-y-auto">
            {filteredEntries.map((entry) => (
              <button
                key={entry.SourcePath}
                type="button"
                className={cn(
                  "grid w-full gap-1 rounded-md border px-3 py-2 text-left transition",
                  entry.SourcePath === selectedPath
                    ? "border-[var(--accent-2)] bg-[rgba(53,194,193,0.16)] text-[var(--text)] shadow-[inset_3px_0_0_var(--accent-2),0_0_16px_rgba(53,194,193,0.16)]"
                    : "border-white/10 bg-black/15 text-[var(--muted)] hover:border-white/20 hover:bg-white/5 hover:text-[var(--text)]",
                )}
                onClick={() => setSelectedPath(entry.SourcePath)}
              >
                <span
                  className={cn(
                    "font-semibold",
                    entry.SourcePath === selectedPath ? "text-[var(--text)]" : "text-inherit",
                  )}
                >
                  {releaseLabel(entry)}
                </span>
                <span className="text-xs text-[var(--muted)]">
                  {entry.LatestUploadStatus || "Stored"}
                </span>
                <span className="text-xs text-[var(--muted)]">
                  Updated {formatDate(entry.MetadataUpdatedAt)}
                </span>
              </button>
            ))}
          </div>
        </aside>

        <div className="overflow-y-auto rounded-lg border border-white/10 bg-white/5 p-3">
          {detailLoading ? <p className="muted">Loading overview...</p> : null}

          {!detailLoading && !overview ? (
            <p className="muted">Select a stored release to view details.</p>
          ) : null}

          {overview ? (
            <div className="grid gap-3">
              <div className="flex justify-end">
                <button
                  type="button"
                  className="ghost border-red-400/45 text-[var(--danger)]"
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

              <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-2 [&_h3]:mb-2 [&_h3]:mt-0 [&_h3]:text-sm">
                <article className="rounded-lg border border-white/10 bg-[var(--panel-light)] p-2.5">
                  <h3>Path</h3>
                  <p className="mono">{overview.SourcePath}</p>
                </article>

                <article className="rounded-lg border border-white/10 bg-[var(--panel-light)] p-2.5 [&_p]:mb-1 [&_p]:mt-0">
                  <h3>External IDs</h3>
                  <p>TMDB: {overview.Identity?.TMDBID || 0}</p>
                  <p>IMDb: {overview.Identity?.IMDBID || 0}</p>
                  <p>TVDB: {overview.Identity?.TVDBID || 0}</p>
                  <p>TVmaze: {overview.Identity?.TVmazeID || 0}</p>
                </article>

                <article className="rounded-lg border border-white/10 bg-[var(--panel-light)] p-2.5 [&_p]:mb-1 [&_p]:mt-0">
                  <h3>Counts</h3>
                  <p>Tracker metadata: {overview.TrackerMetadata?.length || 0}</p>
                  <p>
                    Rule failures:{" "}
                    {overview.TrackerRuleFailures?.filter(
                      (failure) => String(failure.Severity || "blocking") !== "warning",
                    ).length || 0}
                  </p>
                  <p>
                    Rule warnings:{" "}
                    {overview.TrackerRuleFailures?.filter(
                      (failure) => String(failure.Severity || "blocking") === "warning",
                    ).length || 0}
                  </p>
                  <p>Screenshots: {overview.Screenshots?.length || 0}</p>
                  <p>Final selections: {overview.FinalSelections?.length || 0}</p>
                  <p>Uploaded images: {overview.UploadedImages?.length || 0}</p>
                  <p>Upload history: {overview.UploadHistory?.length || 0}</p>
                </article>

                <article className="col-span-full rounded-lg border border-white/10 bg-[var(--panel-light)] p-2.5">
                  <h3>Description Overrides</h3>
                  {descriptionOverrides.length ? (
                    <ul className="m-0 grid gap-1 pl-4">
                      {descriptionOverrides.map((override, index) => {
                        const groupKey = override.GroupKey?.trim() || "default";
                        return (
                          <li key={`${groupKey}-${override.UpdatedAt}-${index}`}>
                            <strong>{groupKey}</strong>
                            <pre className="m-0 max-h-[220px] overflow-auto whitespace-pre-wrap rounded-md bg-black/10 p-2 text-xs [overflow-wrap:anywhere]">
                              {override.Description?.trim() || "(empty)"}
                            </pre>
                          </li>
                        );
                      })}
                    </ul>
                  ) : (
                    <p className="muted">(none)</p>
                  )}
                </article>

                <article className="col-span-full rounded-lg border border-white/10 bg-[var(--panel-light)] p-2.5">
                  <h3>Upload History</h3>
                  {overview.UploadHistory?.length ? (
                    <ul className="m-0 grid gap-1 pl-4">
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

                <article className="col-span-full rounded-lg border border-white/10 bg-[var(--panel-light)] p-2.5">
                  <h3>Tracker Rule Results</h3>
                  {overview.TrackerRuleFailures?.length ? (
                    <ul className="m-0 grid gap-1 pl-4">
                      {overview.TrackerRuleFailures.map((failure, index) => (
                        <li key={`${failure.Tracker}-${failure.Rule}-${index}`}>
                          <strong>{failure.Tracker || "UNKNOWN"}</strong> [
                          {failure.Severity || "blocking"}]: {failure.Rule}{" "}
                          {failure.Reason ? `— ${failure.Reason}` : ""}
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <p className="muted">No tracker rule results stored.</p>
                  )}
                </article>

                <article className="col-span-full rounded-lg border border-white/10 bg-[var(--panel-light)] p-2.5">
                  <h3>Provider Display (diagnostic)</h3>
                  <pre className="m-0 max-h-[220px] overflow-auto whitespace-pre-wrap rounded-md bg-black/10 p-2 text-xs [overflow-wrap:anywhere]">
                    {JSON.stringify(overview.Display || {}, null, 2)}
                  </pre>
                </article>

                <article className="col-span-full rounded-lg border border-white/10 bg-[var(--panel-light)] p-2.5">
                  <h3>Release Overrides (raw)</h3>
                  <pre className="m-0 max-h-[220px] overflow-auto whitespace-pre-wrap rounded-md bg-black/10 p-2 text-xs [overflow-wrap:anywhere]">
                    {JSON.stringify(overview.ReleaseNameOverrides || {}, null, 2)}
                  </pre>
                </article>

                <article className="col-span-full rounded-lg border border-white/10 bg-[var(--panel-light)] p-2.5">
                  <h3>Metadata (raw)</h3>
                  <pre className="m-0 max-h-[220px] overflow-auto whitespace-pre-wrap rounded-md bg-black/10 p-2 text-xs [overflow-wrap:anywhere]">
                    {JSON.stringify(overview.Metadata || {}, null, 2)}
                  </pre>
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
