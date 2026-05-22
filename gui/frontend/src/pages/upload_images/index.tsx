// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useMemo } from "react";
import type { Dispatch, SetStateAction } from "react";
import type {
  ScreenshotLinkedImage,
  ScreenshotPreviewImage,
  UploadedImageLink,
  UploadImageHostFailure,
} from "../../types";
import { handleExternalLinkClick } from "../../utils/externalLinks";
import "./styles.css";

type UploadedByHost = { host: string; items: UploadedImageLink[] };

type Props = Readonly<{
  path: string;
  uploadHost: string;
  setUploadHost: Dispatch<SetStateAction<string>>;
  configuredImageHosts: string[];
  resolveImageHostLabel: (value: string) => string;
  uploadImagesLoading: boolean;
  uploadProgress: { current: number; total: number };
  setAllUploadSelections: (value: boolean) => void;
  handleUploadImages: (selected: ScreenshotPreviewImage[]) => void;
  uploadImagesError: string;
  uploadImageFailures: UploadImageHostFailure[];
  uploadCandidates: ScreenshotPreviewImage[];
  uploadSelections: Record<string, boolean>;
  toggleUploadSelection: (imagePath: string) => void;
  setLightboxImage: Dispatch<SetStateAction<string>>;
  setLightboxAlt: Dispatch<SetStateAction<string>>;
  uploadedRecordByPath: Map<string, UploadedImageLink>;
  uploadedImages: UploadedImageLink[];
  uploadedImageRecords: UploadedImageLink[];
  trackerImageLinks: ScreenshotLinkedImage[];
  handleDeleteUploadedImage: (imagePath: string, host: string) => void;
}>;

