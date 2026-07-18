// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useMemo } from "react";
import { Button } from "../../components/ui/button";
import { TrackerIconImage } from "../../components/ui/tracker-icon";
import type { TrackerIconCache } from "../../hooks/useTrackerIcons";
import { trackerIconFor } from "../../hooks/useTrackerIcons";
import type { UploadFacet } from "../../releaseSession/types";
import type { TrackerDryRunFile, TrackerUploadItem } from "../../types";

type Props = Readonly<{
  facet: UploadFacet;
  trackerUploadItems: readonly TrackerUploadItem[];
  useFavicons?: boolean;
  faviconOnly?: boolean;
  trackerIconSrcByName: TrackerIconCache;
}>;

const terminalStatuses = new Set(["completed", "completed_with_errors", "failed", "canceled"]);
const dryRunPayloadPreviewLimit = 240;
const dryRunBodyPayloadKeys = new Set([
  "description",
  "desc",
  "descr",
  "release_desc",
  "album_desc",
  "mediainfo",
  "mediainfo[]",
  "media_info",
  "bdinfo",
  "bd_info",
  "techinfo",
  "technical_info",
  "technicaldetails",
]);
const sensitiveDryRunPayloadKeys = new Set([
  "anticsrftoken",
  "accesstoken",
  "apikey",
  "apitoken",
  "auth",
  "authorization",
  "authkey",
  "authtoken",
  "cookie",
  "csrf",
  "email",
  "infohash",
  "key",
  "passkey",
  "password",
  "passwordconfirm",
  "passwordconfirmation",
  "popcron",
  "refreshtoken",
  "rsskey",
  "secret",
  "secretkey",
  "sessionkey",
  "sessiontoken",
  "token",
  "torrentpass",
  "torrentpasskey",
  "uid",
  "user",
  "userid",
  "username",
]);

const normalizedDryRunPayloadKey = (key: string) => key.trim().toLowerCase();

const normalizedSensitiveDryRunPayloadKey = (key: string) =>
  key
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]/g, "");

const isSensitiveDryRunPayloadField = (key: string) =>
  sensitiveDryRunPayloadKeys.has(normalizedSensitiveDryRunPayloadKey(key));

