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
  DupeMatchProjection,
  TrackerPreflightAssessment,
  TrackerReleaseProjection,
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

const releaseNameConfirmationState = (projection: TrackerReleaseProjection | undefined) => {
  const decision = projection?.policyDecisions?.find(
    (candidate) => candidate.code === "release_name_confirmation",
  )?.decision;
  return {
    confirmed: decision === "confirmed",
    pending: decision === "confirmation_required",
  };
};

const hasInClientMatch = (result: DupeAssessment["results"][number] | undefined) =>
  Boolean(result?.matches?.some((match) => match.reason?.trim().toLowerCase() === "in_client"));

const reviewRelations = new Set([
  "same_slot",
  "proposed_trumps",
  "manual_review",
  "insufficient_evidence",
]);

const relationLabel = (relation: string | undefined) =>
  relation ? relation.replaceAll("_", " ") : "candidate";

const relationTone = (relation: string | undefined): "neutral" | "info" | "danger" => {
  switch (relation) {
    case "coexists":
      return "info";
    case "proposed_trumps":
      return "neutral";
    default:
      return "danger";
  }
};

const requiresRiskAcknowledgement = (result: DupeAssessment["results"][number] | undefined) =>
  Boolean(
    result &&
    ((Boolean(result.search?.pages) && result.search?.complete === false) ||
      result.matches?.some((match) => reviewRelations.has(match.relation || ""))),
  );

const actionMatches = (result: DupeAssessment["results"][number] | undefined) =>
  (result?.matches || []).filter(
    (match) => match.relation !== "coexists" && match.reason?.trim().toLowerCase() !== "in_client",
  );

const candidateFacts = (match: DupeMatchProjection) =>
  [match.source, match.resolution, match.codec].filter((value): value is string => Boolean(value));

const uniqueMessages = (values: readonly (string | undefined)[]) => {
  const seen = new Set<string>();
  return values.flatMap((value) => {
    const message = value?.trim() || "";
    if (!message || seen.has(message)) return [];
    seen.add(message);
    return [message];
  });
};

const trackerBlockReasons = (
  projection: TrackerReleaseProjection | undefined,
  readiness: TrackerPreflightAssessment["results"][number] | undefined,
  result: DupeAssessment["results"][number] | undefined,
) => {
  const inClientMatches = (result?.matches || []).filter(
    (match) => match.reason?.trim().toLowerCase() === "in_client",
  );
  return uniqueMessages([
    ...inClientMatches.map((match) => `Already in client: ${match.name}`),
    ...(projection?.policyDecisions || [])
      .filter((decision) => decision.blocking)
      .map((decision) => decision.message || decision.code.replaceAll("_", " ")),
    ...(projection?.failures || []).map((failure) => failure.failure.Message),
    ...(readiness?.failures || []).map((failure) => failure.failure.Message),
    ...(result?.failures || []).map((failure) => failure.failure.Message),
    ...(result?.search?.warnings || []),
  ]);
};

