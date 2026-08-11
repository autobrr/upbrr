// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useMemo, useRef, useState } from "react";
import { Switch } from "../../components/ui/switch";
import type { ScreenshotsFacet } from "../../releaseSession/types";
import type { ConfigMap, ConfigValue, ScreenshotSelection } from "../../types";

type Props = Readonly<{
  facet: ScreenshotsFacet;
  screenshotConfig: ConfigMap | null;
  updateScreenshotConfigValue: (key: string, value: ConfigValue) => void;
  loadSettings: () => void;
  settingsLoading: boolean;
  settingsDirty: boolean;
  settingsSaved: string;
  settingsError: string;
  applyScreenshotSettings: () => void;
  setLightboxImage: (value: string) => void;
  setLightboxAlt: (value: string) => void;
}>;

/** Presents screenshot planning, live previews, retained capture, ordering, and final selection. */
export default function ScreenshotsPage({
  facet,
  screenshotConfig,
  updateScreenshotConfigValue,
  loadSettings,
  settingsLoading,
  settingsDirty,
  settingsSaved,
  settingsError,
  applyScreenshotSettings,
  setLightboxImage,
  setLightboxAlt,
}: Props) {
  const { view } = facet;
  const loadRef = useRef(facet.load);
  const [livePreviewSeconds, setLivePreviewSeconds] = useState(0);
  const [finalDragIndex, setFinalDragIndex] = useState<number | null>(null);
  loadRef.current = facet.load;

  useEffect(() => {
    if (
      view.status !== "running" &&
      view.status !== "error" &&
      !view.plan &&
      (view.workflowMode || Boolean(view.staleReason))
    ) {
      void loadRef.current();
    }
  }, [view.plan, view.staleReason, view.status, view.workflowMode]);

  const plan = view.plan;
  const busy = view.status === "running";
  const selections = view.selections;
  const workflowImages = useMemo(
    () =>
      (view.artifacts?.artifacts || [])
        .filter((artifact) => artifact.kind === "screenshot")
        .sort((left, right) => (left.order || 0) - (right.order || 0)),
    [view.artifacts],
  );
  const selectedWorkflowImages = useMemo(
    () => workflowImages.filter((artifact) => artifact.selected),
    [workflowImages],
  );

  const previewDuration = Math.max(plan?.DurationSeconds || 0, 0);
  const previewFrameRate = Math.max(plan?.FrameRate || 0, 0);
  const previewTimingDisabled = previewDuration <= 0 || previewFrameRate <= 0;
  const clampPreviewSeconds = (value: number) => {
    if (!Number.isFinite(value)) return 0;
    return Math.min(Math.max(value, 0), previewDuration);
  };
  const livePreviewFrame =
    previewFrameRate > 0 ? Math.max(0, Math.round(livePreviewSeconds * previewFrameRate)) : 0;

  const runLivePreviewAt = async (value: number) => {
    const next = clampPreviewSeconds(value);
    setLivePreviewSeconds(next);
    await facet.previewFrame(next);
  };

  const stepLivePreview = (direction: number) => {
    if (previewTimingDisabled) return;
    void runLivePreviewAt(livePreviewSeconds + direction / previewFrameRate);
  };

  const captureLivePreview = () => {
    const nextIndex =
      Math.max(
        -1,
        ...selections.map((selection) => selection.Index),
        ...workflowImages.map((artifact) => artifact.index ?? -1),
      ) + 1;
    const selection: ScreenshotSelection = {
      Index: nextIndex,
      TimestampSeconds: clampPreviewSeconds(livePreviewSeconds),
      Frame: livePreviewFrame,
      Source: "manual",
    };
    void facet.generate("final", [selection]);
  };

  return (
    <section className="screens-panel">
      <header className="screens-header">
        <p className="eyebrow">Screenshots</p>
        <h1>Plan &amp; Capture</h1>
        <p className="subtitle">
          Review tracker images, adjust frame times, and generate screenshots.
        </p>
      </header>

      {view.artifacts ? (
        <section className="panel grid gap-1" role="status">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2>Authoritative media set</h2>
            <span className="muted">{view.artifacts.status}</span>
          </div>
          <p className="muted">
            {view.artifacts.artifacts.filter((artifact) => artifact.kind === "screenshot").length}{" "}
            captured screenshot(s)
          </p>
        </section>
      ) : null}

      <section className="panel screens-actions" aria-busy={busy}>
        <div>
          <p className="label">Source path</p>
          <p className="value dupe-path">{plan?.SourcePath || "No prepared source"}</p>
          {plan ? (
            <div className="screens-meta">
              <p className="muted">Duration: {plan.DurationSeconds.toFixed(1)}s</p>
              <p className="muted">Frame rate: {plan.FrameRate.toFixed(3)}</p>
              {plan.DiscType ? <p className="muted">Disc type: {plan.DiscType}</p> : null}
            </div>
          ) : null}
        </div>
        <div className="screens-actions__buttons">
          <button className="ghost" type="button" onClick={() => void facet.load()} disabled={busy}>
            {busy ? "Loading..." : "Load suggestions"}
          </button>
          <button
            className="primary"
            type="button"
            onClick={() => void facet.generate("final")}
            disabled={busy || selections.length === 0}
          >
            {busy ? "Capturing..." : "Generate screenshots"}
          </button>
        </div>
      </section>

      <section className="panel screens-list">
        <details>
          <summary>
            Frame Selection · {selections.length} frame{selections.length === 1 ? "" : "s"}
          </summary>
          <div className="screens-gallery__header mt-3">
            <p className="muted">Adjust timestamps or frame numbers, then preview.</p>
          </div>
          {!plan ? (
            <p className="muted">Load suggestions to edit frame selections.</p>
          ) : selections.length === 0 ? (
            <p className="muted">No selections available yet.</p>
          ) : (
            <div className="screens-rows">
              {selections.map((selection, index) => (
                <div className="screens-row" key={`sel-${selection.Index}`}>
                  <div>
                    <p className="label">Shot {selection.Index + 1}</p>
                    <p className="muted">Source: {selection.Source || "auto"}</p>
                  </div>
                  <label className="screens-field">
                    <span>Seconds</span>
                    <input
                      type="number"
                      step="0.1"
                      value={selection.TimestampSeconds}
                      onChange={(event) =>
                        facet.changeSelection(index, {
                          TimestampSeconds: Number(event.target.value) || 0,
                        })
                      }
                    />
                  </label>
                  <label className="screens-field">
                    <span>Frame</span>
                    <input
                      type="number"
                      step="1"
                      value={selection.Frame}
                      onChange={(event) =>
                        facet.changeSelection(index, { Frame: Number(event.target.value) || 0 })
                      }
                    />
                  </label>
                  <button
                    className="ghost"
                    type="button"
                    disabled={busy}
                    onClick={() => void facet.generate("preview", [selection])}
                  >
                    {busy ? "Previewing..." : "Preview"}
                  </button>
                </div>
              ))}
            </div>
          )}
        </details>
      </section>

      {workflowImages.length ? (
        <section className="panel screens-gallery" aria-busy={busy}>
          <div className="screens-gallery__header">
            <h2>Generated Screenshots</h2>
            <p className="muted">Workflow-owned screenshots ready for description building.</p>
            <button
              className="ghost"
              type="button"
              disabled={busy}
              onClick={() => {
                if (globalThis.confirm("Delete all generated screenshots?"))
                  void facet.deleteArtifacts(workflowImages.map((artifact) => artifact.id));
              }}
            >
              Delete all
            </button>
          </div>
          <div className="screens-grid">
            {workflowImages.map((artifact, index) => {
              const selectedIndex = selectedWorkflowImages.findIndex(
                (selected) => selected.id === artifact.id,
              );
              return (
                <div
                  className="screens-thumb-card"
                  key={artifact.id}
                  draggable={artifact.selected}
                  onDragStart={() => setFinalDragIndex(selectedIndex)}
                  onDragOver={(event) => {
                    if (artifact.selected) event.preventDefault();
                  }}
                  onDrop={(event) => {
                    event.preventDefault();
                    if (artifact.selected && finalDragIndex !== null)
                      void facet.reorderFinal(finalDragIndex, selectedIndex);
                    setFinalDragIndex(null);
                  }}
                  onDragEnd={() => setFinalDragIndex(null)}
                >
                  <button
                    className="screens-thumb"
                    type="button"
                    disabled={!artifact.url}
                    onClick={() => {
                      if (!artifact.url) return;
                      setLightboxImage(artifact.url);
                      setLightboxAlt(`Screenshot ${index + 1}`);
                    }}
                  >
                    {artifact.url ? (
                      <img src={artifact.url} alt={`Screenshot ${index + 1}`} loading="lazy" />
                    ) : (
                      <span className="muted block p-4 text-center text-sm">Image unavailable</span>
                    )}
                  </button>
                  <button
                    className="ghost"
                    type="button"
                    disabled={busy}
                    onClick={() => void facet.selectArtifact(artifact.id, !artifact.selected)}
                  >
                    {artifact.selected ? "Unselect" : "Select"}
                  </button>
                  <button
                    className="screens-thumb-delete"
                    type="button"
                    disabled={busy}
                    onClick={() => void facet.deleteArtifacts([artifact.id])}
                  >
                    Delete
                  </button>
                </div>
              );
            })}
          </div>
        </section>
      ) : view.workflowMode && view.artifacts ? (
        <section className="panel">
          <p className="muted">No generated screenshots retained for this workflow.</p>
        </section>
      ) : null}

      <section className="panel screens-settings">
        <details>
          <summary>Screenshot settings</summary>
          {screenshotConfig ? (
            <div className="screens-settings__grid">
              <label className="settings-field">
                <span>Screenshot count</span>
                <input
                  type="number"
                  value={
                    typeof screenshotConfig.Screens === "number" ? screenshotConfig.Screens : 0
                  }
                  onChange={(event) =>
                    updateScreenshotConfigValue("Screens", Number(event.target.value))
                  }
                />
              </label>
              <div className="settings-toggle">
                <span>Tonemap HDR</span>
                <Switch
                  aria-label="Tonemap HDR"
                  checked={Boolean(screenshotConfig.ToneMap)}
                  onChange={(event) => updateScreenshotConfigValue("ToneMap", event.target.checked)}
                />
              </div>
              <div className="settings-toggle">
                <span>Use libplacebo</span>
                <Switch
                  aria-label="Use libplacebo"
                  checked={Boolean(screenshotConfig.UseLibplacebo)}
                  onChange={(event) =>
                    updateScreenshotConfigValue("UseLibplacebo", event.target.checked)
                  }
                />
              </div>
              <div className="settings-toggle">
                <span>Frame overlay</span>
                <Switch
                  aria-label="Frame overlay"
                  checked={Boolean(screenshotConfig.FrameOverlay)}
                  onChange={(event) =>
                    updateScreenshotConfigValue("FrameOverlay", event.target.checked)
                  }
                />
              </div>
              <label className="settings-field">
                <span>Overlay text size</span>
                <input
                  type="number"
                  value={
                    typeof screenshotConfig.OverlayTextSize === "number"
                      ? screenshotConfig.OverlayTextSize
                      : 0
                  }
                  onChange={(event) =>
                    updateScreenshotConfigValue("OverlayTextSize", Number(event.target.value))
                  }
                />
              </label>
              <label className="settings-field">
                <span>FFmpeg compression</span>
                <input
                  type="number"
                  value={
                    typeof screenshotConfig.FFmpegCompression === "number"
                      ? screenshotConfig.FFmpegCompression
                      : 0
                  }
                  onChange={(event) =>
                    updateScreenshotConfigValue("FFmpegCompression", Number(event.target.value))
                  }
                />
              </label>
              <label className="settings-field">
                <span>Tonemap algorithm</span>
                <input
                  type="text"
                  value={
                    typeof screenshotConfig.TonemapAlgorithm === "string"
                      ? screenshotConfig.TonemapAlgorithm
                      : ""
                  }
                  onChange={(event) =>
                    updateScreenshotConfigValue("TonemapAlgorithm", event.target.value)
                  }
                />
              </label>
              <label className="settings-field">
                <span>Desat</span>
                <input
                  type="number"
                  step="0.01"
                  value={typeof screenshotConfig.Desat === "number" ? screenshotConfig.Desat : 0}
                  onChange={(event) =>
                    updateScreenshotConfigValue("Desat", Number(event.target.value))
                  }
                />
              </label>
              <div className="settings-toggle">
                <span>Limit ffmpeg concurrency</span>
                <Switch
                  aria-label="Limit ffmpeg concurrency"
                  checked={Boolean(screenshotConfig.FFmpegLimit)}
                  onChange={(event) =>
                    updateScreenshotConfigValue("FFmpegLimit", event.target.checked)
                  }
                />
              </div>
              <label className="settings-field">
                <span>FFmpeg concurrency</span>
                <input
                  type="number"
                  value={
                    typeof screenshotConfig.ProcessLimit === "number"
                      ? screenshotConfig.ProcessLimit
                      : 1
                  }
                  onChange={(event) =>
                    updateScreenshotConfigValue("ProcessLimit", Number(event.target.value))
                  }
                />
              </label>
            </div>
          ) : (
            <p className="muted">Load settings to edit screenshot handling.</p>
          )}
          <div className="screens-settings__actions">
            <button
              className="ghost"
              type="button"
              onClick={loadSettings}
              disabled={settingsLoading}
            >
              {settingsLoading ? "Loading..." : "Reload settings"}
            </button>
            <button
              className="primary"
              type="button"
              onClick={applyScreenshotSettings}
              disabled={settingsLoading || !settingsDirty}
            >
              {settingsLoading ? "Applying..." : "Apply settings"}
            </button>
          </div>
          {settingsError ? <p className="error">{settingsError}</p> : null}
          {settingsSaved ? <p className="success">{settingsSaved}</p> : null}
        </details>
      </section>

      {view.error ? (
        <p className="error" role="alert">
          {view.error}
        </p>
      ) : null}
      {plan?.RequiresManualFrames ? (
        <p className="muted">
          Duration or frame rate is missing. Enter manual frame times before capturing.
        </p>
      ) : null}

      <section className="panel screens-preview">
        <div className="screens-gallery__header">
          <h2>Live Preview</h2>
          <p className="muted">Scrub the timeline and capture the current frame.</p>
        </div>
        {plan ? (
          <div className="screens-preview__body">
            <div className="screens-preview__controls">
              <label className="screens-field">
                <span>Seconds</span>
                <input
                  type="number"
                  step="0.1"
                  value={livePreviewSeconds}
                  onChange={(event) =>
                    setLivePreviewSeconds(clampPreviewSeconds(Number(event.target.value)))
                  }
                />
              </label>
              <label className="screens-field">
                <span>Frame</span>
                <input
                  type="number"
                  step="1"
                  value={livePreviewFrame}
                  onChange={(event) => {
                    const frame = Number(event.target.value);
                    setLivePreviewSeconds(
                      previewFrameRate > 0 ? clampPreviewSeconds(frame / previewFrameRate) : 0,
                    );
                  }}
                />
              </label>
              <div className="screens-preview__slider">
                <input
                  aria-label="Preview timeline"
                  type="range"
                  min={0}
                  max={previewDuration}
                  step={previewFrameRate > 0 ? 1 / previewFrameRate : 1}
                  value={clampPreviewSeconds(livePreviewSeconds)}
                  onChange={(event) =>
                    setLivePreviewSeconds(clampPreviewSeconds(Number(event.target.value)))
                  }
                  disabled={previewTimingDisabled}
                />
                <div className="screens-preview__meta">
                  <span className="muted">Duration: {previewDuration.toFixed(1)}s</span>
                  <span className="muted">FPS: {previewFrameRate.toFixed(3)}</span>
                </div>
              </div>
              <div className="screens-preview__buttons">
                <button
                  className="ghost"
                  type="button"
                  onClick={() => stepLivePreview(-1)}
                  disabled={previewTimingDisabled || busy}
                >
                  Prev frame
                </button>
                <button
                  className="ghost"
                  type="button"
                  onClick={() => stepLivePreview(1)}
                  disabled={previewTimingDisabled || busy}
                >
                  Next frame
                </button>
                <button
                  className="ghost"
                  type="button"
                  onClick={() => void runLivePreviewAt(livePreviewSeconds)}
                  disabled={previewTimingDisabled || busy}
                >
                  {busy ? "Loading..." : "Run preview"}
                </button>
                <button
                  className="primary"
                  type="button"
                  onClick={captureLivePreview}
                  disabled={previewTimingDisabled || busy}
                >
                  {busy ? "Capturing..." : "Capture preview"}
                </button>
              </div>
            </div>
            <div className="screens-preview__image">
              {view.previewImage ? (
                <button
                  className="screens-thumb max-w-md"
                  type="button"
                  onClick={() => {
                    setLightboxImage(view.previewImage);
                    setLightboxAlt("Live preview");
                  }}
                >
                  <img src={view.previewImage} alt="Live preview" />
                </button>
              ) : busy ? (
                <p className="muted">Loading preview...</p>
              ) : (
                <p className="muted">No preview yet.</p>
              )}
            </div>
          </div>
        ) : (
          <p className="muted">Load suggestions to enable live preview.</p>
        )}
      </section>
    </section>
  );
}
