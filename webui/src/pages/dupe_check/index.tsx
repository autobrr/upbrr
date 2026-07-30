// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { Badge } from "../../components/ui/badge";
import { Button } from "../../components/ui/button";
import { PillCheckbox } from "../../components/ui/checkbox";
import { Switch } from "../../components/ui/switch";
import { TrackerIconImage } from "../../components/ui/tracker-icon";
import type { TrackerIconCache } from "../../hooks/useTrackerIcons";
import { trackerIconFor } from "../../hooks/useTrackerIcons";
import type { DuplicatesFacet } from "../../releaseSession/types";
import type { TrackerUploadItem } from "../../types";
import { handleExternalLinkClick } from "../../utils/externalLinks";
import type {
  DupeAssessment,
  TrackerPreflightAssessment,
  TrackerReleaseProjectionSet,
} from "../../api/generated/release-workflow";

type Props = {
  facet: DuplicatesFacet;
  sourcePath: string;
  trackerUploadItems: readonly TrackerUploadItem[];
  useFavicons?: boolean;
  faviconOnly?: boolean;
  trackerIconSrcByName: TrackerIconCache;
};

const uniqueFailureMessages = (failures: readonly { failure: { Message: string } }[]): string[] => {
  const seen = new Set<string>();
  return failures.flatMap((failure) => {
    const message = failure.failure.Message.trim();
    if (!message || seen.has(message)) return [];
    seen.add(message);
    return [message];
  });
};

const hasInClientMatch = (result: DupeAssessment["results"][number] | undefined) =>
  Boolean(result?.matches?.some((match) => match.reason?.trim().toLowerCase() === "in_client"));

const workflowDupeSummary = (
  result: DupeAssessment["results"][number] | undefined,
  searchBlocked: boolean,
  inClient: boolean,
) => {
  if (searchBlocked && !result) {
    return "Duplicate search not run because this tracker is blocked.";
  }
  if (result?.status === "skipped") return "Duplicate search skipped.";
  if (!result) return "Duplicate search has not run.";
  if (result.status === "failed") return "Duplicate search failed.";
  if (inClient) return "In client · upload blocked";
  const count = result.matches?.length || 0;
  switch (result.decision) {
    case "accepted":
      return `${count} match(es) · upload blocked`;
    case "ignored":
      return `${count} match(es) · duplicate override enabled`;
    case "pending":
      return `${count} match(es) · review optional`;
    case "no_match":
      return "0 match(es) · no duplicate found";
    case "skipped":
      return "Duplicate search skipped.";
  }
};

const workflowTrackerIDs = (
  assessment: DupeAssessment | null,
  preflight: TrackerPreflightAssessment | null,
  projections: TrackerReleaseProjectionSet | null,
) =>
  Array.from(
    new Set([
      ...(projections?.projections || []).map((projection) => projection.trackerId),
      ...(preflight?.results || []).map((result) => result.trackerId),
      ...(assessment?.results || []).map((result) => result.trackerId),
    ]),
  );

const uploadEligibleTrackerIDs = (
  assessment: DupeAssessment | null,
  preflight: TrackerPreflightAssessment | null,
  projections: TrackerReleaseProjectionSet | null,
) => {
  const projectionByTracker = new Map(
    (projections?.projections || []).map((projection) => [projection.trackerId, projection]),
  );
  const preflightByTracker = new Map(
    (preflight?.results || []).map((result) => [result.trackerId, result]),
  );
  const dupeByTracker = new Map(
    (assessment?.results || []).map((result) => [result.trackerId, result]),
  );
  return workflowTrackerIDs(assessment, preflight, projections).filter((trackerID) => {
    const projection = projectionByTracker.get(trackerID);
    const readiness = preflightByTracker.get(trackerID);
    const result = dupeByTracker.get(trackerID);
    return (
      projection?.readiness === "ready" &&
      readiness?.state === "ready" &&
      result?.status === "completed" &&
      !hasInClientMatch(result) &&
      ["no_match", "ignored"].includes(result.decision)
    );
  });
};

