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
  TrackerPolicyDecision,
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

const uniqueFailureMessages = (
  failures: readonly { failure: { Message: string } }[],
  excludedMessages: ReadonlySet<string> = new Set(),
): string[] => {
  const seen = new Set<string>();
  return failures.flatMap((failure) => {
    const message = failure.failure.Message.trim();
    if (!message || seen.has(message) || excludedMessages.has(message)) return [];
    seen.add(message);
    return [message];
  });
};

type PolicyDecisionGroup = "strict" | "waivable" | "advisory";

/** Keeps explicit rule outcomes and legacy blockers while hiding policy provenance entries. */
const auditablePolicyDecision = (decision: TrackerPolicyDecision) =>
  Boolean(decision.disposition) ||
  decision.blocking ||
  ["ineligible", "bypassed"].includes(decision.decision.trim().toLowerCase());

const policyDecisionGroup = (decision: TrackerPolicyDecision): PolicyDecisionGroup => {
  switch (decision.disposition) {
    case "strict":
    case "waivable":
    case "advisory":
      return decision.disposition;
    default:
      return decision.blocking ? "strict" : "advisory";
  }
};

const policyDecisionGroups = (decisions: readonly TrackerPolicyDecision[]) => ({
  strict: decisions.filter((decision) => policyDecisionGroup(decision) === "strict"),
  waivable: decisions.filter((decision) => policyDecisionGroup(decision) === "waivable"),
  advisory: decisions.filter((decision) => policyDecisionGroup(decision) === "advisory"),
});

const evidenceStatusText = (status: string | undefined) => {
  const normalized = status?.trim().toLowerCase();
  switch (normalized) {
    case "complete":
      return "Evidence complete";
    case "partial":
      return "Evidence partial · manual review needed";
    case "unavailable":
      return "Evidence unavailable · prerequisite/action needed";
    case "contradictory":
      return "Evidence contradictory · manual review needed";
    default:
      return normalized ? `Evidence ${normalized.replaceAll("_", " ")}` : "Evidence not reported";
  }
};

const policyReason = (decision: TrackerPolicyDecision) =>
  decision.message?.trim() || decision.code.replaceAll("_", " ");

function PolicyDecisionGroups({
  decisions,
}: Readonly<{ decisions: readonly TrackerPolicyDecision[] }>) {
  const groups = policyDecisionGroups(decisions);
  const presentation: readonly {
    id: PolicyDecisionGroup;
    label: string;
    tone: "neutral" | "info" | "danger";
  }[] = [
    { id: "strict", label: "Strict blockers", tone: "danger" },
    { id: "waivable", label: "Manual review / waivable", tone: "neutral" },
    { id: "advisory", label: "Advisories", tone: "info" },
  ];

  return (
    <div aria-label="Tracker policy decisions" className="grid gap-2">
      {presentation.map((group) => {
        const groupDecisions = groups[group.id];
        if (!groupDecisions.length) return null;
        return (
          <section
            aria-label={group.label}
            className="rounded border border-[var(--border)] bg-black/10 p-2"
            key={group.id}
          >
            <div className="mb-1 flex items-center justify-between gap-2">
              <p className="label">{group.label}</p>
              <Badge tone={group.tone}>{groupDecisions.length}</Badge>
            </div>
            <ul className="m-0 grid list-none gap-1 p-0">
              {groupDecisions.map((decision, index) => (
                <li
                  className="rounded border border-white/10 bg-white/5 px-2 py-1.5 text-sm"
                  key={`${decision.code}-${decision.decision}-${index}`}
                >
                  <p className="font-semibold">{policyReason(decision)}</p>
                  <p className="muted text-xs">
                    Rule {decision.code} · {evidenceStatusText(decision.evidenceStatus)}
                  </p>
                  {decision.evidenceStatus?.trim().toLowerCase() === "unavailable" ? (
                    <p className="muted text-xs">
                      Provide missing metadata or evidence before relying on this decision.
                    </p>
                  ) : null}
                </li>
              ))}
            </ul>
          </section>
        );
      })}
    </div>
  );
}

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

