// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useRef } from "react";
import { pageStyle } from "../../components/ui/pageStyle";
import { Badge } from "../../components/ui/badge";
import { Button } from "../../components/ui/button";
import { PillCheckbox } from "../../components/ui/checkbox";
import { Select } from "../../components/ui/select";
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
  SubmissionExclusion,
  TrackerPreflightAssessment,
  TrackerPolicyDecision,
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
  submissionExclusions: readonly SubmissionExclusion[];
  workflowComplete: boolean;
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

const releaseNameOverrideNotices = (projection: TrackerReleaseProjection | undefined) =>
  (projection?.policyDecisions || []).filter(
    (decision) =>
      decision.code.startsWith("release_name_override") &&
      (decision.decision === "enforced" || decision.decision === "rebuilt"),
  );

const releaseNameOverrideKey = (decision: TrackerPolicyDecision) =>
  [decision.code, decision.namingRole || "", decision.namingRuleId || ""].join("\u0000");

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
    result.decision === "pending" &&
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

const ruleAcknowledgementAction = (projection: TrackerReleaseProjection | undefined) =>
  projection?.requiredActions?.find(
    (action) =>
      action.kind === "authorize_rules" &&
      (action.status === "pending" ||
        (action.status === "resolved" &&
          Boolean(projection.waivableRuleFingerprint) &&
          projection.ruleAuthorizationFingerprint === projection.waivableRuleFingerprint)),
  );

