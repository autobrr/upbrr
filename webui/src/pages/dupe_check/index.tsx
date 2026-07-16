// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { Badge } from "../../components/ui/badge";
import { Button } from "../../components/ui/button";
import { PillCheckbox } from "../../components/ui/checkbox";
import { Switch } from "../../components/ui/switch";
import { TrackerIconImage } from "../../components/ui/tracker-icon";
import type { DuplicatesFacet } from "../../releaseSession/types";
import type { DupeCheckResult, DupeCheckTrackerState, TrackerUploadItem } from "../../types";
import type { TrackerIconCache } from "../../hooks/useTrackerIcons";
import { trackerIconFor } from "../../hooks/useTrackerIcons";
import { cn } from "../../utils/cn";
import { dupeSkipReason, isRuleSkippedResult } from "../../utils/dupeCheck";
import { handleExternalLinkClick } from "../../utils/externalLinks";

type Props = {
  facet: DuplicatesFacet;
  sourcePath: string;
  trackerUploadItems: readonly TrackerUploadItem[];
  useFavicons?: boolean;
  faviconOnly?: boolean;
  trackerIconSrcByName: TrackerIconCache;
};

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

/** Prefers per-tracker snapshot results over summary rows. */
const displayResultsFor = (
  summaryResults: DupeCheckResult[],
  trackerStates: DupeCheckTrackerState[] | undefined,
) => {
  const stateResults = (trackerStates || [])
    .map(resultFromTrackerState)
    .filter((result): result is DupeCheckResult => Boolean(result));
  return stateResults.length > 0 ? stateResults : summaryResults;
};

