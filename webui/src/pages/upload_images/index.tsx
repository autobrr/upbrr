// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useMemo, useRef } from "react";
import { Button } from "../../components/ui/button";
import type { UploadedImagesFacet } from "../../releaseSession/types";
import { handleExternalLinkClick } from "../../utils/externalLinks";

type Props = Readonly<{
  facet: UploadedImagesFacet;
  resolveImageHostLabel: (value: string) => string;
  setLightboxImage: (value: string) => void;
  setLightboxAlt: (value: string) => void;
}>;

/** Presents exact-generation image-host candidates, upload progress, and results. */
export default function UploadImagesPage({
  facet,
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

  const selected = useMemo(() => new Set(view.selectedArtifactIDs), [view.selectedArtifactIDs]);
  const selectedCount = view.candidates.filter((item) =>
    selected.has(item.image.artifactID),
  ).length;
  const busy = view.status === "running";
  const attempts = view.progress.attempts;
  const progressTotal = attempts.reduce((total, attempt) => total + attempt.total, 0);
  const progressCurrent = attempts.reduce(
    (total, attempt) => total + Math.min(attempt.completed, attempt.total),
    0,
  );
  const uploading = busy && Boolean(view.progress.correlationID);

  return (
    <section className="flex flex-col gap-4">
      <header className="max-w-3xl">
        <p className="eyebrow">Image Hosting</p>
        <h1>Upload Images</h1>
        <p className="subtitle">
          Choose final images. Required hosts and fallbacks are derived from eligible trackers.
        </p>
      </header>

      <section className="panel grid gap-3">
        <div className="flex flex-wrap items-end gap-3">
          <Button
            type="button"
            variant="primary"
            disabled={busy || selectedCount === 0}
            onClick={() => void facet.upload()}
          >
            {uploading
              ? "Uploading images..."
              : busy
                ? "Working..."
                : `Prepare required hosts (${selectedCount})`}
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
              const artifactID = item.image.artifactID;
              const checked = selected.has(artifactID);
              return (
                <article className="grid gap-2" key={artifactID}>
                  <button
                    className="screens-thumb"
                    type="button"
                    aria-label={`Preview image ${index + 1}`}
                    onClick={() => {
                      setLightboxImage(item.contentURL);
                      setLightboxAlt(`Upload image ${index + 1}`);
                    }}
                  >
                    <img src={item.contentURL} alt="" />
                  </button>
                  <label className="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={(event) => facet.select(artifactID, event.target.checked)}
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
              const url = item.url;
              return (
                <article
                  className="flex flex-wrap items-center justify-between gap-2 rounded border border-white/10 bg-white/5 p-2"
                  key={`${item.host}-${item.artifactID}`}
                >
                  <div className="min-w-0">
                    <p className="font-semibold">{resolveImageHostLabel(item.host)}</p>
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
                      <p className="muted break-all">Hosted image unavailable</p>
                    )}
                  </div>
                  <button
                    className="danger"
                    type="button"
                    disabled={busy}
                    onClick={() => void facet.remove(item.artifactID, item.host)}
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
