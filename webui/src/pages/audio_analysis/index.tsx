// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { pageStyle } from "../../components/ui/pageStyle";
import { useMemo, useState } from "react";
import { Select } from "../../components/ui/select";
import type { AudioAnalysisFacet, AudioAnalysisGenerateInput } from "../../releaseSession/types";

type Props = Readonly<{
  facet: AudioAnalysisFacet;
  setLightboxImage: (value: string) => void;
  setLightboxAlt: (value: string) => void;
}>;

const trackLabel = (ordinal: number, title: string) =>
  title.trim() ? `Track ${ordinal}: ${title.trim()}` : `Track ${ordinal}`;

/** Presents opt-in prepared-track selection and retained local analysis outputs. */
export default function AudioAnalysisPage({ facet, setLightboxImage, setLightboxAlt }: Props) {
  const { view } = facet;
  const resourceIDs = useMemo(
    () => Array.from(new Set(view.tracks.map((track) => track.ResourceID).filter(Boolean))),
    [view.tracks],
  );
  const primaryResourceID =
    view.tracks.find((track) => track.ID === view.primaryTrackID)?.ResourceID ||
    resourceIDs[0] ||
    "";
  const [resourceID, setResourceID] = useState(primaryResourceID);
  const [selection, setSelection] = useState<AudioAnalysisGenerateInput["selection"]>("primary");
  const [selectedTrackIDs, setSelectedTrackIDs] = useState<readonly string[]>([]);
  const [variants, setVariants] = useState<readonly string[]>(["waveform", "spectrogram", "stats"]);
  const [decoderThreads, setDecoderThreads] = useState(2);

  const effectiveResourceID = resourceIDs.includes(resourceID) ? resourceID : primaryResourceID;
  const tracks = view.tracks.filter((track) => track.ResourceID === effectiveResourceID);
  const primaryTrack = tracks.find((track) => track.ID === view.primaryTrackID);
  const selected = new Set(selectedTrackIDs);
  const requestedTrackIDs =
    selection === "primary"
      ? primaryTrack
        ? [primaryTrack.ID]
        : []
      : selection === "all"
        ? tracks.map((track) => track.ID)
        : tracks.filter((track) => selected.has(track.ID)).map((track) => track.ID);
  const busy = view.status === "running";
  const mutationsBlocked = busy || Boolean(view.mutationBlockedReason);
  const canGenerate =
    view.available &&
    !mutationsBlocked &&
    Boolean(effectiveResourceID) &&
    requestedTrackIDs.length > 0 &&
    variants.length > 0;

  const toggleSelectedTrack = (trackID: string, checked: boolean) => {
    setSelectedTrackIDs((current) =>
      checked
        ? Array.from(new Set([...current, trackID]))
        : current.filter((candidate) => candidate !== trackID),
    );
  };
  const toggleVariant = (variant: string, checked: boolean) => {
    setVariants((current) =>
      checked
        ? Array.from(new Set([...current, variant]))
        : current.filter((candidate) => candidate !== variant),
    );
  };
  const generate = () =>
    facet.generate({
      resourceID: effectiveResourceID,
      selection,
      trackIDs: requestedTrackIDs,
      variants,
      resourceLimits: { decoderThreads },
    });

  return (
    <section className="grid gap-4">
      <header>
        <p className={pageStyle.eyebrow}>Audio Analysis</p>
        <h1>Waveforms, Spectrograms &amp; Statistics</h1>
        <p className={pageStyle.subtitle}>
          Generate waveform, spectrogram, and amplitude statistics outputs for selected prepared
          audio tracks. Nothing runs until you choose Generate.
        </p>
      </header>

      <section className={`${pageStyle.panel} grid gap-3`} aria-labelledby="audio-analysis-source">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <h2 id="audio-analysis-source" className="break-words">
              {view.sourceLabel}
            </h2>
            <p className="text-muted-foreground">
              Generation {view.releaseGeneration}
              {view.sourceContext ? ` · ${view.sourceContext}` : ""}
            </p>
          </div>
          <span className="text-muted-foreground">{view.enabled ? "Enabled" : "Disabled"}</span>
        </div>

        {resourceIDs.length > 1 ? (
          <label className="grid gap-1">
            <span>Prepared resource</span>
            <Select
              value={effectiveResourceID}
              disabled={mutationsBlocked}
              onChange={(event) => {
                setResourceID(event.target.value);
                setSelectedTrackIDs([]);
              }}
            >
              {resourceIDs.map((id, index) => (
                <option key={id} value={id}>
                  Resource {index + 1}
                </option>
              ))}
            </Select>
          </label>
        ) : null}

        <fieldset className="grid gap-2" disabled={mutationsBlocked}>
          <legend>Tracks</legend>
          <div className="flex flex-wrap items-center gap-4">
            <label className="inline-flex min-h-9 cursor-pointer items-center gap-2">
              <input
                type="radio"
                name="audio-analysis-selection"
                value="primary"
                checked={selection === "primary"}
                disabled={!primaryTrack}
                onChange={() => setSelection("primary")}
              />{" "}
              Primary
            </label>
            <label className="inline-flex min-h-9 cursor-pointer items-center gap-2">
              <input
                type="radio"
                name="audio-analysis-selection"
                value="all"
                checked={selection === "all"}
                onChange={() => setSelection("all")}
              />{" "}
              All
            </label>
            <label className="inline-flex min-h-9 cursor-pointer items-center gap-2">
              <input
                type="radio"
                name="audio-analysis-selection"
                value="selected"
                checked={selection === "selected"}
                onChange={() => setSelection("selected")}
              />{" "}
              Selected
            </label>
            <label className="flex min-h-9 basis-full items-center gap-2 whitespace-nowrap sm:basis-auto">
              <span>Threads per decoder</span>
              <Select
                className="max-w-20 min-w-20"
                value={decoderThreads}
                onChange={(event) => setDecoderThreads(Number(event.target.value))}
              >
                {[1, 2, 4, 8, 16].map((value) => (
                  <option key={value} value={value}>
                    {value}
                  </option>
                ))}
              </Select>
            </label>
          </div>
          {selection === "selected" && requestedTrackIDs.length === 0 ? (
            <p className={pageStyle.error} role="alert">
              Select at least one audio track.
            </p>
          ) : null}
          <div className="grid gap-2 sm:grid-cols-2">
            {tracks.map((track) => (
              <label key={track.ID} className={`${pageStyle.panel} min-w-0 p-3`}>
                {selection === "selected" ? (
                  <input
                    type="checkbox"
                    checked={selected.has(track.ID)}
                    onChange={(event) => toggleSelectedTrack(track.ID, event.target.checked)}
                  />
                ) : null}{" "}
                <strong className="break-words">{trackLabel(track.Ordinal, track.Title)}</strong>
                <span className="mt-1 block break-words text-sm text-muted-foreground">
                  {[
                    track.Codec,
                    track.ChannelLayout || `${track.Channels} channels`,
                    track.SampleRate ? `${track.SampleRate} Hz` : "",
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                </span>
                <span className="block text-sm text-muted-foreground">
                  {track.Languages.join(", ") || "Language unknown"}
                  {track.Default ? " · default" : ""}
                  {track.Commentary ? " · commentary" : ""}
                </span>
              </label>
            ))}
          </div>
        </fieldset>

        <fieldset className="grid gap-2" disabled={mutationsBlocked}>
          <legend>Outputs</legend>
          <div className="flex flex-wrap gap-4">
            {[
              ["waveform", "Waveform"],
              ["spectrogram", "Spectrogram"],
              ["stats", "Amplitude statistics"],
            ].map(([variant, label]) => (
              <label
                key={variant}
                className="inline-flex min-h-9 cursor-pointer items-center gap-2"
              >
                <input
                  type="checkbox"
                  checked={variants.includes(variant)}
                  onChange={(event) => toggleVariant(variant, event.target.checked)}
                />{" "}
                {label}
              </label>
            ))}
          </div>
          {variants.length === 0 ? (
            <p className={pageStyle.error} role="alert">
              Select at least one output type.
            </p>
          ) : null}
        </fieldset>

        <div className="flex flex-wrap gap-2">
          <button type="button" disabled={!canGenerate} onClick={() => void generate()}>
            {view.result ? "Generate again" : "Generate"}
          </button>
          {busy ? (
            <button type="button" className="secondary" onClick={() => void facet.cancel()}>
              Cancel
            </button>
          ) : null}
          {view.enabled ? (
            <button
              type="button"
              className="secondary"
              disabled={Boolean(view.mutationBlockedReason)}
              onClick={() => void facet.disable()}
            >
              Disable
            </button>
          ) : null}
          {view.result &&
          ["partial", "failed", "canceled", "interrupted"].includes(view.result.status) &&
          !mutationsBlocked ? (
            <button type="button" className="secondary" onClick={() => void facet.retry()}>
              Retry failed work
            </button>
          ) : null}
        </div>
        {busy ? (
          <div role="status" className="grid gap-2 text-muted-foreground">
            <p>
              Generating audio analysis… {view.completed}/{view.total || requestedTrackIDs.length}
            </p>
            {view.operationItems.length > 0 ? (
              <ul>
                {view.operationItems.map((item) => (
                  <li key={item.id}>
                    {item.label}: {item.status}
                    {(item.total ?? 0) > 0 &&
                    (item.status === "running" || item.status === "completed")
                      ? ` — ${Math.min(100, Math.round(((item.completed ?? 0) * 100) / (item.total ?? 1)))}%`
                      : ""}
                    {item.message ? ` — ${item.message}` : ""}
                  </li>
                ))}
              </ul>
            ) : null}
          </div>
        ) : null}
        {view.mutationBlockedReason ? (
          <p className="text-muted-foreground" role="status">
            {view.mutationBlockedReason}
          </p>
        ) : null}
        {view.error ? (
          <p className={pageStyle.error} role="alert">
            {view.error}
          </p>
        ) : null}
      </section>

      {view.result ? (
        <section className="grid gap-4" aria-labelledby="audio-analysis-results">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2 id="audio-analysis-results">Results</h2>
            <span className="text-muted-foreground">{view.result.status}</span>
          </div>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(min(100%,320px),1fr))] items-start gap-3">
            {view.result.tracks.map((track) => (
              <article key={track.trackId} className={`${pageStyle.panel} grid min-w-0 gap-3`}>
                <div>
                  <h3 className="break-words">{trackLabel(track.ordinal, track.title || "")}</h3>
                  <p className="text-muted-foreground">
                    {[
                      track.codec,
                      track.channelLayout || `${track.channels} channels`,
                      `${track.sampleRate} Hz`,
                      `${track.durationSeconds.toFixed(2)} s`,
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                  </p>
                </div>
                {track.failure ? (
                  <p className={pageStyle.error}>
                    {track.failure.code}: {track.failure.message}
                  </p>
                ) : null}
                <div className="grid grid-cols-[minmax(0,1fr)] gap-2.5 [&>section]:w-fit [&>section]:max-w-full">
                  {track.artifacts.map((artifact) => {
                    const url =
                      artifact.status === "completed" ? facet.artifactURL(artifact.id) : "";
                    const alt = `${trackLabel(track.ordinal, track.title || "")} ${artifact.variant}`;
                    return (
                      <section key={artifact.variant} className="grid min-w-0 gap-2">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <h4 className="capitalize">
                            {artifact.variant === "stats"
                              ? "Amplitude statistics"
                              : artifact.variant}
                          </h4>
                          {url ? (
                            <a href={url} download>
                              {artifact.variant === "stats"
                                ? "Download text file"
                                : "Download native PNG"}
                            </a>
                          ) : null}
                        </div>
                        {artifact.variant === "stats" ? (
                          artifact.status === "completed" && artifact.text !== undefined ? (
                            <pre
                              className={`${pageStyle.panel} max-h-40 max-w-full overflow-auto p-3 font-mono text-xs whitespace-pre`}
                              tabIndex={0}
                              role="region"
                              aria-label={`${trackLabel(track.ordinal, track.title || "")} amplitude statistics`}
                            >
                              {artifact.text}
                            </pre>
                          ) : artifact.failure ? (
                            <p className={`error ${pageStyle.error}`}>
                              {artifact.failure.code}: {artifact.failure.message}
                            </p>
                          ) : (
                            <p className="text-muted-foreground">
                              No retained statistics are available.
                            </p>
                          )
                        ) : url ? (
                          <button
                            type="button"
                            className="audio-analysis-thumbnail h-auto w-fit max-w-full justify-self-start cursor-zoom-in overflow-hidden rounded-[10px] border border-foreground/10 bg-card/60 p-0 [&>img]:block [&>img]:h-auto [&>img]:max-h-40 [&>img]:w-auto [&>img]:max-w-full"
                            aria-label={`Open ${alt} full size`}
                            onClick={() => {
                              setLightboxImage(url);
                              setLightboxAlt(alt);
                            }}
                          >
                            <img src={url} alt={alt} loading="lazy" />
                          </button>
                        ) : artifact.failure ? (
                          <p className={pageStyle.error}>
                            {artifact.failure.code}: {artifact.failure.message}
                          </p>
                        ) : (
                          <p className="text-muted-foreground">No retained image is available.</p>
                        )}
                      </section>
                    );
                  })}
                </div>
              </article>
            ))}
          </div>
        </section>
      ) : null}
    </section>
  );
}
