// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { Dispatch, SetStateAction } from "react";
import { Badge } from "../../components/ui/badge";
import { Button } from "../../components/ui/button";
import { Switch } from "../../components/ui/switch";
import { TrackerIconImage } from "../../components/ui/tracker-icon";
import type { DupeCheckResult, DupeCheckSummary, DupeCheckTrackerState } from "../../types";
import type { TrackerIconCache } from "../../hooks/useTrackerIcons";
import { trackerIconFor } from "../../hooks/useTrackerIcons";
import { cn } from "../../utils/cn";
import { handleExternalLinkClick } from "../../utils/externalLinks";

type Props = {
  path: string;
  dupeLoading: boolean;
  dupeError: string;
  dupeSummary: DupeCheckSummary;
  dupeTrackerStates?: DupeCheckTrackerState[];
  dupeTrackerFlags: Record<string, boolean>;
  dupeIgnore: Record<string, boolean>;
  ruleSkippedTrackerSet: Set<string>;
  ruleSkipReasons: Record<string, string>;
  dupeProgressStatus: string;
  dupeCompletedCount: number;
  dupeTotalCount: number;
  useFavicons?: boolean;
  faviconOnly?: boolean;
  trackerIconSrcByName: TrackerIconCache;
  handleDupeCheck: () => void;
  setDupeIgnore: Dispatch<SetStateAction<Record<string, boolean>>>;
};

const pathedNote = "pathed torrent match found; skipping dupe search";

const splitTrackerLabel = (value: string) =>
  value
    .split(",")
    .map((entry) => entry.trim())
    .filter((entry) => entry.length > 0);

const isRuleFailureReason = (reason: string) =>
  reason.toLowerCase().trim().startsWith("rule check failed");

const isFinishedTrackerStatus = (status: string) => {
  switch (status.toLowerCase().trim()) {
    case "complete":
    case "completed":
    case "skipped":
    case "failed":
    case "canceled":
      return true;
    default:
      return false;
  }
};

/**
 * Converts a finished tracker job state into a displayable dupe result.
 * Pending states without backend results are skipped so progress snapshots do not
 * appear as upload-eligible tracker rows.
 */
const resultFromTrackerState = (state: DupeCheckTrackerState): DupeCheckResult | null => {
  const resultTracker = String(state.result?.Tracker || "").trim();
  const status = String(state.result?.Status || state.status || "").trim();
  if (!resultTracker && !isFinishedTrackerStatus(status)) return null;
  const tracker = String(resultTracker || state.tracker || "").trim();
  if (!tracker) return null;
  return {
    ...state.result,
    Tracker: tracker,
    Status: status,
  };
};

/**
 * Prefers per-tracker snapshot results over grouped summary rows.
 *
 * The backend may group rule-failure skips by reason for compact summaries, but
 * the page needs individual tracker labels/icons/counts when job state is present.
 */
const displayResultsFor = (
  summaryResults: DupeCheckResult[],
  trackerStates: DupeCheckTrackerState[] | undefined,
) => {
  const stateResults = (trackerStates || [])
    .map(resultFromTrackerState)
    .filter((result): result is DupeCheckResult => Boolean(result));
  return stateResults.length > 0 ? stateResults : summaryResults;
};

/** Expands comma-grouped tracker labels while preserving normal single-tracker rows. */
const trackerNamesForResult = (result: DupeCheckResult) => {
  const splitNames = splitTrackerLabel(result.Tracker);
  return splitNames.length > 0 && result.Tracker.includes(",") ? splitNames : [result.Tracker];
};

