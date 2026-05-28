// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useMemo } from "react";
import type { Dispatch, SetStateAction } from "react";
import { Button } from "../../components/ui/button";
import { Switch } from "../../components/ui/switch";
import type {
  MetadataPreview,
  TrackerDryRunPreview,
  TrackerUploadItem,
  TrackerUploadSnapshot,
} from "../../types";
import { cn } from "../../utils/cn";

type Props = {
  trackerUploadItems: TrackerUploadItem[];
  releasePageTrackerSelection: Record<string, boolean>;
  dupedTrackerSet: Set<string>;
  ruleSkipReasons: Record<string, string>;
  ruleSkippedTrackerSet: Set<string>;
  failedDupeTrackerSet: Set<string>;
  uploadToggles: Record<string, boolean>;
  setUploadToggles: Dispatch<SetStateAction<Record<string, boolean>>>;
  namingOverrides: Array<[string, unknown]>;
  preview: MetadataPreview;
  formatLabel: (value: string) => string;
  uploadRunning: boolean;
  uploadError: string;
  uploadSnapshot: TrackerUploadSnapshot | null;
  dryRunLoading: boolean;
  dryRunError: string;
  dryRunPreview: TrackerDryRunPreview;
  trackerQuestionnaireAnswers: Record<string, Record<string, string>>;
  onQuestionnaireAnswerChange: (tracker: string, key: string, value: string) => void;
  onRunDryRun: () => void;
  onStartUpload: () => void;
  onCancelUpload: () => void;
  onRetryFailed: () => void;
};

const statusClass = (status: string) => {
  const normalized = status.replaceAll("_", "-");
  if (["running", "queued", "ready"].includes(normalized)) {
    return "border-blue-400/45 text-blue-100";
  }
  if (["success", "completed"].includes(normalized)) {
    return "border-emerald-400/45 text-emerald-100";
  }
  if (["failed", "completed-with-errors", "canceled", "blocked"].includes(normalized)) {
    return "border-red-400/45 text-red-100";
  }
  return "border-white/15 text-[var(--muted)]";
};

const subtleBox = "rounded-md border border-white/10 bg-white/5 px-2 py-1.5";
const blockReasonClass =
  "inline-flex h-5 items-center rounded border border-red-400/30 bg-red-500/10 px-1.5 text-[11px] font-semibold leading-none text-red-700 dark:text-red-100";