const redactDryRunSecretFragments = (value: string) =>
  value
    .replace(/([?&])([^=&#\s]+)=([^&#\s]*)/g, (match, separator, key) =>
      isSensitiveDryRunPayloadField(String(key)) ? `${separator}${key}=[REDACTED]` : match,
    )
    .replace(
      /(^|[\s,{])([A-Za-z][A-Za-z0-9_-]*(?:key|token|pass|password|cookie|auth|secret|csrf|uid|user))\s*[:=]\s*[^\s,;}]+/gi,
      (match, prefix, key) =>
        isSensitiveDryRunPayloadField(String(key)) ? `${prefix}${key}=[REDACTED]` : match,
    );

const summarizeDryRunBody = (value: string) => {
  const trimmed = value.trim();
  if (!trimmed) return "";
  const lines = trimmed.split(/\r\n|\r|\n/).length;
  return `[${trimmed.length} bytes, ${lines} lines omitted]`;
};

const formatDryRunPayloadValue = (key: string, value: unknown) => {
  const trimmed = String(value ?? "").trim();
  if (!trimmed) return "";
  if (isSensitiveDryRunPayloadField(key)) return "[REDACTED]";

  const redacted = redactDryRunSecretFragments(trimmed);
  if (dryRunBodyPayloadKeys.has(normalizedDryRunPayloadKey(key))) {
    return summarizeDryRunBody(redacted);
  }

  const compact = redacted.replace(/\s+/g, " ");
  if (compact.length <= dryRunPayloadPreviewLimit) return compact;
  return `${compact.slice(0, dryRunPayloadPreviewLimit)}... [${redacted.length} bytes total]`;
};

const formatDryRunFilePath = (value: string) => {
  const trimmed = value.trim();
  if (!trimmed) return "(none)";

  const normalized = trimmed.replaceAll("\\", "/").toLowerCase();
  const original = trimmed.replaceAll("\\", "/");
  for (const marker of [".upbrr/tmp/", ".upbrr/cache/", ".upbrr/logs/"]) {
    if (normalized.startsWith(marker)) return original;
    const index = normalized.indexOf(`/${marker}`);
    if (index >= 0) return original.slice(index + 1);
  }
  return "[local path]";
};

const dryRunFileKey = (file: TrackerDryRunFile, index: number) =>
  `${file.Field}-${file.Path}-${index}`;

/** Thin presentation adapter for dry-run, review, and retained upload-job state. */
export default function TrackerUploadPage({
  facet,
  trackerUploadItems,
  useFavicons = true,
  faviconOnly = false,
  trackerIconSrcByName,
}: Props) {
  const { view } = facet;
  const selected = useMemo(() => new Set(view.selectedTrackers), [view.selectedTrackers]);
  const eligibilityByTracker = useMemo(
    () => new Map((view.eligibility?.Trackers || []).map((item) => [item.Tracker, item])),
    [view.eligibility],
  );
  const uploadRunning = Boolean(view.snapshot && !terminalStatuses.has(view.snapshot.status));
  const failedTrackers = view.snapshot?.failedTrackers || [];
  const snapshotTrackers = view.snapshot?.trackers || [];
  const hideTrackerNames = faviconOnly && useFavicons;

  const toggleTracker = (tracker: string, checked: boolean) => {
    const next = new Set(view.selectedTrackers);
    if (checked) next.add(tracker);
    else next.delete(tracker);
    facet.chooseTrackers([...next]);
  };

  return (
    <section className="flex flex-col gap-4">
      <header className="max-w-3xl">
        <p className="eyebrow">Tracker Upload</p>
        <h1>Review &amp; Upload</h1>
        <p className="subtitle">Select trackers, run an explicit dry run, review, then upload.</p>
      </header>

      <section className="panel grid gap-3">
        <h2>Tracker intent</h2>
        <div className="flex flex-wrap gap-2">
          {trackerUploadItems.map((item) => {
            const tracker = item.name.trim().toUpperCase();
            const eligibility = eligibilityByTracker.get(tracker);
            const blocked = eligibility ? !eligibility.Eligible : false;
            return (
              <label
                className="flex items-center gap-2 rounded border border-white/10 bg-white/5 px-2 py-1.5"
                key={tracker}
              >
                <input
                  type="checkbox"
                  checked={selected.has(tracker)}
                  onChange={(event) => toggleTracker(tracker, event.target.checked)}
                />
                <TrackerIconImage
                  tracker={tracker}
                  iconSrc={trackerIconFor(trackerIconSrcByName, tracker)}
                  enabled={useFavicons}
                />
                {hideTrackerNames ? null : <span>{tracker}</span>}
                {blocked ? <span className="text-xs text-red-300">Blocked</span> : null}
              </label>
            );
          })}
        </div>
        {(view.eligibility?.Trackers || [])
          .filter((item) => !item.Eligible && selected.has(item.Tracker))
          .map((item) => (
            <p className="error" key={item.Tracker}>
              {item.Tracker}: {(item.Reasons || []).map((reason) => reason.Message).join(" ")}
            </p>
          ))}
      </section>

      <section className="panel grid gap-3">
        <h2>Run options</h2>
        <div className="flex flex-wrap gap-4">
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={view.options.noSeed}
              onChange={(event) => facet.changeOptions({ noSeed: event.target.checked })}
            />
            Skip client injection
          </label>
          <label className="grid gap-1">
            <span className="label">Log level</span>
            <select
              value={view.options.runLogLevel}
              onChange={(event) => facet.changeOptions({ runLogLevel: event.target.value })}
            >
              {["trace", "debug", "info", "warn", "error"].map((level) => (
                <option key={level} value={level}>
                  {level}
                </option>
              ))}
            </select>
          </label>
        </div>
      </section>

      <section className="panel grid gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="primary"
            type="button"
            disabled={view.dryRunStatus === "running" || view.selectedTrackers.length === 0}
            onClick={() => void facet.runDryRun()}
          >
            {view.dryRunStatus === "running" ? "Running..." : "Run dry run"}
          </Button>
          <Button
            type="button"
            disabled={view.reviewStatus === "running" || Boolean(view.dryRunStaleReason)}
            onClick={() => void facet.review()}
          >
            {view.reviewStatus === "running" ? "Reviewing..." : "Review upload"}
          </Button>
          <Button
            variant="primary"
            type="button"
            disabled={uploadRunning || Boolean(view.reviewStaleReason) || !view.review?.Token}
            onClick={() => void facet.start()}
          >
            {uploadRunning ? "Uploading..." : "Start upload"}
          </Button>
          {uploadRunning ? (
            <button className="danger" type="button" onClick={() => void facet.cancel()}>
              Cancel upload
            </button>
          ) : null}
          {failedTrackers.length ? (
            <button className="ghost" type="button" onClick={() => void facet.retry()}>
              Retry failed
            </button>
          ) : null}
        </div>
        {view.dryRunStaleReason ? <p className="muted">Dry run: {view.dryRunStaleReason}</p> : null}
        {view.reviewStaleReason ? <p className="muted">Review: {view.reviewStaleReason}</p> : null}
        {view.error ? (
          <p className="error" role="alert">
            {view.error}
          </p>
        ) : null}
        {view.transientError ? (
          <p className="muted">Live update interrupted: {view.transientError}</p>
        ) : null}
      </section>

      {(view.dryRun?.Trackers || []).map((entry) => {
        const files = entry.Files || [];
        const questionnaireFields = entry.Questionnaire?.Fields || [];
        const ruleDecisions = entry.Diagnostics?.RuleDecisions || [];
        const duplicate = entry.Diagnostics?.Duplicate;
        const duplicateMatches = duplicate?.Filtered?.length
          ? duplicate.Filtered
          : duplicate?.Raw || [];
        const liveReasons = entry.Diagnostics?.LiveEligibilityReasons || [];
        return (
          <section className="panel grid gap-3" key={entry.Tracker}>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h2>{entry.Tracker}</h2>
              <span className="muted">{entry.Status || "pending"}</span>
            </div>
            {entry.Message ? <p className="muted">{entry.Message}</p> : null}
            {ruleDecisions.length ? (
              <div className="grid gap-2">
                <h3 className="text-sm font-semibold">Rule diagnostics</h3>
                {ruleDecisions.map((decision) => {
                  const disposition = String(decision.Disposition || "waivable");
                  const waivable = disposition === "waivable";
                  const authorized = Boolean(
                    view.authorizedRulesByTracker[entry.Tracker]?.includes(decision.Rule),
                  );
                  return (
                    <div
                      className="flex flex-wrap items-center justify-between gap-2 rounded border border-white/10 bg-white/5 p-2"
                      key={`${entry.Tracker}-${decision.Rule}`}
                    >
                      <span>
                        <strong>{decision.Rule}</strong> [{disposition}]
                        {decision.Reason ? ` — ${decision.Reason}` : ""}
                      </span>
                      {waivable ? (
                        <label className="flex items-center gap-2 text-xs font-semibold">
                          <span>Authorize</span>
                          <input
                            aria-label={`Authorize ${decision.Rule} for ${entry.Tracker}`}
                            type="checkbox"
                            checked={authorized}
                            onChange={(event) =>
                              facet.setRuleAuthorized(
                                entry.Tracker,
                                decision.Rule,
                                event.target.checked,
                              )
                            }
                          />
                        </label>
                      ) : null}
                    </div>
                  );
                })}
              </div>
            ) : null}
            {duplicate &&
            (duplicate.HasDupes || duplicate.Skipped || duplicate.Error || duplicate.Status) ? (
              <div className="grid gap-1">
                <h3 className="text-sm font-semibold">Duplicate diagnostics</h3>
                <p className="muted">
                  Status: {duplicate.Status || "unknown"} · Potential matches:{" "}
                  {duplicateMatches.length}
                </p>
                {duplicate.SkipReason ? <p className="muted">{duplicate.SkipReason}</p> : null}
                {duplicate.Error ? <p className="error">{duplicate.Error}</p> : null}
                {duplicateMatches.length ? (
                  <ul className="m-0 grid gap-1 pl-4">
                    {duplicateMatches.map((match, index) => (
                      <li key={`${match.ID || match.Name}-${index}`}>{match.Name || match.ID}</li>
                    ))}
                  </ul>
                ) : null}
              </div>
            ) : null}
            {liveReasons.length ? (
              <p className="muted">
                Live upload: {liveReasons.map((reason) => reason.Message).join(" ")}
              </p>
            ) : null}
            <dl className="grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-2">
              <div>
                <dt className="label">Release name</dt>
                <dd className="value break-all">
                  {entry.UploadReleaseName || entry.ReleaseName || "-"}
                </dd>
              </div>
              <div>
                <dt className="label">Description group</dt>
                <dd className="value">{entry.DescriptionGroup || "-"}</dd>
              </div>
              <div>
                <dt className="label">Files ready</dt>
                <dd className="value">
                  {files.filter((file) => file.Present).length}/{files.length}
                </dd>
              </div>
            </dl>
            {entry.Endpoint ? (
              <div className="grid gap-1">
                <h3 className="text-sm font-semibold">Endpoint</h3>
                <p className="value break-all">
                  {formatDryRunPayloadValue("endpoint", entry.Endpoint)}
                </p>
              </div>
            ) : null}
            {entry.DebugSections?.length ? (
              <div className="grid gap-2">
                <h3 className="text-sm font-semibold">Debug sections</h3>
                {entry.DebugSections.map((section, sectionIndex) => (
                  <div
                    className="grid gap-2 rounded border border-white/10 bg-white/5 p-2"
                    key={`${section.Title || "debug-section"}-${sectionIndex}`}
                  >
                    <h4 className="label">{section.Title || "Debug section"}</h4>
                    {section.Endpoint ? (
                      <p className="value break-all">
                        {formatDryRunPayloadValue("endpoint", section.Endpoint)}
                      </p>
                    ) : null}
                    {(section.Files || []).map((file, fileIndex) => (
                      <div key={dryRunFileKey(file, fileIndex)}>
                        <p className="label">
                          File · {file.Field} · {file.Present ? "present" : "missing"}
                        </p>
                        <p className="value break-all">{formatDryRunFilePath(file.Path)}</p>
                      </div>
                    ))}
                    {Object.entries(section.Payload || {})
                      .sort(([left], [right]) => left.localeCompare(right))
                      .map(([key, value]) => (
                        <div key={key}>
                          <p className="label">{key}</p>
                          <p className="value break-all">{formatDryRunPayloadValue(key, value)}</p>
                        </div>
                      ))}
                  </div>
                ))}
              </div>
            ) : null}
            {files.length ? (
              <div className="grid gap-2">
                <h3 className="text-sm font-semibold">Files</h3>
                {files.map((file, fileIndex) => (
                  <div
                    className="rounded border border-white/10 bg-white/5 p-2"
                    key={dryRunFileKey(file, fileIndex)}
                  >
                    <p className="label">
                      {file.Field} · {file.Present ? "present" : "missing"}
                    </p>
                    <p className="value break-all">{formatDryRunFilePath(file.Path)}</p>
                  </div>
                ))}
              </div>
            ) : null}
            {Object.keys(entry.Payload || {}).length ? (
              <div className="grid gap-2">
                <h3 className="text-sm font-semibold">Payload</h3>
                {Object.entries(entry.Payload || {})
                  .sort(([left], [right]) => left.localeCompare(right))
                  .map(([key, value]) => (
                    <div className="rounded border border-white/10 bg-white/5 p-2" key={key}>
                      <p className="label">{key}</p>
                      <p className="value break-all">{formatDryRunPayloadValue(key, value)}</p>
                    </div>
                  ))}
              </div>
            ) : null}
            {questionnaireFields.map((field) => (
              <label className="grid gap-1" key={field.Key}>
                <span className="label">
                  {field.Label || field.Key}
                  {field.Required ? " *" : ""}
                </span>
                {field.Kind === "select" ? (
                  <select
                    value={
                      view.questionnaireAnswers[entry.Tracker]?.[field.Key] ?? field.Value ?? ""
                    }
                    onChange={(event) =>
                      facet.answerQuestionnaire(entry.Tracker, field.Key, event.target.value)
                    }
                  >
                    <option value="">Select</option>
                    {(field.Options || []).map((option) => (
                      <option key={option} value={option}>
                        {option}
                      </option>
                    ))}
                  </select>
                ) : (
                  <input
                    value={
                      view.questionnaireAnswers[entry.Tracker]?.[field.Key] ?? field.Value ?? ""
                    }
                    placeholder={field.Placeholder}
                    onChange={(event) =>
                      facet.answerQuestionnaire(entry.Tracker, field.Key, event.target.value)
                    }
                  />
                )}
                {field.Help ? <span className="muted text-xs">{field.Help}</span> : null}
              </label>
            ))}
          </section>
        );
      })}

      {view.snapshot ? (
        <section className="panel grid gap-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2>Upload job</h2>
            <span className="muted">{view.snapshot.status.replaceAll("_", " ")}</span>
          </div>
          {view.snapshot.currentMessage ? <p>{view.snapshot.currentMessage}</p> : null}
          <p className="muted">
            Uploaded {view.snapshot.uploadedCount} · Failed {failedTrackers.length}
          </p>
          {snapshotTrackers.map((tracker) => (
            <div
              className="flex items-center justify-between gap-2 rounded border border-white/10 bg-white/5 p-2"
              key={tracker.tracker}
            >
              <span>{tracker.tracker}</span>
              <span className="muted">{tracker.status}</span>
            </div>
          ))}
        </section>
      ) : null}
    </section>
  );
}