export default function UploadImagesPage(props: Props) {
  const {
    path,
    uploadHost,
    setUploadHost,
    configuredImageHosts,
    resolveImageHostLabel,
    uploadImagesLoading,
    uploadProgress,
    setAllUploadSelections,
    handleUploadImages,
    uploadImagesError,
    uploadImageFailures,
    uploadCandidates,
    uploadSelections,
    toggleUploadSelection,
    setLightboxImage,
    setLightboxAlt,
    uploadedRecordByPath,
    uploadedImages,
    uploadedImageRecords,
    trackerImageLinks,
    handleDeleteUploadedImage,
  } = props;

  const uploadCandidateCount = uploadCandidates.length;

  const selectedUploadCandidates = useMemo(() => {
    return uploadCandidates.filter((item) => {
      const pathValue = item.image.Path;
      if (!pathValue) return false;
      if (uploadSelections[pathValue] === undefined) return true;
      return Boolean(uploadSelections[pathValue]);
    });
  }, [uploadCandidates, uploadSelections]);

  const uploadSelectedCount = selectedUploadCandidates.length;

  const previouslyUploadedImages = useMemo(() => {
    return [...uploadedImageRecords].sort((left, right) => {
      const leftTime = left.UploadedAt ? Date.parse(left.UploadedAt) : 0;
      const rightTime = right.UploadedAt ? Date.parse(right.UploadedAt) : 0;
      if (leftTime !== rightTime) return rightTime - leftTime;
      return left.ImagePath.localeCompare(right.ImagePath);
    });
  }, [uploadedImageRecords]);

  const previouslyUploadedByHost: UploadedByHost[] = useMemo(() => {
    const grouped = new Map<string, UploadedImageLink[]>();

    // Add actual uploaded images from database.
    previouslyUploadedImages.forEach((img) => {
      const hostKey = (img.Host || "unknown").trim() || "unknown";
      const bucket = grouped.get(hostKey) || [];
      bucket.push(img);
      grouped.set(hostKey, bucket);
    });

    // Add tracker images as if they were previously uploaded.
    trackerImageLinks.forEach((link) => {
      const hostKey = (link.Host || "unknown").trim() || "unknown";
      const bucket = grouped.get(hostKey) || [];
      const uploadedFormat: UploadedImageLink = {
        SourcePath: "",
        ImagePath: link.Path,
        Host: hostKey,
        UsageScope: "global",
        ImgURL: link.URL,
        RawURL: link.URL,
        WebURL: link.URL,
        SizeBytes: 0,
        UploadedAt: "",
      };
      bucket.push(uploadedFormat);
      grouped.set(hostKey, bucket);
    });

    const hostIndex = new Map<string, number>();
    configuredImageHosts.forEach((host, index) => {
      hostIndex.set(host, index);
    });
    return Array.from(grouped.entries())
      .map(([host, items]) => ({ host, items }))
      .sort((left, right) => {
        const leftRank = hostIndex.has(left.host)
          ? hostIndex.get(left.host)!
          : Number.MAX_SAFE_INTEGER;
        const rightRank = hostIndex.has(right.host)
          ? hostIndex.get(right.host)!
          : Number.MAX_SAFE_INTEGER;
        if (leftRank !== rightRank) return leftRank - rightRank;
        return left.host.localeCompare(right.host);
      });
  }, [configuredImageHosts, previouslyUploadedImages, trackerImageLinks]);

  return (
    <section className="upload-images-panel">
      <header className="upload-images-header">
        <p className="eyebrow">Image Hosting</p>
        <h1>Upload Images</h1>
        <p className="subtitle">
          Select screenshots and upload to every host needed by the active trackers.
        </p>
      </header>

      <section className="panel upload-images-controls">
        <div>
          <p className="label">Source path</p>
          <p className="value dupe-path">{path || "No path selected"}</p>
        </div>
        <div className="upload-images-controls__row">
          <label className="settings-field">
            <span>Default upload host</span>
            <select
              value={uploadHost}
              onChange={(event) => setUploadHost(event.target.value)}
              disabled={configuredImageHosts.length === 0}
            >
              {configuredImageHosts.length === 0 ? (
                <option value="">No hosts configured</option>
              ) : null}
              {configuredImageHosts.map((host) => (
                <option key={host} value={host}>
                  {resolveImageHostLabel(host)}
                </option>
              ))}
            </select>
          </label>
          <div className="upload-images-actions">
            <button className="ghost" type="button" onClick={() => setAllUploadSelections(true)}>
              Select all
            </button>
            <button className="ghost" type="button" onClick={() => setAllUploadSelections(false)}>
              Select none
            </button>
            <button
              className="primary"
              type="button"
              onClick={() => handleUploadImages(selectedUploadCandidates)}
              disabled={uploadImagesLoading || uploadSelectedCount === 0 || !uploadHost}
            >
              {uploadImagesLoading ? "Uploading..." : `Upload ${uploadSelectedCount}`}
            </button>
          </div>
        </div>
        <div className="upload-images-meta">
          <span className="muted">Available: {uploadCandidateCount}</span>
          <span className="muted">Selected: {uploadSelectedCount}</span>
        </div>
        {uploadImagesLoading && uploadProgress.total > 0 ? (
          <div className="upload-progress-container">
            <div className="upload-progress-bar-wrapper">
              <div
                className="upload-progress-bar"
                style={{
                  width: `${Math.round((uploadProgress.current / uploadProgress.total) * 100)}%`,
                }}
              />
            </div>
            <p className="upload-progress-text">
              Uploading... {uploadProgress.current} of {uploadProgress.total}
            </p>
          </div>
        ) : null}
        {uploadImagesError ? <p className="error">{uploadImagesError}</p> : null}
        {uploadImageFailures.length > 0 ? (
          <div className="upload-images-failures">
            {uploadImageFailures.map((failure, index) => {
              const trackers = (failure.Trackers || []).filter(Boolean);
              const trackerLabel = trackers.length > 0 ? ` Blocks: ${trackers.join(", ")}.` : "";
              return (
                <p className="error" key={`${failure.Host || "host"}-${index}`}>
                  {failure.Host || "Image host"} failed: {failure.Message || "upload failed"}.
                  {trackerLabel}
                </p>
              );
            })}
          </div>
        ) : null}
      </section>

      {uploadCandidateCount === 0 ? (
        <section className="panel upload-images-empty">
          <p className="muted">No screenshots available yet. Generate screenshots first.</p>
        </section>
      ) : (
        <section className="panel screens-gallery">
          <div className="screens-gallery__header">
            <h2>Available Images</h2>
            <p className="muted">Click a thumbnail to preview. Toggle to include in upload.</p>
          </div>
          <div className="screens-grid upload-images-grid">
            {uploadCandidates.map((item) => {
              const pathValue = item.image.Path;
              const selected = pathValue ? uploadSelections[pathValue] !== false : false;
              const record = pathValue ? uploadedRecordByPath.get(pathValue) : undefined;
              const hostLabel = item.image.Host || record?.Host;
              const imgLink = item.image.ImgURL || record?.ImgURL;
              const rawLink = item.image.RawURL || record?.RawURL;
              const isUploaded = Boolean(hostLabel && rawLink);
              return (
                <div
                  className={`upload-images-card ${selected ? "selected" : ""} ${isUploaded ? "uploaded" : ""}`}
                  key={`upload-${pathValue || item.image.Index}`}
                >
                  <button
                    className="screens-thumb"
                    type="button"
                    onClick={() => {
                      setLightboxImage(item.dataUri);
                      setLightboxAlt("Upload candidate");
                    }}
                  >
                    <img src={item.dataUri} alt="Upload candidate" />
                    {isUploaded ? (
                      <span className="upload-badge" title={`Already uploaded to ${hostLabel}`}>
                        ✓ {hostLabel}
                      </span>
                    ) : null}
                  </button>
                  <button
                    className={`upload-images-toggle ${selected ? "selected" : ""}`}
                    type="button"
                    onClick={() => pathValue && toggleUploadSelection(pathValue)}
                  >
                    {selected ? "Selected" : "Select"}
                  </button>
                  {isUploaded && imgLink ? (
                    <div className="upload-links">
                      <a
                        className="upload-link"
                        href={imgLink}
                        target="_blank"
                        rel="noreferrer"
                        title="View image"
                        onClick={handleExternalLinkClick}
                      >
                        🔗
                      </a>
                    </div>
                  ) : null}
                </div>
              );
            })}
          </div>
        </section>
      )}

      {previouslyUploadedByHost.length > 0 ? (
        <section className="panel upload-images-results">
          <div className="screens-gallery__header">
            <h2>Previously Uploaded Images</h2>
            <p className="muted">
              Images from previous uploads and tracker descriptions. Click delete to remove from
              database.
            </p>
          </div>
          <div className="upload-images-results__hosts">
            {previouslyUploadedByHost.map((group) => (
              <div className="upload-images-host" key={`prev-host-${group.host}`}>
                <h3 className="upload-images-host__title">{resolveImageHostLabel(group.host)}</h3>
                <div className="upload-images-results__grid">
                  {group.items.map((img, index) => (
                    <div
                      className="upload-images-result"
                      key={`prev-uploaded-${img.ImagePath}-${img.Host}-${index}`}
                    >
                      <p className="label">Image</p>
                      <p className="value mono">{img.ImagePath || "Unknown"}</p>
                      {img.ImgURL ? (
                        <a
                          className="tracker-link"
                          href={img.ImgURL}
                          target="_blank"
                          rel="noreferrer"
                          onClick={handleExternalLinkClick}
                        >
                          View image
                        </a>
                      ) : null}
                      {img.RawURL ? (
                        <a
                          className="tracker-link"
                          href={img.RawURL}
                          target="_blank"
                          rel="noreferrer"
                          onClick={handleExternalLinkClick}
                        >
                          Raw URL
                        </a>
                      ) : null}
                      {img.WebURL ? (
                        <a
                          className="tracker-link"
                          href={img.WebURL}
                          target="_blank"
                          rel="noreferrer"
                          onClick={handleExternalLinkClick}
                        >
                          Web URL
                        </a>
                      ) : null}
                      <button
                        className="danger"
                        type="button"
                        onClick={() => handleDeleteUploadedImage(img.ImagePath, img.Host)}
                      >
                        Delete
                      </button>
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </section>
      ) : null}

      {uploadedImages.length > 0 ? (
        <section className="panel upload-images-results">
          <div className="screens-gallery__header">
            <h2>Upload Results</h2>
            <p className="muted">Links returned from the image host.</p>
          </div>
          <div className="upload-images-results__grid">
            {uploadedImages.map((image, index) => (
              <div className="upload-images-result" key={`uploaded-${image.ImagePath}-${index}`}>
                <p className="label">Image</p>
                <p className="value mono">{image.ImagePath || "Unknown"}</p>
                <p className="label">Host</p>
                <p className="value mono">{image.Host || uploadHost}</p>
                {image.ImgURL ? (
                  <a
                    className="tracker-link"
                    href={image.ImgURL}
                    target="_blank"
                    rel="noreferrer"
                    onClick={handleExternalLinkClick}
                  >
                    View image
                  </a>
                ) : null}
                {image.RawURL ? (
                  <a
                    className="tracker-link"
                    href={image.RawURL}
                    target="_blank"
                    rel="noreferrer"
                    onClick={handleExternalLinkClick}
                  >
                    Raw URL
                  </a>
                ) : null}
                {image.WebURL ? (
                  <a
                    className="tracker-link"
                    href={image.WebURL}
                    target="_blank"
                    rel="noreferrer"
                    onClick={handleExternalLinkClick}
                  >
                    Web URL
                  </a>
                ) : null}
                <button
                  className="danger"
                  type="button"
                  onClick={() =>
                    handleDeleteUploadedImage(image.ImagePath, image.Host || uploadHost)
                  }
                >
                  Delete
                </button>
              </div>
            ))}
          </div>
        </section>
      ) : null}
    </section>
  );
}
