// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { Dispatch, SetStateAction } from "react";
import type { DupeCheckSummary } from "../../types";
import { handleExternalLinkClick } from "../../utils/externalLinks";
import "./styles.css";

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
  handleDupeCheck: () => void;
  setDupeIgnore: Dispatch<SetStateAction<Record<string, boolean>>>;
};

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

  return (
    <section className="dupe-panel">
      <header className="dupe-header">
        <p className="eyebrow">Dupe Checking</p>
        <h1>Check Trackers</h1>
        <p className="subtitle">Scan selected trackers for potential dupes before upload.</p>
      </header>

      <section className="panel dupe-actions">
        <div>
          <p className="label">Source path</p>
          <p className="value dupe-path">{path || "No path selected"}</p>
        </div>
        <button
          className="primary"
          type="button"
          onClick={handleDupeCheck}
          disabled={dupeLoading || !path.trim()}
        >
          {dupeLoading
            ? `Checking ${dupeCompletedCount}/${dupeTotalCount || "?"}...`
            : "Run dupe check"}
        </button>
      </section>

      {showProgress ? <p className="muted">Tracker search progress: {progressText}</p> : null}

      {dupeError ? <p className="error">{dupeError}</p> : null}

      {hasDupeNotes ? (
        <div className="dupe-notes">
          {dupeSummaryNotes.map((note, index) => (
            <span className="dupe-pill" key={`${note}-${index}`}>
              {note}
            </span>
          ))}
        </div>
      ) : null}

      {hasDupeResults ? (
        <div className="dupe-grid">
          {dupeSummary.Results.slice()
            .sort((left, right) => {
              const pathedNote = "pathed torrent match found; skipping dupe search";
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
            })
            .map((result) => {
              const dupeCount = result.Filtered?.length ?? 0;
              const hasDupes = result.HasDupes ?? false;
              const pathedNote = "pathed torrent match found; skipping dupe search";
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
                <article className="dupe-card" key={result.Tracker}>
                  <div className="dupe-card__header">
                    <div>
                      <p className="label">Tracker</p>
                      <p className="value dupe-tracker-title">
                        <span>{result.Tracker}</span>
                        {hasPathedNote ? (
                          <span className="dupe-badge dupe-badge--pathed">
                            Existing torrent in client
                          </span>
                        ) : null}
                        {ruleSkipReason ? (
                          <span className="dupe-badge dupe-badge--rule" title={ruleSkipReason}>
                            Rule checking failed
                          </span>
                        ) : null}
                        {hasFailure ? (
                          <span
                            className="dupe-badge dupe-badge--rule"
                            title={result.Error || "tracker check failed"}
                          >
                            Tracker error
                          </span>
                        ) : null}
                      </p>
                    </div>
                    <div>
                      <p className="label">Dupes</p>
                      <p className="value">{displayDupeCount}</p>
                    </div>
                    {showIgnoreToggle ? (
                      <label className="dupe-toggle">
                        <span>Ignore dupes</span>
                        <input
                          type="checkbox"
                          checked={dupeIgnore[result.Tracker] ?? false}
                          onChange={(event) =>
                            setDupeIgnore((prev) => ({
                              ...prev,
                              [result.Tracker]: event.target.checked,
                            }))
                          }
                        />
                        <span className="dupe-toggle__pill" />
                      </label>
                    ) : null}
                  </div>

                  {visibleNotes.length ? (
                    <div className="dupe-notes">
                      {visibleNotes.map((note, index) => (
                        <span className="dupe-pill" key={`${note}-${index}`}>
                          {note}
                        </span>
                      ))}
                    </div>
                  ) : null}

                  {hasFailure ? (
                    <p className="muted">{result.Error || "Tracker dupe check failed"}</p>
                  ) : null}

                  {result.Filtered?.length ? (
                    <div className="dupe-inline">
                      <p className="value">
                        {result.Filtered.map((entry, index) => (
                          <span className="dupe-inline__item" key={`${entry.Name}-${index}`}>
                            {entry.Link ? (
                              <a
                                href={entry.Link}
                                target="_blank"
                                rel="noreferrer"
                                className="tracker-link"
                                onClick={handleExternalLinkClick}
                              >
                                {entry.Name}
                              </a>
                            ) : (
                              <span>{entry.Name}</span>
                            )}
                            {index < result.Filtered.length - 1 ? (
                              <span className="dupe-inline__sep">, </span>
                            ) : null}
                          </span>
                        ))}
                      </p>
                    </div>
                  ) : null}
                </article>
              );
            })}
        </div>
      ) : (
        <p className="muted">{dupeEmptyMessage}</p>
      )}
    </section>
  );
}