const hdrSummary = (match: DupeMatchProjection) => {
  const formats = match.hdr?.formats?.map((format) => format.replaceAll("_", " ")) || [];
  const profile = match.hdr?.dolbyVisionProfile ? `profile ${match.hdr.dolbyVisionProfile}` : "";
  return [formats.join(" + "), profile].filter(Boolean).join(" · ");
};

const candidateFacts = (match: DupeMatchProjection) =>
  [
    match.type,
    match.source,
    match.provider,
    match.resolution,
    match.codec,
    match.container,
    match.edition,
    match.region,
    match.threeD,
    match.repack,
    match.date,
    match.pack ? "season pack" : "",
  ].filter((value): value is string => Boolean(value));

const requiresRiskAcknowledgement = (result: DupeAssessment["results"][number] | undefined) =>
  Boolean(
    result &&
    ((Boolean(result.search?.pages) && result.search?.complete === false) ||
      result.matches?.some((match) => reviewRelations.has(match.relation || ""))),
  );

const actionMatches = (result: DupeAssessment["results"][number] | undefined) =>
  (result?.matches || []).filter((match) => match.relation !== "coexists");

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
  const count = actionMatches(result).length;
  switch (result.decision) {
    case "accepted":
      return `${count} candidate(s) · upload blocked`;
    case "ignored":
      return `${count} candidate(s) · policy risk acknowledged`;
    case "pending":
      return `${count} candidate(s) · review required`;
    case "no_match":
      return count
        ? `${count} candidate(s) · no blocking duplicate`
        : "0 candidates · no duplicate found";
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
        const policyDecisions = (projection?.policyDecisions || []).filter(auditablePolicyDecision);
        const ruleGroups = policyDecisionGroups(policyDecisions);
        const policyMessages = new Set(
          policyDecisions
            .map((decision) => decision.message?.trim())
            .filter((message): message is string => Boolean(message)),
        );
        const failureMessages = uniqueFailureMessages(
          [
            ...(projection?.failures || []),
            ...(readiness?.failures || []),
            ...(result?.failures || []),
          ],
          policyMessages,
        );
        const inClient = hasInClientMatch(result);
        const searchBlocked = Boolean(
          (projection && projection.readiness !== "ready") ||
          (readiness && readiness.state !== "ready"),
        );
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
                {ruleGroups.strict.length ? (
                  <Badge tone="danger">{ruleGroups.strict.length} strict</Badge>
                ) : null}
                {ruleGroups.waivable.length ? (
                  <Badge tone="neutral">{ruleGroups.waivable.length} manual review</Badge>
                ) : null}
                {ruleGroups.advisory.length ? (
                  <Badge tone="info">{ruleGroups.advisory.length} advisory</Badge>
                ) : null}
                {policyDecisions.length ? null : <Badge tone="info">Rules ready</Badge>}
                {inClient ? <Badge tone="danger">In client</Badge> : null}
                {result?.search?.pages ? (
                  <Badge tone={result.search.complete ? "info" : "danger"}>
                    Search {result.search.complete ? "complete" : "incomplete"} ·{" "}
                    {result.search.pages} page(s)
                  </Badge>
                ) : null}
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
              {result?.policyId ? <p className="muted text-xs">Policy: {result.policyId}</p> : null}
            </div>
            {result?.search?.warnings?.length ? (
              <div className="grid gap-1 text-sm">
                {result.search.warnings.map((warning) => (
                  <p className="error" key={warning}>
                    {warning}
                  </p>
                ))}
              </div>
            ) : null}
            {failureMessages.length ? (
              <div className="grid gap-1 text-sm">
                {failureMessages.map((message) => (
                  <p className="error" key={message}>
                    {message}
                  </p>
                ))}
              </div>
            ) : null}
            {policyDecisions.length ? <PolicyDecisionGroups decisions={policyDecisions} /> : null}
            {matches.length ? (
              <div className="grid gap-2 text-sm">
                {matches.map((match) => {
                  const facts = candidateFacts(match);
                  const hdr = hdrSummary(match);
                  return (
                    <div
                      className="rounded border border-[var(--border)] bg-black/10 p-2"
                      key={`${match.id || ""}-${match.name}-${match.relation || ""}`}
                    >
                      <div className="flex flex-wrap items-center gap-2">
                        <Badge tone={relationTone(match.relation)}>
                          {relationLabel(match.relation)}
                        </Badge>
                        {match.link ? (
                          <a
                            className="tracker-link break-all"
                            href={match.link}
                            onAuxClick={handleExternalLinkClick}
                            onClick={handleExternalLinkClick}
                            rel="noreferrer"
                            target="_blank"
                          >
                            {match.name}
                          </a>
                        ) : (
                          <span className="break-all">{match.name}</span>
                        )}
                      </div>
                      {facts.length ? <p className="muted mt-1">{facts.join(" · ")}</p> : null}
                      {hdr || match.evidenceStatus || match.hdr?.origin ? (
                        <p className="muted mt-1 text-xs">
                          HDR: {hdr || "unknown"} · evidence{" "}
                          {match.evidenceStatus || match.hdr?.status || "missing"}
                          {match.hdr?.origin ? ` (${match.hdr.origin})` : ""}
                        </p>
                      ) : null}
                      {(match.reasons || []).map((reason) => (
                        <p className="muted mt-1 text-xs" key={reason.code}>
                          {reason.code}
                          {reason.message ? ` — ${reason.message}` : ""}
                        </p>
                      ))}
                    </div>
                  );
                })}
              </div>
            ) : null}
            {riskAcknowledgement ? (
              <p className="error text-sm">
                Automatic clearance is unavailable. Review candidate relations and incomplete
                evidence before acknowledging tracker policy risk.
              </p>
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
  const releaseNameConfirmations = (projections?.projections || []).filter(
    (projection) =>
      selectedTrackers.has(projection.trackerId) &&
      projection.policyDecisions?.some(
        (decision) =>
          decision.code === "release_name_confirmation" &&
          decision.decision === "confirmation_required",
      ),
  );
  const releaseNameConfirmationInvalid = releaseNameConfirmations.some(
    (projection) =>
      !(view.releaseNameOverrides[projection.trackerId] ?? projection.uploadReleaseName).trim(),
  );

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

      {releaseNameConfirmations.length ? (
        <section className="panel grid gap-3 py-3" aria-label="Release name confirmation">
          <div>
            <p className="label">Release names</p>
            <h2>Confirm non-scene tracker names</h2>
            <p className="muted text-sm">
              Confirm the source folder or filename, or enter the exact tracker release name.
            </p>
          </div>
          {releaseNameConfirmations.map((projection) => (
            <label className="grid gap-1" key={`release-name-${projection.trackerId}`}>
              <span className="font-semibold">
                {projection.displayName || projection.trackerId}
              </span>
              <input
                aria-label={`Release name for ${projection.trackerId}`}
                value={
                  view.releaseNameOverrides[projection.trackerId] ?? projection.uploadReleaseName
                }
                onChange={(event) =>
                  facet.confirmReleaseName(projection.trackerId, event.target.value)
                }
              />
            </label>
          ))}
        </section>
      ) : null}

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
          disabled={
            dupeLoading ||
            !sourcePath.trim() ||
            trackerSelectionRequired ||
            releaseNameConfirmationInvalid
          }
        >
          {dupeLoading
            ? `Checking ${view.completed}/${view.total || "?"}...`
            : releaseNameConfirmations.length
              ? "Confirm names & run dupe check"
              : "Run dupe check"}
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
