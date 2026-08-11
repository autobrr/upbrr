// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { RequiredAction, WorkflowContinuation } from "../api/generated/release-workflow";
import type { ReleaseRoute } from "../releaseSession/types";

const actionRoutes: Readonly<Record<string, ReleaseRoute>> = {
  answer_questionnaire: "upload",
  confirm_rescan: "input",
  reprepare: "input",
  select_metadata: "input",
  select_playlist: "input",
};

const actionLabels: Readonly<Record<string, string>> = {
  answer_questionnaire: "Answer tracker questions",
  confirm_rescan: "Review source",
  reprepare: "Review source",
  select_metadata: "Review metadata",
  select_playlist: "Select playlist",
};

const actionOwnedByDupeCard = (action: RequiredAction) =>
  action.kind === "review_duplicates" ||
  (action.kind === "provide_tracker_input" && Boolean(action.trackerId));

function actionScope(action: RequiredAction): string {
  if (action.trackerId) return `Tracker: ${action.trackerId}`;
  if (action.effectKind || action.effectScopeId) {
    return [action.effectKind, action.effectScopeId].filter(Boolean).join(" · ");
  }
  return "Workflow action";
}

/** Shows pending non-dupe actions and lets users confirm tracker rules or open the owning workflow step. */
export function WorkflowRequiredActions({
  continuation,
  onConfirm,
  onNavigate,
}: Readonly<{
  continuation?: WorkflowContinuation | null;
  onConfirm: (action: RequiredAction) => void;
  onNavigate: (route: ReleaseRoute) => void;
}>) {
  const actions = (continuation?.requiredActions || []).filter(
    (action) => action.status === "pending" && !actionOwnedByDupeCard(action),
  );
  if (!actions.length) return null;

  return (
    <section className="panel mb-3 grid gap-3 py-3" aria-live="polite">
      <div>
        <p className="label">Action required</p>
        <h2>Release workflow needs input</h2>
      </div>
      <div className="grid gap-2">
        {actions.map((action) => {
          const route = actionRoutes[action.kind];
          return (
            <article
              className="grid gap-2 rounded border border-amber-300/25 bg-amber-300/5 p-3"
              key={action.id}
            >
              <div className="flex flex-wrap items-start justify-between gap-2">
                <p className="font-semibold text-[var(--text)]">{action.prompt}</p>
                <span className="muted text-sm">{actionScope(action)}</span>
              </div>
              <p className="muted text-xs">Action: {action.kind.replaceAll("_", " ")}</p>
              {action.kind === "authorize_rules" ? (
                <div>
                  <button className="ghost" type="button" onClick={() => onConfirm(action)}>
                    Continue upload
                  </button>
                </div>
              ) : route ? (
                <div>
                  <button className="ghost" type="button" onClick={() => onNavigate(route)}>
                    {actionLabels[action.kind] || "Open workflow step"}
                  </button>
                </div>
              ) : (
                <p className="muted text-sm">Continue from the relevant workflow page.</p>
              )}
            </article>
          );
        })}
      </div>
    </section>
  );
}