/** Presents per-tracker duplicate progress, outcomes, and authorization controls. */
export default function DupeCheckPage(props: Readonly<Props>) {
  const {
    facet,
    sourcePath,
    trackerUploadItems,
    useFavicons = true,
    faviconOnly = false,
    trackerIconSrcByName,
  } = props;
  const { view } = facet;
  const snapshot = view.snapshot;
  const dupeLoading = view.status === "running";
  const dupeSummary = snapshot?.summary;
  const dupeSummaryNotes = dupeSummary?.Notes || [];
  const dupeTrackerStates = snapshot?.trackers;
  const ignoredTrackers = new Set(view.ignoredTrackers);
  const dupeError = view.error || view.transientError;
  const dupeProgressStatus = snapshot?.status || view.status;
  const dupeCompletedCount = snapshot?.completedCount || 0;
  const dupeTotalCount = snapshot?.totalCount || view.selectedTrackers.length;
  const activeTrackerStates = (dupeTrackerStates || []).filter(
    (tracker) => !isFinishedTrackerStatus(tracker.status),
  );

  const hasDupeNotes = dupeSummaryNotes.length > 0;
  const displayResults = displayResultsFor(dupeSummary?.Results || [], dupeTrackerStates);
  const hasDupeResults = displayResults.length > 0;
  const dupeEmptyMessage = hasDupeNotes ? dupeSummaryNotes.join(" ") : "No dupe results yet.";
  const showProgress =
    dupeLoading || dupeProgressStatus === "running" || dupeProgressStatus === "queued";
  const progressText =
    dupeTotalCount > 0
      ? `${Math.min(dupeCompletedCount, dupeTotalCount)}/${dupeTotalCount} trackers complete`
      : "Preparing tracker search";
  const progressPercent =
    dupeTotalCount > 0
      ? Math.round((Math.min(dupeCompletedCount, dupeTotalCount) / dupeTotalCount) * 100)
      : 0;
  const sortedResults = displayResults.slice().sort((left, right) => {
    const leftCount = left.Filtered?.length ?? 0;
    const rightCount = right.Filtered?.length ?? 0;
    const leftInClient = left.Match?.MatchedReason === "in_client";
    const rightInClient = right.Match?.MatchedReason === "in_client";
    const leftRuleSkip = isRuleSkippedResult(left);
    const rightRuleSkip = isRuleSkippedResult(right);
    const leftHasDupes = leftCount > 0;
    const rightHasDupes = rightCount > 0;

    if (leftHasDupes && rightHasDupes && rightCount !== leftCount) {
      return rightCount - leftCount;
    }
    if (leftHasDupes !== rightHasDupes) {
      return leftHasDupes ? -1 : 1;
    }
    if (leftInClient !== rightInClient) {
      return leftInClient ? -1 : 1;
    }
    if (leftRuleSkip !== rightRuleSkip) {
      return leftRuleSkip ? -1 : 1;
    }
    return left.Tracker.localeCompare(right.Tracker);
  });
  const eligibility = view.eligibility;
  const availableTrackers = eligibility?.EligibleTrackers || [];
  const unavailableCount = (eligibility?.Trackers || []).filter(
    (tracker) => !tracker.Eligible,
  ).length;
  const hideTrackerNames = faviconOnly && useFavicons;
  const selectedTrackers = new Set(view.selectedTrackers);
  const trackerSelectionRequired = selectedTrackers.size === 0;

  return (
    <section className="flex flex-col gap-3">
      <header className="max-w-3xl">
        <p className="eyebrow">Dupe Checking</p>
        <h1>Check Trackers</h1>
        <p className="subtitle">Scan selected trackers for potential dupes before upload.</p>
      </header>

      <section className="panel flex flex-col gap-2 py-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <p className="label">Trackers</p>
            <p className="muted text-sm">Select trackers for this duplicate check.</p>
          </div>
          <span className="muted text-xs">
            {selectedTrackers.size}/{trackerUploadItems.length} selected
          </span>
        </div>
        {trackerUploadItems.length ? (
          <div className="tracker-pills">
            {trackerUploadItems.map((tracker) => {
              const normalized = tracker.name.trim().toUpperCase();
              return (
                <PillCheckbox
                  aria-label={tracker.name}
                  checked={selectedTrackers.has(normalized)}
                  key={tracker.name}
                  onCheckedChange={(checked) => {
                    const next = new Set(selectedTrackers);
                    if (checked) next.add(normalized);
                    else next.delete(normalized);
                    facet.chooseTrackers([...next]);
                  }}
                >
                  <span className="flex items-center gap-1.5">
                    <TrackerIconImage
                      tracker={tracker.name}
                      iconSrc={trackerIconFor(trackerIconSrcByName, tracker.name)}
                      enabled={useFavicons}
                    />
                    {hideTrackerNames ? null : tracker.name}
                  </span>
                </PillCheckbox>
              );
            })}
          </div>
        ) : (
          <p className="muted">No configured tracker entries found.</p>
        )}
        {trackerSelectionRequired ? (
          <p className="muted text-sm">Select at least one tracker to run duplicate checking.</p>
        ) : null}
      </section>

      <section className="panel flex flex-wrap items-center justify-between gap-3 py-3">
        <div className="min-w-0">
          <p className="label">Source path</p>
          <p className="value break-words text-sm">{sourcePath || "No path selected"}</p>
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
          onClick={() => void facet.run()}
          disabled={dupeLoading || !sourcePath.trim() || trackerSelectionRequired}
        >
          {dupeLoading
            ? `Checking ${dupeCompletedCount}/${dupeTotalCount || "?"}...`
            : "Run dupe check"}
        </Button>
      </section>

      {showProgress ? (
        <section className="panel grid gap-2 py-3" role="status" aria-live="polite">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <p className="font-semibold text-[var(--text)]">Duplicate check progress</p>
            <p className="muted text-sm">{progressText}</p>
          </div>
          <div
            aria-label="Duplicate check progress"
            aria-valuemax={dupeTotalCount || undefined}
            aria-valuemin={0}
            aria-valuenow={dupeCompletedCount}
            className="h-2 w-full overflow-hidden rounded-full bg-white/10"
            role="progressbar"
          >
            <div
              className="h-full rounded-full bg-[var(--accent-2)] transition-[width]"
              style={{ width: `${progressPercent}%` }}
            />
          </div>
          {activeTrackerStates.length ? (
            <div className="grid gap-1 text-sm">
              {activeTrackerStates.map((tracker) => (
                <div
                  className="flex flex-wrap items-center justify-between gap-2 rounded border border-white/10 bg-white/5 px-2 py-1.5"
                  key={tracker.tracker}
                >
                  <span className="font-semibold">{tracker.tracker}</span>
                  <span className="muted">{tracker.message || tracker.status || "Queued"}</span>
                </div>
              ))}
            </div>
          ) : null}
        </section>
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
              const inClient = result.Match?.MatchedReason === "in_client";
              const status = String(result.Status || "")
                .toLowerCase()
                .trim();
              const hasFailure = status === "failed";
              const skipReason = dupeSkipReason(result);
              const ruleSkipReason = isRuleSkippedResult(result)
                ? skipReason || "rule check failed"
                : "";
              const authRequired =
                result.SkipCode === "tracker_auth_not_ready" ||
                result.SkipCode === "auth_not_ready";
              const skippedReason =
                result.Skipped && !ruleSkipReason && !authRequired ? skipReason : "";
              const visibleNotes = result.Notes || [];
              const showIgnoreToggle =
                !inClient && (hasDupes || Boolean(result.Skipped) || hasFailure);
              const displayDupeCount = dupeCount;

              return (
                <article
                  className="grid grid-cols-[minmax(72px,96px)_44px_minmax(0,1fr)] gap-2 px-2 py-2 text-sm md:grid-cols-[minmax(90px,140px)_58px_minmax(0,1fr)_116px] md:gap-3 md:px-3"
                  key={result.Tracker}
                >
                  <div className="min-w-0 flex items-center gap-2">
                    <div
                      className="flex shrink-0 flex-wrap items-center gap-1"
                      aria-label={hideTrackerNames ? result.Tracker : undefined}
                    >
                      <TrackerIconImage
                        tracker={result.Tracker}
                        iconSrc={trackerIconFor(trackerIconSrcByName, result.Tracker)}
                        enabled={useFavicons}
                      />
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
                    {inClient ||
                    ruleSkipReason ||
                    authRequired ||
                    skippedReason ||
                    hasFailure ||
                    visibleNotes.length ? (
                      <p className="mb-1 flex flex-wrap items-center gap-1 text-sm leading-5">
                        {inClient ? <Badge tone="info">In client</Badge> : null}

                        {ruleSkipReason ? (
                          <>
                            <Badge tone="danger">Rule failed</Badge>
                            <span className="text-[var(--muted)]">{ruleSkipReason}</span>
                          </>
                        ) : null}

                        {authRequired ? (
                          <>
                            <Badge tone="danger">Auth required</Badge>
                            <span className="text-[var(--muted)]">{skipReason}</span>
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
                          checked={ignoredTrackers.has(result.Tracker)}
                          onChange={(event) =>
                            facet.setIgnored(result.Tracker, event.target.checked)
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