export default function DupeCheckPage(props: Readonly<Props>) {
  const {
    path,
    dupeLoading,
    dupeError,
    dupeSummary,
    dupeTrackerStates,
    dupeTrackerFlags,
    dupeIgnore,
    ruleSkippedTrackerSet,
    ruleSkipReasons,
    dupeProgressStatus,
    dupeCompletedCount,
    dupeTotalCount,
    useFavicons = true,
    faviconOnly = false,
    trackerIconSrcByName,
    handleDupeCheck,
    setDupeIgnore,
  } = props;

  const dupeSummaryNotes = dupeSummary.Notes || [];
  const hasDupeNotes = dupeSummaryNotes.length > 0;
  const displayResults = displayResultsFor(dupeSummary.Results || [], dupeTrackerStates);
  const hasDupeResults = displayResults.length > 0;
  const dupeEmptyMessage = hasDupeNotes ? dupeSummaryNotes.join(" ") : "No dupe results yet.";
  const showProgress =
    dupeLoading || dupeProgressStatus === "running" || dupeProgressStatus === "queued";
  const progressText =
    dupeTotalCount > 0
      ? `${Math.min(dupeCompletedCount, dupeTotalCount)}/${dupeTotalCount} trackers complete`
      : "Preparing tracker search";
  const sortedResults = displayResults.slice().sort((left, right) => {
    const leftCount = left.Filtered?.length ?? 0;
    const rightCount = right.Filtered?.length ?? 0;
    const leftPathed = left.Notes?.includes(pathedNote) ?? false;
    const rightPathed = right.Notes?.includes(pathedNote) ?? false;
    const leftReason = String(left.SkipReason || "").trim();
    const rightReason = String(right.SkipReason || "").trim();
    const leftRuleSkip =
      ruleSkippedTrackerSet.has(left.Tracker.toLowerCase().trim()) ||
      (left.Skipped && isRuleFailureReason(leftReason));
    const rightRuleSkip =
      ruleSkippedTrackerSet.has(right.Tracker.toLowerCase().trim()) ||
      (right.Skipped && isRuleFailureReason(rightReason));
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
  const availability = sortedResults.reduce(
    (next, result) => {
      const normalizedTracker = result.Tracker.toLowerCase().trim();
      const status = String(result.Status || "")
        .toLowerCase()
        .trim();
      const hasFailure = status === "failed" || Boolean(result.Error?.trim());
      const hasPathedNote = result.Notes?.includes(pathedNote) ?? false;
      const isUnavailable =
        hasFailure ||
        hasPathedNote ||
        Boolean(result.Skipped) ||
        Boolean(dupeTrackerFlags[result.Tracker]) ||
        ruleSkippedTrackerSet.has(normalizedTracker);

      trackerNamesForResult(result).forEach((tracker) => {
        const normalized = tracker.toLowerCase().trim();
        if (!normalized || next.seen.has(normalized)) return;
        next.seen.add(normalized);
        if (isUnavailable) {
          next.unavailableCount += 1;
        } else {
          next.availableTrackers.push(tracker);
        }
      });
      return next;
    },
    { availableTrackers: [] as string[], unavailableCount: 0, seen: new Set<string>() },
  );
  const availableTrackers = availability.availableTrackers;
  const unavailableCount = availability.unavailableCount;
  const hideTrackerNames = faviconOnly && useFavicons;

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
                availableTrackers.map((tracker) => {
                  const iconSrc = trackerIconFor(trackerIconSrcByName, tracker);
                  return (
                    <Badge
                      aria-label={hideTrackerNames ? tracker : undefined}
                      className="text-[var(--text)] flex items-center gap-1"
                      style={{
                        backgroundColor: "color-mix(in srgb, var(--accent-2) 14%, transparent)",
                        borderColor: "color-mix(in srgb, var(--accent-2) 42%, transparent)",
                      }}
                      key={`available-${tracker}`}
                    >
                      <TrackerIconImage tracker={tracker} iconSrc={iconSrc} enabled={useFavicons} />
                      {hideTrackerNames ? null : tracker}
                    </Badge>
                  );
                })
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
              const skipReason = String(result.SkipReason || "").trim();
              const ruleSkipReason =
                ruleSkipReasons[normalizedTracker] ||
                (result.Skipped && isRuleFailureReason(skipReason) ? skipReason : "");
              const skippedReason = result.Skipped && !ruleSkipReason ? skipReason : "";
              const visibleNotes =
                result.Notes?.filter((note) => {
                  if (note === pathedNote) return false;
                  const normalizedNote = note.toLowerCase().trim();
                  if (normalizedNote.startsWith("skip:")) return false;
                  if (normalizedNote.startsWith("rule check failed")) return false;
                  if (ruleSkipReason && note.trim() === ruleSkipReason) return false;
                  if (skippedReason && note.trim() === skippedReason) return false;
                  return true;
                }) ?? [];
              const showIgnoreToggle = !hasPathedNote && (hasDupes || dupeCount > 0);
              const displayDupeCount =
                (dupeTrackerFlags[result.Tracker] ?? hasDupes) ? dupeCount : 0;
              const displayTrackers =
                hasPathedNote || ruleSkipReason || skippedReason
                  ? trackerNamesForResult(result)
                  : [];
              const iconTrackers = displayTrackers.length > 0 ? displayTrackers : [result.Tracker];

              return (
                <article
                  className="grid grid-cols-[minmax(72px,96px)_44px_minmax(0,1fr)] gap-2 px-2 py-2 text-sm md:grid-cols-[minmax(90px,140px)_58px_minmax(0,1fr)_116px] md:gap-3 md:px-3"
                  key={result.Tracker}
                >
                  <div className="min-w-0 flex items-center gap-2">
                    <div
                      className="flex shrink-0 flex-wrap items-center gap-1"
                      aria-label={hideTrackerNames ? iconTrackers.join(", ") : undefined}
                    >
                      {iconTrackers.map((tracker) => (
                        <TrackerIconImage
                          tracker={tracker}
                          iconSrc={trackerIconFor(trackerIconSrcByName, tracker)}
                          enabled={useFavicons}
                          key={`${result.Tracker}-${tracker}`}
                        />
                      ))}
                    </div>
                    {hideTrackerNames ? null : (
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
                    {hasPathedNote ||
                    ruleSkipReason ||
                    skippedReason ||
                    hasFailure ||
                    visibleNotes.length ? (
                      <p className="mb-1 flex flex-wrap items-center gap-1 text-sm leading-5">
                        {hasPathedNote ? <Badge tone="info">In client</Badge> : null}

                        {ruleSkipReason ? (
                          <>
                            <Badge tone="danger">Rule failed</Badge>
                            <span className="text-[var(--muted)]">{ruleSkipReason}</span>
                          </>
                        ) : null}

                        {skippedReason ? (
                          <>
                            <Badge tone="info">Skipped</Badge>
                            <span className="text-[var(--muted)]">{skippedReason}</span>
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
