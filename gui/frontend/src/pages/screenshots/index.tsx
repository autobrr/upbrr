// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { Dispatch, SetStateAction } from "react";
import type {
  ConfigMap,
  ConfigValue,
  ScreenshotImage,
  ScreenshotPlan,
  ScreenshotPreviewImage,
  ScreenshotResult,
  ScreenshotSelection,
} from "../../types";

type Props = Readonly<{
  path: string;
  screenshotPlan: ScreenshotPlan | null;
  screenshotsLoading: boolean;
  screenshotsError: string;
  screenshotsEnabled: boolean;
  setScreenshotsEnabled: Dispatch<SetStateAction<boolean>>;
  loadScreenshotPlan: (revealSelections?: boolean) => void;
  handleGenerateScreenshots: () => void;
  screenshotConfig: ConfigMap | null;
  updateScreenshotConfigValue: (key: string, value: ConfigValue) => void;
  loadSettings: () => void;
  settingsLoading: boolean;
  applyScreenshotSettings: () => void;
  settingsDirty: boolean;
  screenshotsSettingsSaving: boolean;
  livePreviewSeconds: number;
  setLivePreviewSeconds: Dispatch<SetStateAction<number>>;
  livePreviewFrame: number;
  previewDuration: number;
  previewFrameRate: number;
  clampPreviewSeconds: (value: number) => number;
  stepLivePreview: (direction: number) => void;
  runLivePreview: () => void;
  livePreviewLoading: boolean;
  liveCaptureLoading: boolean;
  handleCapturePreviewFrame: () => void;
  livePreviewError: string;
  livePreviewImage: string;
  setLightboxImage: Dispatch<SetStateAction<string>>;
  setLightboxAlt: Dispatch<SetStateAction<string>>;
  trackerImageURLs: string[];
  handleDeleteAllTrackerImageURLs: () => void;
  handleDeleteTrackerImage: (url: string) => void;
  existingImages: ScreenshotPreviewImage[];
  addFinalSelection: (item: ScreenshotPreviewImage) => void;
  isFinalImageSelected: (pathValue: string) => boolean;
  removeFinalSelection: (imagePath: string) => void;
  handleDeleteAllExistingImages: () => void;
  existingTrackerImages: ScreenshotPreviewImage[];
  handleDeleteAllTrackerImages: () => void;
  handleDeleteExistingImage: (image: ScreenshotImage) => void;
  showFrameSelections: boolean;
  screenshotSelections: ScreenshotSelection[];
  updateSelectionTime: (index: number, value: string) => void;
  updateSelectionFrame: (index: number, value: string) => void;
  handlePreviewSelection: (selection: ScreenshotSelection) => void;
  previewLoadingIndex: number | null;
  previewImages: ScreenshotPreviewImage[];
  handleDeleteAllPreviewImages: () => void;
  finalImages: ScreenshotPreviewImage[];
  finalDragIndex: number | null;
  setFinalDragIndex: Dispatch<SetStateAction<number | null>>;
  reorderFinalSelections: (fromIndex: number, toIndex: number) => void;
  finalResult: ScreenshotResult | null;
  handleDeleteAllFinalImages: () => void;
}>;

