// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useRef, useState } from "react";
import { pageStyle } from "../../components/ui/pageStyle";
import type { HDRAnalysisFacet } from "../../releaseSession/types";

type Props = Readonly<{
  facet: HDRAnalysisFacet;
  setLightboxImage: (value: string) => void;
  setLightboxAlt: (value: string) => void;
}>;

/** Renders backend-owned selection, retained outputs and workflow intents. */
export default function HDRAnalysisPage({ facet, setLightboxImage, setLightboxAlt }: Props) {
  const { view } = facet;
  const supportedTargets = view.targets.filter((target) => target.supported);
  const [selected, setSelected] = useState<readonly string[]>(
    view.result?.targetIds || (supportedTargets.length === 1 ? [supportedTargets[0]!.id] : []),
  );
  const [peakSource, setPeakSource] = useState(view.result?.peakSource || "histogram");
  const targetIDs = view.targets
    .filter((target) => target.supported && selected.includes(target.id))
    .map((target) => target.id);
  const busy = view.status === "running";
  const generateButton = useRef<HTMLButtonElement>(null);
  const wasBusy = useRef(busy);
  useEffect(() => {
    if (wasBusy.current && !busy) generateButton.current?.focus();
    wasBusy.current = busy;
  }, [busy]);
  const blocked = busy || Boolean(view.mutationBlockedReason);
  const complete = view.result?.status === "completed";
  const button = "rounded border border-border px-3 py-2 disabled:opacity-50";

  return (
    <section className="grid gap-4">
      <header>
        <p className={pageStyle.eyebrow}>HDR Analysis</p>
        <h1>HDR10+ Brightness</h1>
        <p className={pageStyle.subtitle}>
          Plot dynamic HDR10+ metadata across every presentation frame. Static HDR, HLG and Dolby
          Vision alone do not provide HDR10+ measurements.
        </p>
      </header>
      <section className={`${pageStyle.panel} grid gap-3`} aria-labelledby="hdr-selection">
        <h2 id="hdr-selection">Prepared targets</h2>
        <p className={pageStyle.subtitle}>
          MKV tracks require MediaInfo-confirmed HDR10+. For Blu-ray, enable the HDR10+ check in
          playlist selection first. Changing the peak estimator reuses complete retained metadata.
        </p>
        {view.targets.length === 0 && (
          <p>No supported MKV or Blu-ray target is available. Prepare a supported source first.</p>
        )}
        <fieldset disabled={blocked} className="grid gap-2">
          <legend className="sr-only">Select HDR targets in source order</legend>
          {view.targets.map((target) => (
            <label key={target.id} className="flex items-start gap-2">
              <input
                type="checkbox"
                checked={selected.includes(target.id)}
                disabled={!target.supported}
                onChange={(event) =>
                  setSelected((current) =>
                    event.target.checked
                      ? [...current, target.id]
                      : current.filter((id) => id !== target.id),
                  )
                }
              />
              <span>
                {target.label}
                <span className="block text-sm text-muted-foreground">
                  {target.selectionPolicy === "unique_hevc"
                    ? "MediaInfo-confirmed HDR10+ · unique HEVC video track"
                    : "Primary HEVC · angle 0 · selected playlist timeline"}
                  {target.reason ? ` · ${target.reason}` : ""}
                </span>
              </span>
            </label>
          ))}
          <label className="grid gap-1" htmlFor="hdr-peak">
            Peak estimator
            <select
              id="hdr-peak"
              className="rounded border border-border bg-card p-2"
              value={peakSource}
              onChange={(event) => setPeakSource(event.target.value)}
            >
              <option value="histogram">Histogram (default)</option>
              <option value="histogram99">Histogram 99th percentile</option>
              <option value="max-scl">MaxSCL</option>
              <option value="max-scl-luminance">MaxSCL luminance</option>
            </select>
          </label>
        </fieldset>
        <div className="flex flex-wrap gap-2">
          <button
            ref={generateButton}
            className={button}
            disabled={blocked || !view.available || targetIDs.length === 0}
            onClick={() => void facet.generate({ targetIDs, peakSource })}
          >
            Generate
          </button>
          <button
            className={button}
            disabled={
              blocked ||
              !view.result ||
              view.result.targetIds.some(
                (id) => !view.targets.some((target) => target.id === id && target.supported),
              )
            }
            onClick={() => void facet.retry()}
          >
            Retry
          </button>
          <button className={button} disabled={!busy} onClick={() => void facet.cancel()}>
            Cancel
          </button>
        </div>
        <p>Completed HDR plots are automatically included in generated descriptions.</p>
        {view.result && !complete && (
          <p>
            Every selected target must complete before inclusion. Retry or generate with fewer
            targets.
          </p>
        )}
        <p role="status" aria-live="polite">
          {busy
            ? `${view.message || view.phase.replaceAll("_", " ") || "Inspecting HDR source…"} (${Math.round(view.progress)}%)`
            : view.mutationBlockedReason}
        </p>
        {view.error && (
          <p role="alert" className={pageStyle.error}>
            {view.error}
          </p>
        )}
      </section>
      {view.result?.targets.map((target) => (
        <section key={target.targetId} className={`${pageStyle.panel} grid gap-3`}>
          <h2>{target.label}</h2>
          {target.artifact ? (
            <>
              <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1">
                <dt>Frames</dt>
                <dd>{target.frames?.toLocaleString()}</dd>
                <dt>Scenes</dt>
                <dd>{target.scenes?.toLocaleString()}</dd>
                <dt>Profile</dt>
                <dd>{target.profile}</dd>
                <dt>Peak estimator</dt>
                <dd>{view.result?.peakSource}</dd>
              </dl>
              <button
                aria-label={`Preview ${target.label} HDR10+ plot`}
                onClick={() => {
                  setLightboxAlt(`${target.label} HDR10+ plot`);
                  setLightboxImage(facet.artifactURL(target.artifact!.id));
                }}
              >
                <img
                  src={facet.artifactURL(target.artifact.id)}
                  alt={`${target.label} HDR10+ brightness plot`}
                  width={target.artifact.width}
                  height={target.artifact.height}
                  className="h-auto max-w-full rounded"
                />
              </button>
              <a href={facet.artifactURL(target.artifact.id)} download="hdr10plus.png">
                Download PNG
              </a>
            </>
          ) : (
            <p>{target.failure?.message || target.status}</p>
          )}
        </section>
      ))}
    </section>
  );
}
