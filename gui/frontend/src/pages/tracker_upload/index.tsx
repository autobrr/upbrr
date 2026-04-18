// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useMemo } from "react";
import type { Dispatch, SetStateAction } from "react";
import type {
  MetadataPreview,
  TrackerDryRunPreview,
  TrackerUploadItem,
  TrackerUploadSnapshot,
} from "../../types";

type Props = {
  trackerUploadItems: TrackerUploadItem[];
  releasePageTrackerSelection: Record<string, boolean>;
  dupedTrackerSet: Set<string>;
  ruleSkipReasons: Record<string, string>;
  ruleSkippedTrackerSet: Set<string>;
  overrideRuleBlocks: boolean;
  setOverrideRuleBlocks: Dispatch<SetStateAction<boolean>>;
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

export default function TrackerUploadPage(props: Readonly<Props>) {
  const {
    trackerUploadItems,
    releasePageTrackerSelection,
    dupedTrackerSet,
    ruleSkipReasons,
    ruleSkippedTrackerSet,
    overrideRuleBlocks,
    setOverrideRuleBlocks,
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

  const selectedTrackerCount = useMemo(
    () =>
      visibleTrackers.filter((tracker) => {
        const normalized = tracker.name.toLowerCase().trim();
        if (!uploadToggles[tracker.name]) return false;
        if (dupedTrackerSet.has(normalized) && !overrideRuleBlocks) return false;
        if (ruleSkippedTrackerSet.has(normalized) && !overrideRuleBlocks) return false;
        return true;
      }).length,
    [visibleTrackers, uploadToggles, dupedTrackerSet, ruleSkippedTrackerSet, overrideRuleBlocks],
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
          className="text-input upload-questionnaire__input upload-questionnaire__textarea"
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
          className="text-input upload-questionnaire__input upload-questionnaire__select"
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
        <label className="upload-questionnaire__checkbox">
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
        className="text-input upload-questionnaire__input"
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
    <section className="upload-panel">
      <header className="upload-header">
        <p className="eyebrow">Tracker Upload</p>
        <h1>Upload Targets</h1>
        <p className="subtitle">Toggle trackers and review naming changes before upload.</p>
        <div className="upload-actions">
          <label className="upload-toggle upload-toggle--labelled">
            <input
              type="checkbox"
              aria-label="Override tracker blocks"
              checked={overrideRuleBlocks}
              onChange={(event) => setOverrideRuleBlocks(event.target.checked)}
            />
            <span className="upload-toggle__pill" />
            <span className="upload-toggle__label">Override blocks</span>
          </label>
          <button
            type="button"
            className="upload-action-button upload-action-button--primary"
            onClick={onStartUpload}
            disabled={uploadRunning || selectedTrackerCount === 0}
          >
            {uploadRunning ? "Uploading..." : "Start Upload"}
          </button>
          <button
            type="button"
            className="upload-action-button"
            onClick={onRunDryRun}
            disabled={dryRunLoading || uploadRunning || selectedTrackerCount === 0}
          >
            {dryRunLoading ? "Running Dry Run..." : "Run Dry Run"}
          </button>
          <button
            type="button"
            className="upload-action-button"
            onClick={onCancelUpload}
            disabled={!uploadRunning}
          >
            Cancel
          </button>
          <button
            type="button"
            className="upload-action-button"
            onClick={onRetryFailed}
            disabled={!canRetry}
          >
            Retry Failed
          </button>
          <p className="upload-summary-text">
            Selected: {selectedTrackerCount} · Uploaded: {uploadSnapshot?.uploadedCount || 0}
          </p>
          {uploadStatus ? (
            <p className="upload-summary-text">Job status: {uploadStatus.replaceAll("_", " ")}</p>
          ) : null}
        </div>
        {uploadError ? <p className="error-text">{uploadError}</p> : null}
        {dryRunError ? <p className="error-text">{dryRunError}</p> : null}
      </header>

      {visibleTrackers.length === 0 ? (
        <p className="muted">No tracker entries with credentials or details were found.</p>
      ) : (
        <div className="upload-grid">
          {visibleTrackers.map((tracker) => {
            const normalizedTrackerName = tracker.name.toLowerCase().trim();
            const hasDupes = dupedTrackerSet.has(tracker.name.toLowerCase());
            const ruleSkipReason = ruleSkipReasons[tracker.name.toLowerCase()] || "";
            const hasRuleSkip = ruleSkippedTrackerSet.has(tracker.name.toLowerCase());
            const selected = Boolean(uploadToggles[tracker.name]);
            const enabled =
              selected && (!hasDupes || overrideRuleBlocks) && (!hasRuleSkip || overrideRuleBlocks);
            const showBadges = hasRuleSkip || hasDupes;
            const trackerStatus = trackerStatusMap[tracker.name];
            const dryRun = dryRunMap[normalizedTrackerName];
            const questionnaire = dryRun?.Questionnaire;
            const questionnaireAnswers =
              trackerQuestionnaireAnswers[tracker.name.toUpperCase().trim()] || {};
            let statusLabel = trackerStatus?.status || "";
            if (!statusLabel) {
              if ((hasDupes && !overrideRuleBlocks) || (hasRuleSkip && !overrideRuleBlocks)) {
                statusLabel = "blocked";
              } else if (enabled) {
                statusLabel = "ready";
              } else {
                statusLabel = "disabled";
              }
            }
            return (
              <article className="upload-card" key={tracker.name}>
                <div className="upload-card__header">
                  <div className="upload-title-row">
                    <p className="value upload-title">{tracker.name}</p>
                    <span
                      className={`upload-status-pill upload-status-pill--${statusLabel.replaceAll("_", "-")}`}
                    >
                      {statusLabel.replaceAll("_", " ")}
                    </span>
                    {showBadges ? (
                      <div className="upload-badges">
                        {hasRuleSkip ? (
                          <span
                            className="tracker-rule-badge"
                            title={ruleSkipReason || "Rule check failed"}
                          >
                            {overrideRuleBlocks ? "Rule override" : "Rule check failed"}
                          </span>
                        ) : null}
                        {hasDupes ? (
                          <span className="tracker-dupe-badge">
                            {overrideRuleBlocks ? "Dupe override" : "Dupes found"}
                          </span>
                        ) : null}
                      </div>
                    ) : null}
                  </div>
                  <label
                    className="upload-toggle"
                    aria-label={`Toggle upload target for ${tracker.name}`}
                  >
                    <input
                      type="checkbox"
                      aria-label={`Enable upload for ${tracker.name}`}
                      checked={selected}
                      disabled={false}
                      onChange={(event) =>
                        setUploadToggles((prev) => ({
                          ...prev,
                          [tracker.name]: event.target.checked,
                        }))
                      }
                    />
                    <span className="upload-toggle__pill" />
                  </label>
                </div>

                {trackerStatus?.message ? (
                  <p className="upload-status-message">{trackerStatus.message}</p>
                ) : null}
                {hasDupes && !overrideRuleBlocks ? (
                  <p className="upload-status-message">
                    Dupes found. Enable override blocks to upload anyway.
                  </p>
                ) : null}
                {hasRuleSkip && !overrideRuleBlocks ? (
                  <p className="upload-status-message">
                    {ruleSkipReason ||
                      "Rule check failed. Enable override blocks to upload anyway."}
                  </p>
                ) : null}

                <details className="upload-details">
                  <summary>Dry run data</summary>
                  <div className="upload-details__body">
                    {dryRun ? (
                      <>
                        <div className="upload-detail">
                          <p className="label">Status</p>
                          <p className="value mono">{dryRun.Status || "ready"}</p>
                        </div>
                        {dryRun.Message ? (
                          <div className="upload-detail">
                            <p className="label">Message</p>
                            <p className="value">{dryRun.Message}</p>
                          </div>
                        ) : null}
                        {dryRun.ReleaseName ? (
                          <div className="upload-detail">
                            <p className="label">Release name</p>
                            <p className="value mono">{dryRun.ReleaseName}</p>
                          </div>
                        ) : null}
                        {dryRun.DescriptionGroup ? (
                          <div className="upload-detail">
                            <p className="label">Description group</p>
                            <p className="value mono">{dryRun.DescriptionGroup}</p>
                          </div>
                        ) : null}
                        {dryRun.Endpoint ? (
                          <div className="upload-detail">
                            <p className="label">Endpoint</p>
                            <p className="value mono">{dryRun.Endpoint}</p>
                          </div>
                        ) : null}
                        {dryRun.Files?.length ? (
                          <div className="upload-changes">
                            {dryRun.Files.map((file) => (
                              <div className="upload-change" key={`${file.Field}-${file.Path}`}>
                                <p className="label">File · {file.Field}</p>
                                <p className="value mono">{file.Path || "(missing)"}</p>
                              </div>
                            ))}
                          </div>
                        ) : null}
                        {Object.keys(dryRun.Payload || {}).length ? (
                          <div className="upload-changes">
                            {Object.entries(dryRun.Payload)
                              .sort(([left], [right]) => left.localeCompare(right))
                              .map(([key, value]) => (
                                <div className="upload-change" key={key}>
                                  <p className="label">{key}</p>
                                  <p className="value mono">{String(value)}</p>
                                </div>
                              ))}
                          </div>
                        ) : null}
                        {questionnaire?.Fields?.length ? (
                          <div className="upload-questionnaire">
                            <p className="label">Questionnaire</p>
                            <div className="upload-changes">
                              {questionnaire.Fields.map((field) => (
                                <label className="upload-change" key={field.Key}>
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
                  <details className="upload-details">
                    <summary>Naming changes</summary>
                    <div className="upload-details__body">
                      <div className="upload-detail">
                        <p className="label">Release name</p>
                        <p className="value mono">{preview.ReleaseName || "No release name yet"}</p>
                      </div>
                      <div className="upload-changes">
                        {namingOverrides.map(([key, value]) => (
                          <div className="upload-change" key={key}>
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