export default function ScreenshotsPage(props: Props) {
  const {
    path,
    screenshotPlan,
    screenshotsLoading,
    screenshotsError,
    screenshotsEnabled,
    setScreenshotsEnabled,
    loadScreenshotPlan,
    handleGenerateScreenshots,
    screenshotConfig,
    updateScreenshotConfigValue,
    loadSettings,
    settingsLoading,
    applyScreenshotSettings,
    settingsDirty,
    screenshotsSettingsSaving,
    livePreviewSeconds,
    setLivePreviewSeconds,
    livePreviewFrame,
    previewDuration,
    previewFrameRate,
    clampPreviewSeconds,
    stepLivePreview,
    runLivePreview,
    livePreviewLoading,
    liveCaptureLoading,
    handleCapturePreviewFrame,
    livePreviewError,
    livePreviewImage,
    setLightboxImage,
    setLightboxAlt,
    trackerImageURLs,
    handleDeleteAllTrackerImageURLs,
    handleDeleteTrackerImage,
    existingImages,
    addFinalSelection,
    isFinalImageSelected,
    removeFinalSelection,
    handleDeleteAllExistingImages,
    existingTrackerImages,
    handleDeleteAllTrackerImages,
    handleDeleteExistingImage,
    showFrameSelections,
    screenshotSelections,
    updateSelectionTime,
    updateSelectionFrame,
    handlePreviewSelection,
    previewLoadingIndex,
    previewImages,
    handleDeleteAllPreviewImages,
    finalImages,
    finalDragIndex,
    setFinalDragIndex,
    reorderFinalSelections,
    finalResult,
    handleDeleteAllFinalImages,
  } = props;

  return (
    <section className="screens-panel">
      <header className="screens-header">
        <p className="eyebrow">Screenshots</p>
        <h1>Plan & Capture</h1>
        <p className="subtitle">
          Review tracker images, adjust frame times, and generate screenshots.
        </p>
      </header>

      <section className="panel screens-actions">
        <div>
          <p className="label">Source path</p>
          <p className="value dupe-path">{path || "No path selected"}</p>
          {screenshotPlan ? (
            <div className="screens-meta">
              <p className="muted">Duration: {screenshotPlan.DurationSeconds.toFixed(1)}s</p>
              <p className="muted">Frame rate: {screenshotPlan.FrameRate.toFixed(3)}</p>
              {screenshotPlan.DiscType ? (
                <p className="muted">Disc type: {screenshotPlan.DiscType}</p>
              ) : null}
            </div>
          ) : null}
        </div>
        <div className="screens-actions__buttons">
          <label className="screens-toggle">
            <input
              type="checkbox"
              checked={screenshotsEnabled}
              onChange={(event) => setScreenshotsEnabled(event.target.checked)}
            />
            <span>Enable capture</span>
          </label>
          <button
            className="ghost"
            type="button"
            onClick={() => loadScreenshotPlan(true)}
            disabled={screenshotsLoading || !path.trim()}
          >
            {screenshotsLoading ? "Loading..." : "Load suggestions"}
          </button>
          <button
            className="primary"
            type="button"
            onClick={handleGenerateScreenshots}
            disabled={screenshotsLoading || !path.trim() || !screenshotsEnabled}
          >
            {screenshotsLoading ? "Capturing..." : "Generate screenshots"}
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
              <label className="settings-toggle">
                <span>Tonemap HDR</span>
                <input
                  type="checkbox"
                  checked={Boolean(screenshotConfig.ToneMap)}
                  onChange={(event) => updateScreenshotConfigValue("ToneMap", event.target.checked)}
                />
                <span className="settings-toggle__pill" />
              </label>
              <label className="settings-toggle">
                <span>Use libplacebo</span>
                <input
                  type="checkbox"
                  checked={Boolean(screenshotConfig.UseLibplacebo)}
                  onChange={(event) =>
                    updateScreenshotConfigValue("UseLibplacebo", event.target.checked)
                  }
                />
                <span className="settings-toggle__pill" />
              </label>
              <label className="settings-toggle">
                <span>Frame overlay</span>
                <input
                  type="checkbox"
                  checked={Boolean(screenshotConfig.FrameOverlay)}
                  onChange={(event) =>
                    updateScreenshotConfigValue("FrameOverlay", event.target.checked)
                  }
                />
                <span className="settings-toggle__pill" />
              </label>
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
              <label className="settings-toggle">
                <span>Limit ffmpeg concurrency</span>
                <input
                  type="checkbox"
                  checked={Boolean(screenshotConfig.FFmpegLimit)}
                  onChange={(event) =>
                    updateScreenshotConfigValue("FFmpegLimit", event.target.checked)
                  }
                />
                <span className="settings-toggle__pill" />
              </label>
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
              disabled={settingsLoading || screenshotsSettingsSaving || !settingsDirty}
            >
              {screenshotsSettingsSaving ? "Applying..." : "Apply settings"}
            </button>
          </div>
        </details>
      </section>

      {screenshotsError ? <p className="error">{screenshotsError}</p> : null}

      {screenshotPlan?.RequiresManualFrames ? (
        <p className="muted">
          Duration or frame rate is missing. Enter manual frame times before capturing.
        </p>
      ) : null}

      <section className="panel screens-preview">
        <div className="screens-gallery__header">
          <h2>Live Preview</h2>
          <p className="muted">Scrub the timeline and capture the current frame.</p>
        </div>
        {screenshotPlan ? (
          <div className="screens-preview__body">
            <div className="screens-preview__controls">
              <label className="screens-field">
                <span>Seconds</span>
                <input
                  type="number"
                  step="0.1"
                  value={Number.isFinite(livePreviewSeconds) ? livePreviewSeconds : 0}
                  onChange={(event) =>
                    setLivePreviewSeconds(clampPreviewSeconds(Number(event.target.value)))
                  }
                  disabled={!screenshotsEnabled}
                />
              </label>
              <label className="screens-field">
                <span>Frame</span>
                <input
                  type="number"
                  step="1"
                  value={livePreviewFrame}
                  onChange={(event) => {
                    const nextFrame = Number(event.target.value);
                    if (Number.isFinite(nextFrame) && previewFrameRate > 0) {
                      setLivePreviewSeconds(clampPreviewSeconds(nextFrame / previewFrameRate));
                    } else {
                      setLivePreviewSeconds(0);
                    }
                  }}
                  disabled={!screenshotsEnabled}
                />
              </label>
              <div className="screens-preview__slider">
                <input
                  type="range"
                  min={0}
                  max={Math.max(previewDuration, 0)}
                  step={1 / previewFrameRate}
                  value={clampPreviewSeconds(livePreviewSeconds)}
                  onChange={(event) =>
                    setLivePreviewSeconds(clampPreviewSeconds(Number(event.target.value)))
                  }
                  disabled={!screenshotsEnabled || previewDuration <= 0}
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
                  disabled={!screenshotsEnabled}
                >
                  Prev frame
                </button>
                <button
                  className="ghost"
                  type="button"
                  onClick={() => stepLivePreview(1)}
                  disabled={!screenshotsEnabled}
                >
                  Next frame
                </button>
                <button
                  className="ghost"
                  type="button"
                  onClick={runLivePreview}
                  disabled={!screenshotsEnabled || livePreviewLoading}
                >
                  {livePreviewLoading ? "Loading..." : "Run preview"}
                </button>
                <button
                  className="primary"
                  type="button"
                  onClick={handleCapturePreviewFrame}
                  disabled={!screenshotsEnabled || liveCaptureLoading}
                >
                  {liveCaptureLoading ? "Capturing..." : "Capture preview"}
                </button>
              </div>
            </div>
            {livePreviewError ? <p className="error">{livePreviewError}</p> : null}
            <div className="screens-preview__image">
              {livePreviewImage ? (
                <div style={{ position: "relative" }}>
                  <button
                    className="screens-thumb"
                    type="button"
                    onClick={() => {
                      setLightboxImage(livePreviewImage);
                      setLightboxAlt("Live preview");
                    }}
                    style={livePreviewLoading ? { opacity: 0.6 } : {}}
                  >
                    <img src={livePreviewImage} alt="Live preview" />
                  </button>
                  {livePreviewLoading && (
                    <div
                      style={{
                        position: "absolute",
                        top: "50%",
                        left: "50%",
                        transform: "translate(-50%, -50%)",
                        pointerEvents: "none",
                        color: "white",
                        textShadow: "0 0 4px black",
                        fontWeight: "bold",
                      }}
                    >
                      Loading...
                    </div>
                  )}
                </div>
              ) : livePreviewLoading ? (
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

      {trackerImageURLs.length > 0 ? (
        <section className="panel screens-gallery">
          <div className="screens-gallery__header">
            <h2>Tracker Images</h2>
            <p className="muted">Already available from tracker data.</p>
            <button className="ghost" type="button" onClick={handleDeleteAllTrackerImageURLs}>
              Delete all
            </button>
          </div>
          <div className="screens-grid">
            {trackerImageURLs.map((url, index) => (
              <div className="screens-thumb-card" key={`${url}-${index}`}>
                <button
                  className="screens-thumb"
                  type="button"
                  onClick={() => {
                    setLightboxImage(url);
                    setLightboxAlt("Tracker image");
                  }}
                >
                  <img src={url} alt="Tracker screenshot" loading="lazy" />
                </button>
                <button
                  className="screens-thumb-delete"
                  type="button"
                  onClick={() => handleDeleteTrackerImage(url)}
                >
                  Delete
                </button>
              </div>
            ))}
          </div>
        </section>
      ) : null}

      {existingImages.length > 0 ? (
        <section className="panel screens-gallery">
          <div className="screens-gallery__header">
            <h2>Existing Captures</h2>
            <p className="muted">Previously generated screenshots in the temp folder.</p>
            <button className="ghost" type="button" onClick={handleDeleteAllExistingImages}>
              Delete all
            </button>
          </div>
          <div className="screens-grid">
            {existingImages.map((item) => (
              <div
                className="screens-thumb-card"
                key={`existing-${item.image.Path || item.image.Index}`}
              >
                <button
                  className="screens-thumb"
                  type="button"
                  onClick={() => {
                    setLightboxImage(item.dataUri);
                    setLightboxAlt(`Existing ${item.image.Index + 1}`);
                  }}
                >
                  <img src={item.dataUri} alt={`Existing ${item.image.Index + 1}`} />
                </button>
                <button
                  className="ghost"
                  type="button"
                  onClick={() => addFinalSelection(item)}
                  disabled={isFinalImageSelected(item.image.Path)}
                >
                  {isFinalImageSelected(item.image.Path) ? "Added" : "Add to final"}
                </button>
                <button
                  className="screens-thumb-delete"
                  type="button"
                  onClick={() => removeFinalSelection(item.image.Path)}
                >
                  Remove
                </button>
              </div>
            ))}
          </div>
        </section>
      ) : null}

      {existingTrackerImages.length > 0 ? (
        <section className="panel screens-gallery">
          <div className="screens-gallery__header">
            <h2>Tracker Temp Images</h2>
            <p className="muted">Images stored in tracker temp folders.</p>
            <button className="ghost" type="button" onClick={handleDeleteAllTrackerImages}>
              Delete all
            </button>
          </div>
          <div className="screens-grid">
            {existingTrackerImages.map((item) => (
              <div
                className="screens-thumb-card"
                key={`tracker-${item.image.Path || item.image.Index}`}
              >
                <button
                  className="screens-thumb"
                  type="button"
                  onClick={() => {
                    setLightboxImage(item.dataUri);
                    setLightboxAlt("Tracker temp image");
                  }}
                >
                  <img src={item.dataUri} alt="Tracker temp screenshot" />
                </button>
                <button
                  className="ghost"
                  type="button"
                  onClick={() => addFinalSelection(item)}
                  disabled={isFinalImageSelected(item.image.Path)}
                >
                  {isFinalImageSelected(item.image.Path) ? "Added" : "Add to final"}
                </button>
                <button
                  className="screens-thumb-delete"
                  type="button"
                  onClick={() => handleDeleteExistingImage(item.image)}
                >
                  Delete
                </button>
              </div>
            ))}
          </div>
        </section>
      ) : null}

      <section className="panel screens-list">
        <div className="screens-gallery__header">
          <h2>Frame Selection</h2>
          <p className="muted">Adjust timestamps or frame numbers, then preview.</p>
        </div>
        {!showFrameSelections ? (
          <p className="muted">Load suggestions to edit frame selections.</p>
        ) : screenshotSelections.length === 0 ? (
          <p className="muted">No selections available yet.</p>
        ) : (
          <div className="screens-rows">
            {screenshotSelections.map((selection) => (
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
                    value={
                      Number.isFinite(selection.TimestampSeconds) ? selection.TimestampSeconds : 0
                    }
                    onChange={(event) => updateSelectionTime(selection.Index, event.target.value)}
                  />
                </label>
                <label className="screens-field">
                  <span>Frame</span>
                  <input
                    type="number"
                    step="1"
                    value={Number.isFinite(selection.Frame) ? selection.Frame : 0}
                    onChange={(event) => updateSelectionFrame(selection.Index, event.target.value)}
                  />
                </label>
                <button
                  className="ghost"
                  type="button"
                  onClick={() => handlePreviewSelection(selection)}
                  disabled={!screenshotsEnabled || previewLoadingIndex === selection.Index}
                >
                  {previewLoadingIndex === selection.Index ? "Previewing..." : "Preview"}
                </button>
              </div>
            ))}
          </div>
        )}
      </section>

      {previewImages.length > 0 ? (
        <section className="panel screens-gallery">
          <div className="screens-gallery__header">
            <h2>Preview Captures</h2>
            <p className="muted">Click any image to view full size.</p>
            <button className="ghost" type="button" onClick={handleDeleteAllPreviewImages}>
              Delete all
            </button>
          </div>
          <div className="screens-grid">
            {previewImages.map((item) => (
              <button
                className="screens-thumb"
                type="button"
                key={`preview-${item.image.Index}`}
                onClick={() => {
                  setLightboxImage(item.dataUri);
                  setLightboxAlt(`Preview ${item.image.Index + 1}`);
                }}
              >
                <img src={item.dataUri} alt={`Preview ${item.image.Index + 1}`} />
              </button>
            ))}
          </div>
        </section>
      ) : null}

      {finalImages.length > 0 ? (
        <section className="panel screens-gallery">
          <div className="screens-gallery__header">
            <h2>Final Captures</h2>
            <p className="muted">Generated screenshots ready for upload.</p>
            <button className="ghost" type="button" onClick={handleDeleteAllFinalImages}>
              Delete all
            </button>
          </div>
          <div className="screens-grid">
            {finalImages.map((item, index) => (
              <div
                className="screens-thumb-card"
                key={`final-${item.image.Path || item.image.Index}`}
              >
                <button
                  className="screens-thumb"
                  type="button"
                  draggable
                  onDragStart={() => setFinalDragIndex(index)}
                  onDragOver={(event) => event.preventDefault()}
                  onDrop={(event) => {
                    event.preventDefault();
                    if (finalDragIndex === null) return;
                    reorderFinalSelections(finalDragIndex, index);
                    setFinalDragIndex(null);
                  }}
                  onDragEnd={() => setFinalDragIndex(null)}
                  onClick={() => {
                    setLightboxImage(item.dataUri);
                    setLightboxAlt(`Screenshot ${index + 1}`);
                  }}
                >
                  <img src={item.dataUri} alt={`Screenshot ${index + 1}`} />
                </button>
                <button
                  className="screens-thumb-delete"
                  type="button"
                  onClick={() => handleDeleteExistingImage(item.image)}
                >
                  Delete
                </button>
              </div>
            ))}
          </div>
        </section>
      ) : null}

      {finalResult?.Errors?.length ? (
        <section className="panel screens-errors">
          <div className="screens-gallery__header">
            <h2>Capture Warnings</h2>
          </div>
          <ul>
            {finalResult.Errors.map((entry, index) => (
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
