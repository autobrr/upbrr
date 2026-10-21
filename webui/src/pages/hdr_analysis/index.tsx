// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useRef, useState } from "react";
import type {
  HDRAnalysisTargetResult,
  HDRTypedArtifact,
} from "../../api/generated/release-workflow";
import { pageStyle } from "../../components/ui/pageStyle";
import { Select } from "../../components/ui/select";
import type { HDRAnalysisFacet } from "../../releaseSession/types";

type Props = Readonly<{
  facet: HDRAnalysisFacet;
  setLightboxImage: (value: string) => void;
  setLightboxAlt: (value: string) => void;
}>;

const formatLabel = (format?: string) => (format === "dolby_vision" ? "Dolby Vision" : "HDR10+");
const decodedLabel = (mode: string) =>
  ({
    automatic: "Automatic HDR picture checks",
    audio: "Audio analysis",
    boundary_sync: "Bounded scene synchronization",
    scene_sync: "Scene synchronization",
    active_area: "Active area",
    grade: "Brightness grade",
    colour: "Colour comparison",
  })[mode] || mode;
const artifactLabel = (kind: string, format?: string) =>
  ({
    picture_brightness_plot: "Measured picture brightness",
    reference_brightness_plot: "Measured HDR10 reference brightness",
    dolby_vision_overlay: "Dolby Vision L1 over measured picture",
    hdr10plus_overlay: "HDR10+ over measured picture",
    dolby_vision_difference: "Dolby Vision metadata difference",
    hdr10plus_difference: "HDR10+ metadata difference",
    dolby_vision_scene_sheet: "Dolby Vision scene synchronization",
    hdr10plus_scene_sheet: "HDR10+ scene synchronization",
    automatic_active_area: "L5 and picture borders",
    automatic_grade_difference: "Picture grade difference",
    automatic_colour_bias: "Colour bias",
    chromaticity_plot: "Chromaticity",
    audio_waveform: "Audio waveform",
    audio_spectrogram: "Audio spectrogram",
    audio_stats: "Audio statistics",
    scene_contact_sheet: "Scene synchronization",
    boundary_dolby_vision_scene_sheet: "Dolby Vision bounded scene synchronization",
    boundary_hdr10plus_scene_sheet: "HDR10+ bounded scene synchronization",
    active_area_contact_sheet: "Active area",
    grade_plot: "Brightness grade",
    colour_plot: "Colour comparison",
  })[kind] || (format ? formatLabel(format) : kind.replaceAll("_", " "));
const artifactTrackLabel = (target: HDRAnalysisTargetResult, artifact: HDRTypedArtifact) => {
  const track = target.decoded
    ?.flatMap((result) => result.audioTracks || [])
    .find((item) => item.trackId === artifact.audioTrackId);
  return `${artifactLabel(artifact.kind, artifact.format)}${track ? ` · Track ${track.ordinal} · ${track.language || "und"} · ${track.codec} · ${track.channelLayout}` : ""}`;
};
const retryable = new Set(["failed", "canceled", "interrupted", "unsupported", "inconclusive"]);

