// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useMemo, useState } from "react";
import { Button } from "../../components/ui/button";
import type { UploadFacet } from "../../releaseSession/types";
import { canExecuteUpload } from "../../releaseSession/uploadEligibility";
import type {
  TrackerDryRunReport,
  TrackerLaneOutcome,
  TrackerPolicyDecision,
  TrackerReleaseProjection,
} from "../../api/generated/release-workflow";

type Props = Readonly<{
  facet: UploadFacet;
}>;

/** One tracker upload card: the projection plus any dry-run report merged by tracker. */
type TrackerUploadCard = Readonly<{
  trackerId: string;
  projection?: TrackerReleaseProjection;
  report?: TrackerDryRunReport;
  outcome?: TrackerLaneOutcome;
}>;

/**
 * Backend skip reasons rendered as operator-facing text. These state the
 * decision only. A duplicate override is not always available, so no reason
 * sends the owner back to an earlier stage.
 */
const UPLOAD_SKIP_LABELS: Readonly<Record<string, string>> = {
  duplicate_found: "duplicate found",
  duplicate_check_failed: "duplicate check failed",
  not_ready: "tracker is not ready to upload",
  tracker_not_approved_in_gate: "tracker was not approved",
  image_hosting_failed: "image hosting failed",
  description_skipped: "no description was prepared",
  description_failed: "the description could not be prepared",
  upload_preparation_failed: "upload preparation did not complete",
  upload_preparation_skipped: "skipped during upload preparation",
};

/** Renders the backend downstream decision for one tracker; never derives it. */
function uploadEligibilityLabel(outcome?: TrackerLaneOutcome): string {
  if (!outcome) return "";
  if (outcome.uploadEligibility === "eligible") return "Will upload";
  if (outcome.uploadEligibility !== "skipped") return "";
  // The backend detail names the exact rule, so it replaces the generic label.
  const detail = outcome.uploadSkipDetail?.trim();
  if (detail) return `Skipped: ${detail}`;
  const reason = outcome.uploadSkipReason
    ? UPLOAD_SKIP_LABELS[outcome.uploadSkipReason]
    : undefined;
  return reason ? `Skipped: ${reason}` : "Skipped";
}

const releaseNameOverrideNotices = (projection: TrackerReleaseProjection | undefined) =>
  (projection?.policyDecisions || []).filter(
    (decision) =>
      decision.code.startsWith("release_name_override") &&
      (decision.decision === "enforced" || decision.decision === "rebuilt"),
  );

const releaseNameOverrideKey = (decision: TrackerPolicyDecision) =>
  [decision.code, decision.namingRole || "", decision.namingRuleId || ""].join("\u0000");

