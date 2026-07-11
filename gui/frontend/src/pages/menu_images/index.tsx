// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type {
  DVDMenuCaptureSnapshot,
  ExternalIDOverrides,
  ReleaseNameOverrides,
  ScreenshotImage,
} from "../../types";

type Props = Readonly<{
  path: string;
  overrides: ExternalIDOverrides;
  nameOverrides: ReleaseNameOverrides;
  currentDiscType: string;
  maxMenuItems: number;
  browseAvailable: boolean;
  onImagesChanged: () => void;
  onContinue: () => void;
  setLightboxImage: (value: string) => void;
  setLightboxAlt: (value: string) => void;
}>;

type MenuPreview = {
  image: ScreenshotImage;
  dataURI: string;
};

type CaptureJob = Readonly<{
  id: string;
  sourcePath: string;
}>;

type CaptureStart = Readonly<{
  sourcePath: string;
}>;

type CaptureStatus = Readonly<{
  sourcePath: string;
  snapshot: DVDMenuCaptureSnapshot;
}>;

const terminalStatuses = new Set(["completed", "failed", "canceled"]);
const hiddenCaptureWarningCodes = new Set([
  "unsupported_post_link",
  "structural_state",
  "structural_only",
  "nav_scan_limit",
  "structural_discovery",
]);

const errorText = (error: unknown) =>
  error instanceof Error && error.message ? error.message : String(error);

/**
 * Manages manual disc-menu imports and background DVD menu capture for the
 * current prepared source. Persisted images can be previewed or deleted before
 * continuing to image-host selection. Concurrent deletions keep each affected
 * control pending until its own request settles.
 */