/** Renders backend-owned selection, retained outputs and workflow intents. */
export default function HDRAnalysisPage({ facet, setLightboxImage, setLightboxAlt }: Props) {
  const { view } = facet;
  const refreshRequirements = useRef(facet.refreshRequirements);
  refreshRequirements.current = facet.refreshRequirements;
  useEffect(() => {
    if (view.requirementsNeedRefresh) void refreshRequirements.current();
  }, [view.requirementsNeedRefresh]);
  const request = view.pendingRequest || view.result;
  const supportedTargets = view.targets.filter((target) => target.supported);
  const [selected, setSelected] = useState<readonly string[]>(
    request?.targetIds || (supportedTargets.length === 1 ? [supportedTargets[0]!.id] : []),
  );
  const [peakSource, setPeakSource] = useState(request?.peakSource || "histogram");
  const [formats, setFormats] = useState(request?.formats || "auto");
  const [analysisMode, setAnalysisMode] = useState(
    request
      ? request.decoded?.boundarySync
        ? "boundary"
        : request.decoded?.automatic
          ? "pictures_audio"
          : request.decoded?.audio
            ? "metadata_audio"
            : "manual"
      : "metadata_audio",
  );
  const automatic = analysisMode === "pictures_audio";
  const audioOnly = analysisMode === "metadata_audio";
  const boundary = analysisMode === "boundary";
  const [boundaryAudio, setBoundaryAudio] = useState(
    request?.decoded?.boundarySync ? Boolean(request.decoded.audio) : true,
  );
  const [allAudioTracks, setAllAudioTracks] = useState(request?.decoded?.allAudioTracks || false);
  const [decoderThreads, setDecoderThreads] = useState(request?.decoded?.videoDecoderThreads || 0);
  const audioEnabled = automatic || audioOnly || (boundary && boundaryAudio);
  const [fullFrames, setFullFrames] = useState(
    request ? Boolean(request.decoded?.automatic && !request.decoded?.sampled) : false,
  );
  const [sceneSync, setSceneSync] = useState(request?.decoded?.sceneSync || false);
  const [activeArea, setActiveArea] = useState(request?.decoded?.activeArea || false);
  const [grade, setGrade] = useState(request?.decoded?.comparison?.grade || false);
  const [colour, setColour] = useState(request?.decoded?.comparison?.colour || false);
  const [comparisonPath, setComparisonPath] = useState("");
  const [offset, setOffset] = useState(request?.decoded?.comparison?.frameOffset?.toString() || "");
  const priorCrop = request?.decoded?.comparison?.crop;
  const [crop, setCrop] = useState(
    priorCrop ? `${priorCrop.left},${priorCrop.top},${priorCrop.width},${priorCrop.height}` : "",
  );
  const cropParts = crop.split(",").map((part) => Number(part.trim()));
  const validComparison =
    Boolean(view.comparison) &&
    /^-?\d+$/.test(offset) &&
    Math.abs(Number(offset)) <= 500000 &&
    cropParts.length === 4 &&
    cropParts.every(Number.isSafeInteger) &&
    cropParts[0]! >= 0 &&
    cropParts[1]! >= 0 &&
    cropParts[2]! >= 2 &&
    cropParts[3]! >= 2 &&
    cropParts[2]! % 2 === 0 &&
    cropParts[3]! % 2 === 0 &&
    cropParts[0]! + cropParts[2]! <= 8192 &&
    cropParts[1]! + cropParts[3]! <= 4320;
  const pictureEnabled =
    !audioOnly && (automatic || boundary || sceneSync || activeArea || grade || colour);
  const decoded =
    automatic || audioOnly || boundary || sceneSync || activeArea || grade || colour
      ? {
          automatic,
          ...(boundary ? { boundarySync: true } : {}),
          ...(audioOnly || (boundary && boundaryAudio) ? { audio: true } : {}),
          ...(audioEnabled && allAudioTracks ? { allAudioTracks: true } : {}),
          ...(pictureEnabled && decoderThreads ? { videoDecoderThreads: decoderThreads } : {}),
          ...(automatic ? { sampled: !fullFrames } : {}),
          sceneSync: !automatic && !audioOnly && !boundary && sceneSync,
          activeArea: !automatic && !audioOnly && activeArea,
          ...(!audioOnly && !boundary && (grade || colour)
            ? {
                comparison: {
                  sourceId: view.comparison?.id || "",
                  frameOffset: Number(offset),
                  crop: {
                    left: cropParts[0]!,
                    top: cropParts[1]!,
                    width: cropParts[2]!,
                    height: cropParts[3]!,
                  },
                  grade,
                  colour,
                },
              }
            : {}),
        }
      : undefined;
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
        <h1>HDR metadata analysis</h1>
        <p className={pageStyle.subtitle}>
          Inspect HDR10+ and Dolby Vision independently from one shared metadata scan. Metadata
          evidence is separate from optional decoded picture checks.
        </p>
      </header>
      {Boolean(view.gradeComparisonTrackers?.length) && (
        <section className={`${pageStyle.panel} grid gap-3`} aria-labelledby="hdr-grade-required">
          <h2 id="hdr-grade-required">Required hybrid HDR evidence</h2>
          <p>
            {view.gradeComparisonTrackers?.join(", ")} requires a brightness grade comparison and an
            HDR10+ plot for this hybrid release. If already generated, select the resulting images
            for descriptions. Otherwise choose “Metadata only / individual picture checks” and
            enable “Brightness grade comparison”. Use “HDR10+ only” under “Metadata formats” for the
            required plot.
          </p>
          <p>
            Select an HDR10-compatible comparison source with matching content and cadence. Supply
            “Confirmed frame offset (B − A)” and “Common crop: left,top,width,height” with even
            width and height, then generate the checks.
          </p>
          <p>
            Review the comparison against this source. Generating a plot does not confirm grade
            compatibility. If the source cannot produce the required evidence, add it manually to
            the description and answer the tracker’s evidence question.
          </p>
          {(!view.available || supportedTargets.length === 0) && (
            <p>Prepare a supported HDR source before running this check.</p>
          )}
        </section>
      )}
      <section className={`${pageStyle.panel} grid gap-3`} aria-labelledby="hdr-selection">
        <h2 id="hdr-selection">Prepared targets</h2>
        <p className={pageStyle.subtitle}>
          File sources require a unique HEVC video track. For Blu-ray, enable the HDR metadata check
          in playlist selection first. Changing the peak estimator reuses complete retained
          metadata.
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
                    ? "Unique HEVC video track"
                    : "Primary HEVC · angle 0 · selected playlist timeline"}
                  {target.reason ? ` · ${target.reason}` : ""}
                </span>
              </span>
            </label>
          ))}
          <div className="grid min-w-0 gap-3 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1.5fr)]">
            <label className="grid min-w-0 gap-1.5 text-sm font-medium" htmlFor="hdr-peak">
              Peak estimator
              <Select
                id="hdr-peak"
                value={peakSource}
                onChange={(event) => setPeakSource(event.target.value)}
              >
                <option value="histogram">Histogram (default)</option>
                <option value="histogram99">Histogram 99th percentile</option>
                <option value="max-scl">MaxSCL</option>
                <option value="max-scl-luminance">MaxSCL luminance</option>
              </Select>
            </label>
            <label className="grid min-w-0 gap-1.5 text-sm font-medium" htmlFor="hdr-formats">
              Metadata formats
              <Select
                id="hdr-formats"
                value={formats}
                onChange={(event) => setFormats(event.target.value)}
              >
                <option value="auto">Automatic discovery</option>
                <option value="hdr10plus">HDR10+ only</option>
                <option value="dolby-vision">Dolby Vision only</option>
                <option value="both">Require both formats</option>
              </Select>
            </label>
            <label className="grid min-w-0 gap-1.5 text-sm font-medium" htmlFor="hdr-mode">
              Analysis mode
              <Select
                id="hdr-mode"
                value={analysisMode}
                onChange={(event) => {
                  setAnalysisMode(event.target.value);
                  if (event.target.value === "boundary") {
                    setBoundaryAudio(true);
                    setActiveArea(false);
                  }
                  if (event.target.value === "manual") setAllAudioTracks(false);
                }}
              >
                <option value="pictures_audio">Picture checks and primary audio</option>
                <option value="metadata_audio">HDR metadata plots and primary audio</option>
                <option value="boundary">Bounded scene synchronization</option>
                <option value="manual">Metadata only / individual picture checks</option>
              </Select>
            </label>
          </div>
          <fieldset className="grid gap-2">
            <legend>Picture and audio checks</legend>
            {boundary ? (
              <p>
                Read complete native metadata, then decode bounded windows around selected scene
                boundaries. Picture coverage is partial; short mismatches outside those windows can
                be missed.
              </p>
            ) : audioOnly ? (
              <p>
                Read compressed HDR metadata and complete primary audio in one pass, without
                decoding video or measuring pictures.
              </p>
            ) : automatic ? (
              <p>
                Read the input once for complete metadata and audio,{" "}
                {fullFrames ? "full-frame" : "sampled"} picture brightness and borders, and scene
                matching.{" "}
                {!fullFrames && "Sampled checks can miss brief peaks or short mismatches. "}
                An optional HDR10 reference can provide additional grade and colour details.
              </p>
            ) : (
              <p>
                Generate native metadata plots, with optional individual picture checks below. Audio
                analysis is not requested in this mode.
              </p>
            )}
            {automatic && (
              <label className="flex gap-2">
                <input
                  type="checkbox"
                  checked={fullFrames}
                  onChange={(event) => setFullFrames(event.target.checked)}
                />
                Measure every video frame (slower)
              </label>
            )}
            {boundary && (
              <label className="flex gap-2">
                <input
                  type="checkbox"
                  checked={boundaryAudio}
                  onChange={(event) => {
                    setBoundaryAudio(event.target.checked);
                    if (!event.target.checked) setAllAudioTracks(false);
                  }}
                />
                Analyze audio
              </label>
            )}
            {audioEnabled && (
              <label className="flex gap-2">
                <input
                  type="checkbox"
                  checked={allAudioTracks}
                  onChange={(event) => setAllAudioTracks(event.target.checked)}
                />
                Analyze all audio tracks (up to 64)
              </label>
            )}
            {pictureEnabled && (
              <label className="grid gap-1">
                Video decoder threads (0 uses the mode default)
                <input
                  type="number"
                  min={0}
                  max={64}
                  step={1}
                  value={decoderThreads}
                  className="rounded border border-border bg-card p-2"
                  onChange={(event) => setDecoderThreads(Number(event.target.value))}
                />
              </label>
            )}
            {!automatic && !audioOnly && (
              <>
                {!boundary && (
                  <label className="flex gap-2">
                    <input
                      type="checkbox"
                      checked={sceneSync}
                      onChange={(event) => setSceneSync(event.target.checked)}
                    />
                    Scene synchronization
                  </label>
                )}
                <label className="flex gap-2">
                  <input
                    type="checkbox"
                    checked={activeArea}
                    onChange={(event) => setActiveArea(event.target.checked)}
                  />
                  Active-area borders
                </label>
              </>
            )}
            {!audioOnly && !boundary && (
              <>
                <p>
                  These checks decode pictures and may take longer. Uncertain mappings remain
                  inconclusive.
                </p>
                <label className="grid gap-1">
                  Comparison source path
                  <input
                    className="rounded border border-border bg-card p-2"
                    value={comparisonPath}
                    onChange={(event) => setComparisonPath(event.target.value)}
                  />
                </label>
                <button
                  type="button"
                  className={button}
                  disabled={!comparisonPath.trim()}
                  onClick={() => void facet.selectComparison(comparisonPath)}
                >
                  Select comparison source
                </button>
                {view.comparison && (
                  <p>Source B selected · fingerprint {view.comparison.fingerprint}</p>
                )}
                <label className="flex gap-2">
                  <input
                    type="checkbox"
                    checked={grade}
                    onChange={(event) => setGrade(event.target.checked)}
                  />
                  Brightness grade comparison
                </label>
                <label className="flex gap-2">
                  <input
                    type="checkbox"
                    checked={colour}
                    onChange={(event) => setColour(event.target.checked)}
                  />
                  Colour comparison
                </label>
                {(grade || colour) && (
                  <>
                    <label className="grid gap-1">
                      Confirmed frame offset (B − A)
                      <input
                        type="number"
                        min={-500000}
                        max={500000}
                        step={1}
                        className="rounded border border-border bg-card p-2"
                        value={offset}
                        onChange={(event) => setOffset(event.target.value)}
                      />
                    </label>
                    <label className="grid gap-1">
                      Common crop: left,top,width,height
                      <input
                        className="rounded border border-border bg-card p-2"
                        value={crop}
                        onChange={(event) => setCrop(event.target.value)}
                        placeholder="0,0,3840,2160"
                      />
                    </label>
                    <p>
                      Confirm corresponding content, equal cadence and this integer frame mapping.
                      Different edits or unsupported colour mappings cannot be compared.
                    </p>
                    {!validComparison && (
                      <p>Select source B and supply an explicit offset and even crop dimensions.</p>
                    )}
                  </>
                )}
              </>
            )}
          </fieldset>
        </fieldset>
        <div className="flex flex-wrap gap-2">
          <button
            ref={generateButton}
            className={button}
            disabled={
              blocked ||
              !view.available ||
              targetIDs.length === 0 ||
              (!audioOnly && !boundary && (grade || colour) && !validComparison) ||
              (pictureEnabled &&
                (!Number.isInteger(decoderThreads) || decoderThreads < 0 || decoderThreads > 64))
            }
            onClick={() =>
              void facet.generate({
                targetIDs,
                peakSource,
                formats,
                ...(decoded ? { decoded } : {}),
              })
            }
          >
            Generate
          </button>
          <button
            className={button}
            disabled={
              blocked ||
              !request ||
              request.targetIds.some(
                (id) => !view.targets.some((target) => target.id === id && target.supported),
              )
            }
            onClick={() => void facet.retry()}
          >
            {view.pendingRequest ? "Resume" : "Retry"}
          </button>
          <button className={button} disabled={!busy} onClick={() => void facet.cancel()}>
            Cancel
          </button>
        </div>
        <p>
          Native HDR plots, selected audio images, and amplitude statistics are included by default.
          Select picture-check images below to include them in descriptions.
        </p>
        {view.result &&
          !complete &&
          !["hdr-analysis-v2", "hdr-analysis-v3"].includes(view.result.profileVersion) && (
            <p>
              Every selected target must complete before inclusion. Retry or generate with fewer
              targets.
            </p>
          )}
        {view.selectionRecoveryNote && <p role="status">{view.selectionRecoveryNote}</p>}
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
          {target.formats?.map((format) => (
            <section key={format.format} className="grid gap-2">
              <h3>{formatLabel(format.format)}</h3>
              <p>
                Extraction: {format.state} · Coverage: {format.coverage}
              </p>
              <p>
                Pictures: {format.frames.toLocaleString()} · Missing metadata:{" "}
                {format.missing.toLocaleString()} · Profile: {format.profile || "unknown"}
              </p>
              {format.failure && <p>{format.failure.message}</p>}
              {format.checks.map((check) => (
                <p key={check.code}>
                  {check.state}: {check.message}
                </p>
              ))}
            </section>
          ))}
          {target.decoded?.map((result) => (
            <section key={result.mode} className="grid gap-2">
              <h3>{decodedLabel(result.mode)}</h3>
              <p>
                {result.state} · Measured {result.measured} / {result.selected}
                {result.coverage ? ` · Coverage: ${result.coverage}` : ""}
                {result.reason ? ` · ${result.reason.replaceAll("_", " ")}` : ""}
              </p>
              {result.audioTracks?.map((track) => (
                <p key={track.trackId}>
                  Track {track.ordinal} · {track.language || "und"} · {track.codec} ·{" "}
                  {track.channelLayout}: {track.state}
                  {track.pcmMeasured ? " · complete PCM measured" : " · PCM incomplete"}
                  {track.failure ? ` · ${track.failure.message}` : ""}
                </p>
              ))}
              {result.checks?.map((check) => (
                <p key={check.code}>
                  {check.state}: {check.message}
                </p>
              ))}
              {retryable.has(result.state) && (
                <button
                  className={button}
                  disabled={blocked || Boolean(view.pendingRequest)}
                  onClick={() => void facet.retryDecoded([result.mode])}
                >
                  Retry {decodedLabel(result.mode).toLowerCase()} measurement
                </button>
              )}
            </section>
          ))}
          {target.artifacts?.map((artifact) => (
            <div key={artifact.id} className="grid gap-2">
              {artifact.mime === "image/png" && (
                <label className="flex items-center gap-2">
                  <input
                    type="checkbox"
                    disabled={blocked}
                    checked={view.includedArtifactIDs.includes(artifact.id)}
                    onChange={(event) =>
                      void facet.setIncludedArtifacts(
                        event.target.checked
                          ? [...view.includedArtifactIDs, artifact.id]
                          : view.includedArtifactIDs.filter((id) => id !== artifact.id),
                      )
                    }
                  />
                  Include {target.label} {artifactTrackLabel(target, artifact)} in descriptions
                </label>
              )}
              {artifact.mime === "image/png" && (
                <button
                  aria-label={`Preview ${target.label} ${artifactTrackLabel(target, artifact)} plot`}
                  onClick={() => {
                    setLightboxAlt(`${target.label} ${artifactTrackLabel(target, artifact)}`);
                    setLightboxImage(facet.artifactURL(artifact.id));
                  }}
                >
                  <img
                    src={facet.artifactURL(artifact.id)}
                    alt={`${target.label} ${artifactTrackLabel(target, artifact)} plot`}
                    width={artifact.width}
                    height={artifact.height}
                    className="h-auto max-w-full rounded"
                  />
                </button>
              )}
              <a href={facet.artifactURL(artifact.id)} download>
                Download{" "}
                {artifact.audioTrackId
                  ? artifactTrackLabel(target, artifact)
                  : artifact.kind.replaceAll("_", " ")}
              </a>
            </div>
          ))}
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
            !target.formats && <p>{target.failure?.message || target.status}</p>
          )}
        </section>
      ))}
    </section>
  );
}
