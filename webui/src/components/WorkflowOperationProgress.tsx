// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { Operation as WorkflowOperationStatus } from "../api/generated/release-workflow";

const visibleStatuses = new Set(["queued", "running", "blocked", "failed", "interrupted"]);

/** Shows stable operation snapshots plus transient event detail; duplicate checks render on their owning route. */
export function WorkflowOperationProgress({
  operation,
}: Readonly<{ operation?: WorkflowOperationStatus | null }>) {
  if (!operation || operation.operation === "duplicate_check") return null;

  const events = operation.events || [];
  const scopedEvents = events.filter(
    (event) =>
      event.scopeId !== operation.workflowId &&
      (["queued", "running"].includes(event.lifecycle) ||
        event.severity === "warn" ||
        event.severity === "error"),
  );
  if (
    !visibleStatuses.has(operation.status) &&
    !events.some((event) => event.severity === "warn" || event.severity === "error")
  )
    return null;

  const completed = Math.min(operation.completed, operation.total || operation.completed);
  const progress = Math.max(0, Math.min(100, operation.progress));
  const failure = operation.failures?.find((entry) => entry.failure.Message)?.failure;
  const rootEvent = [...events]
    .reverse()
    .find((event) => event.scope === "workflow" && event.scopeId === operation.workflowId);
  const latestFailureEvent = [...events]
    .reverse()
    .find((event) => event.severity === "error" || event.severity === "warn");
  const failureMessage = latestFailureEvent?.message || failure?.Message;
  const eventRecovery = latestFailureEvent?.recovery;
  const recovery =
    eventRecovery && eventRecovery !== "none"
      ? `Recovery: ${eventRecovery.replaceAll("_", " ")}.`
      : failure?.Recovery && failure.Recovery !== "none"
        ? `Recovery: ${failure.Recovery.replaceAll("_", " ")}.`
        : "";
  const eventScopeIds = new Set(scopedEvents.map((event) => event.scopeId));
  const activeItems = (operation.items || []).filter(
    (item) =>
      (item.status === "queued" || item.status === "running" || item.status === "failed") &&
      !eventScopeIds.has(item.id),
  );

  return (
    <section className="panel mb-3 grid gap-2 py-3" role="status" aria-live="polite">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <p className="label">Release workflow</p>
          <p className="font-semibold text-[var(--text)]">
            {(events.length ? rootEvent?.message : failureMessage) ||
              operation.message ||
              operation.phase ||
              operation.command}
          </p>
        </div>
        <span className="muted text-sm">
          {operation.total > 0 ? `${completed}/${operation.total} complete` : `${progress}%`}
        </span>
      </div>
      <div
        aria-label="Release workflow progress"
        aria-valuemax={100}
        aria-valuemin={0}
        aria-valuenow={progress}
        className="h-2 w-full overflow-hidden rounded-full bg-white/10"
        role="progressbar"
      >
        <div
          className="h-full rounded-full bg-[var(--accent-2)] transition-[width]"
          style={{ width: `${progress}%` }}
        />
      </div>
      {recovery ? <p className="muted text-sm">{recovery}</p> : null}
      {scopedEvents.length ? (
        <div className="grid gap-1 text-sm">
          {scopedEvents.map((event) => (
            <div
              className="flex flex-wrap items-center justify-between gap-2 rounded border border-white/10 bg-white/5 px-2 py-1.5"
              key={`${event.sequence}-${event.scope}-${event.scopeId || "workflow"}`}
            >
              <span className="font-semibold">
                {operation.items?.find((item) => item.id === event.scopeId)?.label ||
                  event.scopeId ||
                  event.scope}
              </span>
              <span
                className={
                  event.severity === "error"
                    ? "text-[var(--danger)]"
                    : event.severity === "warn"
                      ? "text-amber-300"
                      : "muted"
                }
              >
                {event.message || event.state}
              </span>
            </div>
          ))}
        </div>
      ) : null}
      {activeItems.length ? (
        <div className="grid gap-1 text-sm">
          {activeItems.map((item) => (
            <div
              className="flex flex-wrap items-center justify-between gap-2 rounded border border-white/10 bg-white/5 px-2 py-1.5"
              key={`${item.kind}-${item.id}`}
            >
              <span className="font-semibold">{item.label || item.id}</span>
              <span className={item.status === "failed" ? "text-[var(--danger)]" : "muted"}>
                {item.message || item.status}
              </span>
            </div>
          ))}
        </div>
      ) : null}
    </section>
  );
}