export default function MenuImagesPage(props: Props) {
  const {
    path,
    overrides,
    nameOverrides,
    currentDiscType,
    maxMenuItems,
    browseAvailable,
    onImagesChanged,
    onContinue,
    setLightboxImage,
    setLightboxAlt,
  } = props;

  const [menuPaths, setMenuPaths] = useState<string[]>([]);
  const [images, setImages] = useState<MenuPreview[]>([]);
  const [listLoading, setListLoading] = useState(false);
  const [importing, setImporting] = useState(false);
  const [deletingPaths, setDeletingPaths] = useState<ReadonlySet<string>>(() => new Set());
  const [captureJob, setCaptureJob] = useState<CaptureJob | null>(null);
  const [captureStart, setCaptureStart] = useState<CaptureStart | null>(null);
  const [captureStatus, setCaptureStatus] = useState<CaptureStatus | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const removeButtonRefs = useRef(new Map<string, HTMLButtonElement>());
  const deletingPathsRef = useRef(new Set<string>());
  const captureButtonRef = useRef<HTMLButtonElement>(null);
  const focusAfterRemovalRef = useRef<number | null>(null);
  const captureJobRef = useRef<CaptureJob | null>(null);
  const captureStartRef = useRef<CaptureStart | null>(null);
  const previousSourcePathRef = useRef(path.trim());
  const refreshRequestRef = useRef(0);

  const sourcePath = path.trim();
  const resolvedMaxMenuItems =
    Number.isFinite(maxMenuItems) && maxMenuItems > 0 ? Math.trunc(maxMenuItems) : 6;
  const automaticCaptureAvailable = currentDiscType.toUpperCase() === "DVD";
  const currentCaptureJob = captureJob?.sourcePath === sourcePath ? captureJob : null;
  const captureStarting = captureStart?.sourcePath === sourcePath;
  const visibleSnapshot = captureStatus?.sourcePath === sourcePath ? captureStatus.snapshot : null;
  const captureActive =
    captureStarting ||
    (currentCaptureJob !== null &&
      (!visibleSnapshot ||
        visibleSnapshot.status === "queued" ||
        visibleSnapshot.status === "running"));

  const refreshImages = useCallback(async () => {
    const requestID = ++refreshRequestRef.current;
    const app = globalThis.go?.guiapp?.App;
    if (!app?.ListDVDMenuScreenshots || !sourcePath) {
      setImages([]);
      setListLoading(false);
      return;
    }
    setListLoading(true);
    try {
      const persisted = await app.ListDVDMenuScreenshots(sourcePath, overrides, nameOverrides);
      const previews = await Promise.all(
        (persisted || []).map(async (image): Promise<MenuPreview | null> => {
          if (!image.Path || !app.ReadScreenshotImage) return null;
          try {
            return { image, dataURI: await app.ReadScreenshotImage(image.Path) };
          } catch {
            return null;
          }
        }),
      );
      if (refreshRequestRef.current !== requestID) return;
      setImages(previews.filter((item): item is MenuPreview => item !== null));
    } catch (loadError) {
      if (refreshRequestRef.current === requestID) setError(errorText(loadError));
    } finally {
      if (refreshRequestRef.current === requestID) setListLoading(false);
    }
  }, [nameOverrides, overrides, sourcePath]);

  useEffect(() => {
    void refreshImages();
  }, [refreshImages]);

  useEffect(() => {
    const previousSourcePath = previousSourcePathRef.current;
    if (previousSourcePath === sourcePath) return;
    previousSourcePathRef.current = sourcePath;

    setCaptureStatus(null);
    setError("");
    setNotice("");

    const staleStart = captureStartRef.current;
    if (staleStart && staleStart.sourcePath !== sourcePath) {
      captureStartRef.current = null;
      setCaptureStart((current) => (current === staleStart ? null : current));
    }

    const staleJob = captureJobRef.current;
    if (staleJob && staleJob.sourcePath !== sourcePath) {
      captureJobRef.current = null;
      setCaptureJob((current) => (current === staleJob ? null : current));
      const cancel = globalThis.go?.guiapp?.App?.CancelDVDMenuCapture;
      if (cancel) void cancel(staleJob.id).catch(() => undefined);
    }
  }, [sourcePath]);

  useEffect(() => {
    const targetIndex = focusAfterRemovalRef.current;
    if (targetIndex === null) return;
    focusAfterRemovalRef.current = null;
    const target = images[Math.min(targetIndex, Math.max(images.length - 1, 0))];
    window.requestAnimationFrame(() => {
      if (target?.image.Path) {
        removeButtonRefs.current.get(target.image.Path)?.focus();
      } else {
        captureButtonRef.current?.focus();
      }
    });
  }, [images]);

  useEffect(() => {
    if (!currentCaptureJob) return;
    let disposed = false;
    let timer: number | undefined;

    const poll = async () => {
      try {
        const loader = globalThis.go?.guiapp?.App?.GetDVDMenuCaptureSnapshot;
        if (!loader) throw new Error("DVD menu capture status is unavailable");
        const next = await loader(currentCaptureJob.id);
        if (disposed) return;
        setCaptureStatus({ sourcePath: currentCaptureJob.sourcePath, snapshot: next });
        if (terminalStatuses.has(next.status)) {
          if (next.status === "completed") {
            await refreshImages();
            if (disposed) return;
            onImagesChanged();
          } else if (next.error) {
            setError(next.error);
          }
          if (captureJobRef.current === currentCaptureJob) captureJobRef.current = null;
          setCaptureJob((current) => (current === currentCaptureJob ? null : current));
          return;
        }
        timer = window.setTimeout(poll, 300);
      } catch (pollError) {
        if (!disposed) {
          if (captureJobRef.current === currentCaptureJob) captureJobRef.current = null;
          setCaptureJob((current) => (current === currentCaptureJob ? null : current));
          setError(errorText(pollError));
        }
      }
    };

    void poll();
    return () => {
      disposed = true;
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [currentCaptureJob, onImagesChanged, refreshImages]);

  const handleBrowseImages = async () => {
    setError("");
    try {
      const app = globalThis.go?.guiapp?.App;
      if (!app) return;
      const selected = app.BrowseImageFiles
        ? await app.BrowseImageFiles()
        : app.BrowseFiles
          ? await app.BrowseFiles()
          : app.BrowseFile
            ? [await app.BrowseFile()]
            : [];
      const valid = selected.map((value) => value.trim()).filter(Boolean);
      if (valid.length > 0) {
        setMenuPaths((previous) => Array.from(new Set([...previous, ...valid])));
        setNotice("");
      }
    } catch (browseError) {
      setError(errorText(browseError));
    }
  };

  const handleImport = async () => {
    if (menuPaths.length === 0) return;
    setImporting(true);
    setError("");
    setNotice("");
    try {
      const importImages = globalThis.go?.guiapp?.App?.ImportMenuImages;
      if (!importImages) throw new Error("Menu image import is unavailable");
      await importImages(path, overrides, nameOverrides, menuPaths);
      setMenuPaths([]);
      setNotice("Menu images imported successfully.");
      await refreshImages();
      onImagesChanged();
    } catch (importError) {
      setError(errorText(importError));
    } finally {
      setImporting(false);
    }
  };

  const handleCapture = async () => {
    if (!sourcePath || captureStartRef.current?.sourcePath === sourcePath) return;
    if (captureJobRef.current?.sourcePath === sourcePath) return;

    const request: CaptureStart = { sourcePath };
    captureStartRef.current = request;
    setCaptureStart(request);
    setError("");
    setNotice("");
    setCaptureStatus(null);
    try {
      const start = globalThis.go?.guiapp?.App?.StartDVDMenuCapture;
      if (!start) throw new Error("DVD menu capture is unavailable");
      const nextJobID = await start(sourcePath, overrides, nameOverrides);
      if (captureStartRef.current !== request) {
        const cancel = globalThis.go?.guiapp?.App?.CancelDVDMenuCapture;
        if (cancel) await cancel(nextJobID).catch(() => undefined);
        return;
      }
      const nextJob: CaptureJob = { id: nextJobID, sourcePath };
      captureJobRef.current = nextJob;
      setCaptureJob(nextJob);
    } catch (captureError) {
      if (captureStartRef.current === request) setError(errorText(captureError));
    } finally {
      if (captureStartRef.current === request) {
        captureStartRef.current = null;
        setCaptureStart(null);
      }
    }
  };

  const handleCancel = async () => {
    if (!currentCaptureJob) return;
    try {
      const cancel = globalThis.go?.guiapp?.App?.CancelDVDMenuCapture;
      if (!cancel) throw new Error("DVD menu capture cancellation is unavailable");
      await cancel(currentCaptureJob.id);
    } catch (cancelError) {
      setError(errorText(cancelError));
    }
  };

  const handleDelete = async (imagePath: string, index: number) => {
    if (deletingPathsRef.current.has(imagePath)) return;
    deletingPathsRef.current.add(imagePath);
    setDeletingPaths(new Set(deletingPathsRef.current));
    setError("");
    try {
      const remove = globalThis.go?.guiapp?.App?.DeleteDVDMenuScreenshot;
      if (!remove) throw new Error("DVD menu image removal is unavailable");
      await remove(path, overrides, nameOverrides, imagePath);
      focusAfterRemovalRef.current = index;
      await refreshImages();
      onImagesChanged();
      setNotice("Menu image removed.");
    } catch (deleteError) {
      setError(errorText(deleteError));
    } finally {
      deletingPathsRef.current.delete(imagePath);
      setDeletingPaths(new Set(deletingPathsRef.current));
    }
  };

  const completionMessage = useMemo(() => {
    if (visibleSnapshot?.status !== "completed") return "";
    if (visibleSnapshot.result.Truncated) return "Maximum reached";
    return `Captured ${visibleSnapshot.capturedCount} DVD menu image${visibleSnapshot.capturedCount === 1 ? "" : "s"}.`;
  }, [visibleSnapshot]);
  const visibleCaptureWarnings =
    visibleSnapshot?.result.Warnings?.filter(
      (warning) => !hiddenCaptureWarningCodes.has(warning.Code),
    ) ?? [];

  return (
    <section className="grid gap-4">
      <header>
        <p className="eyebrow">Disc Menus</p>
        <h1>Menu Images</h1>
        <p className="subtitle">
          Capture DVD menus or import existing disc menu images for upload and descriptions.
        </p>
      </header>

      {automaticCaptureAvailable ? (
        <section className="panel grid gap-3" aria-busy={captureActive}>
          <div>
            <h2>Automatic DVD capture</h2>
            <p className="muted">Capture up to {resolvedMaxMenuItems} distinct DVD menu screens.</p>
          </div>
          <div className="flex flex-wrap gap-2">
            <button
              ref={captureButtonRef}
              className="primary"
              type="button"
              onClick={handleCapture}
              disabled={captureActive || importing || !path.trim()}
            >
              {captureStarting ? "Starting..." : "Capture DVD menus"}
            </button>
            {captureActive ? (
              <button className="ghost" type="button" onClick={handleCancel}>
                Cancel
              </button>
            ) : null}
          </div>
          {visibleSnapshot ? (
            <div className="grid gap-1" role="status" aria-live="polite">
              <p className="m-0 font-medium">{visibleSnapshot.message || visibleSnapshot.phase}</p>
              <p className="muted m-0">
                Menus {visibleSnapshot.discoveredMenus} · States {visibleSnapshot.visitedStates} ·
                Buttons {visibleSnapshot.visitedButtons} · Captured {visibleSnapshot.capturedCount}
                {visibleCaptureWarnings.length > 0
                  ? ` · Warnings ${visibleCaptureWarnings.length}`
                  : ""}
              </p>
              {completionMessage ? <p className="success m-0">{completionMessage}</p> : null}
              {visibleSnapshot.result.Truncated ? (
                <p className="muted m-0">Configured maximum: {visibleSnapshot.result.MaxItems}.</p>
              ) : null}
              {visibleCaptureWarnings.map((warning) => (
                <p className="muted m-0" key={warning.Code}>
                  {warning.Message}
                </p>
              ))}
            </div>
          ) : null}
        </section>
      ) : (
        <section className="panel">
          <p className="muted">
            Automatic capture is available for DVD sources. Manual disc menu import remains
            available for {currentDiscType || "this source"}.
          </p>
        </section>
      )}

      <section className="panel grid gap-3">
        <div>
          <h2>Import menu images</h2>
          <p className="muted">Select PNG, JPEG, or WebP menu images from this computer.</p>
        </div>
        <div className="flex flex-wrap gap-2">
          {browseAvailable ? (
            <button
              className="ghost"
              type="button"
              onClick={handleBrowseImages}
              disabled={importing}
            >
              Add images
            </button>
          ) : (
            <p className="muted">Native file browsing is only available locally.</p>
          )}
          <button
            className="primary"
            type="button"
            onClick={handleImport}
            disabled={importing || menuPaths.length === 0 || captureActive}
          >
            {importing ? "Importing..." : "Import images"}
          </button>
        </div>
        {menuPaths.length > 0 ? (
          <ul className="m-0 grid list-none gap-1 p-0">
            {menuPaths.map((selectedPath) => (
              <li
                className="flex items-center justify-between gap-2 rounded border border-white/10 bg-white/5 p-2"
                key={selectedPath}
              >
                <span className="min-w-0 break-all">{selectedPath}</span>
                <button
                  className="ghost"
                  type="button"
                  onClick={() =>
                    setMenuPaths((previous) => previous.filter((item) => item !== selectedPath))
                  }
                >
                  Remove
                </button>
              </li>
            ))}
          </ul>
        ) : null}
      </section>

      {error ? (
        <p className="error" role="alert">
          {error}
        </p>
      ) : null}
      {notice ? (
        <p className="success" role="status" aria-live="polite">
          {notice}
        </p>
      ) : null}

      <section className="panel grid gap-3">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <h2>Saved menu images</h2>
          <p className="muted">{listLoading ? "Loading..." : `${images.length} saved`}</p>
        </div>
        {images.length > 0 ? (
          <div className="grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-3">
            {images.map((item, index) => {
              const itemNumber = index + 1;
              const deleting = deletingPaths.has(item.image.Path);
              return (
                <article className="grid gap-2" key={item.image.Path}>
                  <button
                    className="screens-thumb"
                    type="button"
                    aria-label={`Preview DVD menu ${itemNumber}`}
                    onClick={() => {
                      setLightboxImage(item.dataURI);
                      setLightboxAlt(`DVD menu ${itemNumber}`);
                    }}
                  >
                    <img src={item.dataURI} alt="" />
                  </button>
                  <button
                    ref={(element) => {
                      if (element) removeButtonRefs.current.set(item.image.Path, element);
                      else removeButtonRefs.current.delete(item.image.Path);
                    }}
                    className="danger"
                    type="button"
                    aria-label={`Remove DVD menu ${itemNumber}`}
                    disabled={deleting || captureActive}
                    onClick={() => handleDelete(item.image.Path, index)}
                  >
                    {deleting ? "Removing..." : "Remove"}
                  </button>
                </article>
              );
            })}
          </div>
        ) : (
          <p className="muted">No saved menu images yet.</p>
        )}
      </section>

      <div className="flex justify-end">
        <button className="primary" type="button" onClick={onContinue}>
          Continue to Upload Images
        </button>
      </div>
    </section>
  );
}
