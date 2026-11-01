// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useRef, useState } from "react";
import type {
  HDRAnalysisTargetResult,
  HDRTypedArtifact,
} from "../../api/generated/release-workflow";
import { FieldHelp } from "../../components/ui/field-help";
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
  return `${artifactLabel(artifact.kind, artifact.format)}${track ? ` · Track ${track.ordinal} · ${track.language || "unknown language"} · ${track.codec} · ${track.channelLayout}` : ""}`;
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
        <div className="flex flex-wrap items-center gap-1">
          <h2 id="hdr-selection">Prepared targets</h2>
          <FieldHelp label="Prepared targets" id="hdr-targets-help">
            Select supported media targets from the current prepared release. One supported target
            is selected automatically; multiple targets need explicit selection. Unsupported targets
            retain their reason inline.
          </FieldHelp>
        </div>
        <p className={pageStyle.subtitle}>
          File sources require a unique HEVC video track. For Blu-ray, enable the HDR metadata check
          in playlist selection first. Changing the peak estimator reuses complete retained
          metadata.
        </p>
        {view.targets.length === 0 && (
          <p>No supported file or Blu-ray target is available. Prepare a supported source first.</p>
        )}
        <fieldset disabled={blocked} className="grid gap-2">
          <legend className="sr-only">Select HDR targets in source order</legend>
          {view.targets.map((target) => (
            <label key={target.id} className="flex items-start gap-2">
              <input
                type="checkbox"
                aria-describedby="hdr-targets-help"
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
            <div className="grid min-w-0 gap-1.5 text-sm font-medium">
              <span className="flex flex-wrap items-center gap-1">
                <label htmlFor="hdr-peak">Peak estimator</label>
                <FieldHelp label="Peak estimator" id="hdr-peak-help">
                  Select how the HDR10+ plot estimates peak brightness from native metadata.
                  Histogram uses the maximum supplied distribution value; Last histogram value uses
                  the last supplied value. MaxSCL uses the maximum scene component, or a luminance
                  estimate weighted from the three RGB components. This does not change decoded
                  picture measurements. Complete retained metadata can be reused.
                </FieldHelp>
              </span>
              <Select
                aria-describedby="hdr-peak-help"
                id="hdr-peak"
                value={peakSource}
                onChange={(event) => setPeakSource(event.target.value)}
              >
                <option value="histogram">Histogram (default)</option>
                <option value="histogram99">Last histogram value</option>
                <option value="max-scl">Maximum scene component (MaxSCL)</option>
                <option value="max-scl-luminance">Scene-component luminance (MaxSCL)</option>
              </Select>
            </div>
            <div className="grid min-w-0 gap-1.5 text-sm font-medium">
              <span className="flex flex-wrap items-center gap-1">
                <label htmlFor="hdr-formats">Metadata formats</label>
                <FieldHelp label="Metadata formats" id="hdr-formats-help">
                  Automatic discovery scans HDR10+ and Dolby Vision independently. Request one
                  format explicitly or require both; requiring an absent format prevents a complete
                  result. Static HDR10 and HLG do not supply dynamic metadata plots.
                </FieldHelp>
              </span>
              <Select
                aria-describedby="hdr-formats-help"
                id="hdr-formats"
                value={formats}
                onChange={(event) => setFormats(event.target.value)}
              >
                <option value="auto">Automatic discovery</option>
                <option value="hdr10plus">HDR10+ only</option>
                <option value="dolby-vision">Dolby Vision only</option>
                <option value="both">Require both formats</option>
              </Select>
            </div>
            <div className="grid min-w-0 gap-1.5 text-sm font-medium">
              <span className="flex flex-wrap items-center gap-1">
                <label htmlFor="hdr-mode">Analysis mode</label>
                <FieldHelp label="Analysis mode" id="hdr-mode-help">
                  Choose the work to request. The default reads native metadata and primary audio
                  without decoding video. Picture modes add sampled, full-frame or bounded checks;
                  individual mode requests only the picture checks you enable and no audio.
                </FieldHelp>
              </span>
              <Select
                aria-describedby="hdr-mode-help"
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
            </div>
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
              <div className="flex items-start gap-1">
                <label className="flex gap-2">
                  <input
                    type="checkbox"
                    aria-describedby="hdr-full-frames-help"
                    checked={fullFrames}
                    onChange={(event) => setFullFrames(event.target.checked)}
                  />
                  Measure every video frame (slower)
                </label>
                <FieldHelp label="Measure every video frame (slower)" id="hdr-full-frames-help">
                  Replace sampled automatic picture measurements with a full-frame pass. Complete
                  metadata and audio are still requested. This increases decoding work; it does not
                  align a reference source automatically.
                </FieldHelp>
              </div>
            )}
            {boundary && (
              <div className="flex items-start gap-1">
                <label className="flex gap-2">
                  <input
                    type="checkbox"
                    aria-describedby="hdr-audio-help"
                    checked={boundaryAudio}
                    onChange={(event) => {
                      setBoundaryAudio(event.target.checked);
                      if (!event.target.checked) setAllAudioTracks(false);
                    }}
                  />
                  Analyze audio
                </label>
                <FieldHelp label="Analyze audio" id="hdr-audio-help">
                  Include audio analysis alongside bounded scene checks. Without this option,
                  bounded mode requests no audio outputs. The primary prepared audio track is used
                  unless all tracks are selected.
                </FieldHelp>
              </div>
            )}
            {audioEnabled && (
              <div className="flex items-start gap-1">
                <label className="flex gap-2">
                  <input
                    type="checkbox"
                    aria-describedby="hdr-all-audio-help"
                    checked={allAudioTracks}
                    onChange={(event) => setAllAudioTracks(event.target.checked)}
                  />
                  Analyze all audio tracks (up to 64)
                </label>
                <FieldHelp label="Analyze all audio tracks (up to 64)" id="hdr-all-audio-help">
                  Analyze every prepared audio track instead of the primary track, retaining
                  separate outcomes and outputs. More tracks require more decoding work. Selections
                  over 64 tracks are rejected rather than truncated.
                </FieldHelp>
              </div>
            )}
            {pictureEnabled && (
              <div className="grid gap-1">
                <span className="flex flex-wrap items-center gap-1">
                  <label htmlFor="hdr-threads">
                    Video decoder threads (0 uses the mode default)
                  </label>
                  <FieldHelp
                    label="Video decoder threads (0 uses the mode default)"
                    id="hdr-threads-help"
                  >
                    Requested maximum threads for picture decoding: an integer from 0 to 64. Zero
                    uses the mode default; CPU availability and shared budgets may reduce the actual
                    count. This does not set audio-decoder threads.
                  </FieldHelp>
                </span>
                <input
                  aria-describedby="hdr-threads-help"
                  id="hdr-threads"
                  type="number"
                  min={0}
                  max={64}
                  step={1}
                  value={decoderThreads}
                  className="rounded border border-border bg-card p-2"
                  onChange={(event) => setDecoderThreads(Number(event.target.value))}
                />
              </div>
            )}
            {!automatic && !audioOnly && (
              <>
                {!boundary && (
                  <div className="flex items-start gap-1">
                    <label className="flex gap-2">
                      <input
                        type="checkbox"
                        aria-describedby="hdr-scene-sync-help"
                        checked={sceneSync}
                        onChange={(event) => setSceneSync(event.target.checked)}
                      />
                      Scene synchronization
                    </label>
                    <FieldHelp label="Scene synchronization" id="hdr-scene-sync-help">
                      Compare native scene boundaries with decoded picture evidence. This is the
                      separate scene check in individual mode; bounded mode selects its own limited
                      windows. Inconclusive evidence does not establish a match.
                    </FieldHelp>
                  </div>
                )}
                <div className="flex items-start gap-1">
                  <label className="flex gap-2">
                    <input
                      type="checkbox"
                      aria-describedby="hdr-active-area-help"
                      checked={activeArea}
                      onChange={(event) => setActiveArea(event.target.checked)}
                    />
                    Active-area borders
                  </label>
                  <FieldHelp label="Active-area borders" id="hdr-active-area-help">
                    Compare decoded picture borders with Dolby Vision active-area metadata. Bounded
                    mode samples fixed positions. This measures evidence; it does not crop or modify
                    the source video.
                  </FieldHelp>
                </div>
              </>
            )}
            {!audioOnly && !boundary && (
              <>
                <p>
                  These checks decode pictures and may take longer. Uncertain mappings remain
                  inconclusive.
                </p>
                <div className="grid gap-1">
                  <span className="flex flex-wrap items-center gap-1">
                    <label htmlFor="hdr-comparison-path">Comparison source path</label>
                    <FieldHelp label="Comparison source path" id="hdr-comparison-path-help">
                      Server-local HDR10-compatible reference file, source B, with corresponding
                      content and cadence. Select comparison source validates and binds it to this
                      release; typing a path alone does not select it or replace Input.
                    </FieldHelp>
                  </span>
                  <input
                    aria-describedby="hdr-comparison-path-help"
                    id="hdr-comparison-path"
                    className="rounded border border-border bg-card p-2"
                    value={comparisonPath}
                    onChange={(event) => setComparisonPath(event.target.value)}
                  />
                </div>
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
                <div className="flex items-start gap-1">
                  <label className="flex gap-2">
                    <input
                      type="checkbox"
                      aria-describedby="hdr-grade-help"
                      checked={grade}
                      onChange={(event) => setGrade(event.target.checked)}
                    />
                    Brightness grade comparison
                  </label>
                  <FieldHelp label="Brightness grade comparison" id="hdr-grade-help">
                    Compare decoded picture brightness with the selected reference. Requires source
                    B, a confirmed integer frame offset and a common crop. A generated plot is
                    evidence to review, not automatic confirmation of compatible grading.
                  </FieldHelp>
                </div>
                <div className="flex items-start gap-1">
                  <label className="flex gap-2">
                    <input
                      type="checkbox"
                      aria-describedby="hdr-colour-help"
                      checked={colour}
                      onChange={(event) => setColour(event.target.checked)}
                    />
                    Colour comparison
                  </label>
                  <FieldHelp label="Colour comparison" id="hdr-colour-help">
                    Compare supported decoded colour evidence within the common crop. Requires an
                    aligned reference with matching content and cadence. Unsupported mappings remain
                    inconclusive; the sources are not automatically aligned or resized.
                  </FieldHelp>
                </div>
                {(grade || colour) && (
                  <>
                    <div className="grid gap-1">
                      <span className="flex flex-wrap items-center gap-1">
                        <label htmlFor="hdr-offset">Confirmed frame offset (B − A)</label>
                        <FieldHelp label="Confirmed frame offset (B − A)" id="hdr-offset-help">
                          Integer mapping: B frame = A frame + offset, from −500000 to 500000.
                          Supply 0 for already aligned sources. Confirm this mapping yourself; it is
                          not estimated from the path.
                        </FieldHelp>
                      </span>
                      <input
                        aria-describedby="hdr-offset-help"
                        id="hdr-offset"
                        type="number"
                        min={-500000}
                        max={500000}
                        step={1}
                        className="rounded border border-border bg-card p-2"
                        value={offset}
                        onChange={(event) => setOffset(event.target.value)}
                      />
                    </div>
                    <div className="grid gap-1">
                      <span className="flex flex-wrap items-center gap-1">
                        <label htmlFor="hdr-crop">Common crop: left,top,width,height</label>
                        <FieldHelp label="Common crop: left,top,width,height" id="hdr-crop-help">
                          Four comma-separated integers in coded pixels, applied to both sources.
                          Left and top must be nonnegative; width and height must be even and at
                          least 2. The crop must fit both pictures; this form allows bounds up to
                          8192 × 4320.
                        </FieldHelp>
                      </span>
                      <input
                        aria-describedby="hdr-crop-help"
                        id="hdr-crop"
                        className="rounded border border-border bg-card p-2"
                        value={crop}
                        onChange={(event) => setCrop(event.target.value)}
                        placeholder="0,0,3840,2160"
                      />
                    </div>
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
        <div className="flex flex-wrap items-center gap-1">
          <span className="font-medium">Analysis actions</span>
          <FieldHelp label="HDR analysis actions" id="hdr-actions-help">
            Generate uses the current selections. Retry or Resume uses the retained request, reusing
            compatible completed work. Cancel stops active work. Individual failed picture
            measurements can be retried below without authorizing a fresh native metadata
            extraction. Progress is completed work, not a time estimate.
          </FieldHelp>
        </div>
        <div className="flex flex-wrap gap-2">
          <button
            aria-describedby="hdr-actions-help"
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
            aria-describedby="hdr-actions-help"
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
          <button
            aria-describedby="hdr-actions-help"
            className={button}
            disabled={!busy}
            onClick={() => void facet.cancel()}
          >
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
          <div className="flex flex-wrap items-center gap-1">
            <h2>{target.label}</h2>
            <FieldHelp label={`${target.label} results`} id={`hdr-result-${target.targetId}-help`}>
              Native metadata coverage and decoded picture coverage describe separate evidence.
              Partial, unsupported or inconclusive checks are not a pass. Measured counts apply to
              the selected pictures or audio work, not necessarily the entire source. Preview opens
              an image; Download saves a local artifact. Successful outputs can remain available
              after a partial attempt.
            </FieldHelp>
          </div>
          {target.formats?.map((format) => (
            <section
              key={format.format}
              className="grid gap-2"
              aria-describedby={`hdr-result-${target.targetId}-help`}
            >
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
            <section
              key={result.mode}
              className="grid gap-2"
              aria-describedby={`hdr-result-${target.targetId}-help`}
            >
              <h3>{decodedLabel(result.mode)}</h3>
              <p>
                {result.state} · Measured {result.measured} / {result.selected}
                {result.coverage ? ` · Coverage: ${result.coverage}` : ""}
                {result.reason ? ` · ${result.reason.replaceAll("_", " ")}` : ""}
              </p>
              {result.audioTracks?.map((track) => (
                <p key={track.trackId}>
                  Track {track.ordinal} · {track.language || "unknown language"} · {track.codec} ·{" "}
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
                <div className="flex items-start gap-1">
                  <label className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      disabled={blocked}
                      aria-describedby={`hdr-artifact-${artifact.id}-help`}
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
                  <FieldHelp
                    label={`Include ${target.label} ${artifactTrackLabel(target, artifact)} in descriptions`}
                    id={`hdr-artifact-${artifact.id}-help`}
                  >
                    Select this successful image for generated descriptions. Native plots and
                    selected audio images are included by default; picture-check images require
                    selection. Final edited descriptions remain authoritative, so inspect them after
                    changing inclusion.
                  </FieldHelp>
                </div>
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
