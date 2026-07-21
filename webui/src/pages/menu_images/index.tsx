// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useMemo, useRef, useState } from "react";
import type { MenuImagesFacet } from "../../releaseSession/types";

type Props = Readonly<{
  facet: MenuImagesFacet;
  currentDiscType: string;
  maxMenuItems: number;
  onContinue: () => void;
  setLightboxImage: (value: string) => void;
  setLightboxAlt: (value: string) => void;
}>;

const hiddenCaptureWarningCodes = new Set([
  "unsupported_post_link",
  "structural_state",
  "structural_only",
  "nav_scan_limit",
  "structural_discovery",
]);

/** Thin presentation adapter for the exact-generation menu-image facet. */
export default function MenuImagesPage({
  facet,
  currentDiscType,
  maxMenuItems,
  onContinue,
  setLightboxImage,
  setLightboxAlt,
}: Props) {
  const [menuPaths, setMenuPaths] = useState<string[]>([]);
  const [menuPathDraft, setMenuPathDraft] = useState("");
  const [notice, setNotice] = useState("");
  const removeButtonRefs = useRef(new Map<string, HTMLButtonElement>());
  const captureButtonRef = useRef<HTMLButtonElement>(null);
  const loadRef = useRef(facet.load);
  loadRef.current = facet.load;
  const { view } = facet;
  const running = view.status === "running";
  const resolvedMaxMenuItems =
    Number.isFinite(maxMenuItems) && maxMenuItems > 0 ? Math.trunc(maxMenuItems) : 6;
  const automaticCaptureAvailable = currentDiscType.toUpperCase() === "DVD";

  useEffect(() => {
    if (view.status === "idle" && view.staleReason) void loadRef.current();
  }, [view.staleReason, view.status]);

  const handleAddPath = () => {
    const selectedPath = menuPathDraft.trim();
    if (!selectedPath) return;
    setMenuPaths((previous) => Array.from(new Set([...previous, selectedPath])));
    setMenuPathDraft("");
    setNotice("");
  };

  const handleImport = async () => {
    if (menuPaths.length === 0) return;
    setNotice("");
    if (await facet.importPaths(menuPaths)) {
      setMenuPaths([]);
      setNotice("Menu images imported successfully.");
    }
  };

  const handleCapture = async () => {
    setNotice("");
    await facet.capture();
  };

  const handleDelete = async (imagePath: string) => {
    setNotice("");
    if (await facet.remove(imagePath)) setNotice("Menu image removed.");
  };

  const completionMessage = useMemo(() => {
    if (!view.capture) return "";
    if (view.capture.Truncated) return "Maximum reached";
    return `Captured ${view.capture.Images.length} DVD menu image${view.capture.Images.length === 1 ? "" : "s"}.`;
  }, [view.capture]);
  const visibleCaptureWarnings =
    view.capture?.Warnings?.filter((warning) => !hiddenCaptureWarningCodes.has(warning.Code)) ?? [];

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
        <section className="panel grid gap-3" aria-busy={running}>
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
              disabled={running || Boolean(view.staleReason)}
            >
              {running ? "Capturing..." : "Capture DVD menus"}
            </button>
            {running ? (
              <button className="ghost" type="button" onClick={facet.cancelCapture}>
                Cancel
              </button>
            ) : null}
          </div>
          {view.capture ? (
            <div className="grid gap-1" role="status" aria-live="polite">
              <p className="muted m-0">
                Menus {view.capture.DiscoveredMenus} · States {view.capture.VisitedStates} · Buttons{" "}
                {view.capture.VisitedButtons} · Captured {view.capture.Images.length}
                {visibleCaptureWarnings.length > 0
                  ? ` · Warnings ${visibleCaptureWarnings.length}`
                  : ""}
              </p>
              {completionMessage ? <p className="success m-0">{completionMessage}</p> : null}
              {view.capture.Truncated ? (
                <p className="muted m-0">Configured maximum: {view.capture.MaxItems}.</p>
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
          <p className="muted">Add PNG, JPEG, or WebP paths visible to the WebUI host.</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <input
            className="min-w-[18rem] flex-1"
            aria-label="Menu image path"
            value={menuPathDraft}
            onChange={(event) => setMenuPathDraft(event.target.value)}
            placeholder="D:\\Media\\menu.png"
          />
          <button className="ghost" type="button" onClick={handleAddPath} disabled={running}>
            Add path
          </button>
          <button
            className="primary"
            type="button"
            onClick={handleImport}
            disabled={running || menuPaths.length === 0}
          >
            {running ? "Working..." : "Import images"}
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

      {view.error ? (
        <p className="error" role="alert">
          {view.error}
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
          <p className="muted">{running ? "Loading..." : `${view.images.length} saved`}</p>
        </div>
        {view.images.length > 0 ? (
          <div className="grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-3">
            {view.images.map((item, index) => {
              const itemNumber = index + 1;
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
                    disabled={running}
                    onClick={() => handleDelete(item.image.Path)}
                  >
                    Remove
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
