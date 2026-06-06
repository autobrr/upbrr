// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useMemo } from "react";
import type { Dispatch, SetStateAction } from "react";
import { Button } from "../../components/ui/button";
import type {
  ScreenshotLinkedImage,
  ScreenshotPreviewImage,
  UploadedImageLink,
  UploadImageHostFailure,
} from "../../types";
import { cn } from "../../utils/cn";
import { handleExternalLinkClick } from "../../utils/externalLinks";

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
  trackerImageURLs: string[];
  handleDeleteUploadedImage: (imagePath: string, host: string) => void;
  handleDeleteTrackerImage: (url: string) => void;
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
    trackerImageURLs,
    handleDeleteUploadedImage,
    handleDeleteTrackerImage,
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

    const trackerURLs = new Set<string>();

    // Add downloaded tracker images as if they were previously uploaded.
    trackerImageLinks.forEach((link) => {
      if (link.URL) {
        trackerURLs.add(link.URL);
      }
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

    // Add remote tracker images even when local artifact download was rejected.
    trackerImageURLs.forEach((url) => {
      if (!url || trackerURLs.has(url)) {
        return;
      }
      let hostKey = "tracker";
      try {
        hostKey = new URL(url).hostname || hostKey;
      } catch {
        // Keep generic tracker bucket for malformed URLs.
      }
      const bucket = grouped.get(hostKey) || [];
      bucket.push({
        SourcePath: "",
        ImagePath: url,
        Host: hostKey,
        UsageScope: "global",
        ImgURL: url,
        RawURL: url,
        WebURL: url,
        SizeBytes: 0,
        UploadedAt: "",
      });
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
  }, [configuredImageHosts, previouslyUploadedImages, trackerImageLinks, trackerImageURLs]);

  return (
    <section className="grid gap-3">
      <header className="max-w-3xl">
        <p className="eyebrow">Image Hosting</p>
        <h1>Upload Images</h1>
        <p className="subtitle">
          Select screenshots and upload to every host needed by the active trackers.
        </p>
      </header>

      <section className="panel grid gap-2.5">
        <div className="min-w-0">
          <p className="label">Source path</p>
          <p className="value [overflow-wrap:anywhere] text-sm">{path || "No path selected"}</p>
        </div>
        <div className="grid items-end gap-3 md:grid-cols-[minmax(220px,1fr)_auto]">
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
          <div className="flex flex-wrap items-center gap-2">
            <Button type="button" onClick={() => setAllUploadSelections(true)}>
              Select all
            </Button>
            <Button type="button" onClick={() => setAllUploadSelections(false)}>
              Select none
            </Button>
            <Button
              variant="primary"
              type="button"
              onClick={() => handleUploadImages(selectedUploadCandidates)}
              disabled={uploadImagesLoading || uploadSelectedCount === 0 || !uploadHost}
            >
              {uploadImagesLoading ? "Uploading..." : `Upload ${uploadSelectedCount}`}
            </Button>
          </div>
        </div>
        <div className="flex flex-wrap gap-3">
          <span className="muted">Available: {uploadCandidateCount}</span>
          <span className="muted">Selected: {uploadSelectedCount}</span>
        </div>
        {uploadImagesLoading && uploadProgress.total > 0 ? (
          <div className="grid gap-1.5">
            <div className="h-4 w-full overflow-hidden rounded-full border border-white/10 bg-white/10">
              <div
                className="h-full rounded-full bg-[var(--accent-2)] transition-[width]"
                style={{
                  width: `${Math.round((uploadProgress.current / uploadProgress.total) * 100)}%`,
                }}
              />
            </div>
            <p className="m-0 text-center text-sm text-[var(--muted)]">
              Uploading... {uploadProgress.current} of {uploadProgress.total}
            </p>
          </div>
        ) : null}
        {uploadImagesError ? <p className="error">{uploadImagesError}</p> : null}
        {uploadImageFailures.length > 0 ? (
          <div className="grid gap-1">
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
        <section className="panel">
          <p className="muted">No screenshots available yet. Generate screenshots first.</p>
        </section>
      ) : (
        <section className="panel grid gap-2.5">
          <div className="flex flex-wrap items-baseline justify-between gap-2">
            <h2>Available Images</h2>
            <p className="muted">Click a thumbnail to preview. Toggle to include in upload.</p>
          </div>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-2">
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
                  className="relative grid gap-1.5"
                  key={`upload-${pathValue || item.image.Index}`}
                >
                  <button
                    className={cn(
                      "screens-thumb",
                      selected &&
                        "border-[var(--accent-2)] shadow-[0_0_0_2px_rgba(53,194,193,0.2)]",
                      isUploaded && "border-emerald-500/60 opacity-85",
                    )}
                    type="button"
                    onClick={() => {
                      setLightboxImage(item.dataUri);
                      setLightboxAlt("Upload candidate");
                    }}
                  >
                    <img src={item.dataUri} alt="Upload candidate" />
                    {isUploaded ? (
                      <span
                        className="pointer-events-none absolute right-1.5 top-1.5 rounded bg-emerald-800 px-1.5 py-1 text-xs font-semibold text-slate-50"
                        title={`Already uploaded to ${hostLabel}`}
                      >
                        Uploaded {hostLabel}
                      </span>
                    ) : null}
                  </button>
                  <Button
                    className={cn(
                      "h-7 text-xs",
                      selected && "border-[var(--accent-2)] bg-[rgba(53,194,193,0.18)]",
                    )}
                    type="button"
                    onClick={() => pathValue && toggleUploadSelection(pathValue)}
                  >
                    {selected ? "Selected" : "Select"}
                  </Button>
                  {isUploaded && imgLink ? (
                    <div className="flex justify-center gap-1.5">
                      <a
                        className="inline-flex h-7 items-center justify-center rounded-md border border-white/10 bg-white/5 px-2 text-xs no-underline hover:bg-white/10"
                        href={imgLink}
                        target="_blank"
                        rel="noreferrer"
                        title="View image"
                        onClick={handleExternalLinkClick}
                      >
                        View
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
        <section className="panel grid gap-2.5">
          <div className="flex flex-wrap items-baseline justify-between gap-2">
            <h2>Previously Uploaded Images</h2>
            <p className="muted">
              Images from previous uploads and tracker descriptions. Click delete to remove from
              database.
            </p>
          </div>
          <div className="grid gap-3">
            {previouslyUploadedByHost.map((group) => (
              <div className="grid gap-2" key={`prev-host-${group.host}`}>
                <h3 className="m-0 text-xs font-semibold uppercase tracking-[0.12em] text-[var(--text)]">
                  {resolveImageHostLabel(group.host)}
                </h3>
                <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-2">
                  {group.items.map((img, index) => (
                    <div
                      className="grid min-w-0 gap-1 rounded-lg border border-white/10 bg-[rgba(12,16,26,0.78)] p-2 [&_.tracker-link]:[overflow-wrap:anywhere] [&_.value]:[overflow-wrap:anywhere]"
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
                      {img.SourcePath ? (
                        <button
                          className="danger justify-self-start"
                          type="button"
                          onClick={() => handleDeleteUploadedImage(img.ImagePath, img.Host)}
                        >
                          Delete
                        </button>
                      ) : (
                        <button
                          className="danger justify-self-start"
                          type="button"
                          onClick={() => handleDeleteTrackerImage(img.RawURL || img.ImgURL)}
                        >
                          Remove
                        </button>
                      )}
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </section>
      ) : null}

      {uploadedImages.length > 0 ? (
        <section className="panel grid gap-2.5">
          <div className="flex flex-wrap items-baseline justify-between gap-2">
            <h2>Upload Results</h2>
            <p className="muted">Links returned from the image host.</p>
          </div>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-2">
            {uploadedImages.map((image, index) => (
              <div
                className="grid min-w-0 gap-1 rounded-lg border border-white/10 bg-[rgba(12,16,26,0.78)] p-2 [&_.tracker-link]:[overflow-wrap:anywhere] [&_.value]:[overflow-wrap:anywhere]"
                key={`uploaded-${image.ImagePath}-${index}`}
              >
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
                  className="danger justify-self-start"
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
