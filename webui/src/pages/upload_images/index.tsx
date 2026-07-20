// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useMemo, useRef } from "react";
import { Button } from "../../components/ui/button";
import type { UploadedImagesFacet } from "../../releaseSession/types";
import type { UploadedImageLink } from "../../types";
import { handleExternalLinkClick } from "../../utils/externalLinks";

type Props = Readonly<{
  facet: UploadedImagesFacet;
  configuredImageHosts: readonly string[];
  resolveImageHostLabel: (value: string) => string;
  setLightboxImage: (value: string) => void;
  setLightboxAlt: (value: string) => void;
}>;

const linkFor = (item: UploadedImageLink) => item.WebURL || item.ImgURL || item.RawURL;

/** Presents exact-generation image-host candidates, upload progress, and results. */
export default function UploadImagesPage({
  facet,
  configuredImageHosts,
  resolveImageHostLabel,
  setLightboxImage,
  setLightboxAlt,
}: Props) {
  const { view } = facet;
  const loadRef = useRef(facet.load);
  loadRef.current = facet.load;

  useEffect(() => {
    if (view.status === "idle" && view.staleReason) void loadRef.current();
  }, [view.staleReason, view.status]);

  const selected = useMemo(() => new Set(view.selectedPaths), [view.selectedPaths]);
  const selectedCount = view.candidates.filter((item) => selected.has(item.image.Path)).length;
  const busy = view.status === "running";
  const attempts = view.progress.attempts;
  const progressTotal = attempts.reduce((total, attempt) => total + attempt.total, 0);
  const progressCurrent = attempts.reduce(
    (total, attempt) => total + Math.min(attempt.completed, attempt.total),
    0,
  );
  const uploading = busy && Boolean(view.progress.correlationID);
  const progressPercent =
    progressTotal > 0 ? Math.round((progressCurrent / progressTotal) * 100) : 0;

  return (
    <section className="flex flex-col gap-4">
      <header className="max-w-3xl">
        <p className="eyebrow">Image Hosting</p>
        <h1>Upload Images</h1>
        <p className="subtitle">Choose final images and publish them to a configured host.</p>
      </header>

      <section className="panel grid gap-3">
        <div className="flex flex-wrap items-end gap-3">
          <label className="grid min-w-52 gap-1">
            <span className="label">Image host</span>
            <select value={view.host} onChange={(event) => facet.chooseHost(event.target.value)}>
              <option value="">Select image host</option>
              {configuredImageHosts.map((host) => (
                <option key={host} value={host}>
                  {resolveImageHostLabel(host)}
                </option>
              ))}
            </select>
          </label>
          <Button
            type="button"
            variant="primary"
            disabled={busy || !view.host || selectedCount === 0}
            onClick={() => void facet.upload()}
          >
            {uploading
              ? "Uploading images..."
              : busy
                ? "Working..."
                : `Upload selected (${selectedCount})`}
          </Button>
          <button
            className="ghost"
            type="button"
            disabled={busy}
            onClick={() => facet.selectAll(true)}
          >
            Select all
          </button>
          <button
            className="ghost"
            type="button"
            disabled={busy}
            onClick={() => facet.selectAll(false)}
          >
            Clear
          </button>
        </div>
        {uploading ? (
          <div className="grid gap-2" aria-live="polite">
            {progressTotal > 0 ? (
              <div
                aria-label="Image host upload progress"
                aria-valuemax={progressTotal}
                aria-valuemin={0}
                aria-valuenow={progressCurrent}
                className="h-4 w-full overflow-hidden rounded-full border border-white/10 bg-white/10"
                role="progressbar"
              >
                <div
                  className="h-full rounded-full bg-[var(--accent-2)] transition-[width]"
                  style={{ width: `${progressPercent}%` }}
                />
              </div>
            ) : null}
            <p className="m-0 text-center text-sm text-[var(--muted)]">
              {progressTotal > 0
                ? `${progressCurrent} of ${progressTotal} image-host uploads processed across ${attempts.length} ${attempts.length === 1 ? "host" : "hosts"}.`
                : "Resolving required image hosts..."}
            </p>
            {attempts.length ? (
              <div className="grid gap-2">
                {attempts.map((attempt) => {
                  const trackerDetail =
                    attempt.trackers.length === 1
                      ? attempt.trackers[0]
                      : `${attempt.trackers.length} trackers`;
                  const resultDetail =
                    attempt.status === "completed"
                      ? `${attempt.total} ready`
                      : attempt.status === "failed"
                        ? `${attempt.succeeded + attempt.reused} ready, ${attempt.failed} failed`
                        : `${attempt.completed}/${attempt.total} processed`;
                  return (
                    <div
                      className="flex flex-wrap items-center justify-between gap-2 rounded border border-white/10 bg-white/5 px-3 py-2 text-sm"
                      key={attempt.attemptID}
                    >
                      <div>
                        <span className="font-semibold">{resolveImageHostLabel(attempt.host)}</span>
                        <span className="muted">
                          {" · "}
                          {trackerDetail || attempt.usageScope}
                          {attempt.fallback ? " · fallback" : ""}
                        </span>
                      </div>
                      <span className={attempt.status === "failed" ? "error" : "muted"}>
                        {resultDetail}
                      </span>
                    </div>
                  );
                })}
              </div>
            ) : null}
          </div>
        ) : null}
        {view.error ? (
          <p className="error" role="alert">
            {view.error}
          </p>
        ) : null}
        {view.failures.map((failure) => (
          <p className="error" key={`${failure.Host}-${failure.UsageScope}`}>
            {failure.Host || "Image host"}: {failure.Message}
          </p>
        ))}
      </section>

      <section className="panel grid gap-3">
        <div className="flex items-baseline justify-between gap-3">
          <h2>Available images</h2>
          <span className="muted">{view.candidates.length} found</span>
        </div>
        {view.candidates.length ? (
          <div className="grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-3">
            {view.candidates.map((item, index) => {
              const imagePath = item.image.Path;
              const checked = selected.has(imagePath);
              return (
                <article className="grid gap-2" key={imagePath}>
                  <button
                    className="screens-thumb"
                    type="button"
                    aria-label={`Preview image ${index + 1}`}
                    onClick={() => {
                      setLightboxImage(item.dataURI);
                      setLightboxAlt(`Upload image ${index + 1}`);
                    }}
                  >
                    <img src={item.dataURI} alt="" />
                  </button>
                  <label className="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={(event) => facet.select(imagePath, event.target.checked)}
                    />
                    Include
                  </label>
                </article>
              );
            })}
          </div>
        ) : (
          <p className="muted">No screenshot candidates available.</p>
        )}
      </section>

      <section className="panel grid gap-3">
        <div className="flex items-baseline justify-between gap-3">
          <h2>Published images</h2>
          <span className="muted">{view.uploaded.length} saved</span>
        </div>
        {view.uploaded.length ? (
          <div className="grid gap-2">
            {view.uploaded.map((item) => {
              const url = linkFor(item);
              return (
                <article
                  className="flex flex-wrap items-center justify-between gap-2 rounded border border-white/10 bg-white/5 p-2"
                  key={`${item.Host}-${item.ImagePath}`}
                >
                  <div className="min-w-0">
                    <p className="font-semibold">{resolveImageHostLabel(item.Host)}</p>
                    {url ? (
                      <a
                        className="tracker-link break-all"
                        href={url}
                        target="_blank"
                        rel="noreferrer"
                        onAuxClick={handleExternalLinkClick}
                        onClick={handleExternalLinkClick}
                      >
                        {url}
                      </a>
                    ) : (
                      <p className="muted break-all">{item.ImagePath}</p>
                    )}
                  </div>
                  <button
                    className="danger"
                    type="button"
                    disabled={busy}
                    onClick={() => void facet.remove(item.ImagePath, item.Host)}
                  >
                    Remove
                  </button>
                </article>
              );
            })}
          </div>
        ) : (
          <p className="muted">No uploaded images yet.</p>
        )}
      </section>
    </section>
  );
}
