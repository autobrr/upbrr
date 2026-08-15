// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useMemo, useState } from "react";
import { Button } from "../../components/ui/button";
import type { UploadFacet } from "../../releaseSession/types";
import type {
  TrackerDryRunReport,
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
}>;

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
    const cards: TrackerUploadCard[] = (view.projections?.projections || [])
      .filter((projection) => selected.has(projection.trackerId))
      .map((projection) => ({
        trackerId: projection.trackerId,
        projection,
        report: reportsByTracker.get(projection.trackerId),
      }));
    const projectedIDs = new Set(cards.map((card) => card.trackerId));
    return [
      ...cards,
      ...reports
        .filter((report) => !projectedIDs.has(report.trackerId))
        .map((report): TrackerUploadCard => ({ trackerId: report.trackerId, report })),
    ];
  }, [selected, view.dryRunResult, view.projections]);
  const uploadRunning = view.uploadStatus === "running";
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
          Optionally run a dry run, or upload directly. A tracker failure does not stop unrelated
          uploads.
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

      <section className="panel grid gap-3">
        <h2>Run options</h2>
        <div className="flex flex-wrap gap-4">
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={view.options.noSeed}
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
            disabled={view.dryRunStatus === "running" || view.selectedTrackers.length === 0}
            onClick={() => void facet.runDryRun()}
          >
            {view.dryRunStatus === "running" ? "Running dry run..." : "Run dry run"}
          </Button>
          <Button
            variant="primary"
            type="button"
            disabled={uploadRunning || view.selectedTrackers.length === 0}
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
            <button className="ghost" type="button" onClick={() => void facet.retry()}>
              Retry failed uploads
            </button>
          ) : null}
          {clientInjectionFailures.length ? (
            <button
              className="ghost"
              type="button"
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
          {trackerCards.map(({ trackerId, projection, report }) => {
            const expansionKey = `dry-run-result:${trackerId}`;
            const expanded = expandedTrackers[expansionKey] ?? false;
            const trackerLabel = report?.displayName || projection?.displayName || trackerId;
            // The projection carries the reviewed tracker name; a dry-run report
            // adds status and detail and must not replace it.
            const uploadName =
              projection?.uploadReleaseName || report?.uploadReleaseName || "Unavailable";
            const canonicalName = projection?.canonicalReleaseName || "";
            return (
              <div
                className="grid gap-2 rounded border border-white/10 bg-white/5 p-3"
                key={trackerId}
              >
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <strong>{trackerLabel}</strong>
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