const trackerBlockReasons = (
  projection: TrackerReleaseProjection | undefined,
  readiness: TrackerPreflightAssessment["results"][number] | undefined,
  result: DupeAssessment["results"][number] | undefined,
) => {
  const hasRuleOverride =
    !hasInClientMatch(result) && Boolean(ruleAcknowledgementAction(projection));
  const inClientMatches = (result?.matches || []).filter(
    (match) => match.reason?.trim().toLowerCase() === "in_client",
  );
  return uniqueMessages([
    ...inClientMatches.map((match) => `Already in client: ${match.name}`),
    ...(projection?.policyDecisions || [])
      .filter(
        (decision) =>
          decision.blocking && (!hasRuleOverride || decision.disposition !== "waivable"),
      )
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
  if (ruleAcknowledgementAction(projection)?.status === "pending") {
    return "Tracker acknowledgement needed";
  }
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
            className="flex flex-wrap items-center gap-x-2 gap-y-1 rounded border border-border bg-muted px-2 py-1.5 text-sm text-foreground"
            key={`${match.id || ""}-${match.name}-${match.relation || ""}`}
          >
            <Badge tone={relationTone(match.relation)}>{relationLabel(match.relation)}</Badge>
            {match.link ? (
              <a
                className="inline-flex items-center gap-[5px] font-semibold text-foreground underline min-w-0 break-all"
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
            {facts.length ? (
              <span className="text-muted-foreground text-xs">{facts.join(" · ")}</span>
            ) : null}
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
  acknowledgeRules,
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
  acknowledgeRules(tracker: string, acknowledged: boolean): Promise<boolean>;
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
        const releaseNameNotices = releaseNameOverrideNotices(projection);
        const releaseName = releaseNameOverrides[trackerID] ?? projection?.uploadReleaseName ?? "";
        const inClient = hasInClientMatch(result);
        const strictBlocked =
          inClient ||
          Boolean(
            projection?.policyDecisions?.some(
              (decision) => decision.blocking && decision.disposition === "strict",
            ),
          );
        const ruleAcknowledgement = !strictBlocked
          ? ruleAcknowledgementAction(projection)
          : undefined;
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
        const showEffectiveNames = Boolean(
          namesModified || (uploadName && releaseNameNotices.length),
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
          <article className={`${pageStyle.panel} grid gap-1 px-3 py-2`} key={trackerID}>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h2 className="text-base">{projection?.displayName || trackerID}</h2>
              <Badge
                tone={
                  ruleAcknowledgement?.status === "pending"
                    ? "info"
                    : inClient ||
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
                  <p className={pageStyle.error} key={message}>
                    {message}
                  </p>
                ))}
              </div>
            ) : null}

            {ruleAcknowledgement ? (
              <div className="grid gap-2 rounded border border-[var(--status-warning)] bg-card p-2 text-sm">
                <p>{ruleAcknowledgement.prompt}</p>
                <label className="inline-flex items-center gap-2 text-xs font-semibold">
                  <span>Acknowledge tracker warnings</span>
                  <Switch
                    aria-label={`Acknowledge warnings for ${trackerID}`}
                    checked={ruleAcknowledgement.status === "resolved"}
                    disabled={busy}
                    onChange={(event) => {
                      void acknowledgeRules(trackerID, event.target.checked);
                    }}
                  />
                </label>
              </div>
            ) : null}

            {showEffectiveNames ? (
              <div
                aria-label={`${namesModified ? "Modified" : "Effective"} tracker names for ${trackerID}`}
                className="grid gap-1 text-sm"
              >
                {namesModified ? (
                  <p className="text-muted-foreground">
                    <span className="font-semibold text-foreground">Canonical:</span>{" "}
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
                {releaseNameNotices.length ? (
                  <div
                    aria-label={`Tracker naming notices for ${trackerID}`}
                    className="grid gap-1 rounded border border-[var(--status-warning)] bg-card p-2"
                  >
                    {releaseNameNotices.map((notice) => (
                      <p key={releaseNameOverrideKey(notice)}>{notice.message}</p>
                    ))}
                  </div>
                ) : null}
              </div>
            ) : null}

            <CandidateList matches={matches} />

            {!strictBlocked && (nameConfirmation.pending || nameConfirmation.confirmed) ? (
              <div
                aria-label={`Tracker naming for ${trackerID}`}
                className="grid gap-2 rounded border border-border bg-muted p-2 text-foreground"
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

/** Presents per-tracker duplicate evidence, policy acknowledgements, tracker questions, and release-name review. */
export default function DupeCheckPage({
  facet,
  sourcePath,
  trackerUploadItems,
  useFavicons = true,
  faviconOnly = false,
  trackerIconSrcByName,
  submissionExclusions,
  workflowComplete,
}: Readonly<Props>) {
  const { view } = facet;
  const refreshRef = useRef(facet.refreshQuestionnaires);
  refreshRef.current = facet.refreshQuestionnaires;
  useEffect(() => {
    if (
      view.questionnaireStatus === "idle" &&
      view.status !== "running" &&
      view.selectedTrackers.length
    ) {
      void refreshRef.current();
    }
  }, [view.questionnaireStatus, view.status, view.selectedTrackers]);
  const assessment = view.assessment || null;
  const preflight = view.preflight || null;
  const projections = view.projections || null;
  const trackerIDs = workflowTrackerIDs(assessment, preflight, projections);
  const ignoredTrackers = new Set(view.ignoredTrackers);
  const selectedTrackers = new Set(view.selectedTrackers);
  const questionnaireProjections = view.questionnaires.filter(
    (projection) => selectedTrackers.has(projection.trackerId) && projection.questionnaire?.length,
  );
  const preparationProjections = view.preparationQuestionnaires.filter(
    (projection) => selectedTrackers.has(projection.trackerId) && projection.questionnaire?.length,
  );
  const questionnaireSections = [
    { title: "Tracker questions", projections: questionnaireProjections },
    { title: "Tracker preparation details", projections: preparationProjections },
  ];
  const trackerSelectionRequired = selectedTrackers.size === 0;
  const questionsLoading = view.questionnaireStatus === "running";
  const dupeLoading = view.status === "running";
  const questionsBusy = dupeLoading || questionsLoading;
  const excludedTrackerIDs = new Set(submissionExclusions.map((exclusion) => exclusion.trackerId));
  const allSelectedTrackersAlreadyUploaded =
    workflowComplete &&
    trackerIDs.length === 0 &&
    submissionExclusions.length > 0 &&
    submissionExclusions.every((exclusion) => exclusion.reason === "already_uploaded") &&
    [...selectedTrackers].every((tracker) => excludedTrackerIDs.has(tracker));

  return (
    <section className="flex flex-col gap-3">
      <header className="max-w-3xl">
        <p className={pageStyle.eyebrow}>Dupe Checking</p>
        <h1>Check Trackers</h1>
        <p className={pageStyle.subtitle}>
          Scan selected trackers for potential dupes before upload.
        </p>
      </header>

      <section className={`${pageStyle.panel} flex flex-col gap-2 py-3`}>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <p className={pageStyle.label}>Trackers</p>
            <p className="text-muted-foreground text-sm">
              Select trackers for this duplicate check.
            </p>
          </div>
          <span className="text-muted-foreground text-xs">
            {selectedTrackers.size}/{trackerUploadItems.length} selected
          </span>
        </div>
        {trackerUploadItems.length ? (
          <fieldset className="flex flex-wrap gap-2 m-0 min-w-0 border-0 p-0">
            <legend className="sr-only">Trackers for duplicate check</legend>
            {trackerUploadItems.map((tracker) => {
              const normalized = tracker.name.trim().toUpperCase();
              return (
                <PillCheckbox
                  aria-label={tracker.name}
                  checked={selectedTrackers.has(normalized)}
                  disabled={dupeLoading}
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
                    {faviconOnly && useFavicons ? null : tracker.name}
                  </span>
                </PillCheckbox>
              );
            })}
          </fieldset>
        ) : (
          <p className="text-muted-foreground">No configured tracker entries found.</p>
        )}
        {trackerSelectionRequired ? (
          <p className="text-muted-foreground text-sm">
            Select at least one tracker to run duplicate checking.
          </p>
        ) : null}
        <Button
          className="ml-auto"
          variant="primary"
          type="button"
          onClick={() => void facet.run()}
          disabled={questionsBusy || !sourcePath.trim() || trackerSelectionRequired}
        >
          {dupeLoading ? `Checking ${view.completed}/${view.total || "?"}...` : "Run dupe check"}
        </Button>
      </section>

      {questionsLoading ? <p role="status">Loading tracker questions…</p> : null}
      {view.questionnaireError ? (
        <div className="flex flex-wrap items-center gap-2">
          <p role="status">{view.questionnaireError}</p>
          <Button onClick={() => void facet.refreshQuestionnaires()} disabled={questionsBusy}>
            Retry tracker questions
          </Button>
        </div>
      ) : null}
      {questionnaireProjections.length ||
      preparationProjections.length ||
      view.questionnaireDirty ? (
        <section className={`${pageStyle.panel} grid gap-3`}>
          {view.questionnaireDirty ? (
            <p role="status">
              Apply tracker answers to refresh the review. Compatible duplicate results are
              retained.
            </p>
          ) : null}
          <Button onClick={() => void facet.applyQuestionnaireAnswers()} disabled={questionsBusy}>
            Apply tracker answers
          </Button>
          {questionnaireSections
            .filter((section) => section.projections.length)
            .map((section) => (
              <section className="grid gap-3" key={section.title} aria-label={section.title}>
                <h2>{section.title}</h2>
                {section.projections.map((projection) => (
                  <details className="rounded border border-border p-3" key={projection.trackerId}>
                    <summary className="cursor-pointer font-semibold focus-visible:outline focus-visible:outline-ring">
                      {projection.displayName}
                      {projection.questionnaire?.some((field) => field.required)
                        ? " · Required"
                        : ""}
                      {projection.questionnaire?.some((field) => {
                        const draft = view.questionnaireAnswers[projection.trackerId]?.[field.key];
                        return (
                          draft !== undefined &&
                          draft.trim() !==
                            (projection.questionnaireAnswers?.[field.key] ?? "").trim()
                        );
                      })
                        ? " · Unapplied changes"
                        : ""}
                    </summary>
                    <fieldset className="mt-3 grid gap-3" disabled={questionsBusy}>
                      <legend className="sr-only">{projection.displayName}</legend>
                      {projection.questionnaire?.map((field) => {
                        const draft = view.questionnaireAnswers[projection.trackerId]?.[field.key];
                        const answer = draft ?? field.value ?? "";
                        const fieldId = `questionnaire-${projection.trackerId}-${field.key}`;
                        const labelId = `${fieldId}-label`;
                        const helpId = field.help ? `${fieldId}-help` : undefined;
                        if (field.kind === "multiselect") {
                          const selected = answer.split(",").filter(Boolean);
                          return (
                            <fieldset
                              className="grid gap-2"
                              key={field.key}
                              aria-describedby={helpId}
                            >
                              <legend>
                                {field.label || field.key}
                                {field.required ? " *" : ""}
                              </legend>
                              {field.help ? (
                                <p id={helpId} className="text-muted-foreground">
                                  {field.help}
                                </p>
                              ) : null}
                              {(field.options || []).map((option) => (
                                <label className="flex items-center gap-2" key={option}>
                                  <input
                                    type="checkbox"
                                    checked={selected.includes(option)}
                                    onChange={(event) =>
                                      facet.answerQuestionnaire(
                                        projection.trackerId,
                                        field.key,
                                        (event.target.checked
                                          ? [...selected, option]
                                          : selected.filter((value) => value !== option)
                                        ).join(","),
                                      )
                                    }
                                  />
                                  {option}
                                </label>
                              ))}
                            </fieldset>
                          );
                        }
                        return (
                          <label className="grid gap-1" key={field.key}>
                            <span id={labelId} className={pageStyle.label}>
                              {field.label || field.key}
                              {field.required ? " *" : ""}
                            </span>
                            {field.help ? (
                              <span id={helpId} className="text-muted-foreground text-sm">
                                {field.help}
                              </span>
                            ) : null}
                            {field.options?.length ? (
                              <Select
                                aria-labelledby={labelId}
                                aria-describedby={helpId}
                                value={answer}
                                onChange={(event) =>
                                  facet.answerQuestionnaire(
                                    projection.trackerId,
                                    field.key,
                                    event.target.value,
                                  )
                                }
                              >
                                <option value="">Select</option>
                                {answer && !field.options.includes(answer) ? (
                                  <option value={answer}>{answer} (saved)</option>
                                ) : null}
                                {field.options.map((option) => (
                                  <option key={option} value={option}>
                                    {option}
                                  </option>
                                ))}
                              </Select>
                            ) : field.kind === "textarea" ? (
                              <textarea
                                aria-labelledby={labelId}
                                aria-describedby={helpId}
                                value={answer}
                                onChange={(event) =>
                                  facet.answerQuestionnaire(
                                    projection.trackerId,
                                    field.key,
                                    event.target.value,
                                  )
                                }
                              />
                            ) : (
                              <input
                                aria-labelledby={labelId}
                                aria-describedby={helpId}
                                value={answer}
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
                        );
                      })}
                    </fieldset>
                  </details>
                ))}
              </section>
            ))}
        </section>
      ) : null}

      {view.error ? <p className={pageStyle.error}>{view.error}</p> : null}

      {submissionExclusions.length ? (
        <section className={`${pageStyle.panel} grid gap-3`} aria-label="Submission exclusions">
          <h2>Already submitted</h2>
          <p className="text-muted-foreground">
            Confirmed tracker submissions are excluded from duplicate checks and upload actions.
          </p>
          <ul className="grid gap-2">
            {submissionExclusions.map((exclusion) => (
              <li
                className="rounded border border-border bg-muted p-3 text-foreground"
                key={exclusion.trackerId}
              >
                <strong>{exclusion.trackerId}</strong>
                <span className="text-muted-foreground">
                  {exclusion.reason === "already_uploaded"
                    ? "Already uploaded"
                    : exclusion.reason.replaceAll("_", " ")}
                  {exclusion.confirmedAt ? ` · ${exclusion.confirmedAt}` : ""}
                </span>
              </li>
            ))}
          </ul>
          {allSelectedTrackersAlreadyUploaded ? (
            <p role="status">All selected trackers were already uploaded. No upload is needed.</p>
          ) : null}
        </section>
      ) : null}

      {trackerIDs.length ? (
        <WorkflowDupeAssessmentView
          acknowledgeReleaseName={facet.acknowledgeReleaseName}
          assessment={assessment}
          busy={dupeLoading}
          confirmReleaseName={facet.confirmReleaseName}
          ignoredTrackers={ignoredTrackers}
          acknowledgeRules={facet.acknowledgeRules}
          preflight={preflight}
          projections={projections}
          releaseNameOverrides={view.releaseNameOverrides}
          setIgnored={facet.setIgnored}
        />
      ) : submissionExclusions.length === 0 ? (
        <p className="text-muted-foreground">No dupe results yet.</p>
      ) : null}
    </section>
  );
}