const trackerSummary = (
  projection: TrackerReleaseProjection | undefined,
  readiness: TrackerPreflightAssessment["results"][number] | undefined,
  result: DupeAssessment["results"][number] | undefined,
) => {
  if (hasInClientMatch(result)) return "In client";
  if (projection?.readiness !== "ready" || (readiness && readiness.state !== "ready")) {
    return "Blocked";
  }
  if (!result) return "Not checked";
  if (result.status === "failed") return "Check failed";
  const count = actionMatches(result).length;
  switch (result.decision) {
    case "accepted":
      return `${count} potential dupe${count === 1 ? "" : "s"} · blocked`;
    case "ignored":
      return `${count} potential dupe${count === 1 ? "" : "s"} · acknowledged`;
    case "pending":
      return `${count} potential dupe${count === 1 ? "" : "s"} · review`;
    case "no_match":
      return count ? `${count} non-blocking candidate${count === 1 ? "" : "s"}` : "No dupes";
    case "bypassed":
      return "Dupe check bypassed";
    case "skipped":
      return "Dupe check skipped";
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

function CandidateList({ matches }: Readonly<{ matches: readonly DupeMatchProjection[] }>) {
  if (!matches.length) return null;
  return (
    <div aria-label="Potential duplicates" className="grid max-h-56 gap-1 overflow-y-auto">
      {matches.map((match) => {
        const facts = candidateFacts(match);
        return (
          <div
            className="flex flex-wrap items-center gap-x-2 gap-y-1 rounded border border-[var(--border)] bg-black/10 px-2 py-1.5 text-sm"
            key={`${match.id || ""}-${match.name}-${match.relation || ""}`}
          >
            <Badge tone={relationTone(match.relation)}>{relationLabel(match.relation)}</Badge>
            {match.link ? (
              <a
                className="tracker-link min-w-0 break-all"
                href={match.link}
                onAuxClick={handleExternalLinkClick}
                onClick={handleExternalLinkClick}
                rel="noreferrer"
                target="_blank"
              >
                {match.name}
              </a>
            ) : (
              <span className="min-w-0 break-all">{match.name}</span>
            )}
            {facts.length ? <span className="muted text-xs">{facts.join(" · ")}</span> : null}
          </div>
        );
      })}
    </div>
  );
}

function WorkflowDupeAssessmentView({
  assessment,
  preflight,
  projections,
  ignoredTrackers,
  releaseNameOverrides,
  busy,
  confirmReleaseName,
  acknowledgeReleaseName,
  setIgnored,
}: Readonly<{
  assessment: DupeAssessment | null;
  preflight: TrackerPreflightAssessment | null;
  projections: TrackerReleaseProjectionSet | null;
  ignoredTrackers: ReadonlySet<string>;
  releaseNameOverrides: Readonly<Record<string, string>>;
  busy: boolean;
  confirmReleaseName(tracker: string, value: string): void;
  acknowledgeReleaseName(tracker: string, acknowledged: boolean): Promise<boolean>;
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

  return (
    <div className="grid gap-2">
      {workflowTrackerIDs(assessment, preflight, projections).map((trackerID) => {
        const result = dupesByTracker.get(trackerID);
        const projection = projectionsByTracker.get(trackerID);
        const readiness = preflightByTracker.get(trackerID);
        const nameConfirmation = releaseNameConfirmationState(projection);
        const releaseName = releaseNameOverrides[trackerID] ?? projection?.uploadReleaseName ?? "";
        const inClient = hasInClientMatch(result);
        const strictBlocked =
          inClient ||
          Boolean(
            projection?.policyDecisions?.some(
              (decision) => decision.blocking && decision.disposition === "strict",
            ),
          );
        const canonicalName = projection?.canonicalReleaseName?.trim() || "";
        const uploadName =
          result?.uploadReleaseName?.trim() || projection?.uploadReleaseName?.trim() || "";
        const searchName =
          result?.criteria?.name?.trim() || projection?.duplicateCriteria?.name?.trim() || "";
        const namesModified = Boolean(
          !strictBlocked &&
          canonicalName &&
          ((uploadName && uploadName !== canonicalName) ||
            (searchName && searchName !== canonicalName)),
        );
        const blockReasons = trackerBlockReasons(projection, readiness, result);
        const riskAcknowledgement = requiresRiskAcknowledgement(result);
        const canOverride = Boolean(
          result &&
          !inClient &&
          (riskAcknowledgement || actionMatches(result).length) &&
          ["pending", "accepted", "ignored"].includes(result.decision),
        );
        const matches = Array.from(
          new Map(
            actionMatches(result).map((match) => [
              `${match.id || ""}\u0000${match.name}\u0000${match.reason || ""}`,
              match,
            ]),
          ).values(),
        );
        return (
          <article className="panel grid gap-1 px-3 py-2" key={trackerID}>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h2 className="text-base">{projection?.displayName || trackerID}</h2>
              <Badge
                tone={
                  inClient ||
                  result?.status === "failed" ||
                  projection?.readiness !== "ready" ||
                  (readiness && readiness.state !== "ready")
                    ? "danger"
                    : "info"
                }
              >
                {trackerSummary(projection, readiness, result)}
              </Badge>
            </div>

            {blockReasons.length ? (
              <div className="grid gap-1 text-sm">
                {blockReasons.map((message) => (
                  <p className="error" key={message}>
                    {message}
                  </p>
                ))}
              </div>
            ) : null}

            {namesModified ? (
              <div
                aria-label={`Modified tracker names for ${trackerID}`}
                className="grid gap-1 text-sm"
              >
                <p className="muted">
                  <span className="font-semibold text-[var(--text)]">Canonical:</span>{" "}
                  {canonicalName}
                </p>
                <p>
                  <span className="font-semibold">Tracker upload:</span> {uploadName}
                </p>
                {searchName && searchName !== uploadName ? (
                  <p>
                    <span className="font-semibold">Duplicate search:</span> {searchName}
                  </p>
                ) : null}
              </div>
            ) : null}

            <CandidateList matches={matches} />

            {!strictBlocked && (nameConfirmation.pending || nameConfirmation.confirmed) ? (
              <div
                aria-label={`Tracker naming for ${trackerID}`}
                className="grid gap-2 rounded border border-[var(--border)] bg-black/10 p-2"
              >
                <label className="grid gap-1">
                  <span className="text-xs font-semibold">Tracker release name</span>
                  <input
                    aria-label={`Release name for ${trackerID}`}
                    disabled={busy || nameConfirmation.confirmed}
                    value={releaseName}
                    onChange={(event) => confirmReleaseName(trackerID, event.target.value)}
                  />
                </label>
                <label className="inline-flex items-center gap-2 text-xs font-semibold">
                  <span>Confirm release name</span>
                  <Switch
                    aria-label={`Confirm release name for ${trackerID}`}
                    checked={nameConfirmation.confirmed}
                    disabled={
                      busy ||
                      !result ||
                      (!nameConfirmation.confirmed &&
                        (!nameConfirmation.pending || !releaseName.trim()))
                    }
                    onChange={(event) => {
                      void acknowledgeReleaseName(trackerID, event.target.checked);
                    }}
                  />
                </label>
              </div>
            ) : null}

            {canOverride ? (
              <label className="inline-flex items-center gap-2 text-xs font-semibold">
                <span>
                  {riskAcknowledgement
                    ? "Acknowledge tracker policy risk"
                    : "Ignore duplicate match"}
                </span>
                <Switch
                  aria-label={
                    riskAcknowledgement
                      ? `Acknowledge dupe risk for ${trackerID}`
                      : `Ignore dupes for ${trackerID}`
                  }
                  checked={result?.decision === "ignored" || ignoredTrackers.has(trackerID)}
                  disabled={busy}
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

/** Presents per-tracker duplicate evidence, policy acknowledgements, and release-name review. */
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
          acknowledgeReleaseName={facet.acknowledgeReleaseName}
          assessment={assessment}
          busy={dupeLoading}
          confirmReleaseName={facet.confirmReleaseName}
          ignoredTrackers={ignoredTrackers}
          preflight={preflight}
          projections={projections}
          releaseNameOverrides={view.releaseNameOverrides}
          setIgnored={facet.setIgnored}
        />
      ) : (
        <p className="muted">No dupe results yet.</p>
      )}
    </section>
  );
}
