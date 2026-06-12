// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useState, useCallback, useMemo, useEffect } from "react";
import type {
  ScreenshotPreviewImage,
  UploadedImageLink,
  UploadImageHostFailure,
  ExternalIDOverrides,
  ReleaseNameOverrides,
} from "../types";
import { normalizeOverrides, normalizeReleaseOverrides } from "../utils";

interface UploadImagesHookProps {
  path: string;
  idOverrideState?: { overrides?: ExternalIDOverrides };
  releaseOverrideState?: { overrides?: ReleaseNameOverrides };
  uploadCandidates?: ScreenshotPreviewImage[];
  configuredImageHosts?: string[];
  selectedTrackers?: string[];
}

export const useUploadImages = ({
  path,
  idOverrideState,
  releaseOverrideState,
  uploadCandidates = [],
  configuredImageHosts = [],
  selectedTrackers = [],
}: UploadImagesHookProps) => {
  // State: Host & selection
  const [uploadHost, setUploadHost] = useState<string>("");
  const [uploadSelections, setUploadSelections] = useState<Record<string, boolean>>({});

  // State: Upload progress & results
  const [uploadImagesLoading, setUploadImagesLoading] = useState(false);
  const [uploadImagesError, setUploadImagesError] = useState("");
  const [uploadImageFailures, setUploadImageFailures] = useState<UploadImageHostFailure[]>([]);
  const [uploadedImages, setUploadedImages] = useState<UploadedImageLink[]>([]);
  const [uploadedImageRecords, setUploadedImageRecords] = useState<UploadedImageLink[]>([]);
  const [uploadProgress, setUploadProgress] = useState<{ current: number; total: number }>({
    current: 0,
    total: 0,
  });

  // Build set of upload candidate paths for filtering
  const uploadCandidatePaths = useMemo(
    () => new Set(uploadCandidates.map((item) => item.image.Path)),
    [uploadCandidates],
  );

  // Build record-by-path map for display
  const uploadedRecordByPath = useMemo(() => {
    const map = new Map<string, UploadedImageLink>();
    uploadedImageRecords.forEach((record) => {
      if (record.ImagePath) {
        const existing = map.get(record.ImagePath);
        if (existing) {
          const existingTime = existing.UploadedAt ? Date.parse(existing.UploadedAt) : 0;
          const recordTime = record.UploadedAt ? Date.parse(record.UploadedAt) : 0;
          if (recordTime > existingTime) {
            map.set(record.ImagePath, record);
          }
        } else {
          map.set(record.ImagePath, record);
        }
      }
    });
    return map;
  }, [uploadedImageRecords]);

  // Initialize host if not set and candidates exist
  useEffect(() => {
    if (uploadHost || configuredImageHosts.length === 0) return;
    setUploadHost(configuredImageHosts[0]);
  }, [configuredImageHosts, uploadHost]);

  // Initialize selections when candidates change
  useEffect(() => {
    if (uploadCandidates.length === 0) {
      setUploadSelections({});
      return;
    }
    setUploadSelections((prev) => {
      const next: Record<string, boolean> = { ...prev };
      uploadCandidates.forEach((item) => {
        const pathValue = item.image.Path;
        if (!pathValue) return;
        if (next[pathValue] === undefined) {
          next[pathValue] = true;
        }
      });
      // Remove selections for paths no longer in candidates
      Object.keys(next).forEach((key) => {
        if (!uploadCandidatePaths.has(key)) {
          delete next[key];
        }
      });
      return next;
    });
  }, [uploadCandidates, uploadCandidatePaths]);

  // Toggle selection of a single image
  const toggleUploadSelection = useCallback((imagePath: string) => {
    if (!imagePath) return;
    setUploadSelections((prev) => ({
      ...prev,
      [imagePath]: prev[imagePath] === undefined ? false : !prev[imagePath],
    }));
  }, []);

  // Set all selections to same state
  const setAllUploadSelections = useCallback(
    (value: boolean) => {
      if (uploadCandidates.length === 0) return;
      const next: Record<string, boolean> = {};
      uploadCandidates.forEach((item) => {
        if (item.image.Path) {
          next[item.image.Path] = value;
        }
      });
      setUploadSelections(next);
    },
    [uploadCandidates],
  );

  // Refresh uploaded images from backend
  const refreshUploadedImages = useCallback(async () => {
    const fetcher = globalThis.go?.guiapp?.App?.ListUploadedImages;
    if (!fetcher) return;
    if (!path.trim()) {
      setUploadedImageRecords([]);
      return;
    }
    try {
      const records = await fetcher(
        path.trim(),
        normalizeOverrides(idOverrideState?.overrides || {}),
        normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
      );
      setUploadedImageRecords(records || []);
    } catch (err) {
      console.error("Failed to load uploaded images:", err);
    }
  }, [path, idOverrideState, releaseOverrideState]);

  // Upload selected images to host
  const handleUploadImages = useCallback(
    async (selected: ScreenshotPreviewImage[]) => {
      setUploadImagesError("");
      setUploadImageFailures([]);
      const uploader = globalThis.go?.guiapp?.App?.UploadImages;
      if (!uploader) {
        setUploadImagesError("Image uploading is unavailable in this build.");
        return;
      }
      if (!path.trim()) {
        setUploadImagesError("Please select a file or folder.");
        return;
      }
      if (!uploadHost) {
        setUploadImagesError("Select an image host to upload.");
        return;
      }

      if (selected.length === 0) {
        setUploadImagesError("Select at least one image to upload.");
        return;
      }

      setUploadImagesLoading(true);
      setUploadProgress({ current: 0, total: selected.length });
      try {
        const result = await uploader(
          path.trim(),
          normalizeOverrides(idOverrideState?.overrides || {}),
          normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
          selectedTrackers,
          uploadHost,
          selected.map((entry) => entry.image),
        );
        const links = result?.Links || [];
        const failures = result?.Failures || [];
        const uploadedCount = new Set(links.map((link) => link.ImagePath).filter(Boolean)).size;
        setUploadedImages(links);
        setUploadImageFailures(failures);
        if (failures.length > 0) {
          const failedHosts = Array.from(
            new Set(failures.map((failure) => failure.Host || "unknown host")),
          ).join(", ");
          setUploadImagesError(`Image upload failed for ${failedHosts}.`);
        }
        if (uploadedCount !== selected.length) {
          console.error("Upload count mismatch", {
            expectedCount: selected.length,
            uploadedCount,
          });
          if (failures.length === 0) {
            setUploadImagesError(
              `Upload completed with an unexpected result count (expected ${selected.length}, got ${uploadedCount}).`,
            );
          }
        }
        setUploadProgress({
          current: uploadedCount,
          total: selected.length,
        });
        await refreshUploadedImages();
      } catch (err) {
        setUploadImagesError(String(err));
        setUploadImageFailures([]);
        setUploadProgress({ current: 0, total: selected.length });
      } finally {
        setUploadImagesLoading(false);
      }
    },
    [
      path,
      idOverrideState,
      releaseOverrideState,
      selectedTrackers,
      uploadHost,
      refreshUploadedImages,
    ],
  );

  // Delete a single uploaded image record
  const handleDeleteUploadedImage = useCallback(
    async (imagePath: string, host: string) => {
      if (!imagePath || !path.trim() || !host) {
        console.error("Cannot delete uploaded image: missing path, imagePath, or host");
        return;
      }

      try {
        await globalThis.go?.guiapp?.App?.DeleteUploadedImage(path.trim(), imagePath, host);
        // Refresh existing images to reflect deletion
        const refreshed = await globalThis.go?.guiapp?.App?.ListUploadCandidates(
          path.trim(),
          normalizeOverrides(idOverrideState?.overrides || {}),
          normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
        );

        if (!refreshed || refreshed.length === 0) {
          setUploadedImages((prev) =>
            prev.filter(
              (image) => !(image.ImagePath === imagePath && (image.Host || uploadHost) === host),
            ),
          );
          await refreshUploadedImages();
          return;
        }

        setUploadedImages((prev) =>
          prev.filter(
            (image) => !(image.ImagePath === imagePath && (image.Host || uploadHost) === host),
          ),
        );
        await refreshUploadedImages();
      } catch (err) {
        console.error("Failed to delete uploaded image:", err);
      }
    },
    [path, idOverrideState, releaseOverrideState, uploadHost, refreshUploadedImages],
  );

  // Reset upload state
  const resetUploadState = useCallback(() => {
    setUploadHost("");
    setUploadSelections({});
    setUploadImagesLoading(false);
    setUploadImagesError("");
    setUploadImageFailures([]);
    setUploadedImages([]);
    setUploadedImageRecords([]);
    setUploadProgress({ current: 0, total: 0 });
  }, []);

  return {
    // State
    uploadHost,
    uploadSelections,
    uploadImagesLoading,
    uploadImagesError,
    uploadImageFailures,
    uploadedImages,
    uploadedImageRecords,
    uploadedRecordByPath,
    uploadProgress,

    // Setters
    setUploadHost,
    setUploadSelections,
    setUploadImagesLoading,
    setUploadImagesError,
    setUploadedImages,
    setUploadedImageRecords,

    // Handlers & utilities
    toggleUploadSelection,
    setAllUploadSelections,
    refreshUploadedImages,
    handleUploadImages,
    handleDeleteUploadedImage,
    resetUploadState,
  };
};
