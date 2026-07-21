// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useMemo, useRef, useState } from "react";
import { Switch } from "../../components/ui/switch";
import type { ScreenshotsFacet } from "../../releaseSession/types";
import type { ConfigMap, ConfigValue, ScreenshotImage, ScreenshotSelection } from "../../types";

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

const uniqueImages = (images: readonly ScreenshotImage[]) => {
  const paths = new Set<string>();
  return images.filter((image) => {
    if (!image.Path || paths.has(image.Path)) return false;
    paths.add(image.Path);
    return true;
  });
};

/** Presents screenshot planning, generation, ordering, preview, and final selection. */
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
  const readImageRef = useRef(facet.readImage);
  const imageCacheRef = useRef<Record<string, string>>({});
  const [imageDataByPath, setImageDataByPath] = useState<Record<string, string>>({});
  const [livePreviewSeconds, setLivePreviewSeconds] = useState(0);
  const [finalDragIndex, setFinalDragIndex] = useState<number | null>(null);
  loadRef.current = facet.load;
  readImageRef.current = facet.readImage;

  useEffect(() => {
    if (view.status === "idle" && view.staleReason) void loadRef.current();
  }, [view.staleReason, view.status]);

  const plan = view.plan;
  const busy = view.status === "running";
  const selections = view.selections;
  const existingImages = uniqueImages(plan?.ExistingScreenshots || []);
  const existingTrackerImages = uniqueImages(plan?.ExistingTrackerScreenshots || []);
  const previewImages = uniqueImages([
    ...(plan?.PreviewImages || []),
    ...(view.result?.Purpose === "preview" ? view.result.Images || [] : []),
  ]);
  const availableImages = uniqueImages([
    ...existingImages,
    ...existingTrackerImages,
    ...(plan?.FinalSelections || []),
    ...previewImages,
    ...(view.result?.Images || []),
  ]);
  const imageByPath = useMemo(
    () => new Map(availableImages.map((image) => [image.Path, image])),
    [availableImages],
  );
  const finalImages = view.finalSelectionPaths
    .map((path) => imageByPath.get(path))
    .filter((image): image is ScreenshotImage => !!image);
  const finalPaths = useMemo(() => new Set(view.finalSelectionPaths), [view.finalSelectionPaths]);
  const localImagePathKey = availableImages
    .map((image) => image.Path)
    .filter(Boolean)
    .join("\u0000");

  useEffect(() => {
    let canceled = false;
    const loadImages = async () => {
      const next = { ...imageCacheRef.current };
      for (const path of localImagePathKey ? localImagePathKey.split("\u0000") : []) {
        if (next[path]) continue;
        try {
          next[path] = await readImageRef.current(path);
        } catch {
          next[path] = "";
        }
        if (canceled) return;
      }
      imageCacheRef.current = next;
      setImageDataByPath(next);
    };
    void loadImages();
    return () => {
      canceled = true;
    };
  }, [localImagePathKey]);

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
    if (selections.length === 0) return;

    const closest = selections.reduce<ScreenshotSelection | null>((current, selection) => {
      if (!current) return selection;
      return Math.abs(selection.TimestampSeconds - livePreviewSeconds) <
        Math.abs(current.TimestampSeconds - livePreviewSeconds)
        ? selection
        : current;
    }, null);
    const selection: ScreenshotSelection = {
      Index: closest?.Index ?? 0,
      TimestampSeconds: clampPreviewSeconds(livePreviewSeconds),
      Frame: livePreviewFrame,
      Source: "manual",
    };
    void facet.generate("preview", [selection]);
  };

  const openLocalImage = (image: ScreenshotImage, label: string) => {
    const dataURI = imageDataByPath[image.Path];
    if (!dataURI) return;
    setLightboxImage(dataURI);
    setLightboxAlt(label);
  };

  const confirmDeleteAll = (label: string, images: readonly ScreenshotImage[]) => {
    if (!images.length || !globalThis.confirm(`Delete all ${label} images from the temp folder?`))
      return;
    void facet.removeMany(images.map((image) => image.Path));
  };

  const renderLocalThumbnail = (image: ScreenshotImage, alt: string) => {
    const dataURI = imageDataByPath[image.Path];
    return dataURI ? (
      <img src={dataURI} alt={alt} />
    ) : (
      <span className="muted block p-4 text-center text-sm">Loading image...</span>
    );
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
                  disabled={previewTimingDisabled || busy || selections.length === 0}
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

      {(plan?.TrackerImageLinks || []).length ? (
        <section className="panel screens-gallery">
          <div className="screens-gallery__header">
            <h2>Tracker Images</h2>
            <p className="muted">Already available from tracker data.</p>
            <button
              className="ghost"
              type="button"
              disabled={busy}
              onClick={() => {
                const links = plan?.TrackerImageLinks || [];
                if (globalThis.confirm("Delete all tracker image links?"))
                  void facet.removeTrackerURLs(links.map((link) => link.URL));
              }}
            >
              Delete all
            </button>
          </div>
          <div className="screens-grid">
            {plan?.TrackerImageLinks.map((link, index) => (
              <div className="screens-thumb-card" key={`${link.URL}-${index}`}>
                <button
                  className="screens-thumb"
                  type="button"
                  onClick={() => {
                    setLightboxImage(link.URL);
                    setLightboxAlt("Tracker image");
                  }}
                >
                  <img src={link.URL} alt="Tracker screenshot" loading="lazy" />
                </button>
                <button
                  className="screens-thumb-delete"
                  type="button"
                  disabled={busy}
                  onClick={() => void facet.removeTrackerURL(link.URL)}
                >
                  Delete
                </button>
              </div>
            ))}
          </div>
        </section>
      ) : null}

      {existingImages.length ? (
        <section className="panel screens-gallery">
          <div className="screens-gallery__header">
            <h2>Existing Captures</h2>
            <p className="muted">Previously generated screenshots in the temp folder.</p>
            <button
              className="ghost"
              type="button"
              disabled={busy}
              onClick={() => confirmDeleteAll("existing", existingImages)}
            >
              Delete all
            </button>
          </div>
          <div className="screens-grid">
            {existingImages.map((image) => {
              const selected = finalPaths.has(image.Path);
              return (
                <div className="screens-thumb-card" key={`existing-${image.Path}`}>
                  <button
                    className="screens-thumb"
                    type="button"
                    onClick={() => openLocalImage(image, `Existing ${image.Index + 1}`)}
                  >
                    {renderLocalThumbnail(image, `Existing ${image.Index + 1}`)}
                  </button>
                  <button
                    className="ghost"
                    type="button"
                    disabled={busy || selected}
                    onClick={() => void facet.selectFinal(image.Path, true)}
                  >
                    {selected ? "Added" : "Add to final"}
                  </button>
                  <button
                    className="screens-thumb-delete"
                    type="button"
                    disabled={busy || !selected}
                    onClick={() => void facet.selectFinal(image.Path, false)}
                  >
                    Remove
                  </button>
                </div>
              );
            })}
          </div>
        </section>
      ) : null}

      {existingTrackerImages.length ? (
        <section className="panel screens-gallery">
          <div className="screens-gallery__header">
            <h2>Tracker Temp Images</h2>
            <p className="muted">Images stored in tracker temp folders.</p>
            <button
              className="ghost"
              type="button"
              disabled={busy}
              onClick={() => confirmDeleteAll("tracker", existingTrackerImages)}
            >
              Delete all
            </button>
          </div>
          <div className="screens-grid">
            {existingTrackerImages.map((image) => {
              const selected = finalPaths.has(image.Path);
              return (
                <div className="screens-thumb-card" key={`tracker-${image.Path}`}>
                  <button
                    className="screens-thumb"
                    type="button"
                    onClick={() => openLocalImage(image, "Tracker temp image")}
                  >
                    {renderLocalThumbnail(image, "Tracker temp screenshot")}
                  </button>
                  <button
                    className="ghost"
                    type="button"
                    disabled={busy || selected}
                    onClick={() => void facet.selectFinal(image.Path, true)}
                  >
                    {selected ? "Added" : "Add to final"}
                  </button>
                  <button
                    className="screens-thumb-delete"
                    type="button"
                    disabled={busy}
                    onClick={() => void facet.remove(image.Path)}
                  >
                    Delete
                  </button>
                </div>
              );
            })}
          </div>
        </section>
      ) : null}

      <section className="panel screens-list">
        <div className="screens-gallery__header">
          <h2>Frame Selection</h2>
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
      </section>

      {previewImages.length ? (
        <section className="panel screens-gallery">
          <div className="screens-gallery__header">
            <h2>Preview Captures</h2>
            <p className="muted">Click any image to view full size.</p>
            <button
              className="ghost"
              type="button"
              disabled={busy}
              onClick={() => confirmDeleteAll("preview", previewImages)}
            >
              Delete all
            </button>
          </div>
          <div className="screens-grid">
            {previewImages.map((image) => (
              <button
                className="screens-thumb"
                type="button"
                key={`preview-${image.Path}`}
                onClick={() => openLocalImage(image, `Preview ${image.Index + 1}`)}
              >
                {renderLocalThumbnail(image, `Preview ${image.Index + 1}`)}
              </button>
            ))}
          </div>
        </section>
      ) : null}

      {finalImages.length ? (
        <section className="panel screens-gallery">
          <div className="screens-gallery__header">
            <h2>Final Captures</h2>
            <p className="muted">Generated screenshots ready for upload.</p>
            <button
              className="ghost"
              type="button"
              disabled={busy}
              onClick={() => confirmDeleteAll("final", finalImages)}
            >
              Delete all
            </button>
          </div>
          <div className="screens-grid">
            {finalImages.map((image, index) => (
              <div className="screens-thumb-card" key={`final-${image.Path}`}>
                <button
                  className="screens-thumb"
                  type="button"
                  draggable
                  onDragStart={() => setFinalDragIndex(index)}
                  onDragOver={(event) => event.preventDefault()}
                  onDrop={(event) => {
                    event.preventDefault();
                    if (finalDragIndex !== null) void facet.reorderFinal(finalDragIndex, index);
                    setFinalDragIndex(null);
                  }}
                  onDragEnd={() => setFinalDragIndex(null)}
                  onClick={() => openLocalImage(image, `Screenshot ${index + 1}`)}
                >
                  {renderLocalThumbnail(image, `Screenshot ${index + 1}`)}
                </button>
                <button
                  className="screens-thumb-delete"
                  type="button"
                  disabled={busy}
                  onClick={() => void facet.remove(image.Path)}
                >
                  Delete
                </button>
              </div>
            ))}
          </div>
        </section>
      ) : null}

      {view.result?.Errors?.length ? (
        <section className="panel screens-errors">
          <div className="screens-gallery__header">
            <h2>Capture Warnings</h2>
          </div>
          <ul>
            {view.result.Errors.map((entry, index) => (
              <li key={`err-${entry.Index}-${index}`}>
                Shot {entry.Index + 1}: {entry.Message}
              </li>
            ))}
          </ul>
        </section>
      ) : null}
    </section>
  );
}