/** Thin presentation adapter for workflow dry-run and upload state. */
export default function TrackerUploadPage({ facet }: Props) {
  const { view } = facet;
  const [expandedTrackers, setExpandedTrackers] = useState<Record<string, boolean>>({});
  const selected = useMemo(() => new Set(view.selectedTrackers), [view.selectedTrackers]);
  const questionnaireProjections = useMemo(
    () =>
      (view.projections?.projections || []).filter(
        (projection) => selected.has(projection.trackerId) && projection.questionnaire?.length,
      ),
    [selected, view.projections],
  );
  const trackerCards = useMemo(() => {
    const reports = view.dryRunResult?.reports || [];
    const reportsByTracker = new Map(reports.map((report) => [report.trackerId, report]));
    const outcomesByTracker = new Map(
      view.trackerOutcomes.map((outcome) => [outcome.trackerId, outcome]),
    );
    const cards: TrackerUploadCard[] = (view.projections?.projections || [])
      .filter((projection) => selected.has(projection.trackerId))
      .map((projection) => ({
        trackerId: projection.trackerId,
        projection,
        report: reportsByTracker.get(projection.trackerId),
        outcome: outcomesByTracker.get(projection.trackerId),
      }));
    const projectedIDs = new Set(cards.map((card) => card.trackerId));
    return [
      ...cards,
      ...reports
        .filter((report) => !projectedIDs.has(report.trackerId))
        .map(
          (report): TrackerUploadCard => ({
            trackerId: report.trackerId,
            report,
            outcome: outcomesByTracker.get(report.trackerId),
          }),
        ),
    ];
  }, [selected, view.dryRunResult, view.projections, view.trackerOutcomes]);
  const uploadRunning = view.uploadStatus === "running";
  const excludedTrackers = useMemo(
    () => new Set(view.submissionExclusions.map((item) => item.trackerId)),
    [view.submissionExclusions],
  );
  const hasDryRunCandidate =
    view.submissionExclusions.length === 0 ||
    view.selectedTrackers.some((tracker) => !excludedTrackers.has(tracker));
  const hasExecutableUpload = canExecuteUpload(
    view.trackerOutcomes,
    view.selectedTrackers,
    excludedTrackers,
  );
  const failedTrackers = (view.result?.results || [])
    .filter((result) => result.submissionStatus === "failed")
    .map((result) => result.trackerId);
  const clientInjectionFailures = (view.result?.results || [])
    .filter(
      (result) =>
        result.submissionStatus === "completed" &&
        result.clientInjectionStatus === "failed" &&
        result.clientFailureCode === "client_injection",
    )
    .map((result) => result.trackerId);

  const toggleTrackerDetails = (key: string) => {
    setExpandedTrackers((current) => ({ ...current, [key]: !current[key] }));
  };

  return (
    <section className="flex flex-col gap-4">
      <header className="max-w-3xl">
        <p className="eyebrow">Tracker Upload</p>
        <h1>Review &amp; Upload</h1>
        <p className="subtitle">
          {view.liveTest
            ? "Run a dry run with normal rules. Live testing disables tracker submission and client injection."
            : "Optionally run a dry run, or upload directly. A tracker failure does not stop unrelated uploads."}
        </p>
      </header>

      {questionnaireProjections.length ? (
        <section className="panel grid gap-3">
          <h2>Tracker questions</h2>
          {questionnaireProjections.map((projection) => (
            <fieldset className="grid gap-3" key={projection.trackerId}>
              <legend className="font-semibold">{projection.displayName}</legend>
              {projection.questionnaire?.map((field) => (
                <label className="grid gap-1" key={field.key}>
                  <span className="label">
                    {field.label || field.key}
                    {field.required ? " *" : ""}
                  </span>
                  {field.options?.length ? (
                    <select
                      value={view.questionnaireAnswers[projection.trackerId]?.[field.key] ?? ""}
                      onChange={(event) =>
                        facet.answerQuestionnaire(
                          projection.trackerId,
                          field.key,
                          event.target.value,
                        )
                      }
                    >
                      <option value="">Select</option>
                      {field.options.map((option) => (
                        <option key={option} value={option}>
                          {option}
                        </option>
                      ))}
                    </select>
                  ) : (
                    <input
                      value={view.questionnaireAnswers[projection.trackerId]?.[field.key] ?? ""}
                      onChange={(event) =>
                        facet.answerQuestionnaire(
                          projection.trackerId,
                          field.key,
                          event.target.value,
                        )
                      }
                    />
                  )}
                </label>
              ))}
            </fieldset>
          ))}
        </section>
      ) : null}

      {view.submissionExclusions.length ? (
        <section className="panel grid gap-3" aria-label="Submission exclusions">
          <h2>Already submitted</h2>
          <p className="muted">
            Confirmed tracker submissions are excluded from duplicate checks and upload actions.
          </p>
          <ul className="grid gap-2">
            {view.submissionExclusions.map((exclusion) => (
              <li
                className="rounded border border-white/10 bg-white/5 p-3"
                key={exclusion.trackerId}
              >
                <strong>{exclusion.trackerId}</strong>
                <span className="muted">
                  {exclusion.reason === "already_uploaded"
                    ? "Already uploaded"
                    : exclusion.reason.replaceAll("_", " ")}
                  {exclusion.confirmedAt ? ` · ${exclusion.confirmedAt}` : ""}
                </span>
              </li>
            ))}
          </ul>
          {!hasDryRunCandidate ? (
            <p role="status">All selected trackers were already uploaded. No upload is needed.</p>
          ) : null}
        </section>
      ) : null}

      <section className="panel grid gap-3">
        <h2>Run options</h2>
        <div className="flex flex-wrap gap-4">
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={view.options.noSeed}
              disabled={view.liveTest}
              onChange={(event) => facet.changeOptions({ noSeed: event.target.checked })}
            />
            Skip client injection
          </label>
          <label className="grid gap-1">
            <span className="label">Log level</span>
            <select
              value={view.options.runLogLevel}
              onChange={(event) => facet.changeOptions({ runLogLevel: event.target.value })}
            >
              {["trace", "debug", "info", "warn", "error"].map((level) => (
                <option key={level} value={level}>
                  {level}
                </option>
              ))}
            </select>
          </label>
        </div>
      </section>

      <section className="panel grid gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="primary"
            type="button"
            disabled={view.dryRunStatus === "running" || !hasDryRunCandidate}
            onClick={() => void facet.runDryRun()}
          >
            {view.dryRunStatus === "running" ? "Running dry run..." : "Run dry run"}
          </Button>
          <Button
            variant="primary"
            type="button"
            disabled={!view.mutationsAllowed || uploadRunning || !hasExecutableUpload}
            onClick={() => void facet.start()}
          >
            {uploadRunning ? "Uploading..." : "Start upload"}
          </Button>
          {uploadRunning ? (
            <button className="danger" type="button" onClick={() => void facet.cancel()}>
              Cancel upload
            </button>
          ) : null}
          {failedTrackers.length ? (
            <button
              className="ghost"
              type="button"
              disabled={!view.mutationsAllowed}
              onClick={() => void facet.retry()}
            >
              Retry failed uploads
            </button>
          ) : null}
          {clientInjectionFailures.length ? (
            <button
              className="ghost"
              type="button"
              disabled={!view.mutationsAllowed}
              onClick={() => void facet.retryClientInjection()}
            >
              Retry client injection
            </button>
          ) : null}
        </div>
        {view.error ? (
          <p className="error" role="alert">
            {view.error}
          </p>
        ) : null}
      </section>

      {trackerCards.length || view.dryRunResult ? (
        <section className="panel grid gap-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2>Tracker uploads</h2>
            {view.dryRunResult ? <span className="muted">{view.dryRunResult.status}</span> : null}
          </div>
          {trackerCards.map(({ trackerId, projection, report, outcome }) => {
            const expansionKey = `dry-run-result:${trackerId}`;
            const expanded = expandedTrackers[expansionKey] ?? false;
            const trackerLabel = report?.displayName || projection?.displayName || trackerId;
            const eligibilityLabel = uploadEligibilityLabel(outcome);
            const skipped = outcome?.uploadEligibility === "skipped";
            // The projection carries the reviewed tracker name; a dry-run report
            // adds status and detail and must not replace it.
            const uploadName =
              projection?.uploadReleaseName || report?.uploadReleaseName || "Unavailable";
            const canonicalName = projection?.canonicalReleaseName || "";
            const releaseNameNotices = releaseNameOverrideNotices(projection);
            return (
              <div
                className="grid gap-2 rounded border border-white/10 bg-white/5 p-3"
                key={trackerId}
              >
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <strong>{trackerLabel}</strong>
                    {eligibilityLabel ? (
                      <span className={skipped ? "error" : "muted"}>{eligibilityLabel}</span>
                    ) : null}
                  </div>
                  {report ? (
                    <div className="flex items-center gap-2">
                      <span className={report.status === "blocked" ? "error" : "muted"}>
                        {report.status}
                      </span>
                      <button
                        aria-expanded={expanded}
                        aria-label={`${expanded ? "Collapse" : "Expand"} ${trackerLabel}`}
                        className="ghost"
                        type="button"
                        onClick={() => toggleTrackerDetails(expansionKey)}
                      >
                        {expanded ? "Collapse" : "Expand"}
                      </button>
                    </div>
                  ) : null}
                </div>
                <p className="value break-all">
                  <span className="font-semibold">Tracker upload:</span> {uploadName}
                </p>
                {releaseNameNotices.length ? (
                  <div
                    aria-label={`Tracker naming notices for ${trackerId}`}
                    className="grid gap-1 rounded border border-amber-300/25 bg-amber-300/5 p-2 text-sm"
                  >
                    {releaseNameNotices.map((notice) => (
                      <p key={releaseNameOverrideKey(notice)}>{notice.message}</p>
                    ))}
                  </div>
                ) : null}
                {canonicalName && canonicalName !== uploadName ? (
                  <p className="muted break-all">
                    <span className="font-semibold">Canonical:</span> {canonicalName}
                  </p>
                ) : null}
                {report && expanded ? (
                  <div className="grid gap-2">
                    {report.endpoint ? <p className="value break-all">{report.endpoint}</p> : null}
                    <p className="muted">
                      Files ready: {(report.files || []).filter((file) => file.present).length}/
                      {(report.files || []).length}
                    </p>
                    {report.fields?.map((field) => (
                      <p className="value break-all" key={field.key}>
                        {field.key}: {field.value}
                      </p>
                    ))}
                    {report.warnings?.map((warning) => (
                      <p className="muted" key={warning}>
                        {warning}
                      </p>
                    ))}
                    {report.failures?.map((failure, index) => (
                      <p className="error" key={`${failure.failure.Code}-${index}`}>
                        {failure.failure.Message}
                      </p>
                    ))}
                    {report.clientInjection.status ? (
                      <p className={report.clientInjection.status === "failed" ? "error" : "muted"}>
                        Client injection: {report.clientInjection.status}
                        {report.clientInjection.message
                          ? ` · ${report.clientInjection.message}`
                          : ""}
                      </p>
                    ) : null}
                  </div>
                ) : null}
              </div>
            );
          })}
        </section>
      ) : null}

      {view.result ? (
        <section className="panel grid gap-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2>Workflow upload result</h2>
            <span className="muted">{view.result.status}</span>
          </div>
          {view.result.results.map((result) => (
            <div
              className="flex flex-wrap items-center justify-between gap-2 rounded border border-white/10 bg-white/5 p-2"
              key={result.trackerId}
            >
              <span>{result.trackerId}</span>
              <span
                className={
                  result.submissionStatus === "failed" || result.clientInjectionStatus === "failed"
                    ? "error"
                    : "muted"
                }
              >
                Submission: {result.submissionStatus || result.status}
                {" · "}
                Client injection: {result.clientInjectionStatus || "unavailable"}
                {result.clientInjectionMessage ? ` · ${result.clientInjectionMessage}` : ""}
              </span>
              {result.failures
                ?.filter((failure) => failure.failure.Message !== result.clientInjectionMessage)
                .map((failure, index) => (
                  <p className="error basis-full" key={`${failure.failure.Code}-${index}`}>
                    {failure.failure.Message}
                  </p>
                ))}
            </div>
          ))}
        </section>
      ) : null}
    </section>
  );
}
