// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { Dispatch, SetStateAction } from "react";
import { Badge } from "../../components/ui/badge";
import { Button } from "../../components/ui/button";
import { Switch } from "../../components/ui/switch";
import { TrackerIconImage } from "../../components/ui/tracker-icon";
import type { DupeCheckSummary } from "../../types";
import { cn } from "../../utils/cn";
import { handleExternalLinkClick } from "../../utils/externalLinks";

type Props = {
  path: string;
  dupeLoading: boolean;
  dupeError: string;
  dupeSummary: DupeCheckSummary;
  dupeTrackerFlags: Record<string, boolean>;
  dupeIgnore: Record<string, boolean>;
  ruleSkippedTrackerSet: Set<string>;
  ruleSkipReasons: Record<string, string>;
  dupeProgressStatus: string;
  dupeCompletedCount: number;
  dupeTotalCount: number;
  useFavicons?: boolean;
  faviconOnly?: boolean;
  handleDupeCheck: () => void;
  setDupeIgnore: Dispatch<SetStateAction<Record<string, boolean>>>;
};

const pathedNote = "pathed torrent match found; skipping dupe search";

export default function DupeCheckPage(props: Readonly<Props>) {
  const {
    path,
    dupeLoading,
    dupeError,
    dupeSummary,
    dupeTrackerFlags,
    dupeIgnore,
    ruleSkippedTrackerSet,
    ruleSkipReasons,
    dupeProgressStatus,
    dupeCompletedCount,
    dupeTotalCount,
    useFavicons = true,
    faviconOnly = false,
    handleDupeCheck,
    setDupeIgnore,
  } = props;

  const dupeSummaryNotes = dupeSummary.Notes || [];
  const hasDupeNotes = dupeSummaryNotes.length > 0;
  const hasDupeResults = dupeSummary.Results && dupeSummary.Results.length > 0;
  const dupeEmptyMessage = hasDupeNotes ? dupeSummaryNotes.join(" ") : "No dupe results yet.";
  const showProgress =
    dupeLoading || dupeProgressStatus === "running" || dupeProgressStatus === "queued";
  const progressText =
    dupeTotalCount > 0
      ? `${Math.min(dupeCompletedCount, dupeTotalCount)}/${dupeTotalCount} trackers complete`
      : "Preparing tracker search";
  const sortedResults = (dupeSummary.Results || []).slice().sort((left, right) => {
    const leftCount = left.Filtered?.length ?? 0;
    const rightCount = right.Filtered?.length ?? 0;
    const leftPathed = left.Notes?.includes(pathedNote) ?? false;
    const rightPathed = right.Notes?.includes(pathedNote) ?? false;
    const leftRuleSkip = ruleSkippedTrackerSet.has(left.Tracker.toLowerCase().trim());
    const rightRuleSkip = ruleSkippedTrackerSet.has(right.Tracker.toLowerCase().trim());
    const leftHasDupes = leftCount > 0;
    const rightHasDupes = rightCount > 0;

    if (leftHasDupes && rightHasDupes && rightCount !== leftCount) {
      return rightCount - leftCount;
    }
    if (leftHasDupes !== rightHasDupes) {
      return leftHasDupes ? -1 : 1;
    }
    if (leftPathed !== rightPathed) {
      return leftPathed ? -1 : 1;
    }
    if (leftRuleSkip !== rightRuleSkip) {
      return leftRuleSkip ? -1 : 1;
    }
    return left.Tracker.localeCompare(right.Tracker);
  });
  const availableTrackers = sortedResults
    .filter((result) => {
      const normalizedTracker = result.Tracker.toLowerCase().trim();
      const status = String(result.Status || "")
        .toLowerCase()
        .trim();
      const hasFailure = status === "failed" || Boolean(result.Error?.trim());
      const hasPathedNote = result.Notes?.includes(pathedNote) ?? false;
      if (hasFailure) return false;
      if (hasPathedNote) return false;
      if (dupeTrackerFlags[result.Tracker]) return false;
      if (ruleSkippedTrackerSet.has(normalizedTracker)) return false;
      return true;
    })
    .map((result) => result.Tracker);
  const unavailableCount = Math.max(sortedResults.length - availableTrackers.length, 0);

  return (
    <section className="flex flex-col gap-3">
      <header className="max-w-3xl">
        <p className="eyebrow">Dupe Checking</p>
        <h1>Check Trackers</h1>
        <p className="subtitle">Scan selected trackers for potential dupes before upload.</p>
      </header>

      <section className="panel flex flex-wrap items-center justify-between gap-3 py-3">
        <div className="min-w-0">
          <p className="label">Source path</p>
          <p className="value break-words text-sm">{path || "No path selected"}</p>
          {hasDupeResults ? (
            <div className="mt-2 flex flex-wrap items-center gap-1.5 text-xs text-[var(--muted)]">
              <span className="font-semibold text-[var(--text)]">
                Available for upload: {availableTrackers.length}
              </span>
              {availableTrackers.length ? (
                availableTrackers.map((tracker) => (
                  <Badge
                    aria-label={faviconOnly && useFavicons ? tracker : undefined}
                    className="text-[var(--text)] flex items-center gap-1"
                    style={{
                      backgroundColor: "color-mix(in srgb, var(--accent-2) 14%, transparent)",
                      borderColor: "color-mix(in srgb, var(--accent-2) 42%, transparent)",
                    }}
                    key={`available-${tracker}`}
                  >
                    <TrackerIconImage tracker={tracker} enabled={useFavicons} />
                    {faviconOnly && useFavicons ? null : tracker}
                  </Badge>
                ))
              ) : (
                <span>No trackers passed.</span>
              )}
              {unavailableCount > 0 ? <span>{unavailableCount} blocked.</span> : null}
            </div>
          ) : null}
        </div>
        <Button
          className="ml-auto"
          variant="primary"
          type="button"
          onClick={handleDupeCheck}
          disabled={dupeLoading || !path.trim()}
        >
          {dupeLoading
            ? `Checking ${dupeCompletedCount}/${dupeTotalCount || "?"}...`
            : "Run dupe check"}
        </Button>
      </section>

      {showProgress ? (
        <p className="muted text-sm">Tracker search progress: {progressText}</p>
      ) : null}

      {dupeError ? <p className="error">{dupeError}</p> : null}

      {hasDupeNotes ? (
        <div className="flex flex-wrap gap-1.5">
          {dupeSummaryNotes.map((note, index) => (
            <Badge tone="info" key={`${note}-${index}`}>
              {note}
            </Badge>
          ))}
        </div>
      ) : null}

      {hasDupeResults ? (
        <div className="overflow-hidden rounded-lg border border-white/10 bg-[rgba(12,16,26,0.76)]">
          <div className="hidden grid-cols-[minmax(90px,140px)_58px_minmax(0,1fr)_116px] gap-3 border-b border-white/10 px-3 py-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-[var(--muted)] md:grid">
            <span>Tracker</span>
            <span>Dupes</span>
            <span>Matches</span>
            <span>Action</span>
          </div>
          <div className="divide-y divide-white/10">
            {sortedResults.map((result) => {
              const dupeCount = result.Filtered?.length ?? 0;
              const hasDupes = result.HasDupes ?? false;
              const hasPathedNote = result.Notes?.includes(pathedNote) ?? false;
              const status = String(result.Status || "")
                .toLowerCase()
                .trim();
              const hasFailure = status === "failed" || Boolean(result.Error?.trim());
              const normalizedTracker = result.Tracker.toLowerCase().trim();
              const ruleSkipReason = ruleSkipReasons[normalizedTracker];
              const visibleNotes =
                result.Notes?.filter((note) => {
                  if (note === pathedNote) return false;
                  const normalizedNote = note.toLowerCase().trim();
                  if (normalizedNote.startsWith("skip:")) return false;
                  if (normalizedNote.startsWith("rule check failed")) return false;
                  if (ruleSkipReason && note.trim() === ruleSkipReason) return false;
                  return true;
                }) ?? [];
              const showIgnoreToggle = !hasPathedNote && (hasDupes || dupeCount > 0);
              const displayDupeCount =
                (dupeTrackerFlags[result.Tracker] ?? hasDupes) ? dupeCount : 0;

              return (
                <article
                  className="grid grid-cols-[minmax(72px,96px)_44px_minmax(0,1fr)] gap-2 px-2 py-2 text-sm md:grid-cols-[minmax(90px,140px)_58px_minmax(0,1fr)_116px] md:gap-3 md:px-3"
                  key={result.Tracker}
                >
                  <div className="min-w-0 flex items-center gap-2">
                    <TrackerIconImage tracker={result.Tracker} enabled={useFavicons} />
                    {faviconOnly && useFavicons ? null : (
                      <p className="font-bold text-[var(--text)]">{result.Tracker}</p>
                    )}
                  </div>

                  <p
                    className={cn(
                      "font-bold tabular-nums",
                      displayDupeCount > 0 ? "text-[var(--accent)]" : "text-[var(--muted)]",
                    )}
                  >
                    {displayDupeCount}
                  </p>

                  <div className="min-w-0">
                    {hasPathedNote || ruleSkipReason || hasFailure || visibleNotes.length ? (
                      <p className="mb-1 flex flex-wrap items-center gap-1 text-sm leading-5">
                        {hasPathedNote ? <Badge tone="info">In client</Badge> : null}

                        {ruleSkipReason ? (
                          <>
                            <Badge tone="danger">Rule failed</Badge>
                            <span className="text-[var(--muted)]">{ruleSkipReason}</span>
                          </>
                        ) : null}

                        {hasFailure ? (
                          <>
                            <Badge tone="danger">Error</Badge>
                            <span className="text-[var(--muted)]">
                              {result.Error || "Tracker dupe check failed"}
                            </span>
                          </>
                        ) : null}

                        {visibleNotes.map((note, index) => (
                          <Badge tone="info" key={`${note}-${index}`}>
                            {note}
                          </Badge>
                        ))}
                      </p>
                    ) : null}

                    {result.Filtered?.length ? (
                      <p className="value text-sm leading-5">
                        {result.Filtered.map((entry, index) => (
                          <span className="inline" key={`${entry.Name}-${index}`}>
                            {entry.Link ? (
                              <a
                                href={entry.Link}
                                target="_blank"
                                rel="noreferrer"
                                className="tracker-link"
                                onAuxClick={handleExternalLinkClick}
                                onClick={handleExternalLinkClick}
                              >
                                {entry.Name}
                              </a>
                            ) : (
                              <span>{entry.Name}</span>
                            )}
                            {index < result.Filtered.length - 1 ? (
                              <span className="text-[var(--muted)]">, </span>
                            ) : null}
                          </span>
                        ))}
                      </p>
                    ) : null}
                  </div>

                  <div className="col-span-3 md:col-span-1">
                    {showIgnoreToggle ? (
                      <div className="inline-flex items-center gap-2 text-xs font-semibold text-[var(--text)]">
                        <span>Ignore</span>
                        <Switch
                          aria-label={`Ignore dupes for ${result.Tracker}`}
                          checked={dupeIgnore[result.Tracker] ?? false}
                          onChange={(event) =>
                            setDupeIgnore((prev) => ({
                              ...prev,
                              [result.Tracker]: event.target.checked,
                            }))
                          }
                        />
                      </div>
                    ) : null}
                  </div>
                </article>
              );
            })}
          </div>
        </div>
      ) : (
        <p className="muted">{dupeEmptyMessage}</p>
      )}
    </section>
  );
}
