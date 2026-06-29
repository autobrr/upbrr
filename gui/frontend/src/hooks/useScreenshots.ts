// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useState, useRef, useCallback, useMemo } from "react";
import type {
  ScreenshotImage,
  ScreenshotPlan,
  ScreenshotSelection,
  ScreenshotPreviewImage,
  ScreenshotResult,
  ScreenshotLinkedImage,
  ExternalIDOverrides,
  MetadataOverrides,
  ReleaseNameOverrides,
  ScreenshotOverrideSnapshot,
} from "../types";
import {
  normalizeMetadataOverrides,
  normalizeOverrides,
  normalizeReleaseOverrides,
} from "../utils";

interface ScreenshotHookProps {
  path: string;
  idOverrideState?: { overrides?: ExternalIDOverrides };
  releaseOverrideState?: { overrides?: ReleaseNameOverrides };
  metadataOverrideState?: { overrides?: MetadataOverrides };
}

export const useScreenshots = ({
  path,
  idOverrideState,
  releaseOverrideState,
  metadataOverrideState,
}: ScreenshotHookProps) => {
  // State: Plan & suggestions
  const [screenshotPlan, setScreenshotPlan] = useState<ScreenshotPlan | null>(null);
  const [screenshotSelections, setScreenshotSelections] = useState<ScreenshotSelection[]>([]);

  // State: UI controls
  const [screenshotsLoading, setScreenshotsLoading] = useState(false);
  const [screenshotsError, setScreenshotsError] = useState("");
  const [screenshotsSettingsSaving, setScreenshotsSettingsSaving] = useState(false);
  const [showFrameSelections, setShowFrameSelections] = useState(false);

  // State: Live preview
  const [livePreviewSeconds, setLivePreviewSeconds] = useState(0);
  const [livePreviewImage, setLivePreviewImage] = useState("");
  const [livePreviewLoading, setLivePreviewLoading] = useState(false);
  const [livePreviewError, setLivePreviewError] = useState("");
  const livePreviewRequestId = useRef(0);

  // State: Image collections
  const [previewLoadingIndex, setPreviewLoadingIndex] = useState<number | null>(null);
  const [previewImages, setPreviewImages] = useState<ScreenshotPreviewImage[]>([]);
  const [existingImages, setExistingImages] = useState<ScreenshotPreviewImage[]>([]);
  const [existingTrackerImages, setExistingTrackerImages] = useState<ScreenshotPreviewImage[]>([]);
  const [finalImages, setFinalImages] = useState<ScreenshotPreviewImage[]>([]);
  const [finalResult, setFinalResult] = useState<ScreenshotResult | null>(null);

  // Deletion tracking for UI updates
  const [deletedTrackerImages, setDeletedTrackerImages] = useState<string[]>([]);

  // Refs for state sync
  const finalImagesRef = useRef<ScreenshotPreviewImage[]>([]);
  const loadedMetadataOverridesRef = useRef<ScreenshotOverrideSnapshot>({
    external: {},
    release: {},
    metadata: {},
  });

  /** Captures the current normalized override tuple for a new screenshot plan request. */
  const snapshotCurrentOverrides = useCallback(
    (): ScreenshotOverrideSnapshot => ({
      external: normalizeOverrides(idOverrideState?.overrides || {}),
      release: normalizeReleaseOverrides(releaseOverrideState?.overrides || {}),
      metadata: normalizeMetadataOverrides(metadataOverrideState?.overrides || {}),
    }),
    [idOverrideState, releaseOverrideState, metadataOverrideState],
  );

  /**
   * Returns the override tuple from the loaded screenshot plan.
   *
   * Callers use this for follow-up screenshot work that must stay bound to the plan
   * even if the editable override state changes after loading.
   */
  const getLoadedScreenshotOverrides = useCallback(() => loadedMetadataOverridesRef.current, []);

  // Memoized tracker image maps
  const trackerImageURLs = useMemo(() => {
    const urls = new Set<string>();
    (screenshotPlan?.TrackerImageLinks || []).forEach((link) => {
      if (link.URL && !deletedTrackerImages.includes(link.URL)) {
        urls.add(link.URL);
      }
    });
    return Array.from(urls);
  }, [screenshotPlan?.TrackerImageLinks, deletedTrackerImages]);

  const trackerLinkByPath = useMemo(() => {
    const map = new Map<string, ScreenshotLinkedImage>();
    (screenshotPlan?.TrackerImageLinks || []).forEach((link) => {
      if (link.Path) {
        map.set(link.Path, link);
      }
    });
    return map;
  }, [screenshotPlan?.TrackerImageLinks]);

  const trackerLinkByURL = useMemo(() => {
    const map = new Map<string, ScreenshotLinkedImage>();
    (screenshotPlan?.TrackerImageLinks || []).forEach((link) => {
      if (link.URL) {
        map.set(link.URL, link);
      }
    });
    return map;
  }, [screenshotPlan?.TrackerImageLinks]);

  // Helper: normalize image path (URI decoding)
  const normalizeImagePath = useCallback((value: string) => {
    if (!value) return "";
    if (value.startsWith("file://")) {
      const trimmed = value.replaceAll("file://", "");
      try {
        return decodeURIComponent(trimmed);
      } catch {
        return trimmed;
      }
    }
    return value;
  }, []);

  // Helper: read screenshot image from backend
  const readScreenshotImage = useCallback(
    async (image: ScreenshotImage): Promise<ScreenshotPreviewImage> => {
      const reader = globalThis.go?.guiapp?.App?.ReadScreenshotImage;
      if (!reader) {
        throw new Error("Screenshot preview is unavailable in this build.");
      }
      const dataUri = await reader(image.Path);
      return { image, dataUri } as ScreenshotPreviewImage;
    },
    [],
  );

  // Load screenshot plan from backend
  const loadScreenshotPlan = useCallback(
    async (revealSelections = false): Promise<ScreenshotPlan | null> => {
      setScreenshotsError("");
      const fetcher = globalThis.go?.guiapp?.App?.FetchScreenshotPlan;
      if (!fetcher) {
        setScreenshotsError("Screenshot planning is unavailable in this build.");
        return null;
      }
      if (!path.trim()) {
        setScreenshotsError("Please select a file or folder.");
        return null;
      }

      setScreenshotsLoading(true);
      try {
        const loadedOverrides = snapshotCurrentOverrides();
        const result = await fetcher(
          path.trim(),
          loadedOverrides.external,
          loadedOverrides.release,
          loadedOverrides.metadata,
        );
        loadedMetadataOverridesRef.current = loadedOverrides;
        setScreenshotPlan(result);
        setScreenshotSelections(result.SuggestedSelections || []);
        setLivePreviewImage("");
        setLivePreviewError("");

        if (revealSelections) {
          setShowFrameSelections(true);
        }

        if (!livePreviewSeconds && (result.SuggestedSelections || []).length > 0) {
          const initial = result.SuggestedSelections[0]?.TimestampSeconds || 0;
          setLivePreviewSeconds(initial);
        }

        setExistingImages([]);
        setExistingTrackerImages([]);
        setPreviewImages([]);
        setFinalResult(null);

        // Load existing screenshot previews
        if (result.ExistingScreenshots && result.ExistingScreenshots.length > 0) {
          const previews = await Promise.all(
            result.ExistingScreenshots.map(async (image) => {
              try {
                return await readScreenshotImage(image);
              } catch {
                return null;
              }
            }),
          );
          setExistingImages(
            previews.filter((entry): entry is ScreenshotPreviewImage => Boolean(entry)),
          );
        }

        // Load existing tracker screenshot previews
        if (result.ExistingTrackerScreenshots && result.ExistingTrackerScreenshots.length > 0) {
          const previews = await Promise.all(
            result.ExistingTrackerScreenshots.map(async (image) => {
              try {
                return await readScreenshotImage(image);
              } catch {
                return null;
              }
            }),
          );
          setExistingTrackerImages(
            previews.filter((entry): entry is ScreenshotPreviewImage => Boolean(entry)),
          );
        }

        // Load final selections
        if (
          result.FinalSelections &&
          result.FinalSelections.length > 0 &&
          finalImagesRef.current.length === 0
        ) {
          const previews = await Promise.all(
            result.FinalSelections.map(async (image) => {
              try {
                return await readScreenshotImage(image);
              } catch {
                return null;
              }
            }),
          );
          setFinalImages(
            previews.filter((entry): entry is ScreenshotPreviewImage => Boolean(entry)),
          );
        }

        return result;
      } catch (err) {
        const message = String(err);
        if (message.includes("screenshot plan requires metadata preview")) {
          setScreenshotsError(
            "Fetch metadata first to cache a preview before planning screenshots.",
          );
        } else {
          setScreenshotsError(message);
        }
        return null;
      } finally {
        setScreenshotsLoading(false);
      }
    },
    [path, snapshotCurrentOverrides, livePreviewSeconds, readScreenshotImage],
  );

  // Save final selections
  const saveFinalSelections = useCallback(
    async (next: ScreenshotPreviewImage[]) => {
      setFinalImages(next);
      const saver = globalThis.go?.guiapp?.App?.SaveFinalScreenshotSelections;
      if (!saver || !path.trim()) {
        return;
      }
      try {
        const loadedOverrides = loadedMetadataOverridesRef.current;
        await saver(
          path.trim(),
          loadedOverrides.external,
          loadedOverrides.release,
          loadedOverrides.metadata,
          next.map((entry) => entry.image),
        );
      } catch (err) {
        setScreenshotsError(String(err));
      }
    },
    [path],
  );

  // Update selection timestamp
  const updateSelectionTime = useCallback((index: number, value: string) => {
    const next = Number.parseFloat(value);
    setScreenshotSelections((prev) => {
      return prev.map((selection) =>
        selection.Index === index
          ? {
              ...selection,
              TimestampSeconds: Number.isFinite(next) ? next : 0,
              Source: "manual",
            }
          : selection,
      );
    });
  }, []);

  // Update selection frame
  const updateSelectionFrame = useCallback((index: number, value: string) => {
    const next = Number.parseInt(value, 10);
    setScreenshotSelections((prev) => {
      return prev.map((selection) =>
        selection.Index === index
          ? {
              ...selection,
              Frame: Number.isFinite(next) ? next : 0,
              Source: "manual",
            }
          : selection,
      );
    });
  }, []);

  // Check if final image is already selected
  const isFinalImageSelected = useCallback((pathValue: string) => {
    if (!pathValue) return false;
    return finalImagesRef.current.some((entry) => entry.image.Path === pathValue);
  }, []);

  const updateFinalSelections = useCallback(
    async (next: ScreenshotPreviewImage[], persist: boolean) => {
      finalImagesRef.current = next;
      setFinalImages(next);
      if (persist) {
        await saveFinalSelections(next);
      }
    },
    [saveFinalSelections],
  );

  // Add image to final selections
  const addFinalSelection = useCallback(
    (image: ScreenshotPreviewImage) => {
      if (!image.image.Path) return;
      const updated = mergeFinalSelections(finalImagesRef.current, [image]);
      void updateFinalSelections(updated, true);
    },
    [updateFinalSelections],
  );

  const removeFinalSelectionInternal = useCallback(
    (pathValue: string, persist: boolean) => {
      if (!pathValue) return false;
      if (!finalImagesRef.current.some((entry) => entry.image.Path === pathValue)) {
        return false;
      }
      const updated = finalImagesRef.current.filter((entry) => entry.image.Path !== pathValue);
      void updateFinalSelections(updated, persist);
      return true;
    },
    [updateFinalSelections],
  );

  // Remove image from final selections
  const removeFinalSelection = useCallback(
    (pathValue: string) => {
      removeFinalSelectionInternal(pathValue, true);
    },
    [removeFinalSelectionInternal],
  );

  // Merge final selections intelligently
  const mergeFinalSelections = (
    current: ScreenshotPreviewImage[],
    additions: ScreenshotPreviewImage[],
  ): ScreenshotPreviewImage[] => {
    if (additions.length === 0) return current;
    const seen = new Map<string, number>();
    const merged = [...current];
    merged.forEach((item, index) => {
      if (item.image.Path) {
        seen.set(item.image.Path, index);
      }
    });

    additions.forEach((item) => {
      const pathValue = item.image.Path;
      if (!pathValue) return;
      const existingIndex = seen.get(pathValue);
      if (existingIndex === undefined) {
        const ts = item.image.TimestampSeconds || 0;
        if (ts > 0) {
          const insertAt = merged.findIndex((entry) => {
            const entryTs = entry.image.TimestampSeconds || 0;
            return entryTs > 0 && entryTs > ts;
          });
          if (insertAt >= 0) {
            merged.splice(insertAt, 0, item);
            seen.clear();
            merged.forEach((entry, idx) => {
              if (entry.image.Path) {
                seen.set(entry.image.Path, idx);
              }
            });
          } else {
            merged.push(item);
            seen.set(pathValue, merged.length - 1);
          }
        } else {
          merged.push(item);
          seen.set(pathValue, merged.length - 1);
        }
      }
    });

    return merged;
  };

  // Reorder final selections via drag
  const reorderFinalSelections = (dragIndex: number, targetIndex: number) => {
    if (dragIndex === targetIndex) return;
    const updated = [...finalImagesRef.current];
    const [dragged] = updated.splice(dragIndex, 1);
    updated.splice(targetIndex, 0, dragged);
    finalImagesRef.current = updated;
    saveFinalSelections(updated);
  };

  // Delete a set of images
  const deleteImageSet = useCallback(
    async (images: ScreenshotImage[], label: string): Promise<ScreenshotImage[]> => {
      if (images.length === 0) return [];
      const deleter = globalThis.go?.guiapp?.App?.DeleteScreenshot;
      if (!deleter || !path.trim()) {
        return [] as ScreenshotImage[];
      }
      if (!globalThis.confirm(`Delete all ${label} images from the temp folder?`)) {
        return [] as ScreenshotImage[];
      }

      const deleted: ScreenshotImage[] = [];
      const failures: string[] = [];
      const loadedOverrides = loadedMetadataOverridesRef.current;
      for (const image of images) {
        try {
          await deleter(
            path.trim(),
            loadedOverrides.external,
            loadedOverrides.release,
            loadedOverrides.metadata,
            image.Path,
          );
          deleted.push(image);
        } catch (err) {
          failures.push(String(err));
        }
      }

      if (failures.length > 0) {
        setScreenshotsError(failures[0]);
      }
      return deleted;
    },
    [path],
  );

  const deleteTrackerImageURL = useCallback(
    async (url: string) => {
      const deleter = (globalThis as any)?.go?.guiapp?.App?.DeleteTrackerImageURL;
      if (!deleter || !path.trim() || !url) {
        return;
      }
      try {
        const loadedOverrides = loadedMetadataOverridesRef.current;
        await deleter(
          path.trim(),
          loadedOverrides.external,
          loadedOverrides.release,
          loadedOverrides.metadata,
          url,
        );
      } catch (err) {
        setScreenshotsError(String(err));
      }
    },
    [path],
  );

  const deleteTrackerImageFile = useCallback(
    async (imagePath: string) => {
      const deleter = globalThis.go?.guiapp?.App?.DeleteScreenshot;
      if (!deleter || !path.trim() || !imagePath) {
        return;
      }
      try {
        const loadedOverrides = loadedMetadataOverridesRef.current;
        await deleter(
          path.trim(),
          loadedOverrides.external,
          loadedOverrides.release,
          loadedOverrides.metadata,
          imagePath,
        );
      } catch (err) {
        setScreenshotsError(String(err));
      }
    },
    [path],
  );

  const removeTrackerImageURLState = useCallback((url: string) => {
    if (!url) return;
    setDeletedTrackerImages((prev) => {
      if (prev.includes(url)) {
        return prev;
      }
      return [...prev, url];
    });
    setScreenshotPlan((prev) => {
      if (!prev) return prev;
      const trackerLinks = prev.TrackerImageLinks || [];
      return {
        ...prev,
        TrackerImageLinks: trackerLinks.filter((entry) => entry.URL !== url),
      };
    });
  }, []);

  // Delete all existing images
  const handleDeleteAllExistingImages = useCallback(async () => {
    const images = screenshotPlan?.ExistingScreenshots?.length
      ? screenshotPlan.ExistingScreenshots
      : existingImages.map((entry) => entry.image);
    const deleted = await deleteImageSet(images, "existing");
    if (deleted.length === 0) return;
    const deletedPaths = new Set(deleted.map((image) => image.Path));
    setExistingImages((prev) => prev.filter((entry) => !deletedPaths.has(entry.image.Path)));
    if (finalImagesRef.current.length > 0) {
      await saveFinalSelections(
        finalImagesRef.current.filter((entry) => !deletedPaths.has(entry.image.Path)),
      );
    }
    setScreenshotPlan((prev) => {
      if (!prev) return prev;
      const existing = prev.ExistingScreenshots || [];
      return {
        ...prev,
        ExistingScreenshots: existing.filter((entry) => !deletedPaths.has(entry.Path)),
      };
    });
  }, [screenshotPlan, existingImages, deleteImageSet, saveFinalSelections]);

  // Delete all tracker images
  const handleDeleteAllTrackerImages = useCallback(async () => {
    const images = screenshotPlan?.ExistingTrackerScreenshots?.length
      ? screenshotPlan.ExistingTrackerScreenshots
      : existingTrackerImages.map((entry) => entry.image);
    const deleted = await deleteImageSet(images, "tracker temp");
    if (deleted.length === 0) return;
    const deletedPaths = new Set(deleted.map((image) => image.Path));
    const linkedURLs = deleted
      .map((image) => trackerLinkByPath.get(image.Path))
      .filter((link): link is ScreenshotLinkedImage => Boolean(link));
    if (linkedURLs.length > 0) {
      setDeletedTrackerImages((prev) => {
        const next = new Set(prev);
        linkedURLs.forEach((link) => {
          if (link.URL) {
            next.add(link.URL);
          }
        });
        return Array.from(next);
      });
      for (const link of linkedURLs) {
        if (!link.URL) {
          continue;
        }
        await deleteTrackerImageURL(link.URL);
      }
    }

    setExistingTrackerImages((prev) => prev.filter((entry) => !deletedPaths.has(entry.image.Path)));
    if (finalImagesRef.current.length > 0) {
      await saveFinalSelections(
        finalImagesRef.current.filter((entry) => !deletedPaths.has(entry.image.Path)),
      );
    }
    setScreenshotPlan((prev) => {
      if (!prev) return prev;
      const trackerExisting = prev.ExistingTrackerScreenshots || [];
      const trackerLinks = prev.TrackerImageLinks || [];
      return {
        ...prev,
        ExistingTrackerScreenshots: trackerExisting.filter(
          (entry) => !deletedPaths.has(entry.Path),
        ),
        TrackerImageLinks: trackerLinks.filter((entry) => !deletedPaths.has(entry.Path)),
      };
    });
  }, [
    screenshotPlan,
    existingTrackerImages,
    trackerLinkByPath,
    deleteImageSet,
    saveFinalSelections,
    deleteTrackerImageURL,
  ]);

  // Delete all preview images
  const handleDeleteAllPreviewImages = useCallback(async () => {
    const images = previewImages.map((entry) => entry.image);
    const deleted = await deleteImageSet(images, "preview");
    if (deleted.length === 0) return;
    const deletedPaths = new Set(deleted.map((image) => image.Path));
    setPreviewImages((prev) => prev.filter((entry) => !deletedPaths.has(entry.image.Path)));
  }, [previewImages, deleteImageSet]);

  // Delete all final images
  const handleDeleteAllFinalImages = useCallback(async () => {
    const images = finalImages.map((entry) => entry.image);
    const deleted = await deleteImageSet(images, "final");
    if (deleted.length === 0) return;
    const deletedPaths = new Set(deleted.map((image) => image.Path));
    const linkedURLs = deleted
      .map((image) => trackerLinkByPath.get(image.Path))
      .filter((link): link is ScreenshotLinkedImage => Boolean(link));
    if (linkedURLs.length > 0) {
      setDeletedTrackerImages((prev) => {
        const next = new Set(prev);
        linkedURLs.forEach((link) => {
          if (link.URL) {
            next.add(link.URL);
          }
        });
        return Array.from(next);
      });
      for (const link of linkedURLs) {
        if (!link.URL) {
          continue;
        }
        await deleteTrackerImageURL(link.URL);
      }
    }
    await saveFinalSelections(
      finalImagesRef.current.filter((entry) => !deletedPaths.has(entry.image.Path)),
    );
    setExistingImages((prev) => prev.filter((entry) => !deletedPaths.has(entry.image.Path)));
    setExistingTrackerImages((prev) => prev.filter((entry) => !deletedPaths.has(entry.image.Path)));
    setPreviewImages((prev) => prev.filter((entry) => !deletedPaths.has(entry.image.Path)));
    setScreenshotPlan((prev) => {
      if (!prev) return prev;
      const existing = prev.ExistingScreenshots || [];
      const trackerExisting = prev.ExistingTrackerScreenshots || [];
      const trackerLinks = prev.TrackerImageLinks || [];
      return {
        ...prev,
        ExistingScreenshots: existing.filter((entry) => !deletedPaths.has(entry.Path)),
        ExistingTrackerScreenshots: trackerExisting.filter(
          (entry) => !deletedPaths.has(entry.Path),
        ),
        TrackerImageLinks: trackerLinks.filter((entry) => !deletedPaths.has(entry.Path)),
      };
    });
  }, [finalImages, deleteImageSet, saveFinalSelections, trackerLinkByPath, deleteTrackerImageURL]);

  const handleDeleteTrackerImageURL = useCallback(
    async (url: string, persistFinal = true): Promise<boolean> => {
      if (!url) return false;
      const linked = trackerLinkByURL.get(url);
      let removedFinal = false;
      if (linked?.Path) {
        await deleteTrackerImageFile(linked.Path);
      }
      await deleteTrackerImageURL(url);
      removeTrackerImageURLState(url);
      if (linked?.Path) {
        removedFinal = removeFinalSelectionInternal(linked.Path, persistFinal);
      }
      return removedFinal;
    },
    [
      deleteTrackerImageURL,
      removeTrackerImageURLState,
      trackerLinkByURL,
      removeFinalSelectionInternal,
      deleteTrackerImageFile,
    ],
  );

  // Delete a specific tracker image
  const handleDeleteTrackerImage = useCallback(
    async (url: string) => {
      setScreenshotsError("");
      if (!url) {
        setScreenshotsError("Tracker image URL is missing.");
        return;
      }
      if (!globalThis.confirm("Remove this tracker image from the list?")) {
        return;
      }
      await handleDeleteTrackerImageURL(url, true);
    },
    [handleDeleteTrackerImageURL],
  );

  // Delete all tracker image URLs
  const handleDeleteAllTrackerImageURLs = useCallback(async () => {
    const urls = trackerImageURLs;
    if (urls.length === 0) return;
    if (!globalThis.confirm("Remove all tracker images from the list?")) {
      return;
    }
    let removedFinal = false;
    for (const url of urls) {
      const removed = await handleDeleteTrackerImageURL(url, false);
      if (removed) {
        removedFinal = true;
      }
    }
    if (removedFinal) {
      await saveFinalSelections(finalImagesRef.current);
    }
  }, [trackerImageURLs, handleDeleteTrackerImageURL, saveFinalSelections]);

  // Reset all screenshot state
  const resetScreenshotState = useCallback(() => {
    setScreenshotPlan(null);
    setScreenshotSelections([]);
    setScreenshotsLoading(false);
    setScreenshotsError("");
    setShowFrameSelections(false);
    setPreviewLoadingIndex(null);
    setPreviewImages([]);
    setExistingImages([]);
    setExistingTrackerImages([]);
    setFinalImages([]);
    setFinalResult(null);
    setLivePreviewSeconds(0);
    setLivePreviewImage("");
    setLivePreviewLoading(false);
    setLivePreviewError("");
    setDeletedTrackerImages([]);
    finalImagesRef.current = [];
    loadedMetadataOverridesRef.current = { external: {}, release: {}, metadata: {} };
  }, []);

  // Sync finalImagesRef with finalImages state
  useMemo(() => {
    finalImagesRef.current = finalImages;
  }, [finalImages]);

  // Compute tracker image links from screenshot plan
  const trackerImageLinks = useMemo(() => {
    return screenshotPlan?.TrackerImageLinks || [];
  }, [screenshotPlan?.TrackerImageLinks]);

  // Compute merged upload candidates from all image sources
  const uploadCandidates = useMemo(() => {
    const merged = new Map<string, ScreenshotPreviewImage>();

    const addImages = (items: ScreenshotPreviewImage[]) => {
      items.forEach((item) => {
        const pathValue = item.image.Path;
        if (!pathValue) return;
        const existing = merged.get(pathValue);
        if (!existing || (item.image.Host && item.image.RawURL && !existing.image.Host)) {
          // Prefer entries with upload metadata
          merged.set(pathValue, item);
        }
      });
    };

    addImages(finalImages);
    addImages(previewImages);
    addImages(existingTrackerImages);
    addImages(existingImages);

    return Array.from(merged.values());
  }, [finalImages, previewImages, existingTrackerImages, existingImages]);

  // Compute set of upload candidate paths
  const uploadCandidatePaths = useMemo(() => {
    const paths = new Set<string>();
    uploadCandidates.forEach((item) => {
      if (item.image.Path) {
        paths.add(item.image.Path);
      }
    });
    return paths;
  }, [uploadCandidates]);

  return {
    // State
    screenshotPlan,
    screenshotSelections,
    screenshotsLoading,
    screenshotsError,
    screenshotsSettingsSaving,
    showFrameSelections,
    livePreviewSeconds,
    livePreviewImage,
    livePreviewLoading,
    livePreviewError,
    livePreviewRequestId,
    previewLoadingIndex,
    previewImages,
    existingImages,
    existingTrackerImages,
    finalImages,
    finalResult,
    deletedTrackerImages,
    trackerImageURLs,
    trackerLinkByPath,
    trackerLinkByURL,
    trackerImageLinks,
    uploadCandidates,
    uploadCandidatePaths,

    // Refs
    finalImagesRef,

    // Setters
    setScreenshotPlan,
    setScreenshotSelections,
    setScreenshotsLoading,
    setScreenshotsError,
    setScreenshotsSettingsSaving,
    setShowFrameSelections,
    setLivePreviewSeconds,
    setLivePreviewImage,
    setLivePreviewLoading,
    setLivePreviewError,
    setPreviewLoadingIndex,
    setPreviewImages,
    setExistingImages,
    setExistingTrackerImages,
    setFinalImages,
    setFinalResult,
    setDeletedTrackerImages,

    // Handlers & utilities
    normalizeImagePath,
    readScreenshotImage,
    getLoadedScreenshotOverrides,
    loadScreenshotPlan,
    saveFinalSelections,
    updateSelectionTime,
    updateSelectionFrame,
    isFinalImageSelected,
    addFinalSelection,
    removeFinalSelection,
    reorderFinalSelections,
    handleDeleteAllExistingImages,
    handleDeleteAllTrackerImages,
    handleDeleteAllPreviewImages,
    handleDeleteAllFinalImages,
    handleDeleteTrackerImage,
    handleDeleteTrackerImageURL,
    handleDeleteAllTrackerImageURLs,
    resetScreenshotState,
  };
};
