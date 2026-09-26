// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { historyClient } from "../../api/app";
import type { HistoryEntry, HistoryOverview, HistoryRuleFailure } from "../../types";
import { cn } from "../../utils/cn";

const emptyHistory: HistoryEntry[] = [];

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

const ruleResultState = (failure: HistoryRuleFailure) => {
  const disposition = String(failure.Disposition || "strict");
  if (disposition === "advisory" || disposition === "strict") {
    return disposition;
  }
  return failure.Authorized ? "waived" : "unwaived";
};

type Props = {
  onReleaseDeleted?: (sourcePath: string) => void;
  onOpenInput?: (sourcePath: string) => Promise<boolean>;
};

/** Displays persisted history and optionally reopens a source through the active release session. */
export default function HistoryPage({ onReleaseDeleted, onOpenInput }: Props) {
  const queryClient = useQueryClient();
  const historyQuery = useQuery({
    queryKey: ["history", "list"],
    queryFn: ({ signal }) => historyClient.list(signal),
  });
  const entries = historyQuery.data ?? emptyHistory;
  const loading = historyQuery.isPending;
  const [selectedPath, setSelectedPath] = useState("");
  const [searchQuery, setSearchQuery] = useState("");
  const [deleting, setDeleting] = useState(false);
  const [opening, setOpening] = useState(false);
  const [error, setError] = useState("");
  const [openFailure, setOpenFailure] = useState<{ sourcePath: string; message: string } | null>(
    null,
  );

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
      return;
    }
    const selectionStillVisible = filteredEntries.some(
      (entry) => entry.SourcePath === selectedPath,
    );
    if (!selectionStillVisible) {
      setSelectedPath(filteredEntries[0].SourcePath);
    }
  }, [filteredEntries, selectedPath]);

  const overviewQuery = useQuery({
    queryKey: ["history", "overview", selectedPath],
    queryFn: ({ signal }) => historyClient.getOverview(selectedPath, signal),
    enabled: selectedPath !== "",
  });
  const overview: HistoryOverview | null = overviewQuery.data ?? null;
  const detailLoading = selectedPath !== "" && overviewQuery.isPending;

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
    const confirmed = window.confirm("Remove this stored release and all associated stored files?");
    if (!confirmed) {
      return;
    }

    setDeleting(true);
    setError("");
    setOpenFailure(null);
    try {
      const deletedPath = selectedPath;
      await deleteHistoryRelease(deletedPath);
      onReleaseDeleted?.(deletedPath);
      queryClient.removeQueries({ queryKey: ["history", "overview", deletedPath] });
      await queryClient.invalidateQueries({ queryKey: ["history", "list"] });
      const refreshed = queryClient.getQueryData<HistoryEntry[]>(["history", "list"]) ?? [];
      if (!refreshed.length) {
        setSelectedPath("");
      }
    } catch (err) {
      setError(String(err));
    } finally {
      setDeleting(false);
    }
  };

  const handleOpenInput = async () => {
    if (!selectedPath || !onOpenInput) return;
    setOpening(true);
    setError("");
    setOpenFailure(null);
    try {
      const opened = await onOpenInput(selectedPath);
      if (!opened) {
        setOpenFailure({
          sourcePath: selectedPath,
          message:
            "Input could not be opened. Check the Input page for errors or recovery actions.",
        });
      }
    } catch (err) {
      setOpenFailure({ sourcePath: selectedPath, message: String(err) });
    } finally {
      setOpening(false);
    }
  };

  const displayedError =
    error ||
    (openFailure?.sourcePath === selectedPath ? openFailure.message : "") ||
    (historyQuery.error ? String(historyQuery.error) : "") ||
    (overviewQuery.error ? String(overviewQuery.error) : "");

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
        <aside className="rounded-lg border border-border bg-card p-3">
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
                aria-pressed={entry.SourcePath === selectedPath}
                className={cn(
                  "grid w-full min-w-0 gap-1 rounded-md border px-3 py-2 text-left transition [overflow-wrap:anywhere] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring",
                  entry.SourcePath === selectedPath
                    ? "border-primary bg-accent text-accent-foreground"
                    : "border-border bg-card text-card-foreground hover:border-primary hover:bg-accent hover:text-accent-foreground",
                )}
                onClick={() => setSelectedPath(entry.SourcePath)}
              >
                <span className="font-semibold">
                  {entry.SourcePath === selectedPath ? <span aria-hidden="true">✓ </span> : null}
                  {releaseLabel(entry)}
                </span>
                <span className="text-xs">{entry.LatestUploadStatus || "Stored"}</span>
                <span className="text-xs">Updated {formatDate(entry.MetadataUpdatedAt)}</span>
              </button>
            ))}
          </div>
        </aside>

        <div className="overflow-y-auto rounded-lg border border-border bg-card p-3">
          {detailLoading ? <p className="muted">Loading overview...</p> : null}

          {!detailLoading && !overview ? (
            <p className="muted">Select a stored release to view details.</p>
          ) : null}

          {overview ? (
            <div className="grid gap-3">
              <div className="flex justify-end gap-2">
                <button
                  type="button"
                  className="ghost"
                  disabled={opening || deleting || detailLoading || !selectedPath || !onOpenInput}
                  onClick={() => void handleOpenInput()}
                >
                  {opening ? "Opening..." : "Open input"}
                </button>
                <button
                  type="button"
                  className="ghost border-destructive text-destructive-text"
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
                <article className="rounded-lg border border-border bg-muted p-2.5 text-foreground">
                  <h3>Path</h3>
                  <p className="mono [overflow-wrap:anywhere]">{overview.SourcePath}</p>
                </article>

                <article className="rounded-lg border border-border bg-muted p-2.5 text-foreground [&_p]:mb-1 [&_p]:mt-0">
                  <h3>External IDs</h3>
                  <p>TMDB: {overview.Identity?.TMDBID || 0}</p>
                  <p>IMDb: {overview.Identity?.IMDBID || 0}</p>
                  <p>TVDB: {overview.Identity?.TVDBID || 0}</p>
                  <p>TVmaze: {overview.Identity?.TVmazeID || 0}</p>
                </article>

                <article className="rounded-lg border border-border bg-muted p-2.5 text-foreground [&_p]:mb-1 [&_p]:mt-0">
                  <h3>Counts</h3>
                  <p>Tracker metadata: {overview.TrackerMetadata?.length || 0}</p>
                  <p>
                    Strict rules:{" "}
                    {overview.TrackerRuleFailures?.filter(
                      (failure) => ruleResultState(failure) === "strict",
                    ).length || 0}
                  </p>
                  <p>
                    Unwaived rules:{" "}
                    {overview.TrackerRuleFailures?.filter(
                      (failure) => ruleResultState(failure) === "unwaived",
                    ).length || 0}
                  </p>
                  <p>
                    Waived rules:{" "}
                    {overview.TrackerRuleFailures?.filter(
                      (failure) => ruleResultState(failure) === "waived",
                    ).length || 0}
                  </p>
                  <p>
                    Rule advisories:{" "}
                    {overview.TrackerRuleFailures?.filter(
                      (failure) => ruleResultState(failure) === "advisory",
                    ).length || 0}
                  </p>
                  <p>Screenshots: {overview.Screenshots?.length || 0}</p>
                  <p>Final selections: {overview.FinalSelections?.length || 0}</p>
                  <p>Uploaded images: {overview.UploadedImages?.length || 0}</p>
                  <p>Upload history: {overview.UploadHistory?.length || 0}</p>
                </article>

                <article className="col-span-full rounded-lg border border-border bg-muted p-2.5 text-foreground">
                  <h3>Description Overrides</h3>
                  {descriptionOverrides.length ? (
                    <ul className="m-0 grid gap-1 pl-4">
                      {descriptionOverrides.map((override, index) => {
                        const groupKey = override.GroupKey?.trim() || "default";
                        return (
                          <li key={`${groupKey}-${override.UpdatedAt}-${index}`}>
                            <strong>{groupKey}</strong>
                            <pre className="m-0 max-h-[220px] overflow-auto whitespace-pre-wrap rounded-md bg-card p-2 text-xs text-card-foreground [overflow-wrap:anywhere]">
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

                <article className="col-span-full rounded-lg border border-border bg-muted p-2.5 text-foreground">
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

                <article className="col-span-full rounded-lg border border-border bg-muted p-2.5 text-foreground">
                  <h3>Tracker Rule Results</h3>
                  {overview.TrackerRuleFailures?.length ? (
                    <ul className="m-0 grid gap-1 pl-4">
                      {overview.TrackerRuleFailures.map((failure, index) => (
                        <li key={`${failure.Tracker}-${failure.Rule}-${index}`}>
                          <strong>{failure.Tracker || "UNKNOWN"}</strong> [
                          {failure.Disposition || "strict"}
                          {ruleResultState(failure) === "waived" ? ", waived" : ""}
                          {ruleResultState(failure) === "unwaived" ? ", unwaived" : ""}]:{" "}
                          {failure.Rule} {failure.Reason ? `— ${failure.Reason}` : ""}
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <p className="muted">No tracker rule results stored.</p>
                  )}
                </article>

                <article className="col-span-full rounded-lg border border-border bg-muted p-2.5 text-foreground">
                  <h3>Provider Display (diagnostic)</h3>
                  <pre className="m-0 max-h-[220px] overflow-auto whitespace-pre-wrap rounded-md bg-card p-2 text-xs text-card-foreground [overflow-wrap:anywhere]">
                    {JSON.stringify(overview.Display || {}, null, 2)}
                  </pre>
                </article>

                <article className="col-span-full rounded-lg border border-border bg-muted p-2.5 text-foreground">
                  <h3>Release Overrides (raw)</h3>
                  <pre className="m-0 max-h-[220px] overflow-auto whitespace-pre-wrap rounded-md bg-card p-2 text-xs text-card-foreground [overflow-wrap:anywhere]">
                    {JSON.stringify(overview.ReleaseNameOverrides || {}, null, 2)}
                  </pre>
                </article>

                <article className="col-span-full rounded-lg border border-border bg-muted p-2.5 text-foreground">
                  <h3>Metadata (raw)</h3>
                  <pre className="m-0 max-h-[220px] overflow-auto whitespace-pre-wrap rounded-md bg-card p-2 text-xs text-card-foreground [overflow-wrap:anywhere]">
                    {JSON.stringify(overview.Metadata || {}, null, 2)}
                  </pre>
                </article>
              </div>
            </div>
          ) : null}

          {displayedError ? <p className="error">{displayedError}</p> : null}
        </div>
      </section>
    </div>
  );
}
