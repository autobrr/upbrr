// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { pageStyle } from "./ui/pageStyle";
import type { RequiredAction, WorkflowContinuation } from "../api/generated/release-workflow";
import type { ReleaseRoute } from "../releaseSession/types";

const actionRoutes: Readonly<Record<string, ReleaseRoute>> = {
  answer_questionnaire: "duplicates",
  confirm_rescan: "input",
  confirm_corrections: "input",
  reprepare: "input",
  select_metadata: "input",
  select_playlist: "input",
  review_comparisons: "screenshots",
};

const actionLabels: Readonly<Record<string, string>> = {
  answer_questionnaire: "Answer tracker questions",
  confirm_rescan: "Review source",
  confirm_corrections: "Review saved values",
  reprepare: "Review source",
  select_metadata: "Review metadata",
  select_playlist: "Select playlist",
  review_comparisons: "Review comparison images",
};

const actionOwnedByDupeCard = (action: RequiredAction) =>
  action.kind === "review_duplicates" ||
  (action.kind === "authorize_rules" && Boolean(action.trackerId)) ||
  (action.kind === "provide_tracker_input" && Boolean(action.trackerId));

function actionScope(action: RequiredAction): string {
  if (action.trackerId) return `Tracker: ${action.trackerId}`;
  if (action.effectKind || action.effectScopeId) {
    return [action.effectKind, action.effectScopeId].filter(Boolean).join(" · ");
  }
  return "Workflow action";
}

/** Shows pending non-dupe actions and lets users resolve confirmations or open the owning workflow step. */
export function WorkflowRequiredActions({
  continuation,
  onScreenshotsPage = false,
  onConfirm,
  onNavigate,
}: Readonly<{
  continuation?: WorkflowContinuation | null;
  onScreenshotsPage?: boolean;
  onConfirm: (action: RequiredAction, confirmed?: boolean) => void;
  onNavigate: (route: ReleaseRoute) => void;
}>) {
  const actions = (continuation?.requiredActions || []).filter(
    (action) => action.status === "pending" && !actionOwnedByDupeCard(action),
  );
  if (!actions.length) return null;
  const onlyComparisonReview =
    onScreenshotsPage && actions.every((action) => action.kind === "review_comparisons");

  return (
    <section className={`${pageStyle.panel} mb-3 grid gap-3 py-3`} aria-live="polite">
      {!onlyComparisonReview && (
        <div>
          <p className={pageStyle.label}>Action required</p>
          <h2>Release workflow needs input</h2>
        </div>
      )}
      <div className="grid gap-2">
        {actions.map((action) => {
          if (onScreenshotsPage && action.kind === "review_comparisons") {
            return (
              <article
                className="rounded border border-[var(--status-warning)] bg-card p-3 text-sm"
                key={action.id}
              >
                <h2 className="font-semibold">Review required before upload</h2>
                <p>{action.prompt}</p>
              </article>
            );
          }
          const route = actionRoutes[action.kind];
          return (
            <article
              className="grid gap-2 rounded border border-[var(--status-warning)] bg-card p-3"
              key={action.id}
            >
              <div className="flex flex-wrap items-start justify-between gap-2">
                <p className="font-semibold text-foreground">{action.prompt}</p>
                <span className="text-muted-foreground text-sm">{actionScope(action)}</span>
              </div>
              <p className="text-muted-foreground text-xs">
                Action: {action.kind.replaceAll("_", " ")}
              </p>
              {action.kind === "authorize_rules" ? (
                <div>
                  <button className="ghost" type="button" onClick={() => onConfirm(action, true)}>
                    Continue upload
                  </button>
                  {action.releaseRule === "modified_release" && (
                    <button
                      className="ghost"
                      type="button"
                      onClick={() => onConfirm(action, false)}
                    >
                      Decline upload
                    </button>
                  )}
                </div>
              ) : action.kind === "reconcile_submission" ? (
                <div>
                  <button className="ghost" type="button" onClick={() => onConfirm(action, true)}>
                    {action.options?.find((option) => option.value === "not_completed")?.label ||
                      "Confirmed not completed"}
                  </button>
                </div>
              ) : action.kind === "resolve_tracker_preparation" ? (
                <div className="flex flex-wrap gap-2">
                  <button className="ghost" type="button" onClick={() => onConfirm(action, true)}>
                    {action.options?.find((option) => option.value === "confirm")?.label ||
                      "Continue upload"}
                  </button>
                  <button className="ghost" type="button" onClick={() => onConfirm(action, false)}>
                    {action.options?.find((option) => option.value === "resolve")?.label ||
                      "Try alternative"}
                  </button>
                </div>
              ) : route ? (
                <div>
                  <button className="ghost" type="button" onClick={() => onNavigate(route)}>
                    {actionLabels[action.kind] || "Open workflow step"}
                  </button>
                </div>
              ) : (
                <p className="text-muted-foreground text-sm">
                  Continue from the relevant workflow page.
                </p>
              )}
            </article>
          );
        })}
      </div>
    </section>
  );
}