export default function TrackerUploadPage(props: Readonly<Props>) {
  const {
    trackerUploadItems,
    releasePageTrackerSelection,
    dupedTrackerSet,
    ruleSkipReasons,
    ruleSkippedTrackerSet,
    failedDupeTrackerSet,
    uploadToggles,
    setUploadToggles,
    namingOverrides,
    preview,
    formatLabel,
    uploadRunning,
    uploadError,
    uploadSnapshot,
    dryRunLoading,
    dryRunError,
    dryRunPreview,
    trackerQuestionnaireAnswers,
    onQuestionnaireAnswerChange,
    onRunDryRun,
    onStartUpload,
    onCancelUpload,
    onRetryFailed,
  } = props;

  const visibleTrackers = useMemo(
    () => trackerUploadItems.filter((tracker) => releasePageTrackerSelection[tracker.name]),
    [trackerUploadItems, releasePageTrackerSelection],
  );

  const trackerBlockState = useMemo(() => {
    const next: Record<string, { blocked: boolean; reasons: string[]; hardBlocked: boolean }> = {};
    visibleTrackers.forEach((tracker) => {
      const normalized = tracker.name.toLowerCase().trim();
      const reasons: string[] = [];
      const hasFailedDupe = failedDupeTrackerSet.has(normalized);
      const hasDupes = dupedTrackerSet.has(normalized);
      const hasRuleSkip = ruleSkippedTrackerSet.has(normalized);
      if (hasFailedDupe) {
        reasons.push("Dupe check failed");
      }
      if (hasDupes) {
        reasons.push("Dupes found");
      }
      if (hasRuleSkip) {
        reasons.push(ruleSkipReasons[normalized] || "Rule check failed");
      }
      next[tracker.name] = {
        blocked: reasons.length > 0,
        reasons,
        hardBlocked: hasFailedDupe,
      };
    });
    return next;
  }, [
    visibleTrackers,
    failedDupeTrackerSet,
    dupedTrackerSet,
    ruleSkippedTrackerSet,
    ruleSkipReasons,
  ]);

  const availableTrackers = useMemo(
    () => visibleTrackers.filter((tracker) => !trackerBlockState[tracker.name]?.blocked),
    [visibleTrackers, trackerBlockState],
  );

  const blockedTrackers = useMemo(
    () => visibleTrackers.filter((tracker) => trackerBlockState[tracker.name]?.blocked),
    [visibleTrackers, trackerBlockState],
  );

  const selectedTrackerCount = useMemo(
    () =>
      availableTrackers.filter((tracker) => {
        const normalized = tracker.name.toLowerCase().trim();
        if (!uploadToggles[tracker.name]) return false;
        if (dupedTrackerSet.has(normalized)) return false;
        if (ruleSkippedTrackerSet.has(normalized)) return false;
        if (failedDupeTrackerSet.has(normalized)) return false;
        return true;
      }).length,
    [
      availableTrackers,
      uploadToggles,
      dupedTrackerSet,
      ruleSkippedTrackerSet,
      failedDupeTrackerSet,
    ],
  );

  const trackerStatusMap = useMemo(() => {
    const next: Record<string, { status: string; message: string }> = {};
    (uploadSnapshot?.trackers || []).forEach((entry) => {
      if (!entry?.tracker) return;
      next[entry.tracker] = {
        status: String(entry.status || "").toLowerCase(),
        message: entry.message || "",
      };
    });
    return next;
  }, [uploadSnapshot]);

  const uploadStatus = String(uploadSnapshot?.status || "").toLowerCase();
  const canRetry = !uploadRunning && (uploadSnapshot?.failedTrackers?.length || 0) > 0;
  const dryRunMap = useMemo(() => {
    const next: Record<string, (typeof dryRunPreview.Trackers)[number]> = {};
    (dryRunPreview?.Trackers || []).forEach((entry) => {
      const key = String(entry?.Tracker || "")
        .toLowerCase()
        .trim();
      if (!key) return;
      next[key] = entry;
    });
    return next;
  }, [dryRunPreview]);

  const renderQuestionnaireField = (
    trackerName: string,
    field: NonNullable<(typeof dryRunPreview.Trackers)[number]["Questionnaire"]>["Fields"][number],
    value: string,
  ) => {
    if (field.Kind === "textarea") {
      return (
        <textarea
          className="text-input min-h-24 w-full resize-y"
          value={value}
          placeholder={field.Placeholder || ""}
          onChange={(event) =>
            onQuestionnaireAnswerChange(trackerName, field.Key, event.target.value)
          }
          rows={4}
        />
      );
    }

    if (field.Kind === "select" && field.Options?.length) {
      return (
        <select
          className="text-input w-full appearance-auto"
          value={value}
          onChange={(event) =>
            onQuestionnaireAnswerChange(trackerName, field.Key, event.target.value)
          }
        >
          <option value="">{field.Placeholder || "Select an option"}</option>
          {field.Options.map((option) => (
            <option key={option} value={option}>
              {option}
            </option>
          ))}
        </select>
      );
    }

    if (field.Kind === "boolean") {
      const checked = value === "true";
      return (
        <label className="inline-flex items-center gap-2 text-sm text-[var(--text)]">
          <input
            type="checkbox"
            checked={checked}
            onChange={(event) =>
              onQuestionnaireAnswerChange(
                trackerName,
                field.Key,
                event.target.checked ? "true" : "false",
              )
            }
          />
          <span>{field.Placeholder || "Enabled"}</span>
        </label>
      );
    }

    return (
      <input
        className="text-input w-full"
        type="text"
        value={value}
        placeholder={field.Placeholder || ""}
        onChange={(event) =>
          onQuestionnaireAnswerChange(trackerName, field.Key, event.target.value)
        }
      />
    );
  };

  return (
    <section className="flex flex-col gap-2.5">
      <header className="max-w-3xl">
        <p className="eyebrow">Tracker Upload</p>
        <h1>Upload Targets</h1>
        <p className="subtitle">Toggle trackers and review naming changes before upload.</p>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <Button
            type="button"
            variant="primary"
            onClick={onStartUpload}
            disabled={uploadRunning || selectedTrackerCount === 0}
          >
            {uploadRunning ? "Uploading..." : "Start Upload"}
          </Button>
          <Button
            type="button"
            onClick={onRunDryRun}
            disabled={dryRunLoading || uploadRunning || selectedTrackerCount === 0}
          >
            {dryRunLoading ? "Running Dry Run..." : "Run Dry Run"}
          </Button>
          <Button type="button" onClick={onCancelUpload} disabled={!uploadRunning}>
            Cancel
          </Button>
          <Button type="button" onClick={onRetryFailed} disabled={!canRetry}>
            Retry Failed
          </Button>
          <p className="m-0 text-xs text-[var(--muted)]">
            Selected: {selectedTrackerCount} · Uploaded: {uploadSnapshot?.uploadedCount || 0}
          </p>
          {uploadStatus ? (
            <p className="m-0 text-xs text-[var(--muted)]">
              Job status: {uploadStatus.replaceAll("_", " ")}
            </p>
          ) : null}
        </div>
        {uploadError ? <p className="error">{uploadError}</p> : null}
        {dryRunError ? <p className="error">{dryRunError}</p> : null}
      </header>

      {visibleTrackers.length === 0 ? (
        <p className="muted">No tracker entries with credentials or details were found.</p>
      ) : (
        <div className="grid gap-1.5">
          {blockedTrackers.length > 0 ? (
            <details className="rounded-lg border border-white/10 bg-white/5 px-2.5 py-2">
              <summary className="cursor-pointer list-none text-sm font-semibold marker:content-[''] [&::-webkit-details-marker]:hidden">
                Blocked trackers ({blockedTrackers.length})
              </summary>
              <div className="mt-2 grid gap-1">
                {blockedTrackers.map((tracker) => {
                  const state = trackerBlockState[tracker.name];
                  return (
                    <div
                      className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-white/10 bg-white/5 px-2 py-1.5"
                      key={tracker.name}
                    >
                      <span className="value text-sm leading-5">{tracker.name}</span>
                      <div className="flex flex-wrap items-center justify-end gap-1">
                        {state?.reasons.map((reason) => (
                          <span className={blockReasonClass} key={`${tracker.name}-${reason}`}>
                            {reason}
                          </span>
                        ))}
                        {state?.hardBlocked ? (
                          <span className="text-xs text-[var(--muted)]">not uploadable</span>
                        ) : null}
                      </div>
                    </div>
                  );
                })}
              </div>
            </details>
          ) : null}

          {availableTrackers.map((tracker) => {
            const normalizedTrackerName = tracker.name.toLowerCase().trim();
            const selected = Boolean(uploadToggles[tracker.name]);
            const enabled = selected;
            const trackerStatus = trackerStatusMap[tracker.name];
            const dryRun = dryRunMap[normalizedTrackerName];
            const imageHost = dryRun?.ImageHost;
            const imageHostWarnings = imageHost?.Warnings || [];
            const imageHostStatus = String(imageHost?.Status || "").toLowerCase();
            const questionnaire = dryRun?.Questionnaire;
            const questionnaireAnswers =
              trackerQuestionnaireAnswers[tracker.name.toUpperCase().trim()] || {};
            let statusLabel = trackerStatus?.status || "";
            if (!statusLabel) {
              statusLabel = enabled ? "ready" : "disabled";
            }

            return (
              <article
                className="grid gap-1.5 rounded-lg border border-white/10 bg-white/5 px-2.5 py-2"
                key={tracker.name}
              >
                <div className="flex items-center justify-between gap-2">
                  <div className="flex flex-wrap items-center gap-1.5">
                    <p className="value text-base leading-5">{tracker.name}</p>
                    <span
                      className={cn(
                        "inline-flex items-center rounded-full border px-2 py-0.5 text-xs capitalize",
                        statusClass(statusLabel),
                      )}
                    >
                      {statusLabel.replaceAll("_", " ")}
                    </span>
                  </div>
                  <Switch
                    aria-label={`Enable upload for ${tracker.name}`}
                    checked={selected}
                    onChange={(event) =>
                      setUploadToggles((prev) => ({
                        ...prev,
                        [tracker.name]: event.target.checked,
                      }))
                    }
                  />
                </div>

                {trackerStatus?.message ? (
                  <p className="m-0 text-xs text-[var(--muted)]">{trackerStatus.message}</p>
                ) : null}
                {imageHost?.Message &&
                (imageHostWarnings.length > 0 || imageHostStatus === "warning") ? (
                  <p className="m-0 rounded-md border border-amber-400/40 bg-amber-400/10 px-2 py-1 text-xs text-amber-100 [overflow-wrap:anywhere]">
                    {imageHost.Message}
                  </p>
                ) : null}
                {imageHostWarnings.map((warning, index) => {
                  const host = String(warning.Host || "").trim();
                  const message = String(warning.Message || "").trim();
                  if (!host && !message) return null;
                  return (
                    <p
                      className="m-0 rounded-md border border-amber-400/40 bg-amber-400/10 px-2 py-1 text-xs text-amber-100 [overflow-wrap:anywhere]"
                      key={`${tracker.name}-${host || "host"}-${index}`}
                    >
                      {host ? `${host} failed` : "Image host warning"}
                      {message ? `: ${message}` : ""}
                    </p>
                  );
                })}

                <details>
                  <summary className="cursor-pointer list-none text-sm font-semibold marker:content-[''] [&::-webkit-details-marker]:hidden">
                    Dry run data
                  </summary>
                  <div className="mt-2 grid gap-1.5">
                    {dryRun ? (
                      <>
                        <div>
                          <p className="label">Status</p>
                          <p className="value mono">{dryRun.Status || "ready"}</p>
                        </div>
                        {dryRun.Message ? (
                          <div>
                            <p className="label">Message</p>
                            <p className="value">{dryRun.Message}</p>
                          </div>
                        ) : null}
                        {dryRun.ReleaseName ? (
                          <div>
                            <p className="label">Release name</p>
                            <p className="value mono">{dryRun.ReleaseName}</p>
                          </div>
                        ) : null}
                        {dryRun.DescriptionGroup ? (
                          <div>
                            <p className="label">Description group</p>
                            <p className="value mono">{dryRun.DescriptionGroup}</p>
                          </div>
                        ) : null}
                        {dryRun.Endpoint ? (
                          <div>
                            <p className="label">Endpoint</p>
                            <p className="value mono">{dryRun.Endpoint}</p>
                          </div>
                        ) : null}
                        {dryRun.Files?.length ? (
                          <div className="grid gap-1.5">
                            {dryRun.Files.map((file) => (
                              <div className={subtleBox} key={`${file.Field}-${file.Path}`}>
                                <p className="label">File · {file.Field}</p>
                                <p className="value mono">{file.Path || "(missing)"}</p>
                              </div>
                            ))}
                          </div>
                        ) : null}
                        {Object.keys(dryRun.Payload || {}).length ? (
                          <div className="grid gap-1.5">
                            {Object.entries(dryRun.Payload)
                              .sort(([left], [right]) => left.localeCompare(right))
                              .map(([key, value]) => (
                                <div className={subtleBox} key={key}>
                                  <p className="label">{key}</p>
                                  <p className="value mono">{String(value)}</p>
                                </div>
                              ))}
                          </div>
                        ) : null}
                        {questionnaire?.Fields?.length ? (
                          <div>
                            <p className="label">Questionnaire</p>
                            <div className="grid gap-1.5">
                              {questionnaire.Fields.map((field) => (
                                <label className={subtleBox} key={field.Key}>
                                  <p className="label">
                                    {field.Label || field.Key}
                                    {field.Required ? " *" : ""}
                                  </p>
                                  {renderQuestionnaireField(
                                    tracker.name,
                                    field,
                                    questionnaireAnswers[field.Key] ?? field.Value ?? "",
                                  )}
                                  {field.Help ? <p className="muted">{field.Help}</p> : null}
                                </label>
                              ))}
                            </div>
                          </div>
                        ) : null}
                      </>
                    ) : (
                      <p className="muted">Run dry run to generate tracker payload fields.</p>
                    )}
                  </div>
                </details>

                {namingOverrides.length > 0 ? (
                  <details>
                    <summary className="cursor-pointer list-none text-sm font-semibold marker:content-[''] [&::-webkit-details-marker]:hidden">
                      Naming changes
                    </summary>
                    <div className="mt-2 grid gap-1.5">
                      <div>
                        <p className="label">Release name</p>
                        <p className="value mono">{preview.ReleaseName || "No release name yet"}</p>
                      </div>
                      <div className="grid gap-1.5">
                        {namingOverrides.map(([key, value]) => (
                          <div className={subtleBox} key={key}>
                            <p className="label">{formatLabel(key)}</p>
                            <p className="value mono">{String(value)}</p>
                          </div>
                        ))}
                      </div>
                    </div>
                  </details>
                ) : null}
              </article>
            );
          })}
        </div>
      )}
    </section>
  );
}