function WorkflowDupeAssessmentView({
  assessment,
  preflight,
  projections,
  ignoredTrackers,
  setIgnored,
}: Readonly<{
  assessment: DupeAssessment | null;
  preflight: TrackerPreflightAssessment | null;
  projections: TrackerReleaseProjectionSet | null;
  ignoredTrackers: ReadonlySet<string>;
  setIgnored(tracker: string, ignored: boolean): void;
}>) {
  const projectionsByTracker = new Map(
    (projections?.projections || []).map((projection) => [projection.trackerId, projection]),
  );
  const preflightByTracker = new Map(
    (preflight?.results || []).map((result) => [result.trackerId, result]),
  );
  const dupesByTracker = new Map(
    (assessment?.results || []).map((result) => [result.trackerId, result]),
  );
  const trackerIDs = workflowTrackerIDs(assessment, preflight, projections);

  return (
    <div className="grid gap-2">
      {trackerIDs.map((trackerID) => {
        const result = dupesByTracker.get(trackerID);
        const projection = projectionsByTracker.get(trackerID);
        const readiness = preflightByTracker.get(trackerID);
        const canonicalName = projection?.canonicalReleaseName || "";
        const ruleStatus = (projection?.policyDecisions || []).filter(
          (decision) => decision.blocking,
        );
        const failureMessages = uniqueFailureMessages([
          ...(projection?.failures || []),
          ...(readiness?.failures || []),
          ...(result?.failures || []),
        ]);
        const inClient = hasInClientMatch(result);
        const searchBlocked = Boolean(
          (projection && projection.readiness !== "ready") ||
          (readiness && readiness.state !== "ready"),
        );
        const canOverride = Boolean(
          result &&
          !inClient &&
          result.matches?.length &&
          ["pending", "accepted", "ignored"].includes(result.decision),
        );
        const matches = Array.from(
          new Map(
            (result?.matches || []).map((match) => [
              `${match.id || ""}\u0000${match.name}\u0000${match.reason || ""}`,
              match,
            ]),
          ).values(),
        );
        const uploadName =
          result?.uploadReleaseName || projection?.uploadReleaseName || "Unavailable";
        const searchName = result?.criteria?.name || projection?.duplicateCriteria?.name;
        return (
          <article className="panel grid gap-2 py-3" key={trackerID}>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <p className="font-bold">{projection?.displayName || trackerID}</p>
              <div className="flex flex-wrap gap-1">
                <Badge tone={projection?.readiness === "ready" ? "info" : "danger"}>
                  Projection {projection?.readiness || "unknown"}
                </Badge>
                <Badge tone={readiness?.state === "ready" ? "info" : "danger"}>
                  Preflight {readiness?.state || "not run"}
                </Badge>
                <Badge tone={ruleStatus.length ? "danger" : "info"}>
                  {ruleStatus.length ? `${ruleStatus.length} blocking rule(s)` : "Rules ready"}
                </Badge>
                {inClient ? <Badge tone="danger">In client</Badge> : null}
              </div>
            </div>
            <div className="grid gap-1 text-sm">
              {canonicalName && canonicalName !== result?.uploadReleaseName ? (
                <p className="muted">
                  <span className="font-semibold text-[var(--text)]">Canonical:</span>{" "}
                  {canonicalName}
                </p>
              ) : null}
              <p>
                <span className="font-semibold">Tracker upload:</span> {uploadName}
              </p>
              {searchName && searchName !== uploadName ? (
                <p>
                  <span className="font-semibold">Duplicate search:</span> {searchName}
                </p>
              ) : null}
              <p className="muted">{workflowDupeSummary(result, searchBlocked, inClient)}</p>
            </div>
            {failureMessages.length ? (
              <div className="grid gap-1 text-sm">
                {failureMessages.map((message) => (
                  <p className="error" key={message}>
                    {message}
                  </p>
                ))}
              </div>
            ) : null}
            {matches.length ? (
              <div className="flex flex-wrap gap-2 text-sm">
                {matches.map((match) =>
                  match.link ? (
                    <a
                      className="tracker-link"
                      href={match.link}
                      key={match.id || match.name}
                      onAuxClick={handleExternalLinkClick}
                      onClick={handleExternalLinkClick}
                      rel="noreferrer"
                      target="_blank"
                    >
                      {match.name}
                    </a>
                  ) : (
                    <span key={match.id || match.name}>{match.name}</span>
                  ),
                )}
              </div>
            ) : null}
            {canOverride ? (
              <label className="inline-flex items-center gap-2 text-xs font-semibold">
                <span>Ignore duplicate match</span>
                <Switch
                  aria-label={`Ignore dupes for ${trackerID}`}
                  checked={result?.decision === "ignored" || ignoredTrackers.has(trackerID)}
                  onChange={(event) => setIgnored(trackerID, event.target.checked)}
                />
              </label>
            ) : null}
          </article>
        );
      })}
    </div>
  );
}

/** Presents exact workflow-owned per-tracker duplicate outcomes and selection controls. */
export default function DupeCheckPage({
  facet,
  sourcePath,
  trackerUploadItems,
  useFavicons = true,
  faviconOnly = false,
  trackerIconSrcByName,
}: Readonly<Props>) {
  const { view } = facet;
  const assessment = view.assessment || null;
  const preflight = view.preflight || null;
  const projections = view.projections || null;
  const trackerIDs = workflowTrackerIDs(assessment, preflight, projections);
  const availableTrackers = uploadEligibleTrackerIDs(assessment, preflight, projections);
  const ignoredTrackers = new Set(view.ignoredTrackers);
  const selectedTrackers = new Set(view.selectedTrackers);
  const trackerSelectionRequired = selectedTrackers.size === 0;
  const dupeLoading = view.status === "running";
  const hideTrackerNames = faviconOnly && useFavicons;

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
          {trackerIDs.length ? (
            <div className="mt-2 flex flex-wrap items-center gap-1.5 text-xs text-[var(--muted)]">
              <span className="font-semibold text-[var(--text)]">
                Available for upload: {availableTrackers.length}
              </span>
              {availableTrackers.map((tracker) => (
                <Badge
                  aria-label={hideTrackerNames ? tracker : undefined}
                  className="text-[var(--text)] flex items-center gap-1"
                  key={`available-${tracker}`}
                  tone="info"
                >
                  <TrackerIconImage
                    tracker={tracker}
                    iconSrc={trackerIconFor(trackerIconSrcByName, tracker)}
                    enabled={useFavicons}
                  />
                  {hideTrackerNames ? null : tracker}
                </Badge>
              ))}
              {trackerIDs.length > availableTrackers.length ? (
                <span>{trackerIDs.length - availableTrackers.length} blocked.</span>
              ) : null}
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
          {dupeLoading ? `Checking ${view.completed}/${view.total || "?"}...` : "Run dupe check"}
        </Button>
      </section>

      {view.error ? <p className="error">{view.error}</p> : null}

      {trackerIDs.length ? (
        <WorkflowDupeAssessmentView
          assessment={assessment}
          ignoredTrackers={ignoredTrackers}
          preflight={preflight}
          projections={projections}
          setIgnored={facet.setIgnored}
        />
      ) : (
        <p className="muted">No dupe results yet.</p>
      )}
    </section>
  );
}
