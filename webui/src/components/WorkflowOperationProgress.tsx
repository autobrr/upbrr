// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { pageStyle } from "./ui/pageStyle";
import type { Operation as WorkflowOperationStatus } from "../api/generated/release-workflow";

const visibleStatuses = new Set(["queued", "running", "blocked", "failed", "interrupted"]);

/** Shows stable operation snapshots plus transient event detail; duplicate checks render on their owning route. */
export function WorkflowOperationProgress({
  operation,
}: Readonly<{ operation?: WorkflowOperationStatus | null }>) {
  if (!operation || operation.operation === "duplicate_check") return null;

  const metadataPreparation = operation.operation === "preparation";
  const comparisonProgress = Boolean(operation.phase?.startsWith("comparisons_"));
  const showComparisonDetail = comparisonProgress && operation.status === "running";
  const comparisonItems = showComparisonDetail
    ? (operation.items || []).filter((item) => item.kind.startsWith("comparison_"))
    : [];
  const comparisonItemIDs = new Set(comparisonItems.map((item) => item.id));
  const phaseItems = comparisonItems.filter(
    (item) => !item.phase || item.phase === operation.phase,
  );
  const comparisonItem =
    phaseItems.find((item) => item.status === "running") ??
    phaseItems.find((item) => item.status === "queued") ??
    (phaseItems.length === 1 ? phaseItems[0] : undefined);
  const itemProgress =
    comparisonItem?.status === "skipped"
      ? 0
      : comparisonItem?.total
        ? Math.max(0, Math.min(100, (100 * (comparisonItem.completed ?? 0)) / comparisonItem.total))
        : undefined;
  const metadataItemIDs = new Set(
    (operation.items || [])
      .filter((item) => item.kind.startsWith("preparation_"))
      .map((item) => item.id),
  );
  const events = operation.events || [];
  const latestMetadataMessages = new Map(
    events
      .filter((event) => metadataItemIDs.has(event.scopeId || ""))
      .map((event) => [event.scopeId, event.message]),
  );
  const scopedEvents = events.filter(
    (event) =>
      event.scopeId !== operation.workflowId &&
      !(metadataPreparation && metadataItemIDs.has(event.scopeId || "")) &&
      ((!comparisonItemIDs.has(event.scopeId || "") &&
        ["queued", "running"].includes(event.lifecycle)) ||
        event.severity === "warn" ||
        event.severity === "error"),
  );
  if (
    !visibleStatuses.has(operation.status) &&
    !operation.items?.some(
      (item) =>
        item.kind.startsWith("preparation_") &&
        (item.status === "partial" || item.status === "canceled"),
    ) &&
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
      ((["queued", "running"].includes(item.status) && !comparisonItemIDs.has(item.id)) ||
        item.status === "failed" ||
        item.status === "partial" ||
        (metadataPreparation && item.status === "canceled")) &&
      !eventScopeIds.has(item.id),
  );

  return (
    <section className={`${pageStyle.panel} mb-3 grid gap-2 py-3`} role="status" aria-live="polite">
      <div
        className={
          showComparisonDetail
            ? "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2"
            : "flex flex-wrap items-center justify-between gap-2"
        }
      >
        <div className="min-w-0">
          <p className={pageStyle.label}>Release workflow</p>
          <p
            className={
              showComparisonDetail
                ? "truncate font-semibold text-foreground"
                : "font-semibold text-foreground"
            }
          >
            {(operation.operation === "analyze_hdr" ||
            (comparisonProgress && operation.status === "running")
              ? operation.message
              : events.length
                ? rootEvent?.message
                : failureMessage) ||
              operation.message ||
              operation.phase ||
              operation.command}
          </p>
        </div>
        <span className="text-muted-foreground text-sm tabular-nums">
          {metadataPreparation
            ? "Metadata stages"
            : operation.total > 0 && operation.operation !== "analyze_hdr" && !comparisonProgress
              ? `${completed}/${operation.total} complete`
              : `${Math.round(progress)}%`}
        </span>
      </div>
      {!metadataPreparation ? (
        <div
          aria-label="Release workflow progress"
          aria-valuemax={100}
          aria-valuemin={0}
          aria-valuenow={progress}
          className="h-2 w-full overflow-hidden rounded-full bg-secondary"
          role="progressbar"
        >
          <div
            className="h-full rounded-full bg-primary motion-safe:transition-[width]"
            style={{ width: `${progress}%` }}
          />
        </div>
      ) : null}
      {showComparisonDetail && (
        <div className="grid gap-1.5 rounded border border-border bg-card px-2 py-1.5 text-sm">
          <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-2">
            <span className="truncate font-semibold" title={comparisonItem?.label}>
              {comparisonItem?.label || "Comparison tasks"}
            </span>
            <span
              className="truncate text-right text-muted-foreground tabular-nums"
              title={comparisonItem?.message}
            >
              {comparisonItem?.message || comparisonItem?.status || "Overall progress only"}
            </span>
          </div>
          {comparisonItem && (
            <div
              aria-label="Comparison task progress"
              aria-valuemax={100}
              aria-valuemin={0}
              aria-valuenow={itemProgress}
              className="h-1.5 w-full overflow-hidden rounded-full bg-secondary"
              role="progressbar"
            >
              <div
                className="h-full bg-primary motion-safe:transition-[width]"
                style={{ width: `${itemProgress ?? 0}%` }}
              />
            </div>
          )}
        </div>
      )}
      {recovery ? <p className="text-muted-foreground text-sm">{recovery}</p> : null}
      {scopedEvents.length ? (
        <div className="grid gap-1 text-sm">
          {scopedEvents.map((event) => (
            <div
              className="flex flex-wrap items-center justify-between gap-2 rounded border border-border bg-card px-2 py-1.5"
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
                    ? "text-destructive-text"
                    : event.severity === "warn"
                      ? "text-[var(--status-warning)]"
                      : "text-muted-foreground"
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
              className="flex flex-wrap items-center justify-between gap-2 rounded border border-border bg-card px-2 py-1.5"
              key={`${item.kind}-${item.id}`}
            >
              <span className="font-semibold">{item.label || item.id}</span>
              <span
                className={
                  item.status === "failed" ? "text-destructive-text" : "text-muted-foreground"
                }
              >
                {item.message || latestMetadataMessages.get(item.id) || item.status}
              </span>
            </div>
          ))}
        </div>
      ) : null}
    </section>
  );
}
