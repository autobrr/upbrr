// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useEffect, useRef, useState } from "react";
import type { MenuImagesFacet } from "../../releaseSession/types";

type Props = Readonly<{
  facet: MenuImagesFacet;
  currentDiscType: string;
  maxMenuItems: number;
  onContinue: () => void;
  setLightboxImage: (value: string) => void;
  setLightboxAlt: (value: string) => void;
}>;

/** Thin presentation adapter for the exact-generation menu-image facet. */
export default function MenuImagesPage({
  facet,
  currentDiscType,
  maxMenuItems,
  onContinue,
  setLightboxImage,
  setLightboxAlt,
}: Props) {
  const [menuFiles, setMenuFiles] = useState<File[]>([]);
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

  const handleImport = async () => {
    if (menuFiles.length === 0) return;
    setNotice("");
    if (await facet.importFiles(menuFiles)) {
      setMenuFiles([]);
      setNotice("Menu images imported successfully.");
    }
  };

  const handleCapture = async () => {
    setNotice("");
    await facet.capture();
  };

  const handleDelete = async (artifactID: string) => {
    setNotice("");
    if (await facet.remove(artifactID)) setNotice("Menu image removed.");
  };

  return (
    <section className="grid gap-4">
      <header>
        <p className="eyebrow">Disc Menus</p>
        <h1>Menu Images</h1>
        <p className="subtitle">
          Capture DVD menus or import existing disc menu images for upload and descriptions.
        </p>
      </header>

      {view.artifacts ? (
        <section className="panel grid gap-1" role="status">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2>Authoritative DVD menu set</h2>
            <span className="muted">{view.artifacts.status}</span>
          </div>
          <p className="muted">
            {view.artifacts.artifacts.filter((artifact) => artifact.kind === "dvd_menu").length}{" "}
            captured menu image(s)
          </p>
        </section>
      ) : null}

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
          <p className="muted">Choose PNG, JPEG, or WebP files from this device.</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <input
            className="min-w-[18rem] flex-1"
            aria-label="Menu image files"
            type="file"
            accept="image/png,image/jpeg,image/webp"
            multiple
            onChange={(event) => setMenuFiles(Array.from(event.target.files || []))}
            disabled={running}
          />
          <button
            className="primary"
            type="button"
            onClick={handleImport}
            disabled={running || menuFiles.length === 0}
          >
            {running ? "Working..." : "Import images"}
          </button>
        </div>
        {menuFiles.length > 0 ? (
          <ul className="m-0 grid list-none gap-1 p-0">
            {menuFiles.map((file) => (
              <li
                className="flex items-center justify-between gap-2 rounded border border-white/10 bg-white/5 p-2"
                key={`${file.name}-${file.size}-${file.lastModified}`}
              >
                <span className="min-w-0 break-all">{file.name}</span>
                <button
                  className="ghost"
                  type="button"
                  onClick={() =>
                    setMenuFiles((previous) => previous.filter((item) => item !== file))
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
                <article className="grid gap-2" key={item.image.artifactID}>
                  <button
                    className="screens-thumb"
                    type="button"
                    aria-label={`Preview DVD menu ${itemNumber}`}
                    onClick={() => {
                      setLightboxImage(item.contentURL);
                      setLightboxAlt(`DVD menu ${itemNumber}`);
                    }}
                  >
                    <img src={item.contentURL} alt="" />
                  </button>
                  <button
                    ref={(element) => {
                      if (element) removeButtonRefs.current.set(item.image.artifactID, element);
                      else removeButtonRefs.current.delete(item.image.artifactID);
                    }}
                    className="danger"
                    type="button"
                    aria-label={`Remove DVD menu ${itemNumber}`}
                    disabled={running}
                    onClick={() => handleDelete(item.image.artifactID)}
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
